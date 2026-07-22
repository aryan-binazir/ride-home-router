package routesession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"sync"
	"time"
)

const (
	defaultTTL             = 2 * time.Hour
	defaultCleanupInterval = 15 * time.Minute
)

var (
	ErrNotFound               = errors.New("route session not found")
	ErrInvalidRouteIndex      = errors.New("invalid route index")
	ErrParticipantNotFound    = errors.New("participant not found")
	ErrParticipantNotInSource = errors.New("participant not found in source route")
	ErrSwapMissingDriver      = errors.New("cannot swap - route is missing a driver")
	ErrSwapCapacity           = errors.New("cannot swap - capacity constraints violated")
	ErrDriverNotSelected      = errors.New("driver not found in selected drivers")
	ErrDriverAlreadyInRoutes  = errors.New("driver is already in routes")
	ErrUnbalanced             = errors.New("routes must be balanced before saving")
)

type Move struct {
	ParticipantID    int64
	FromRouteIndex   int
	ToRouteIndex     int
	InsertAtPosition int
}

type ApplyMovesOptions struct {
	RequireClaimedSource bool
}

type CreateInput struct {
	Routes            []models.CalculatedRoute
	SelectedDrivers   []models.Driver
	ActivityLocation  *models.ActivityLocation
	UseMiles          bool
	RouteTime         string
	Mode              models.RouteMode
	DriverOrgVehicles map[int64]*models.OrganizationVehicle
}

type Snapshot struct {
	ID               string
	Routes           []models.CalculatedRoute
	Summary          models.RoutingSummary
	ActivityLocation *models.ActivityLocation
	UseMiles         bool
	RouteTime        string
	Mode             models.RouteMode
	UnusedDrivers    []models.Driver
	IsEditing        bool
	OverCapacity     []bool
	IsOutOfBalance   bool
}

type session struct {
	id                string
	originalRoutes    []models.CalculatedRoute
	currentRoutes     []models.CalculatedRoute
	dirtyRouteIndexes map[int]struct{}
	selectedDrivers   []models.Driver
	driverOrgVehicles map[int64]*models.OrganizationVehicle
	activityLocation  *models.ActivityLocation
	useMiles          bool
	routeTime         string
	mode              models.RouteMode
	lastAccessedAt    time.Time
	deleted           bool
	mu                sync.Mutex
}

type Store struct {
	distanceCalc    distance.DistanceCalculator
	sessions        map[string]*session
	mu              sync.Mutex
	ttl             time.Duration
	cleanupInterval time.Duration
	now             func() time.Time
	stopCleanup     chan struct{}
	cleanupDone     chan struct{}
	closeOnce       sync.Once
}

func NewStore(distanceCalc distance.DistanceCalculator) *Store {
	return newStore(distanceCalc, defaultTTL, defaultCleanupInterval, time.Now)
}

func newStore(distanceCalc distance.DistanceCalculator, ttl, cleanupInterval time.Duration, now func() time.Time) *Store {
	store := &Store{
		distanceCalc: distanceCalc, sessions: make(map[string]*session), ttl: ttl,
		cleanupInterval: cleanupInterval, now: now,
		stopCleanup: make(chan struct{}), cleanupDone: make(chan struct{}),
	}
	go store.cleanupLoop()
	return store
}

func (s *Store) Create(input CreateInput) Snapshot {
	state := &session{
		id:                generateID(),
		originalRoutes:    copyRoutes(input.Routes),
		currentRoutes:     copyRoutes(input.Routes),
		dirtyRouteIndexes: make(map[int]struct{}),
		selectedDrivers:   append([]models.Driver(nil), input.SelectedDrivers...),
		driverOrgVehicles: copyVehicles(input.DriverOrgVehicles),
		activityLocation:  copyLocation(input.ActivityLocation),
		useMiles:          input.UseMiles,
		routeTime:         input.RouteTime,
		mode:              input.Mode,
		lastAccessedAt:    s.now(),
	}
	s.mu.Lock()
	s.sessions[state.id] = state
	s.mu.Unlock()
	log.Printf("[SESSION] Created route session: id=%s routes=%d drivers=%d mode=%s", state.id, len(input.Routes), len(input.SelectedDrivers), input.Mode)
	return snapshotOf(state)
}

func (s *Store) Snapshot(id string) (Snapshot, bool) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, false
	}
	defer state.mu.Unlock()
	return snapshotOf(state), true
}

