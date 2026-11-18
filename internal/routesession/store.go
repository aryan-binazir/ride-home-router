package routesession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"strings"
)

var (
	ErrNotFound               = errors.New("route session not found")
	ErrAlreadyCommitted       = errors.New("route session already committed")
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
	// ChangedRouteIndexes lists routes whose driver or ordered stops differ
	// from before the last mutation; a fresh calculation lists every route.
	ChangedRouteIndexes []int
	ID                  string
	Routes              []models.CalculatedRoute
	Summary             models.RoutingSummary
	ActivityLocation    *models.ActivityLocation
	UseMiles            bool
	RouteTime           string
	Mode                models.RouteMode
	UnusedDrivers       []models.Driver
	IsEditing           bool
	OverCapacity        []bool
	IsOutOfBalance      bool
	Changes             []RouteChange
	ReviewerNote        string
}

// CommitSnapshot is a deep copy of a live route session that callbacks may mutate safely.
type CommitSnapshot struct {
	RouteTime         string
	SessionID         string
	Original          []models.CalculatedRoute
	Final             []models.CalculatedRoute
	Summary           models.RoutingSummary
	SelectedDrivers   []models.Driver
	DriverOrgVehicles map[int64]*models.OrganizationVehicle
	ActivityLocation  *models.ActivityLocation
	Mode              models.RouteMode
	Changes           []RouteChange
	ReviewerNote      string
}

func (s CommitSnapshot) RoutingResult() models.RoutingResult {
	return models.RoutingResult{
		Routes:  copyRoutes(s.Final),
		Summary: copySummary(s.Summary),
		Mode:    s.Mode,
	}
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
	reviewerNote      string
}

type Store struct {
	records      database.WorkflowRepository
	distanceCalc distance.Lookup
}

func newSession(input CreateInput) *session {
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
	}
	return state
}

func (s *Store) ApplyMoves(ctx context.Context, id string, moves []Move, options ApplyMovesOptions) (Snapshot, error) {
	return s.update(ctx, id, func(state *session) (Snapshot, error) {
		before := routeIdentity(state.currentRoutes)

		for _, move := range moves {
			from, ok := findParticipant(state.currentRoutes, move.ParticipantID)
			if !ok {
				return Snapshot{}, ErrParticipantNotFound
			}
			if options.RequireClaimedSource && from != move.FromRouteIndex {
				return Snapshot{}, ErrParticipantNotInSource
			}
			if err := applyMove(state, move, from); err != nil {
				return Snapshot{}, err
			}
			_, unbalanced := capacityState(state.currentRoutes)
			if !unbalanced {
				if err := s.recalculateDirty(ctx, state); err != nil {
					return Snapshot{}, err
				}
			}
		}
		return snapshotWithChanges(state, changedRoutes(before, state.currentRoutes)), nil
	})
}

func (s *Store) SwapDrivers(ctx context.Context, id string, first, second int) (Snapshot, error) {
	return s.update(ctx, id, func(state *session) (Snapshot, error) {
		before := routeIdentity(state.currentRoutes)
		if first < 0 || first >= len(state.currentRoutes) || second < 0 || second >= len(state.currentRoutes) {
			return Snapshot{}, ErrInvalidRouteIndex
		}
		dirtyBefore := state.dirtyRouteIndexes
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
		route1.EffectiveCapacity, route2.EffectiveCapacity = route2.EffectiveCapacity, route1.EffectiveCapacity
		route1.OrgVehicleID, route2.OrgVehicleID = route2.OrgVehicleID, route1.OrgVehicleID
		route1.OrgVehicleName, route2.OrgVehicleName = route2.OrgVehicleName, route1.OrgVehicleName
		_, unbalanced := capacityState(state.currentRoutes)
		if !unbalanced {
			if err := s.recalculateDirty(ctx, state); err != nil {
				return Snapshot{}, err
			}
		}
		for _, index := range []int{first, second} {
			if dirtyRouteAlreadyOptimized(dirtyBefore, index, unbalanced) {
				continue
			}
			if err := s.recalculateRoute(ctx, state, &state.currentRoutes[index]); err != nil {
				return Snapshot{}, err
			}
		}
		return snapshotWithChanges(state, changedRoutes(before, state.currentRoutes)), nil
	})
}

