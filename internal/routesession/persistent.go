package routesession

import (
	"context"
	"encoding/json"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"time"
)

// persistedState is versioned by the database migration that introduces it.
// A request loads its own copy; no application instance owns a session.
type persistedState struct {
	Original []models.CalculatedRoute
	Current  []models.CalculatedRoute
	Dirty    map[int]struct{}
	Drivers  []models.Driver
	Vehicles map[int64]*models.OrganizationVehicle
	Location *models.ActivityLocation
	UseMiles bool
	Time     string
	Mode     models.RouteMode
}

func encodeState(state *session) ([]byte, error) {
	return json.Marshal(persistedState{state.originalRoutes, state.currentRoutes, state.dirtyRouteIndexes, state.selectedDrivers, state.driverOrgVehicles, state.activityLocation, state.useMiles, state.routeTime, state.mode})
}

func (s *Store) engine(id string, data []byte) (*Store, error) {
	var p persistedState
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Dirty == nil {
		p.Dirty = make(map[int]struct{})
	}
	state := &session{id: id, originalRoutes: p.Original, currentRoutes: p.Current, dirtyRouteIndexes: p.Dirty, selectedDrivers: p.Drivers, driverOrgVehicles: p.Vehicles, activityLocation: p.Location, useMiles: p.UseMiles, routeTime: p.Time, mode: p.Mode, lastAccessedAt: s.now()}
	return &Store{distanceCalc: s.distanceCalc, sessions: map[string]*session{id: state}, committed: make(map[string]time.Time), ttl: s.ttl, now: s.now}, nil
}

func NewPersistentStore(calc distance.Lookup, records database.WorkflowRepository) *Store {
	if records == nil {
		panic("routesession: workflow repository is required")
	}
	return &Store{distanceCalc: calc, records: records, ttl: defaultTTL, now: time.Now}
}

func (s *Store) CreateContext(ctx context.Context, input CreateInput) (Snapshot, error) {
	if s.records == nil {
		return s.Create(input), nil
	}
	engine := &Store{distanceCalc: s.distanceCalc, sessions: make(map[string]*session), committed: make(map[string]time.Time), ttl: s.ttl, now: s.now}
	snapshot := engine.Create(input)
	data, err := encodeState(engine.sessions[snapshot.ID])
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.records.Create(ctx, "route", snapshot.ID, data, s.ttl); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Store) Load(ctx context.Context, id string) (Snapshot, bool, error) {
	if s.records == nil {
		snapshot, ok := s.Snapshot(id)
		return snapshot, ok, nil
	}
	record, err := s.records.Load(ctx, "route", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) || record.Consumed {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	engine, err := s.engine(id, record.Data)
	if err != nil {
		return Snapshot{}, false, err
	}
	return snapshotOf(engine.sessions[id]), true, nil
}

func (s *Store) change(ctx context.Context, id string, mutate func(*Store) (Snapshot, error)) (Snapshot, error) {
	record, err := s.records.Load(ctx, "route", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) || record.Consumed {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, err
	}
	engine, err := s.engine(id, record.Data)
	if err != nil {
		return Snapshot{}, err
	}
	result, err := mutate(engine)
	if err != nil {
		return Snapshot{}, err
	}
	data, err := encodeState(engine.sessions[id])
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.records.CompareAndSwap(ctx, "route", id, record.Revision, data, s.ttl); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, err
	}
	return result, nil
}

func (s *Store) ResetContext(ctx context.Context, id string) (Snapshot, error) {
	if s.records == nil {
		return s.Reset(id)
	}
	return s.change(ctx, id, func(engine *Store) (Snapshot, error) { return engine.Reset(id) })
}

func (s *Store) DeleteContext(ctx context.Context, id string) error {
	if s.records == nil {
		s.Delete(id)
		return nil
	}
	return s.records.Delete(ctx, "route", id)
}

// CommitEvent atomically creates the event and consumes the route session.
// The writer belongs to this transaction and must not escape the callback.
func (s *Store) CommitEvent(ctx context.Context, id string, persist func(context.Context, CommitSnapshot, database.WorkflowWrites) error) error {
	if s.records == nil {
		return s.Commit(ctx, id, func(ctx context.Context, snapshot CommitSnapshot) error { return persist(ctx, snapshot, nil) })
	}
	err := s.records.Transact(ctx, "route", id, s.ttl, func(record *database.WorkflowRecord, w database.WorkflowWrites) error {
		if record.Consumed {
			return ErrAlreadyCommitted
		}
		engine, err := s.engine(id, record.Data)
		if err != nil {
			return err
		}
		err = engine.Commit(ctx, id, func(ctx context.Context, snapshot CommitSnapshot) error { return persist(ctx, snapshot, w) })
		if err != nil {
			return err
		}
		record.Consumed = true
		record.Data = []byte(`{}`)
		return nil
	})
	if errors.Is(err, database.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
