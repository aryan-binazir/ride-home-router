package importer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"sync"
	"time"
)

const (
	MaxGeocodeAddresses   = 200
	MaxConcurrentSessions = 4

	defaultSessionTTL      = 30 * time.Minute
	defaultCleanupInterval = 5 * time.Minute
	defaultCommitTimeout   = 60 * time.Second
	geocodeMaxRetries      = 3
)

var (
	ErrSessionNotFound         = errors.New("import session not found")
	ErrStoreClosed             = errors.New("import session store is closed")
	ErrStoreFull               = errors.New("all import sessions are currently committing")
	ErrInvalidSessionState     = errors.New("invalid import session state")
	ErrInvalidSelection        = errors.New("selection count does not match import rows")
	ErrGeocodingInProgress     = errors.New("geocoding is still in progress")
	ErrCommitConsumed          = errors.New("import session commit token has already been consumed")
	ErrTooManyGeocodeAddresses = errors.New("import exceeds the geocoding address limit")
)

// Status is the lifecycle state of an import staging session.
type Status string

const (
	StatusMapping    Status = "mapping"
	StatusPreviewing Status = "previewing"
	StatusCommitting Status = "committing"
	StatusCommitted  Status = "committed"
	StatusFailed     Status = "failed"
)

// GeocodeProgress describes the serial geocoding job for a session.
type GeocodeProgress struct {
	Done    int
	Total   int
	Running bool
}

// CommitResult summarizes the terminal batch operation.
type CommitResult struct {
	Created     int
	Updated     int
	NotSelected int
}

// Snapshot is a concurrency-safe copy of an import session.
type Snapshot struct {
	ID              string
	Kind            Kind
	Filename        string
	Grid            Grid
	Mapping         Mapping
	Rows            []Row
	Selected        []bool
	GeocodeProgress GeocodeProgress
	Status          Status
	Failure         string
	CommitResult    CommitResult
}

type session struct {
	id             string
	kind           Kind
	filename       string
	grid           Grid
	mapping        Mapping
	rows           []Row
	selected       []bool
	progress       GeocodeProgress
	status         Status
	failure        string
	commitResult   CommitResult
	applying       bool
	commitStarted  bool
	createdAt      time.Time
	lastAccessedAt time.Time
	ctx            context.Context
	cancel         context.CancelFunc
	deleted        bool
	mu             sync.Mutex
}

func (s *session) afterCancel(f func()) func() bool {
	return context.AfterFunc(s.ctx, f)
}

// Store owns short-lived import staging sessions and their background jobs.
type Store struct {
	geocoder        geocoding.Geocoder
	db              database.DataStore
	sessions        map[string]*session
	ttl             time.Duration
	cleanupInterval time.Duration
	now             func() time.Time
	stopCleanup     chan struct{}
	cleanupDone     chan struct{}
	closed          bool
	closeOnce       sync.Once
	jobs            sync.WaitGroup
	// lifecycleMu serializes session-map membership changes that require
	// inspecting session state. It is never acquired while state.mu is held.
	lifecycleMu sync.Mutex
	mu          sync.Mutex
}

// NewStore creates an import staging store with a 30-minute sliding TTL.
func NewStore(geocoder geocoding.Geocoder, db database.DataStore) *Store {
	return newStore(geocoder, db, defaultSessionTTL, defaultCleanupInterval, time.Now)
}

func newStore(geocoder geocoding.Geocoder, db database.DataStore, ttl, cleanupInterval time.Duration, now func() time.Time) *Store {
	store := &Store{
		geocoder: geocoder, db: db, sessions: make(map[string]*session), ttl: ttl,
		cleanupInterval: cleanupInterval, now: now,
		stopCleanup: make(chan struct{}), cleanupDone: make(chan struct{}),
	}
	go store.cleanupLoop()
	return store
}

// Create stages a file under a consume-once 128-bit token.
func (s *Store) Create(kind Kind, filename string, grid *Grid) (Snapshot, error) {
	if grid == nil {
		return Snapshot{}, errors.New("import grid is required")
	}
	if kind != KindParticipant && kind != KindDriver {
		return Snapshot{}, fmt.Errorf("unsupported roster kind %q", kind)
	}
	id, err := newSessionID()
	if err != nil {
		return Snapshot{}, fmt.Errorf("generate import session ID: %w", err)
	}
	now := s.now()
	ctx, cancel := context.WithCancel(context.Background())
	state := &session{
		id: id, kind: kind, filename: filename, grid: copyGrid(*grid), mapping: copyMapping(AutoMap(grid.Headers)),
		status: StatusMapping, createdAt: now, lastAccessedAt: now, ctx: ctx, cancel: cancel,
	}

	snapshot := snapshotOf(state)
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	for attempts := 0; ; attempts++ {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			cancel()
			return Snapshot{}, ErrStoreClosed
		}
		if len(s.sessions) < MaxConcurrentSessions {
			s.sessions[id] = state
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		if attempts >= MaxConcurrentSessions || !s.evictOldest() {
			cancel()
			return Snapshot{}, ErrStoreFull
		}
	}

	log.Printf("[IMPORT] Created staging session: kind=%s rows=%d", kind, grid.Len())
	return snapshot, nil
}

