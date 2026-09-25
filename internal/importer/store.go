package importer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

	defaultSessionTTL    = 30 * time.Minute
	defaultCommitTimeout = 60 * time.Second
	geocodeMaxRetries    = 3
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
	// Guessed counts written rows whose address the geocoder only guessed.
	Guessed int
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

// ProgressSnapshot holds only the counts and state needed by the progress panel.
type ProgressSnapshot struct {
	ID                      string
	Status                  Status
	GeocodeProgress         GeocodeProgress
	RowCount, SelectedCount int
}

// Store coordinates durable import staging and its background geocoding worker.
type Store struct {
	records      database.WorkflowRepository
	durableJobs  database.ImportJobRepository
	workerCancel context.CancelFunc
	workerDone   chan struct{}
	workerWake   chan struct{}
	geocoder     geocoding.Geocoder
	db           database.DataStore
	ttl          time.Duration
	closeOnce    sync.Once
}

// Commit atomically writes selected rows and consumes the import token.
func (s *Store) Commit(ctx context.Context, id string, selection []bool) (CommitResult, error) {
	return s.commitPersistent(ctx, id, selection, nil)
}

// Close stops the background worker after its active geocoding call returns.
func (s *Store) Close() {
	s.closeOnce.Do(func() { s.workerCancel(); <-s.workerDone })
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

func createBatch(ctx context.Context, kind Kind, rows []Row, selected []bool, w database.WorkflowWrites) (CommitResult, error) {
	result := CommitResult{NotSelected: len(rows)}
	indices := make([]int, 0, len(rows))
	for i := range rows {
		if !selected[i] || len(rows[i].Errors) > 0 || !rows[i].HasCoordinates {
			continue
		}
		indices = append(indices, i)
	}
	result.NotSelected -= len(indices)
	for _, rowIndex := range indices {
		if rows[rowIndex].AddressGuessed {
			result.Guessed++
		}
	}

	switch kind {
	case KindParticipant:
		batch := make([]*models.Participant, len(indices))
		for i, rowIndex := range indices {
			row := rows[rowIndex]
			batch[i] = &models.Participant{
				Name: row.Name, Address: row.Address, AddressName: row.AddressName, Lat: row.Lat, Lng: row.Lng, GeocodedAt: row.GeocodedAt,
				MatchedAddress: row.MatchedAddress, AddressMatch: models.AddressMatchFor(row.AddressGuessed),
			}
		}
		batchResult, err := w.UpsertParticipants(ctx, batch)
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
			batch[i] = &models.Driver{
				Name: row.Name, Address: row.Address, AddressName: row.AddressName, Lat: row.Lat, Lng: row.Lng, GeocodedAt: row.GeocodedAt, VehicleCapacity: capacity,
				MatchedAddress: row.MatchedAddress, AddressMatch: models.AddressMatchFor(row.AddressGuessed),
			}
		}
		batchResult, err := w.UpsertDrivers(ctx, batch)
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

func defaultSelections(rows []Row) []bool {
	selected := make([]bool, len(rows))
	for i := range rows {
		selected[i] = len(rows[i].Errors) == 0
	}
	return selected
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

func newSessionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// geocodeFailureMessage preserves actionable setup failures without exposing provider details.
func geocodeFailureMessage(err error) string {
	if errors.Is(err, database.ErrUsageExhausted) {
		return "The app's Google usage limit has been reached. Ask an administrator to check usage before uploading the file again."
	}
	if failure, ok := errors.AsType[*geocoding.ErrGeocodingFailed](err); errors.Is(err, geocoding.ErrNotConfigured) || ok && failure.Configuration {
		return "Google address lookup is unavailable. Ask an administrator to check the saved key, Geocoding API permissions and billing, then upload the file again."
	}
	return "address could not be geocoded"
}