func (s *Store) ApplyMoves(ctx context.Context, id string, moves []Move, options ApplyMovesOptions) (Snapshot, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	defer state.mu.Unlock()

	backupRoutes := copyRoutes(state.currentRoutes)
	backupDirty := copyDirty(state.dirtyRouteIndexes)
	rollback := func() { state.currentRoutes = backupRoutes; state.dirtyRouteIndexes = backupDirty }
	for _, move := range moves {
		from, ok := findParticipant(state.currentRoutes, move.ParticipantID)
		if !ok {
			rollback()
			return Snapshot{}, ErrParticipantNotFound
		}
		if options.RequireClaimedSource && from != move.FromRouteIndex {
			rollback()
			return Snapshot{}, ErrParticipantNotInSource
		}
		if err := applyMove(state, move, from); err != nil {
			rollback()
			return Snapshot{}, err
		}
		_, unbalanced := capacityState(state.currentRoutes)
		if !unbalanced {
			if err := s.recalculateDirty(ctx, state); err != nil {
				rollback()
				return Snapshot{}, err
			}
		}
	}
	return snapshotOf(state), nil
}

func (s *Store) SwapDrivers(ctx context.Context, id string, first, second int) (Snapshot, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	defer state.mu.Unlock()
	if first < 0 || first >= len(state.currentRoutes) || second < 0 || second >= len(state.currentRoutes) {
		return Snapshot{}, ErrInvalidRouteIndex
	}
	backup := copyRoutes(state.currentRoutes)
	route1, route2 := &state.currentRoutes[first], &state.currentRoutes[second]
	cap1, ok := routeCapacity(*route1)
	if !ok {
		return Snapshot{}, ErrSwapMissingDriver
	}
	cap2, ok := routeCapacity(*route2)
	if !ok {
		return Snapshot{}, ErrSwapMissingDriver
	}
	if len(route1.Stops) > cap2 || len(route2.Stops) > cap1 {
		return Snapshot{}, ErrSwapCapacity
	}
	route1.Driver, route2.Driver = route2.Driver, route1.Driver
	if err := s.recalculateRoute(ctx, state, route1); err != nil {
		state.currentRoutes = backup
		return Snapshot{}, err
	}
	if err := s.recalculateRoute(ctx, state, route2); err != nil {
		state.currentRoutes = backup
		return Snapshot{}, err
	}
	return snapshotOf(state), nil
}

func (s *Store) Reset(id string) (Snapshot, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	defer state.mu.Unlock()
	state.currentRoutes = copyRoutes(state.originalRoutes)
	state.dirtyRouteIndexes = make(map[int]struct{})
	return snapshotOf(state), nil
}

func (s *Store) AddDriver(ctx context.Context, id string, driverID int64) (Snapshot, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	defer state.mu.Unlock()
	var driver *models.Driver
	for i := range state.selectedDrivers {
		if state.selectedDrivers[i].ID == driverID {
			driver = copyDriver(&state.selectedDrivers[i])
			break
		}
	}
	if driver == nil {
		return Snapshot{}, ErrDriverNotSelected
	}
	for _, route := range state.currentRoutes {
		if route.Driver != nil && route.Driver.ID == driverID {
			return Snapshot{}, ErrDriverAlreadyInRoutes
		}
	}
	newRoute := models.CalculatedRoute{Driver: driver, Stops: []models.RouteStop{}, EffectiveCapacity: driver.VehicleCapacity, Mode: state.mode}
	if vehicle := state.driverOrgVehicles[driverID]; vehicle != nil {
		newRoute.OrgVehicleID, newRoute.OrgVehicleName, newRoute.EffectiveCapacity = vehicle.ID, vehicle.Name, vehicle.Capacity
	}
	if err := s.recalculateRoute(ctx, state, &newRoute); err != nil {
		return Snapshot{}, err
	}
	state.currentRoutes = append(state.currentRoutes, newRoute)
	return snapshotOf(state), nil
}

func (s *Store) SaveSnapshot(id string) (models.RoutingResult, error) {
	state, err := s.lockSession(id)
	if err != nil {
		return models.RoutingResult{}, err
	}
	defer state.mu.Unlock()
	_, unbalanced := capacityState(state.currentRoutes)
	if unbalanced {
		return models.RoutingResult{}, ErrUnbalanced
	}
	return models.RoutingResult{Routes: copyRoutes(state.currentRoutes), Summary: calculateSummary(state.currentRoutes), Mode: state.mode}, nil
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	state := s.sessions[id]
	s.mu.Unlock()
	if state != nil {
		state.mu.Lock()
		state.deleted = true
		state.mu.Unlock()
		s.remove(id, state)
	}
	log.Printf("[SESSION] Deleted route session: id=%s", id)
}

