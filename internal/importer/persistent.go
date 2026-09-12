package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"time"
)

type storedGridRow struct {
	SourceRow               int
	Cells, Errors, Warnings []string
	XLSX                    bool
}
type storedGrid struct {
	Headers, Warnings []string
	Rows              []storedGridRow
}

func storeGrid(g Grid) *storedGrid {
	p := &storedGrid{Headers: g.Headers, Warnings: g.Warnings, Rows: make([]storedGridRow, len(g.rows))}
	for i, r := range g.rows {
		p.Rows[i] = storedGridRow{r.sourceRow, r.cells, r.errors, r.warnings, r.xlsx}
	}
	return p
}

func (p *storedGrid) grid() Grid {
	if p == nil {
		return Grid{}
	}
	g := Grid{Headers: p.Headers, Warnings: p.Warnings, rows: make([]gridRow, len(p.Rows))}
	for i, r := range p.Rows {
		g.rows[i] = gridRow{sourceRow: r.SourceRow, cells: r.Cells, errors: r.Errors, warnings: r.Warnings, xlsx: r.XLSX}
	}
	return g
}

type importHeader struct {
	Kind     Kind
	Filename string
	Grid     *storedGrid `json:",omitempty"`
	Mapping  Mapping
	Status   Status
	Failure  string
	Result   CommitResult
}

func NewPersistentStore(parent context.Context, g geocoding.Geocoder, db database.DataStore, records database.WorkflowRepository, jobs database.ImportJobRepository) *Store {
	if records == nil || jobs == nil {
		panic("importer: shared workflow storage is required")
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s := &Store{geocoder: g, db: db, records: records, durableJobs: jobs, ttl: defaultSessionTTL, now: time.Now, workerCancel: cancel, workerDone: make(chan struct{})}
	go s.durableWorker(ctx)
	return s
}

func (s *Store) CreateContext(ctx context.Context, kind Kind, filename string, grid *Grid) (Snapshot, error) {
	if s.records == nil {
		return s.Create(kind, filename, grid)
	}
	select {
	case <-s.workerDone:
		return Snapshot{}, ErrStoreClosed
	default:
	}
	if grid == nil {
		return Snapshot{}, errors.New("import grid is required")
	}
	if kind != KindParticipant && kind != KindDriver {
		return Snapshot{}, errors.New("unsupported roster kind")
	}
	id, err := newSessionID()
	if err != nil {
		return Snapshot{}, err
	}
	header := importHeader{Kind: kind, Filename: filename, Grid: storeGrid(*grid), Mapping: AutoMap(grid.Headers), Status: StatusMapping}
	data, err := json.Marshal(header)
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.records.Create(ctx, "import", id, data, s.ttl); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{ID: id, Kind: kind, Filename: filename, Grid: copyGrid(*grid), Mapping: header.Mapping, Status: StatusMapping}, nil
}

func (s *Store) Load(ctx context.Context, id string) (Snapshot, bool, error) {
	if s.records == nil {
		snapshot, ok := s.Snapshot(id)
		return snapshot, ok, nil
	}
	record, err := s.records.Load(ctx, "import", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	var header importHeader
	if err = json.Unmarshal(record.Data, &header); err != nil {
		return Snapshot{}, false, err
	}
	snapshot := Snapshot{ID: id, Kind: header.Kind, Filename: header.Filename, Grid: header.Grid.grid(), Mapping: header.Mapping, Status: header.Status, Failure: header.Failure, CommitResult: header.Result}
	stored, err := s.durableJobs.Rows(ctx, id)
	if err != nil {
		return Snapshot{}, false, err
	}
	snapshot.Rows, snapshot.Selected, err = decodeImportRows(stored)
	if err != nil {
		return Snapshot{}, false, err
	}
	done, total, err := s.durableJobs.Progress(ctx, id)
	if err != nil {
		return Snapshot{}, false, err
	}
	snapshot.GeocodeProgress = GeocodeProgress{Done: done, Total: total, Running: done < total}
	return snapshot, true, nil
}

func decodeImportRows(stored []database.ImportRow) ([]Row, []bool, error) {
	rows := make([]Row, len(stored))
	selected := make([]bool, len(stored))
	for i, r := range stored {
		if r.Index != i {
			return nil, nil, errors.New("invalid import row sequence")
		}
		if err := json.Unmarshal(r.Data, &rows[i]); err != nil {
			return nil, nil, err
		}
		selected[i] = r.Selected
	}
	return rows, selected, nil
}

func (s *Store) applyMappingPersistent(ctx context.Context, id string, mapping Mapping) (Snapshot, error) {
	record, err := s.records.Load(ctx, "import", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) {
		return Snapshot{}, ErrSessionNotFound
	}
	if err != nil {
		return Snapshot{}, err
	}
	var header importHeader
	if err = json.Unmarshal(record.Data, &header); err != nil {
		return Snapshot{}, err
	}
	if header.Status != StatusMapping {
		return Snapshot{}, ErrInvalidSessionState
	}
	existing, err := s.listExisting(ctx, header.Kind)
	if err != nil {
		return Snapshot{}, err
	}
	grid := header.Grid.grid()
	rows := Validate(&grid, mapping, header.Kind, existing)
	groups := geocodeGroups(rows)
	if len(groups) > MaxGeocodeAddresses {
		return Snapshot{}, ErrTooManyGeocodeAddresses
	}
	stored := make([]database.ImportRow, len(rows))
	selections := defaultSelections(rows)
	for i, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			return Snapshot{}, err
		}
		stored[i] = database.ImportRow{Index: i, Data: data, Selected: selections[i]}
	}
	jobs := make([]database.ImportJob, len(groups))
	for i, g := range groups {
		jobs[i] = database.ImportJob{SessionID: id, Index: i, Address: g.address, Rows: g.rows}
	}
	err = s.records.Transact(ctx, "import", id, s.ttl, func(current *database.WorkflowRecord, w database.WorkflowWrites) error {
		if current.Revision != record.Revision || current.Consumed {
			return database.ErrWorkflowConflict
		}
		if err := w.StageImport(ctx, id, stored, jobs); err != nil {
			return err
		}
		header.Grid = nil
		header.Mapping = copyMapping(mapping)
		header.Status = StatusPreviewing
		data, err := json.Marshal(header)
		current.Data = data
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	result, _, err := s.Load(ctx, id)
	return result, err
}

func (s *Store) SelectRowsContext(ctx context.Context, id string, selected []bool) (Snapshot, error) {
	if s.records == nil {
		return s.SelectRows(id, selected)
	}
	err := s.records.Transact(ctx, "import", id, s.ttl, func(record *database.WorkflowRecord, w database.WorkflowWrites) error {
		var h importHeader
		if err := json.Unmarshal(record.Data, &h); err != nil {
			return err
		}
		if h.Status != StatusPreviewing || record.Consumed {
			return ErrInvalidSessionState
		}
		return w.SelectImportRows(ctx, id, selected)
	})
	if err != nil {
		return Snapshot{}, err
	}
	result, _, err := s.Load(ctx, id)
	return result, err
}

func (s *Store) commitPersistent(ctx context.Context, id string, selection []bool) (CommitResult, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCommitTimeout)
	defer cancel()
	var result CommitResult
	err := s.records.Transact(ctx, "import", id, s.ttl, func(record *database.WorkflowRecord, w database.WorkflowWrites) error {
		if record.Consumed {
			return ErrCommitConsumed
		}
		var h importHeader
		if err := json.Unmarshal(record.Data, &h); err != nil {
			return err
		}
		if h.Status != StatusPreviewing {
			return ErrInvalidSessionState
		}
		stored, err := w.ImportRows(ctx, id)
		if err != nil {
			return err
		}
		rows, selected, err := decodeImportRows(stored)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.NeedsGeocoding {
				return ErrGeocodingInProgress
			}
		}
		if selection != nil {
			if len(selection) != len(rows) {
				return ErrInvalidSelection
			}
			selected = selection
		}
		result, err = s.createBatchWithWriter(ctx, h.Kind, rows, selected, w)
		if err != nil {
			return err
		}
		if err = w.ClearImportRows(ctx, id); err != nil {
			return err
		}
		h.Status = StatusCommitted
		h.Result = result
		record.Consumed = true
		data, err := json.Marshal(h)
		record.Data = data
		return err
	})
	if errors.Is(err, database.ErrNotFound) {
		return CommitResult{}, ErrSessionNotFound
	}
	return result, err
}