// Snapshot returns the current session state and extends its sliding TTL.
func (s *Store) Snapshot(id string) (Snapshot, bool) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, false
	}
	defer state.mu.Unlock()
	return snapshotOf(state), true
}

// ApplyMapping validates the staged grid and starts its serial geocoding job.
func (s *Store) ApplyMapping(ctx context.Context, id string, mapping Mapping) (Snapshot, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	if state.status != StatusMapping {
		status := state.status
		state.mu.Unlock()
		return Snapshot{}, fmt.Errorf("%w: cannot apply mapping while %s", ErrInvalidSessionState, status)
	}
	if state.applying {
		state.mu.Unlock()
		return Snapshot{}, fmt.Errorf("%w: another mapping is already in progress", ErrInvalidSessionState)
	}
	state.applying = true
	grid := copyGrid(state.grid)
	kind := state.kind
	mappingCtx, cancel := context.WithCancel(ctx)
	stopCancel := state.afterCancel(cancel)
	// Close marks every session deleted under this lock before waiting, so no
	// mapping operation can be added after the wait begins.
	s.jobs.Add(1)
	state.mu.Unlock()
	defer func() {
		stopCancel()
		cancel()
		state.mu.Lock()
		state.applying = false
		state.mu.Unlock()
		s.jobs.Done()
	}()

	existing, err := s.listExisting(mappingCtx, kind)
	var rows []Row
	var groups []geocodeGroup
	if err == nil {
		rows = Validate(&grid, mapping, kind, existing)
		groups = geocodeGroups(rows)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.deleted {
		return Snapshot{}, ErrSessionNotFound
	}
	if ctx.Err() != nil && state.ctx.Err() == nil {
		return snapshotOf(state), ctx.Err()
	}
	if err != nil {
		state.status = StatusFailed
		state.failure = "could not load the current roster"
		return snapshotOf(state), fmt.Errorf("load current roster for import: %w", err)
	}
	if len(groups) > MaxGeocodeAddresses {
		state.status = StatusFailed
		state.failure = fmt.Sprintf("import needs geocoding for %d unique addresses; maximum is %d", len(groups), MaxGeocodeAddresses)
		return snapshotOf(state), fmt.Errorf("%w: %s", ErrTooManyGeocodeAddresses, state.failure)
	}

	state.mapping = copyMapping(mapping)
	state.rows = copyRows(rows)
	state.selected = defaultSelections(rows)
	state.progress = GeocodeProgress{Total: len(groups), Running: len(groups) > 0}
	state.status = StatusPreviewing
	state.failure = ""
	if len(groups) > 0 {
		// Close marks every session deleted under this lock before waiting, so
		// no job can be added after the wait begins.
		s.jobs.Add(1)
		go s.runGeocodeJob(state, groups)
	}
	return snapshotOf(state), nil
}

// SelectRows replaces the per-row selection state used by Commit.
func (s *Store) SelectRows(id string, selected []bool) (Snapshot, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	defer state.mu.Unlock()
	if state.status != StatusPreviewing {
		return Snapshot{}, fmt.Errorf("%w: cannot select rows while %s", ErrInvalidSessionState, state.status)
	}
	if len(selected) != len(state.rows) {
		return Snapshot{}, ErrInvalidSelection
	}
	state.selected = append([]bool(nil), selected...)
	return snapshotOf(state), nil
}

// Commit consumes the token before writing, so retries cannot duplicate a batch.
func (s *Store) Commit(ctx context.Context, id string) (CommitResult, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return CommitResult{}, err
	}
	if state.commitStarted {
		state.mu.Unlock()
		return CommitResult{}, ErrCommitConsumed
	}
	if state.status != StatusPreviewing {
		status := state.status
		state.mu.Unlock()
		return CommitResult{}, fmt.Errorf("%w: cannot commit while %s", ErrInvalidSessionState, status)
	}
	if state.progress.Running {
		state.mu.Unlock()
		return CommitResult{}, ErrGeocodingInProgress
	}
	if len(state.selected) != len(state.rows) {
		state.mu.Unlock()
		return CommitResult{}, ErrInvalidSelection
	}
	commitCtx, cancel := context.WithTimeout(ctx, defaultCommitTimeout)
	stopCancel := state.afterCancel(cancel)
	state.status = StatusCommitting
	state.commitStarted = true
	rows := copyRows(state.rows)
	selected := append([]bool(nil), state.selected...)
	kind := state.kind
	// Close marks every session deleted under this lock before waiting, so no
	// commit can be added after the wait begins.
	s.jobs.Add(1)
	state.mu.Unlock()

	defer func() {
		stopCancel()
		cancel()
		s.jobs.Done()
	}()

	var result CommitResult
	state.mu.Lock()
	deleted := state.deleted
	state.mu.Unlock()
	if deleted {
		err = ErrSessionNotFound
	} else if err = commitCtx.Err(); err == nil {
		result, err = s.createBatch(commitCtx, kind, rows, selected)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.deleted {
		if err != nil {
			return CommitResult{}, err
		}
		return result, nil
	}
	if err != nil {
		state.status = StatusFailed
		state.failure = "the import batch could not be saved"
		return CommitResult{}, err
	}
	state.status = StatusCommitted
	state.commitResult = result
	state.grid = Grid{}
	state.rows = nil
	state.selected = nil
	state.cancel()
	log.Printf("[IMPORT] Committed batch: kind=%s created=%d updated=%d not_selected=%d", kind, result.Created, result.Updated, result.NotSelected)
	return result, nil
}

// Cancel removes a session immediately and cancels any running work.
func (s *Store) Cancel(id string) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	state := s.sessions[id]
	s.mu.Unlock()
	if state == nil {
		return false
	}
	state.mu.Lock()
	if state.deleted {
		state.mu.Unlock()
		return false
	}
	state.deleted = true
	state.cancel()
	clearSessionData(state)
	state.mu.Unlock()
	s.removeSession(id, state)
	return true
}