func (s *Store) Close() { s.closeOnce.Do(func() { close(s.stopCleanup); <-s.cleanupDone }) }

func (s *Store) lockSession(id string) (*session, error) {
	s.mu.Lock()
	state := s.sessions[id]
	s.mu.Unlock()
	if state == nil {
		return nil, ErrNotFound
	}
	state.mu.Lock()
	if state.deleted {
		state.mu.Unlock()
		return nil, ErrNotFound
	}
	now := s.now()
	if now.Sub(state.lastAccessedAt) > s.ttl {
		state.deleted = true
		state.mu.Unlock()
		s.remove(id, state)
		return nil, ErrNotFound
	}
	state.lastAccessedAt = now
	return state, nil
}

func (s *Store) remove(id string, state *session) {
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
	type candidate struct {
		id    string
		state *session
	}
	s.mu.Lock()
	states := make([]candidate, 0, len(s.sessions))
	for id, state := range s.sessions {
		states = append(states, candidate{id: id, state: state})
	}
	s.mu.Unlock()
	for _, candidate := range states {
		id, state := candidate.id, candidate.state
		if !state.mu.TryLock() {
			continue
		}
		expired := !state.deleted && now.Sub(state.lastAccessedAt) > s.ttl
		if expired {
			state.deleted = true
		}
		state.mu.Unlock()
		if expired {
			s.remove(id, state)
		}
	}
}

func (s *Store) recalculateDirty(ctx context.Context, state *session) error {
	for index := range state.dirtyRouteIndexes {
		if index < 0 || index >= len(state.currentRoutes) {
			continue
		}
		if state.activityLocation == nil {
			return errors.New("activity location is required")
		}
		if err := routing.OptimizeRouteOrder(ctx, s.distanceCalc, state.activityLocation.GetCoords(), state.mode, &state.currentRoutes[index]); err != nil {
			return err
		}
	}
	state.dirtyRouteIndexes = make(map[int]struct{})
	return nil
}

func (s *Store) recalculateRoute(ctx context.Context, state *session, route *models.CalculatedRoute) error {
	if state.activityLocation == nil {
		return errors.New("activity location is required")
	}
	return routing.PopulateRouteMetrics(ctx, s.distanceCalc, state.activityLocation.GetCoords(), state.mode, route)
}

func applyMove(state *session, move Move, from int) error {
	if from < 0 || from >= len(state.currentRoutes) || move.ToRouteIndex < 0 || move.ToRouteIndex >= len(state.currentRoutes) {
		return ErrInvalidRouteIndex
	}
	fromRoute, toRoute := &state.currentRoutes[from], &state.currentRoutes[move.ToRouteIndex]
	stopIndex := -1
	for i, stop := range fromRoute.Stops {
		if stop.Participant != nil && stop.Participant.ID == move.ParticipantID {
			stopIndex = i
			break
		}
	}
	if stopIndex < 0 {
		return ErrParticipantNotFound
	}
	participant := fromRoute.Stops[stopIndex].Participant
	fromRoute.Stops = append(fromRoute.Stops[:stopIndex], fromRoute.Stops[stopIndex+1:]...)
	newStop := models.RouteStop{Participant: participant}
	if move.InsertAtPosition < 0 || move.InsertAtPosition >= len(toRoute.Stops) {
		toRoute.Stops = append(toRoute.Stops, newStop)
	} else {
		toRoute.Stops = append(toRoute.Stops[:move.InsertAtPosition], append([]models.RouteStop{newStop}, toRoute.Stops[move.InsertAtPosition:]...)...)
	}
	state.dirtyRouteIndexes[from] = struct{}{}
	state.dirtyRouteIndexes[move.ToRouteIndex] = struct{}{}
	return nil
}

func snapshotOf(state *session) Snapshot {
	routes := copyRoutes(state.currentRoutes)
	over, out := capacityState(routes)
	return Snapshot{
		ID: state.id, Routes: routes, Summary: calculateSummary(routes), ActivityLocation: copyLocation(state.activityLocation),
		UseMiles: state.useMiles, RouteTime: state.routeTime, Mode: state.mode, UnusedDrivers: unusedDrivers(routes, state.selectedDrivers),
		IsEditing: !routesEqual(state.originalRoutes, state.currentRoutes), OverCapacity: over, IsOutOfBalance: out,
	}
}