func (s *Store) AddDriver(ctx context.Context, id string, driverID int64) (Snapshot, error) {
	return s.update(ctx, id, func(state *session) (Snapshot, error) {
		before := routeIdentity(state.currentRoutes)
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
		return snapshotWithChanges(state, changedRoutes(before, state.currentRoutes)), nil
	})
}

func commitSnapshot(state *session) (CommitSnapshot, error) {
	_, unbalanced := capacityState(state.currentRoutes)
	if unbalanced {
		return CommitSnapshot{}, ErrUnbalanced
	}
	return CommitSnapshot{
		RouteTime:         state.routeTime,
		SessionID:         state.id,
		Original:          copyRoutes(state.originalRoutes),
		Final:             copyRoutes(state.currentRoutes),
		Summary:           calculateSummary(state.currentRoutes),
		SelectedDrivers:   append([]models.Driver(nil), state.selectedDrivers...),
		DriverOrgVehicles: copyVehicles(state.driverOrgVehicles),
		ActivityLocation:  copyLocation(state.activityLocation),
		Mode:              state.mode,
		Changes:           routeChanges(state.originalRoutes, state.currentRoutes),
		ReviewerNote:      state.reviewerNote,
	}, nil
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
	household := routing.HouseholdKey(fromRoute.Stops[stopIndex].Participant)
	moving, remaining := detachHousehold(fromRoute.Stops, household)
	fromRoute.Stops = remaining
	if move.InsertAtPosition < 0 || move.InsertAtPosition >= len(toRoute.Stops) {
		toRoute.Stops = append(toRoute.Stops, moving...)
	} else {
		toRoute.Stops = append(toRoute.Stops[:move.InsertAtPosition], append(moving, toRoute.Stops[move.InsertAtPosition:]...)...)
	}
	state.dirtyRouteIndexes[from] = struct{}{}
	state.dirtyRouteIndexes[move.ToRouteIndex] = struct{}{}
	return nil
}

func detachHousehold(stops []models.RouteStop, household string) (moving, remaining []models.RouteStop) {
	moving = make([]models.RouteStop, 0, 1)
	remaining = make([]models.RouteStop, 0, len(stops))
	for _, stop := range stops {
		if stop.Participant != nil && routing.HouseholdKey(stop.Participant) == household {
			moving = append(moving, models.RouteStop{Participant: stop.Participant})
		} else {
			remaining = append(remaining, stop)
		}
	}
	return moving, remaining
}

func dirtyRouteAlreadyOptimized(dirtyBefore map[int]struct{}, index int, unbalanced bool) bool {
	if unbalanced {
		return false
	}
	_, wasDirty := dirtyBefore[index]
	return wasDirty
}

func routeIdentity(routes []models.CalculatedRoute) []string {
	keys := make([]string, len(routes))
	for i, route := range routes {
		var b strings.Builder
		fmt.Fprintf(&b, "%d:", driverID(route.Driver))
		for _, stop := range route.Stops {
			fmt.Fprintf(&b, "%d,", participantID(stop.Participant))
		}
		keys[i] = b.String()
	}
	return keys
}

func changedRoutes(before []string, routes []models.CalculatedRoute) []int {
	after := routeIdentity(routes)
	changed := make([]int, 0)
	for i, key := range after {
		if i >= len(before) || before[i] != key {
			changed = append(changed, i)
		}
	}
	return changed
}

func snapshotWithChanges(state *session, changed []int) Snapshot {
	snapshot := snapshotOf(state)
	snapshot.ChangedRouteIndexes = changed
	return snapshot
}

func snapshotOf(state *session) Snapshot {
	routes := copyRoutes(state.currentRoutes)
	over, out := capacityState(routes)
	return Snapshot{
		ID: state.id, Routes: routes, Summary: calculateSummary(routes), ActivityLocation: copyLocation(state.activityLocation),
		UseMiles: state.useMiles, RouteTime: state.routeTime, Mode: state.mode, UnusedDrivers: unusedDrivers(routes, state.selectedDrivers),
		IsEditing: !routesEqual(state.originalRoutes, state.currentRoutes), OverCapacity: over, IsOutOfBalance: out,
		Changes: routeChanges(state.originalRoutes, state.currentRoutes), ReviewerNote: state.reviewerNote,
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

func copySummary(summary models.RoutingSummary) models.RoutingSummary {
	result := summary
	result.UnassignedParticipants = append([]int64(nil), summary.UnassignedParticipants...)
	return result
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

func generateID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