func (s *Store) CancelContext(ctx context.Context, id string) (bool, error) {
	if s.records == nil {
		return s.Cancel(id), nil
	}
	_, err := s.records.Load(ctx, "import", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = s.records.Delete(ctx, "import", id)
	return err == nil, err
}

func (s *Store) durableWorker(ctx context.Context) {
	defer close(s.workerDone)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		token, err := newSessionID()
		if err != nil {
			log.Printf("[IMPORT] Generate claim token: %v", err)
			continue
		}
		job, ok, err := s.durableJobs.Claim(ctx, token, time.Minute)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[IMPORT] Claim job: %v", err)
			continue
		}
		if !ok {
			continue
		}
		if err = s.processJob(ctx, job); err != nil && !errors.Is(err, database.ErrNotFound) && !errors.Is(err, database.ErrWorkflowConflict) && ctx.Err() == nil {
			log.Printf("[IMPORT] Process job: %v", err)
		}
	}
}

func (s *Store) processJob(ctx context.Context, job database.ImportJob) error {
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := s.durableJobs.Release(releaseCtx, job); err != nil {
			log.Printf("[IMPORT] Release job: %v", err)
		}
	}()
	workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := s.geocoder.GeocodeWithRetry(workCtx, job.Address, geocodeMaxRetries)
	if workCtx.Err() != nil {
		return workCtx.Err()
	}
	failed := err != nil || result == nil || !validCoordinatePair(result.Coords.Lat, result.Coords.Lng)
	stored, err := s.durableJobs.Rows(workCtx, job.SessionID)
	if err != nil {
		return err
	}
	updated := make([]database.ImportRow, 0, len(job.Rows))
	for _, index := range job.Rows {
		if index < 0 || index >= len(stored) {
			return fmt.Errorf("invalid geocode row index")
		}
		var row Row
		if err = json.Unmarshal(stored[index].Data, &row); err != nil {
			return err
		}
		row.NeedsGeocoding = false
		if failed {
			row.addError("address could not be geocoded")
		} else {
			row.Lat = result.Coords.Lat
			row.Lng = result.Coords.Lng
			row.HasCoordinates = true
		}
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		updated = append(updated, database.ImportRow{Index: index, Data: data})
	}
	return s.durableJobs.Finish(workCtx, job, updated, failed)
}