// Close cancels jobs, releases sessions, and waits for workers to stop.
func (s *Store) Close() {
	s.closeOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.mu.Lock()
		s.closed = true
		states := make([]*session, 0, len(s.sessions))
		for id, state := range s.sessions {
			delete(s.sessions, id)
			states = append(states, state)
		}
		close(s.stopCleanup)
		s.mu.Unlock()
		for _, state := range states {
			state.mu.Lock()
			state.deleted = true
			state.cancel()
			clearSessionData(state)
			state.mu.Unlock()
		}
		s.lifecycleMu.Unlock()
		<-s.cleanupDone
		s.jobs.Wait()
	})
}

func (s *Store) listExisting(ctx context.Context, kind Kind) ([]Existing, error) {
	switch kind {
	case KindParticipant:
		rows, err := s.db.Participants().List(ctx, "")
		if err != nil {
			return nil, err
		}
		existing := make([]Existing, len(rows))
		for i := range rows {
			existing[i] = Existing{Name: rows[i].Name, Address: rows[i].Address}
		}
		return existing, nil
	case KindDriver:
		rows, err := s.db.Drivers().List(ctx, "")
		if err != nil {
			return nil, err
		}
		existing := make([]Existing, len(rows))
		for i := range rows {
			existing[i] = Existing{Name: rows[i].Name, Address: rows[i].Address}
		}
		return existing, nil
	default:
		return nil, fmt.Errorf("unsupported roster kind %q", kind)
	}
}

