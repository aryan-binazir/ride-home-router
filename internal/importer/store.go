package importer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
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
	Created          int
	SkippedDuplicate int
	NotSelected      int
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
	commitStarted  bool
	createdAt      time.Time
	lastAccessedAt time.Time
	ctx            context.Context
	cancel         context.CancelFunc
	deleted        bool
	mu             sync.Mutex
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
	mu              sync.Mutex
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

// Create stages a parsed file for mapping. The generated 128-bit ID is also
// the consume-once commit token.
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

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return Snapshot{}, ErrStoreClosed
	}
	if len(s.sessions) >= MaxConcurrentSessions && !s.evictOldestLocked() {
		s.mu.Unlock()
		cancel()
		return Snapshot{}, ErrStoreFull
	}
	s.sessions[id] = state
	s.mu.Unlock()

	log.Printf("[IMPORT] Created staging session: kind=%s rows=%d", kind, grid.Len())
	return snapshotOf(state), nil
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
func (s *Store) ApplyMapping(id string, mapping Mapping) (Snapshot, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	defer state.mu.Unlock()
	if state.status != StatusMapping {
		return Snapshot{}, fmt.Errorf("%w: cannot apply mapping while %s", ErrInvalidSessionState, state.status)
	}

	existing, err := s.listExisting(state.ctx, state.kind)
	if err != nil {
		state.status = StatusFailed
		state.failure = "could not load the current roster"
		return snapshotOf(state), fmt.Errorf("load current roster for import: %w", err)
	}
	rows := Validate(&state.grid, mapping, state.kind, existing)
	groups := geocodeGroups(rows)
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

// Commit consumes the session token before attempting the atomic batch. A
// failed or retried commit can never create a second batch.
func (s *Store) Commit(ctx context.Context, id string, selected []bool) (CommitResult, error) {
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
	if len(selected) != len(state.rows) {
		state.mu.Unlock()
		return CommitResult{}, ErrInvalidSelection
	}
	state.selected = append([]bool(nil), selected...)
	state.status = StatusCommitting
	state.commitStarted = true
	rows := copyRows(state.rows)
	kind := state.kind
	sessionCtx := state.ctx
	state.mu.Unlock()

	commitCtx, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(sessionCtx, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()

	result, err := s.createBatch(commitCtx, kind, rows, selected)
	state.mu.Lock()
	defer state.mu.Unlock()
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
	log.Printf("[IMPORT] Committed batch: kind=%s created=%d skipped_duplicates=%d not_selected=%d", kind, result.Created, result.SkippedDuplicate, result.NotSelected)
	return result, nil
}

// Cancel removes a session immediately and cancels any running work.
func (s *Store) Cancel(id string) bool {
	s.mu.Lock()
	state := s.sessions[id]
	if state == nil {
		s.mu.Unlock()
		return false
	}
	delete(s.sessions, id)
	state.mu.Lock()
	s.mu.Unlock()
	state.deleted = true
	state.cancel()
	clearSessionData(state)
	state.mu.Unlock()
	return true
}

// Close stops cleanup, cancels all jobs, releases staged data, and waits for
// background jobs to observe cancellation.
func (s *Store) Close() {
	s.closeOnce.Do(func() {
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
			existing[i] = Existing{Name: rows[i].Name, Address: rows[i].Address, Lat: rows[i].Lat, Lng: rows[i].Lng}
		}
		return existing, nil
	case KindDriver:
		rows, err := s.db.Drivers().List(ctx, "")
		if err != nil {
			return nil, err
		}
		existing := make([]Existing, len(rows))
		for i := range rows {
			existing[i] = Existing{Name: rows[i].Name, Address: rows[i].Address, Lat: rows[i].Lat, Lng: rows[i].Lng}
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
		allowExistingDuplicate := make([]bool, len(indices))
		for i, rowIndex := range indices {
			row := rows[rowIndex]
			batch[i] = &models.Participant{Name: row.Name, Address: row.Address, AddressName: row.AddressName, Lat: row.Lat, Lng: row.Lng}
			allowExistingDuplicate[i] = row.DuplicateOfExisting
		}
		batchResult, err := s.db.Participants().CreateBatch(ctx, batch, allowExistingDuplicate)
		if err != nil {
			return CommitResult{}, fmt.Errorf("create participant import batch: %w", err)
		}
		result.Created = batchResult.Created
		result.SkippedDuplicate = batchResult.SkippedDuplicate
	case KindDriver:
		batch := make([]*models.Driver, len(indices))
		allowExistingDuplicate := make([]bool, len(indices))
		for i, rowIndex := range indices {
			row := rows[rowIndex]
			batch[i] = &models.Driver{Name: row.Name, Address: row.Address, AddressName: row.AddressName, Lat: row.Lat, Lng: row.Lng, VehicleCapacity: row.Capacity}
			allowExistingDuplicate[i] = row.DuplicateOfExisting
		}
		batchResult, err := s.db.Drivers().CreateBatch(ctx, batch, allowExistingDuplicate)
		if err != nil {
			return CommitResult{}, fmt.Errorf("create driver import batch: %w", err)
		}
		result.Created = batchResult.Created
		result.SkippedDuplicate = batchResult.SkippedDuplicate
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
		selected[i] = len(rows[i].Errors) == 0 && !rows[i].DuplicateInFile && !rows[i].DuplicateOfExisting
	}
	return selected
}

func (s *Store) lockSession(id string) (*session, error) {
	s.mu.Lock()
	state := s.sessions[id]
	if state == nil {
		s.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	state.mu.Lock()
	if state.deleted {
		state.mu.Unlock()
		s.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	now := s.now()
	if now.Sub(state.lastAccessedAt) > s.ttl {
		delete(s.sessions, id)
		state.deleted = true
		state.cancel()
		clearSessionData(state)
		state.mu.Unlock()
		s.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	state.lastAccessedAt = now
	s.mu.Unlock()
	return state, nil
}

func (s *Store) evictOldestLocked() bool {
	var oldest *session
	for _, state := range s.sessions {
		state.mu.Lock()
		eligible := !state.deleted && state.status != StatusCommitting
		if eligible && (oldest == nil || state.lastAccessedAt.Before(oldest.lastAccessedAt)) {
			if oldest != nil {
				oldest.mu.Unlock()
			}
			oldest = state
			continue
		}
		state.mu.Unlock()
	}
	if oldest == nil {
		return false
	}
	delete(s.sessions, oldest.id)
	oldest.deleted = true
	oldest.cancel()
	clearSessionData(oldest)
	oldest.mu.Unlock()
	return true
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, state := range s.sessions {
		if !state.mu.TryLock() {
			continue
		}
		if !state.deleted && now.Sub(state.lastAccessedAt) > s.ttl {
			delete(s.sessions, id)
			state.deleted = true
			state.cancel()
			clearSessionData(state)
		}
		state.mu.Unlock()
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
	copy := Grid{Headers: append([]string(nil), grid.Headers...), rows: make([]gridRow, len(grid.rows))}
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
