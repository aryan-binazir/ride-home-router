package importer

import (
	"context"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreSlidingTTLExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	store := newStore(nil, newFakeDataStore(), time.Minute, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	created, err := store.Create(KindParticipant, "riders.csv", testGrid(t, addressCSV("Rider", "1 Main St")))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now = now.Add(45 * time.Second)
	if _, ok := store.Snapshot(created.ID); !ok {
		t.Fatal("session expired before TTL touch")
	}
	now = now.Add(45 * time.Second)
	store.deleteExpired(now)
	if _, ok := store.Snapshot(created.ID); !ok {
		t.Fatal("Snapshot() did not extend the sliding TTL")
	}
	now = now.Add(61 * time.Second)
	store.deleteExpired(now)
	if _, ok := store.Snapshot(created.ID); ok {
		t.Fatal("expired session was not removed")
	}
}

func TestStoreEvictsLeastRecentlyAccessedNonCommittingSession(t *testing.T) {
	now := time.Unix(100, 0)
	store := newStore(nil, newFakeDataStore(), time.Hour, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	grid := testGrid(t, addressCSV("Rider", "1 Main St"))
	var ids []string
	for i := range MaxConcurrentSessions {
		created, err := store.Create(KindParticipant, fmt.Sprintf("%d.csv", i), grid)
		if err != nil {
			t.Fatalf("Create(%d) error = %v", i, err)
		}
		ids = append(ids, created.ID)
		now = now.Add(time.Second)
	}
	if _, ok := store.Snapshot(ids[0]); !ok {
		t.Fatal("failed to touch oldest-created session")
	}
	now = now.Add(time.Second)
	fifth, err := store.Create(KindParticipant, "fifth.csv", grid)
	if err != nil {
		t.Fatalf("fifth Create() error = %v", err)
	}
	if _, ok := store.Snapshot(ids[1]); ok {
		t.Fatal("least-recently-accessed session survived bounded-store eviction")
	}
	for _, id := range append([]string{ids[0]}, append(ids[2:], fifth.ID)...) {
		if _, ok := store.Snapshot(id); !ok {
			t.Fatal("a non-evicted session was removed")
		}
	}
}

func TestCancelDuringStalledApplyMappingDoesNotBlockStore(t *testing.T) {
	db := newFakeDataStore()
	listStarted := make(chan struct{})
	listRelease := make(chan struct{})
	var listCalls atomic.Int32
	db.participants.beforeList = func(ctx context.Context) error {
		if listCalls.Add(1) != 1 {
			return nil
		}
		close(listStarted)
		select {
		case <-listRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, addressCSV("Rider", "1 Main St"))
	stalled, err := store.Create(KindParticipant, "stalled.csv", grid)
	if err != nil {
		t.Fatalf("Create(stalled) error = %v", err)
	}
	usable, err := store.Create(KindParticipant, "usable.csv", grid)
	if err != nil {
		t.Fatalf("Create(usable) error = %v", err)
	}

	applyErr := make(chan error, 1)
	go func() {
		_, err := store.ApplyMapping(context.Background(), stalled.ID, AutoMap(grid.Headers))
		applyErr <- err
	}()
	select {
	case <-listStarted:
	case <-time.After(time.Second):
		t.Fatal("ApplyMapping did not reach the repository")
	}
	if _, err := store.ApplyMapping(context.Background(), stalled.ID, AutoMap(grid.Headers)); !errors.Is(err, ErrInvalidSessionState) {
		t.Fatalf("concurrent ApplyMapping error = %v, want ErrInvalidSessionState", err)
	}
	if calls := listCalls.Load(); calls != 1 {
		t.Fatalf("concurrent repository List calls = %d, want 1", calls)
	}

	cancelled := make(chan bool, 1)
	go func() { cancelled <- store.Cancel(stalled.ID) }()
	select {
	case ok := <-cancelled:
		if !ok {
			t.Fatal("Cancel() = false")
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel blocked behind ApplyMapping")
	}
	if _, ok := store.Snapshot(usable.ID); !ok {
		t.Fatal("other session became unavailable")
	}
	if _, err := store.ApplyMapping(context.Background(), usable.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping(other session) error = %v", err)
	}

	close(listRelease)
	if err := <-applyErr; !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("stalled ApplyMapping error = %v, want ErrSessionNotFound", err)
	}
}

func TestCanceledApplyMappingRequestLeavesSessionRetryable(t *testing.T) {
	db := newFakeDataStore()
	db.participants.beforeList = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, addressCSV("Rider", "1 Main St"))
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.ApplyMapping(ctx, created.ID, AutoMap(grid.Headers)); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyMapping() error = %v, want context.Canceled", err)
	}
	snapshot, ok := store.Snapshot(created.ID)
	if !ok || snapshot.Status != StatusMapping {
		t.Fatalf("session after canceled request: ok=%v status=%s, want mapping", ok, snapshot.Status)
	}

	db.participants.beforeList = nil
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("retry ApplyMapping() error = %v", err)
	}
}