func (s *Store) createBatch(ctx context.Context, kind Kind, rows []Row, selected []bool) (CommitResult, error) {
	result := CommitResult{NotSelected: len(rows)}
	indices := make([]int, 0, len(rows))
	for i := range rows {
		if !selected[i] || len(rows[i].Errors) > 0 || !rows[i].HasCoordinates {
			continue
		}
		indices = append(indices, i)
	}
	result.NotSelected -= len(indices)

	switch kind {
	case KindParticipant:
		batch := make([]*models.Participant, len(indices))
		for i, rowIndex := range indices {
			row := rows[rowIndex]
			batch[i] = &models.Participant{Name: row.Name, Address: row.Address, AddressName: row.AddressName, Lat: row.Lat, Lng: row.Lng}
		}
		batchResult, err := s.db.Participants().UpsertBatch(ctx, batch)
		if err != nil {
			return CommitResult{}, fmt.Errorf("upsert participant import batch: %w", err)
		}
		result.Created = batchResult.Created
		result.Updated = batchResult.Updated
	case KindDriver:
		batch := make([]*models.Driver, len(indices))
		for i, rowIndex := range indices {
			row := rows[rowIndex]
			capacity := row.Capacity
			if row.CapacityDefaulted {
				capacity = 0 // UpsertBatch keeps an existing driver's capacity and defaults new ones.
			}
			batch[i] = &models.Driver{Name: row.Name, Address: row.Address, AddressName: row.AddressName, Lat: row.Lat, Lng: row.Lng, VehicleCapacity: capacity}
		}
		batchResult, err := s.db.Drivers().UpsertBatch(ctx, batch)
		if err != nil {
			return CommitResult{}, fmt.Errorf("upsert driver import batch: %w", err)
		}
		result.Created = batchResult.Created
		result.Updated = batchResult.Updated
	default:
		return CommitResult{}, fmt.Errorf("unsupported roster kind %q", kind)
	}
	return result, nil
}

type geocodeGroup struct {
	address string
	rows    []int
}

func geocodeGroups(rows []Row) []geocodeGroup {
	byAddress := make(map[string]int)
	groups := make([]geocodeGroup, 0)
	for i := range rows {
		if !rows[i].NeedsGeocoding || len(rows[i].Errors) > 0 {
			continue
		}
		key := NormalizeRosterText(rows[i].Address)
		if key == "" {
			continue
		}
		if groupIndex, ok := byAddress[key]; ok {
			groups[groupIndex].rows = append(groups[groupIndex].rows, i)
			continue
		}
		byAddress[key] = len(groups)
		groups = append(groups, geocodeGroup{address: rows[i].Address, rows: []int{i}})
	}
	return groups
}

func validCoordinatePair(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsInf(lat, 0) && !math.IsNaN(lng) && !math.IsInf(lng, 0) &&
		lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

func (s *Store) runGeocodeJob(state *session, groups []geocodeGroup) {
	defer s.jobs.Done()
	defer func() {
		state.mu.Lock()
		state.progress.Running = false
		state.mu.Unlock()
	}()
	started := time.Now()
	failures := 0
	for _, group := range groups {
		result, err := s.geocoder.GeocodeWithRetry(state.ctx, group.address, geocodeMaxRetries)
		if state.ctx.Err() != nil {
			state.mu.Lock()
			completed := state.progress.Done
			state.mu.Unlock()
			log.Printf("[IMPORT] Geocoding cancelled: total=%d completed=%d duration=%s", len(groups), completed, time.Since(started).Round(time.Millisecond))
			return
		}

		state.mu.Lock()
		if state.deleted || state.status != StatusPreviewing {
			state.mu.Unlock()
			return
		}
		if err != nil || result == nil || !validCoordinatePair(result.Coords.Lat, result.Coords.Lng) {
			failures++
			for _, rowIndex := range group.rows {
				state.rows[rowIndex].addError("address could not be geocoded")
				state.rows[rowIndex].NeedsGeocoding = false
				state.selected[rowIndex] = false
			}
		} else {
			for _, rowIndex := range group.rows {
				state.rows[rowIndex].Lat = result.Coords.Lat
				state.rows[rowIndex].Lng = result.Coords.Lng
				state.rows[rowIndex].HasCoordinates = true
				state.rows[rowIndex].NeedsGeocoding = false
			}
		}
		state.progress.Done++
		state.mu.Unlock()
	}
	log.Printf("[IMPORT] Geocoding complete: total=%d failures=%d duration=%s", len(groups), failures, time.Since(started).Round(time.Millisecond))
}

func defaultSelections(rows []Row) []bool {
	selected := make([]bool, len(rows))
	for i := range rows {
		selected[i] = len(rows[i].Errors) == 0
	}
	return selected
}

func (s *Store) lockSession(id string) (*session, error) {
	s.mu.Lock()
	state := s.sessions[id]
	s.mu.Unlock()
	if state == nil {
		return nil, ErrSessionNotFound
	}
	state.mu.Lock()
	if state.deleted {
		state.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	now := s.now()
	if state.status != StatusCommitting && now.Sub(state.lastAccessedAt) > s.ttl {
		state.mu.Unlock()

		s.lifecycleMu.Lock()
		state.mu.Lock()
		if state.deleted {
			state.mu.Unlock()
			s.lifecycleMu.Unlock()
			return nil, ErrSessionNotFound
		}
		now = s.now()
		if state.status != StatusCommitting && now.Sub(state.lastAccessedAt) > s.ttl {
			state.deleted = true
			state.cancel()
			clearSessionData(state)
			state.mu.Unlock()
			s.removeSession(id, state)
			s.lifecycleMu.Unlock()
			return nil, ErrSessionNotFound
		}
		state.lastAccessedAt = now
		s.lifecycleMu.Unlock()
		return state, nil
	}
	state.lastAccessedAt = now
	return state, nil
}

func (s *Store) evictOldest() bool {
	for range MaxConcurrentSessions {
		s.mu.Lock()
		states := make([]*session, 0, len(s.sessions))
		for _, state := range s.sessions {
			states = append(states, state)
		}
		s.mu.Unlock()

		var oldest *session
		var oldestAccess time.Time
		deleted := make([]*session, 0, len(states))
		for _, state := range states {
			state.mu.Lock()
			eligible := !state.deleted && state.status != StatusCommitting
			lastAccessedAt := state.lastAccessedAt
			if state.deleted {
				deleted = append(deleted, state)
			}
			state.mu.Unlock()
			if eligible && (oldest == nil || lastAccessedAt.Before(oldestAccess)) {
				oldest = state
				oldestAccess = lastAccessedAt
			}
		}
		if len(deleted) > 0 {
			for _, state := range deleted {
				s.removeSession(state.id, state)
			}
			return true
		}
		if oldest == nil {
			return false
		}
		oldest.mu.Lock()
		if oldest.deleted || oldest.status == StatusCommitting || !oldest.lastAccessedAt.Equal(oldestAccess) {
			oldest.mu.Unlock()
			continue
		}
		oldest.deleted = true
		oldest.cancel()
		clearSessionData(oldest)
		oldest.mu.Unlock()
		s.removeSession(oldest.id, oldest)
		return true
	}
	return true
}

func (s *Store) removeSession(id string, state *session) {
	s.mu.Lock()
	if s.sessions[id] == state {
		delete(s.sessions, id)
	}
	s.mu.Unlock()
}

func (s *Store) cleanupLoop() {
	defer close(s.cleanupDone)
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.deleteExpired(s.now())
		case <-s.stopCleanup:
			return
		}
	}
}