func calculateSummary(routes []models.CalculatedRoute) models.RoutingSummary {
	var summary models.RoutingSummary
	usedVehicles := make(map[int64]struct{})
	for _, route := range routes {
		summary.TotalParticipants += len(route.Stops)
		if len(route.Stops) > 0 {
			summary.TotalDriversUsed++
		}
		summary.TotalDropoffDistanceMeters += route.TotalDropoffDistanceMeters
		summary.TotalDistanceMeters += route.TotalDistanceMeters
		if route.DetourSecs > summary.MaxDetourSecs {
			summary.MaxDetourSecs = route.DetourSecs
		}
		summary.SumDetourSecs += route.DetourSecs
		if route.OrgVehicleID != 0 && len(route.Stops) > 0 {
			usedVehicles[route.OrgVehicleID] = struct{}{}
		}
	}
	summary.OrgVehiclesUsed = len(usedVehicles)
	if summary.TotalDriversUsed > 0 {
		summary.AverageDetourSecs = summary.SumDetourSecs / float64(summary.TotalDriversUsed)
	}
	return summary
}

func capacityState(routes []models.CalculatedRoute) ([]bool, bool) {
	over := make([]bool, len(routes))
	out := false
	for i, route := range routes {
		capacity, _ := routeCapacity(route)
		over[i] = len(route.Stops) > capacity
		out = out || over[i]
	}
	return over, out
}

func routeCapacity(route models.CalculatedRoute) (int, bool) {
	if route.EffectiveCapacity > 0 {
		return route.EffectiveCapacity, true
	}
	if route.Driver == nil {
		return 0, false
	}
	return route.Driver.VehicleCapacity, true
}

func unusedDrivers(routes []models.CalculatedRoute, selected []models.Driver) []models.Driver {
	used := map[int64]bool{}
	for _, r := range routes {
		if r.Driver != nil {
			used[r.Driver.ID] = true
		}
	}
	var result []models.Driver
	for _, d := range selected {
		if !used[d.ID] {
			result = append(result, d)
		}
	}
	return result
}

func findParticipant(routes []models.CalculatedRoute, id int64) (int, bool) {
	for i, route := range routes {
		for _, stop := range route.Stops {
			if stop.Participant != nil && stop.Participant.ID == id {
				return i, true
			}
		}
	}
	return -1, false
}

func routesEqual(a, b []models.CalculatedRoute) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i].Stops) != len(b[i].Stops) || driverID(a[i].Driver) != driverID(b[i].Driver) {
			return false
		}
		for j := range a[i].Stops {
			if participantID(a[i].Stops[j].Participant) != participantID(b[i].Stops[j].Participant) {
				return false
			}
		}
	}
	return true
}

func driverID(d *models.Driver) int64 {
	if d == nil {
		return 0
	}
	return d.ID
}

func participantID(p *models.Participant) int64 {
	if p == nil {
		return 0
	}
	return p.ID
}

func copyRoutes(routes []models.CalculatedRoute) []models.CalculatedRoute {
	result := make([]models.CalculatedRoute, len(routes))
	for i, route := range routes {
		result[i] = route
		result[i].Driver = copyDriver(route.Driver)
		result[i].Stops = make([]models.RouteStop, len(route.Stops))
		for j, stop := range route.Stops {
			result[i].Stops[j] = stop
			if stop.Participant != nil {
				p := *stop.Participant
				result[i].Stops[j].Participant = &p
			}
		}
	}
	return result
}

func copyDriver(driver *models.Driver) *models.Driver {
	if driver == nil {
		return nil
	}
	result := *driver
	return &result
}

func copyLocation(location *models.ActivityLocation) *models.ActivityLocation {
	if location == nil {
		return nil
	}
	result := *location
	return &result
}

func copyVehicles(source map[int64]*models.OrganizationVehicle) map[int64]*models.OrganizationVehicle {
	result := make(map[int64]*models.OrganizationVehicle, len(source))
	for id, vehicle := range source {
		if vehicle != nil {
			copy := *vehicle
			result[id] = &copy
		}
	}
	return result
}

func copyDirty(source map[int]struct{}) map[int]struct{} {
	result := make(map[int]struct{}, len(source))
	for key := range source {
		result[key] = struct{}{}
	}
	return result
}

func generateID() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