func TestCanceledApplyMappingAfterListLeavesSessionRetryable(t *testing.T) {
	db := newFakeDataStore()
	ctx, cancel := context.WithCancel(context.Background())
	db.participants.beforeList = func(context.Context) error {
		cancel()
		return nil
	}
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, addressCSV("Rider", "1 Main St"))
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := store.ApplyMapping(ctx, created.ID, AutoMap(grid.Headers)); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyMapping() error = %v, want context.Canceled", err)
	}
	snapshot, ok := store.Snapshot(created.ID)
	if !ok || snapshot.Status != StatusMapping {
		t.Fatalf("session after canceled request: ok=%v status=%s, want mapping", ok, snapshot.Status)
	}
}

func TestCommitReportsCountsAndConsumesTokenOnce(t *testing.T) {
	db := newFakeDataStore()
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nFirst,1 Main St\nSecond,2 Main St\nThird,3 Main St\n")
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers))
	if err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	waitForGeocoding(t, store, created.ID)

	// This duplicate appears after preview and gets updated instead of created.
	db.participants.addExisting(models.Participant{Name: " second ", Address: "2   MAIN ST", Lat: 40.2, Lng: -73.2})
	selected := []bool{true, true, false}
	selectedSnapshot, err := store.SelectRows(created.ID, selected)
	if err != nil {
		t.Fatalf("SelectRows() error = %v", err)
	}
	if selectedSnapshot.Selected[2] {
		t.Fatal("SelectRows() did not retain the deselected row")
	}
	result, err := store.Commit(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result != (CommitResult{Created: 1, Updated: 1, NotSelected: 1}) {
		t.Fatalf("Commit() result = %#v", result)
	}
	if _, err := store.Commit(context.Background(), created.ID); !errors.Is(err, ErrCommitConsumed) {
		t.Fatalf("second Commit() error = %v, want ErrCommitConsumed", err)
	}
	if calls := db.participants.batchCalls(); calls != 1 {
		t.Fatalf("CreateBatch calls = %d, want 1", calls)
	}
}

func TestCancelRacingCommitDoesNotPersist(t *testing.T) {
	db := newFakeDataStore()
	batchStarted := make(chan struct{})
	db.participants.beforeBatch = func(ctx context.Context) error {
		close(batchStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, addressCSV("Rider", "1 Main St"))
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	waitForGeocoding(t, store, created.ID)

	commitErr := make(chan error, 1)
	go func() {
		_, err := store.Commit(context.Background(), created.ID)
		commitErr <- err
	}()
	select {
	case <-batchStarted:
	case <-time.After(time.Second):
		t.Fatal("Commit did not reach the repository")
	}
	if !store.Cancel(created.ID) {
		t.Fatal("Cancel() = false")
	}
	if err := <-commitErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("Commit() error = %v, want context.Canceled", err)
	}
	db.participants.mu.Lock()
	defer db.participants.mu.Unlock()
	if len(db.participants.rows) != 0 {
		t.Fatalf("persisted participants = %d, want 0", len(db.participants.rows))
	}
	if db.participants.batchCall != 1 {
		t.Fatalf("UpsertBatch calls = %d, want 1", db.participants.batchCall)
	}
}

