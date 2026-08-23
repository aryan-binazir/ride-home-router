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
	"testing"
	"time"
)

func TestStoreSlidingTTLExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	store := newStore(nil, newFakeDataStore(), time.Minute, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	created, err := store.Create(KindParticipant, "riders.csv", testGrid(t, coordinateCSV("Rider", "1 Main St")))
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
	grid := testGrid(t, coordinateCSV("Rider", "1 Main St"))
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

func TestCommitReportsCountsAndConsumesTokenOnce(t *testing.T) {
	db := newFakeDataStore()
	store := newStore(nil, db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address,lat,lng\nFirst,1 Main St,40.1,-73.1\nSecond,2 Main St,40.2,-73.2\nThird,3 Main St,40.3,-73.3\n")
	created, err := store.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	preview, err := store.ApplyMapping(created.ID, AutoMap(grid.Headers))
	if err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	if preview.GeocodeProgress.Running {
		t.Fatal("coordinate-complete rows unexpectedly started geocoding")
	}

	// This duplicate appears after preview. CreateBatch sees it in its
	// transaction and leaves the corresponding imported model ID at zero.
	db.participants.addExisting(models.Participant{Name: " second ", Address: "2   MAIN ST", Lat: 40.2, Lng: -73.2})
	selected := []bool{true, true, false}
	selectedSnapshot, err := store.SelectRows(created.ID, selected)
	if err != nil {
		t.Fatalf("SelectRows() error = %v", err)
	}
	if selectedSnapshot.Selected[2] {
		t.Fatal("SelectRows() did not retain the deselected row")
	}
	result, err := store.Commit(context.Background(), created.ID, selected)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result != (CommitResult{Created: 1, SkippedDuplicate: 1, NotSelected: 1}) {
		t.Fatalf("Commit() result = %#v", result)
	}
	if _, err := store.Commit(context.Background(), created.ID, selected); !errors.Is(err, ErrCommitConsumed) {
		t.Fatalf("second Commit() error = %v, want ErrCommitConsumed", err)
	}
	if calls := db.participants.batchCalls(); calls != 1 {
		t.Fatalf("CreateBatch calls = %d, want 1", calls)
	}
}

func TestCommitAllowsExplicitPreviewDuplicateOverride(t *testing.T) {
	db := newFakeDataStore()
	db.participants.addExisting(models.Participant{Name: "Existing Rider", Address: "1 Main St", Lat: 40.1, Lng: -73.1})
	store := newStore(nil, db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address\nExisting Rider,1 Main St\n")
	created, err := store.Create(KindParticipant, "duplicate.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	preview, err := store.ApplyMapping(created.ID, AutoMap(grid.Headers))
	if err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	if !preview.Rows[0].DuplicateOfExisting || preview.Selected[0] {
		t.Fatalf("duplicate preview row = %#v selected=%v", preview.Rows[0], preview.Selected[0])
	}
	result, err := store.Commit(context.Background(), created.ID, []bool{true})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result != (CommitResult{Created: 1}) {
		t.Fatalf("Commit() result = %#v, want one override-created row", result)
	}
}

func TestCommitCreatesDriverBatch(t *testing.T) {
	db := newFakeDataStore()
	store := newStore(nil, db, time.Hour, time.Hour, time.Now)
	t.Cleanup(store.Close)
	grid := testGrid(t, "name,address,lat,lng,capacity\nDriver,1 Main St,40.1,-73.1,6\n")
	created, err := store.Create(KindDriver, "drivers.csv", grid)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.ApplyMapping(created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatalf("ApplyMapping() error = %v", err)
	}
	result, err := store.Commit(context.Background(), created.ID, []bool{true})
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
	if _, err := store.ApplyMapping(created.ID, AutoMap(grid.Headers)); err != nil {
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
	if _, err := store.ApplyMapping(created.ID, AutoMap(grid.Headers)); err != nil {
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
	if _, err := store.ApplyMapping(created.ID, AutoMap(grid.Headers)); err != nil {
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
	snapshot, err := store.ApplyMapping(created.ID, AutoMap(grid.Headers))
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

func coordinateCSV(name, address string) string {
	return fmt.Sprintf("name,address,lat,lng\n%s,%s,40,-73\n", name, address)
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
	mu        sync.Mutex
	rows      []models.Participant
	nextID    int64
	batchCall int
}

func (r *fakeParticipantRepository) List(context.Context, string) ([]models.Participant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]models.Participant(nil), r.rows...), nil
}

func (r *fakeParticipantRepository) CreateBatch(_ context.Context, batch []*models.Participant, allowExistingDuplicate []bool) (database.BatchCreateResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchCall++
	keys := make(map[string]struct{}, len(r.rows))
	for i := range r.rows {
		keys[DuplicateKey(r.rows[i].Name, r.rows[i].Address)] = struct{}{}
	}
	result := database.BatchCreateResult{}
	for i, participant := range batch {
		key := DuplicateKey(participant.Name, participant.Address)
		if _, duplicate := keys[key]; duplicate && (i >= len(allowExistingDuplicate) || !allowExistingDuplicate[i]) {
			result.SkippedDuplicate++
			continue
		}
		participant.ID = r.nextID
		r.nextID++
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

func (r *fakeDriverRepository) CreateBatch(_ context.Context, batch []*models.Driver, allowExistingDuplicate []bool) (database.BatchCreateResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make(map[string]struct{}, len(r.rows))
	for i := range r.rows {
		keys[DuplicateKey(r.rows[i].Name, r.rows[i].Address)] = struct{}{}
	}
	result := database.BatchCreateResult{}
	for i, driver := range batch {
		key := DuplicateKey(driver.Name, driver.Address)
		if _, duplicate := keys[key]; duplicate && (i >= len(allowExistingDuplicate) || !allowExistingDuplicate[i]) {
			result.SkippedDuplicate++
			continue
		}
		driver.ID = r.nextID
		r.nextID++
		r.rows = append(r.rows, *driver)
		result.Created++
	}
	return result, nil
}
