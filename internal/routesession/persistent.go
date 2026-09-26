package routesession

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"time"
)

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
	Note     string `json:",omitempty"`
}

func encodeState(state *session) ([]byte, error) {
	return json.Marshal(persistedState{state.originalRoutes, state.currentRoutes, state.dirtyRouteIndexes, state.selectedDrivers, state.driverOrgVehicles, state.activityLocation, state.useMiles, state.routeTime, state.mode, state.reviewerNote})
}

func (s *Store) decodeState(ctx context.Context, id string, data []byte) (*session, error) {
	var p persistedState
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Dirty == nil {
		p.Dirty = make(map[int]struct{})
	}
	state := &session{id: id, originalRoutes: p.Original, currentRoutes: p.Current, dirtyRouteIndexes: p.Dirty, selectedDrivers: p.Drivers, driverOrgVehicles: p.Vehicles, activityLocation: p.Location, useMiles: p.UseMiles, routeTime: p.Time, mode: p.Mode, reviewerNote: p.Note, lastAccessedAt: s.now()}
	s.estimateStoredRoutes(ctx, state)
	return state, nil
}

func (s *Store) estimateStoredRoutes(ctx context.Context, state *session) {
	estimator, ok := s.distanceCalc.(interface{ NoPrewarm() bool })
	if !ok || !estimator.NoPrewarm() {
		return
	}
	for _, routes := range [][]models.CalculatedRoute{state.originalRoutes, state.currentRoutes} {
		for i := range routes {
			if !routeMetricsEstimated(ctx, s.distanceCalc, state, &routes[i]) {
				routing.ZeroRouteMetrics(&routes[i])
			}
		}
	}
}

func routeMetricsEstimated(ctx context.Context, calc distance.Lookup, state *session, route *models.CalculatedRoute) bool {
	if state.activityLocation == nil || route.Driver == nil {
		return false
	}
	return routing.PopulateRouteMetrics(ctx, calc, state.activityLocation.GetCoords(), state.mode, route) == nil
}

func NewPersistentStore(calc distance.Lookup, records database.WorkflowRepository) *Store {
	if records == nil {
		panic("routesession: workflow repository is required")
	}
	return &Store{distanceCalc: calc, records: records, ttl: defaultTTL, now: time.Now}
}

func (s *Store) CreateContext(ctx context.Context, input CreateInput) (Snapshot, error) {
	state := newSession(input, s.now())
	snapshot := snapshotWithChanges(state, changedRoutes(nil, state.currentRoutes))
	if s.records == nil {
		s.insert(state)
		log.Printf("[SESSION] Created route session: id=%s routes=%d drivers=%d mode=%s", state.id, len(input.Routes), len(input.SelectedDrivers), input.Mode)
		return snapshot, nil
	}
	log.Printf("[SESSION] Created route session: id=%s routes=%d drivers=%d mode=%s", state.id, len(input.Routes), len(input.SelectedDrivers), input.Mode)
	data, err := encodeState(state)
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
		state, err := s.lockSession(id)
		if errors.Is(err, ErrNotFound) {
			return Snapshot{}, false, nil
		}
		if err != nil {
			return Snapshot{}, false, err
		}
		defer state.mu.Unlock()
		return snapshotOf(state), true, nil
	}
	record, err := s.records.Load(ctx, "route", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) || record.Consumed {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	state, err := s.decodeState(ctx, id, record.Data)
	if err != nil {
		return Snapshot{}, false, err
	}
	return snapshotOf(state), true, nil
}

func (s *Store) update(ctx context.Context, id string, mutate func(*session) (Snapshot, error)) (Snapshot, error) {
	if s.records == nil {
		state, err := s.lockSession(id)
		if err != nil {
			return Snapshot{}, err
		}
		defer state.mu.Unlock()
		return mutate(state)
	}
	record, err := s.records.Load(ctx, "route", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) || record.Consumed {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, err
	}
	state, err := s.decodeState(ctx, id, record.Data)
	if err != nil {
		return Snapshot{}, err
	}
	result, err := mutate(state)
	if err != nil {
		return Snapshot{}, err
	}
	data, err := encodeState(state)
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
	return s.update(ctx, id, func(state *session) (Snapshot, error) {
		before := routeIdentity(state.currentRoutes)
		state.currentRoutes = copyRoutes(state.originalRoutes)
		state.dirtyRouteIndexes = make(map[int]struct{})
		return snapshotWithChanges(state, changedRoutes(before, state.currentRoutes)), nil
	})
}

func (s *Store) DeleteContext(ctx context.Context, id string) error {
	if s.records == nil {
		s.deleteMemory(id)
		return nil
	}
	return s.records.Delete(ctx, "route", id)
}

// CommitEvent atomically creates the event and consumes the route session.
// The writer belongs to this transaction and must not escape the callback.
// The callback must not call Store methods: memory sessions remain locked until
// it returns, and lock waiters cannot cancel. Memory callbacks receive a nil writer.
func (s *Store) CommitEvent(ctx context.Context, id string, persist func(context.Context, CommitSnapshot, database.WorkflowWrites) error) error {
	if s.records == nil {
		return s.commitMemory(ctx, id, persist)
	}
	err := s.records.Transact(ctx, "route", id, s.ttl, func(record *database.WorkflowRecord, w database.WorkflowWrites) error {
		if record.Consumed {
			return ErrAlreadyCommitted
		}
		state, err := s.decodeState(ctx, id, record.Data)
		if err != nil {
			return err
		}
		snapshot, err := commitSnapshot(state)
		if err != nil {
			return err
		}
		if err := persist(ctx, snapshot, w); err != nil {
			return err
		}
		record.Consumed = true
		record.Data = []byte(`{}`)
		return nil
	})
	if errors.Is(err, database.ErrNotFound) {
		return ErrNotFound
	}
	if err == nil {
		log.Printf("[SESSION] Deleted route session: id=%s", id)
	}
	return err
}