func (s *Store) deleteExpired(now time.Time) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	states := make([]*session, 0, len(s.sessions))
	for _, state := range s.sessions {
		states = append(states, state)
	}
	s.mu.Unlock()
	for _, state := range states {
		state.mu.Lock()
		expired := !state.deleted && state.status != StatusCommitting && now.Sub(state.lastAccessedAt) > s.ttl
		if expired {
			state.deleted = true
			state.cancel()
			clearSessionData(state)
		}
		state.mu.Unlock()
		if expired {
			s.removeSession(state.id, state)
		}
	}
}

func snapshotOf(state *session) Snapshot {
	return Snapshot{
		ID: state.id, Kind: state.kind, Filename: state.filename, Grid: copyGrid(state.grid), Mapping: copyMapping(state.mapping),
		Rows: copyRows(state.rows), Selected: append([]bool(nil), state.selected...), GeocodeProgress: state.progress,
		Status: state.status, Failure: state.failure, CommitResult: state.commitResult,
	}
}

func clearSessionData(state *session) {
	state.filename = ""
	state.grid = Grid{}
	state.mapping = Mapping{}
	state.rows = nil
	state.selected = nil
}

func copyGrid(grid Grid) Grid {
	copy := Grid{
		Headers:  append([]string(nil), grid.Headers...),
		Warnings: append([]string(nil), grid.Warnings...),
		rows:     make([]gridRow, len(grid.rows)),
	}
	for i := range grid.rows {
		row := grid.rows[i]
		row.cells = append([]string(nil), row.cells...)
		row.errors = append([]string(nil), row.errors...)
		row.warnings = append([]string(nil), row.warnings...)
		copy.rows[i] = row
	}
	return copy
}

func copyMapping(mapping Mapping) Mapping {
	copy := mapping
	copy.Ambiguous = make(map[Field][]int, len(mapping.Ambiguous))
	for field, columns := range mapping.Ambiguous {
		copy.Ambiguous[field] = append([]int(nil), columns...)
	}
	copy.Ignored = append([]int(nil), mapping.Ignored...)
	return copy
}

func copyRows(rows []Row) []Row {
	copy := make([]Row, len(rows))
	for i := range rows {
		copy[i] = rows[i]
		copy[i].Errors = append([]string(nil), rows[i].Errors...)
		copy[i].Warnings = append([]string(nil), rows[i].Warnings...)
	}
	return copy
}

func newSessionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