func TestCommitUsesLatestStoredSelection(t *testing.T) {
	db := newFakeDataStore()
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nFirst,1 Main St\nSecond,2 Main St\n")
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	waitForGeocoding(t, store, created.ID)
	if _, err := store.SelectRows(created.ID, []bool{true, true}); err != nil {
		t.Fatalf("first SelectRows() error = %v", err)
	}
	if _, err := store.SelectRows(created.ID, []bool{true, false}); err != nil {
		t.Fatalf("latest SelectRows() error = %v", err)
	}

	result, err := store.Commit(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result != (CommitResult{Created: 1, NotSelected: 1}) {
		t.Fatalf("Commit() result = %#v", result)
	}
	db.participants.mu.Lock()
	defer db.participants.mu.Unlock()
	if len(db.participants.rows) != 1 || db.participants.rows[0].Name != "First" {
		t.Fatalf("persisted participants = %#v", db.participants.rows)
	}
}

func TestSelectRowsDuringCommitIsRejectedWithoutLosingSelection(t *testing.T) {
	db := newFakeDataStore()
	batchStarted := make(chan struct{})
	batchRelease := make(chan struct{})
	db.participants.beforeBatch = func(ctx context.Context) error {
		close(batchStarted)
		select {
		case <-batchRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nFirst,1 Main St\nSecond,2 Main St\n")
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	waitForGeocoding(t, store, created.ID)

	commitResult := make(chan CommitResult, 1)
	commitErr := make(chan error, 1)
	go func() {
		result, err := store.Commit(context.Background(), created.ID)
		commitResult <- result
		commitErr <- err
	}()
	select {
	case <-batchStarted:
	case <-time.After(time.Second):
		t.Fatal("Commit did not reach the repository")
	}
	if _, err := store.SelectRows(created.ID, []bool{true, false}); !errors.Is(err, ErrInvalidSessionState) {
		t.Fatalf("SelectRows() error = %v, want ErrInvalidSessionState", err)
	}
	close(batchRelease)
	if err := <-commitErr; err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result := <-commitResult; result != (CommitResult{Created: 2}) {
		t.Fatalf("Commit() result = %#v, want original two-row selection", result)
	}
}

func TestDeleteExpiredSkipsCommittingSession(t *testing.T) {
	now := time.Unix(100, 0)
	db := newFakeDataStore()
	batchStarted := make(chan struct{})
	batchRelease := make(chan struct{})
	db.participants.beforeBatch = func(ctx context.Context) error {
		close(batchStarted)
		select {
		case <-batchRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	store := newStore(successfulTestGeocoder(), db, time.Minute, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	grid := testGrid(t, addressCSV("Rider", "1 Main St"))
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	waitForGeocoding(t, store, created.ID)

	commitErr := make(chan error, 1)
	go func() {
		_, err := store.Commit(context.Background(), created.ID)
		commitErr <- err
	}()
	select {
	case <-batchStarted:
	case <-time.After(time.Second):
		t.Fatal("Commit did not reach the repository")
	}
	now = now.Add(2 * time.Minute)
	store.deleteExpired(now)
	if snapshot, ok := store.Snapshot(created.ID); !ok || snapshot.Status != StatusCommitting {
		t.Fatalf("committing session expired: ok=%v status=%s", ok, snapshot.Status)
	}
	close(batchRelease)
	if err := <-commitErr; err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

func TestCommitUpdatesPreviewKnownDuplicates(t *testing.T) {
	db := newFakeDataStore()
	db.participants.addExisting(models.Participant{Name: "Existing Rider", Address: "1 Main St", Lat: 40.1, Lng: -73.1})
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nExisting Rider,1 Main St\n")
	created, err := store.Create(KindParticipant, "duplicate.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	preview, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers))
	if err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	if !preview.Rows[0].DuplicateOfExisting || !preview.Selected[0] {
		t.Fatalf("duplicate preview row = %#v selected=%v, want flagged and selected", preview.Rows[0], preview.Selected[0])
	}
	waitForGeocoding(t, store, created.ID)
	result, err := store.Commit(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result != (CommitResult{Updated: 1}) {
		t.Fatalf("Commit() result = %#v, want one updated row", result)
	}
	if rows, _ := db.participants.List(context.Background(), ""); len(rows) != 1 {
		t.Fatalf("participants after commit = %d, want the existing row only", len(rows))
	}
}

func TestCommitCreatesDriverBatch(t *testing.T) {
	db := newFakeDataStore()
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address,capacity\nDriver,1 Main St,6\n")
	created, err := store.Create(KindDriver, "drivers.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	waitForGeocoding(t, store, created.ID)
	result, err := store.Commit(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result != (CommitResult{Created: 1}) {
		t.Fatalf("Commit() result = %#v", result)
	}
	db.drivers.mu.Lock()
	defer db.drivers.mu.Unlock()
	if len(db.drivers.rows) != 1 || db.drivers.rows[0].VehicleCapacity != 6 {
		t.Fatalf("driver batch rows = %#v", db.drivers.rows)
	}
}

func TestCommitSendsZeroCapacityWhenColumnUnmapped(t *testing.T) {
	db := newFakeDataStore()
	store := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nDriver,1 Main St\n")
	created, err := store.Create(KindDriver, "drivers.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	waitForGeocoding(t, store, created.ID)
	if _, err := store.Commit(context.Background(), created.ID); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	db.drivers.mu.Lock()
	defer db.drivers.mu.Unlock()
	if len(db.drivers.rows) != 1 || db.drivers.rows[0].VehicleCapacity != 0 {
		t.Fatalf("driver batch rows = %#v, want capacity 0 so the repository preserves or defaults it", db.drivers.rows)
	}
}

func TestGeocodeJobDeduplicatesAndMarksFailures(t *testing.T) {
	geocoder := &fakeGeocoder{result: func(_ context.Context, address string, retries int) (*geocoding.GeocodingResult, error) {
		if retries != geocodeMaxRetries {
			return nil, fmt.Errorf("retries = %d", retries)
		}
		if NormalizeRosterText(address) == "2 bad st" {
			return nil, errors.New("not found")
		}
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 40.5, Lng: -73.5}}, nil
	}}
	store := newStore(geocoder, newFakeDataStore(), time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nFirst,1 Main St\nSecond,  1 MAIN st \nThird,2 Bad St\n")
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	finished := waitForGeocoding(t, store, created.ID)
	if got := geocoder.callCount(); got != 2 {
		t.Fatalf("GeocodeWithRetry calls = %d, want 2 unique addresses", got)
	}
	if finished.GeocodeProgress != (GeocodeProgress{Done: 2, Total: 2, Running: false}) {
		t.Fatalf("progress = %#v", finished.GeocodeProgress)
	}
	for i := range 2 {
		if !finished.Rows[i].HasCoordinates || finished.Rows[i].Lat != 40.5 || finished.Rows[i].Lng != -73.5 {
			t.Errorf("row %d coordinates = %#v", i, finished.Rows[i])
		}
	}
	failed := finished.Rows[2]
	if failed.HasCoordinates || failed.NeedsGeocoding || !containsString(failed.Errors, "address could not be geocoded") {
		t.Fatalf("failed row = %#v", failed)
	}
	if finished.Selected[2] {
		t.Fatal("failed geocode row remained selected")
	}
}

func TestCommitSkipsRowsWithoutGeocodedCoordinates(t *testing.T) {
	db := newFakeDataStore()
	store := newStore(nil, db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	ctx, cancel := context.WithCancel(context.Background())
	state := &session{
		id: "missing-coordinates", kind: KindParticipant, status: StatusPreviewing,
		rows: []Row{{Name: "Rider", Address: "1 Main St"}}, selected: []bool{true},
		createdAt: time.Now(), lastAccessedAt: time.Now(), ctx: ctx, cancel: cancel,
	}
	store.sessions[state.id] = state

	result, err := store.Commit(context.Background(), state.id)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result != (CommitResult{NotSelected: 1}) || db.participants.batchCalls() != 1 {
		t.Fatalf("Commit() result = %#v batch calls = %d, want row excluded from empty batch", result, db.participants.batchCalls())
	}
}

func TestGeocodeJobCancellation(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	geocoder := &fakeGeocoder{result: func(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}}
	store := newStore(geocoder, newFakeDataStore(), time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nRider,1 Main St\n")
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("geocoder did not start")
	}
	if !store.Cancel(created.ID) {
		t.Fatal("Cancel() = false")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("geocoder context was not cancelled")
	}
	if _, ok := store.Snapshot(created.ID); ok {
		t.Fatal("cancelled session retained staged data")
	}
}

func TestRunGeocodeJobClearsRunningOnEarlyExit(t *testing.T) {
	geocoder := &fakeGeocoder{result: func(context.Context, string, int) (*geocoding.GeocodingResult, error) {
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 40, Lng: -73}}, nil
	}}
	store := &Store{geocoder: geocoder}
	state := &session{
		ctx:      context.Background(),
		status:   StatusCommitted,
		progress: GeocodeProgress{Total: 1, Running: true},
	}
	store.jobs.Add(1)
	store.runGeocodeJob(state, []geocodeGroup{{address: "1 Main St"}})
	if state.progress.Running {
		t.Fatal("early geocode-job exit left progress.Running true")
	}
}

func TestStoreCloseCancelsGeocodeJob(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	geocoder := &fakeGeocoder{result: func(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}}
	store := newStore(geocoder, newFakeDataStore(), time.Hour, time.Hour, time.Now)
	grid := testGrid(t, "name,address\nRider,1 Main St\n")
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("geocoder did not start")
	}
	store.Close()
	select {
	case <-cancelled:
	default:
		t.Fatal("Close() returned before geocoder observed cancellation")
	}
	if _, err := store.Create(KindParticipant, "closed.csv", grid); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Create() after Close error = %v, want ErrStoreClosed", err)
	}
}

func TestApplyMappingRejectsGeocodeAddressCap(t *testing.T) {
	geocoder := &fakeGeocoder{}
	store := newStore(geocoder, newFakeDataStore(), time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	var csv strings.Builder
	csv.WriteString("name,address\n")
	for i := 0; i <= MaxGeocodeAddresses; i++ {
		fmt.Fprintf(&csv, "Rider %d,%d Main St\n", i, i)
	}
	grid := testGrid(t, csv.String())
	created, err := store.Create(KindParticipant, "too-many.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	snapshot, err := store.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers))
	if !errors.Is(err, ErrTooManyGeocodeAddresses) {
		t.Fatalf("ApplyMapping() error = %v, want ErrTooManyGeocodeAddresses", err)
	}
	if snapshot.Status != StatusFailed || !strings.Contains(snapshot.Failure, "maximum is 200") {
		t.Fatalf("failed snapshot = %#v", snapshot)
	}
	if geocoder.callCount() != 0 {
		t.Fatal("over-cap import started geocoding")
	}
}

func waitForGeocoding(t *testing.T, store *Store, id string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := store.Snapshot(id)
		if !ok {
			t.Fatal("session disappeared while geocoding")
		}
		if !snapshot.GeocodeProgress.Running {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("geocoding did not finish")
	return Snapshot{}
}

func testGrid(t *testing.T, csv string) *Grid {
	t.Helper()
	grid, err := Parse(strings.NewReader(csv), FormatCSV, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return grid
}

func addressCSV(name, address string) string {
	return fmt.Sprintf("name,address\n%s,%s\n", name, address)
}

func successfulTestGeocoder() *fakeGeocoder {
	return &fakeGeocoder{result: func(context.Context, string, int) (*geocoding.GeocodingResult, error) {
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 40, Lng: -73}}, nil
	}}
}

type fakeGeocoder struct {
	geocoding.Geocoder
	mu     sync.Mutex
	calls  []string
	result func(context.Context, string, int) (*geocoding.GeocodingResult, error)
}

func (g *fakeGeocoder) GeocodeWithRetry(ctx context.Context, address string, retries int) (*geocoding.GeocodingResult, error) {
	g.mu.Lock()
	g.calls = append(g.calls, address)
	g.mu.Unlock()
	if g.result == nil {
		return nil, errors.New("unexpected geocode call")
	}
	return g.result(ctx, address, retries)
}

func (g *fakeGeocoder) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

type fakeDataStore struct {
	database.DataStore
	participants *fakeParticipantRepository
	drivers      *fakeDriverRepository
}

func newFakeDataStore() *fakeDataStore {
	return &fakeDataStore{participants: &fakeParticipantRepository{nextID: 1}, drivers: &fakeDriverRepository{nextID: 1}}
}

func (s *fakeDataStore) Participants() database.ParticipantRepository { return s.participants }
func (s *fakeDataStore) Drivers() database.DriverRepository           { return s.drivers }

type fakeParticipantRepository struct {
	database.ParticipantRepository
	mu          sync.Mutex
	rows        []models.Participant
	nextID      int64
	batchCall   int
	beforeList  func(context.Context) error
	beforeBatch func(context.Context) error
}

func (r *fakeParticipantRepository) List(ctx context.Context, _ string) ([]models.Participant, error) {
	if r.beforeList != nil {
		if err := r.beforeList(ctx); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]models.Participant(nil), r.rows...), nil
}

func (r *fakeParticipantRepository) UpsertBatch(ctx context.Context, batch []*models.Participant) (database.BatchUpsertResult, error) {
	r.mu.Lock()
	r.batchCall++
	r.mu.Unlock()
	if r.beforeBatch != nil {
		if err := r.beforeBatch(ctx); err != nil {
			return database.BatchUpsertResult{}, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make(map[string]int, len(r.rows))
	for i := range r.rows {
		keys[DuplicateKey(r.rows[i].Name, r.rows[i].Address)] = i
	}
	result := database.BatchUpsertResult{}
	for _, participant := range batch {
		key := DuplicateKey(participant.Name, participant.Address)
		if i, duplicate := keys[key]; duplicate {
			participant.ID = r.rows[i].ID
			result.Updated++
			continue
		}
		participant.ID = r.nextID
		r.nextID++
		keys[key] = len(r.rows)
		r.rows = append(r.rows, *participant)
		result.Created++
	}
	return result, nil
}

func (r *fakeParticipantRepository) addExisting(participant models.Participant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	participant.ID = r.nextID
	r.nextID++
	r.rows = append(r.rows, participant)
}

func (r *fakeParticipantRepository) batchCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.batchCall
}

type fakeDriverRepository struct {
	database.DriverRepository
	mu     sync.Mutex
	rows   []models.Driver
	nextID int64
}

func (r *fakeDriverRepository) List(context.Context, string) ([]models.Driver, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]models.Driver(nil), r.rows...), nil
}

func (r *fakeDriverRepository) UpsertBatch(_ context.Context, batch []*models.Driver) (database.BatchUpsertResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make(map[string]int, len(r.rows))
	for i := range r.rows {
		keys[DuplicateKey(r.rows[i].Name, r.rows[i].Address)] = i
	}
	result := database.BatchUpsertResult{}
	for _, driver := range batch {
		key := DuplicateKey(driver.Name, driver.Address)
		if i, duplicate := keys[key]; duplicate {
			driver.ID = r.rows[i].ID
			result.Updated++
			continue
		}
		driver.ID = r.nextID
		r.nextID++
		keys[key] = len(r.rows)
		r.rows = append(r.rows, *driver)
		result.Created++
	}
	return result, nil
}
