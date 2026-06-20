package routing

import (
	"context"
	"fmt"
	"math"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"sync"
	"testing"
)

type stableDistanceCalculator struct{}

func (stableDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	dist := math.Hypot(dest.Lat-origin.Lat, dest.Lng-origin.Lng) * 1000
	return &distance.DistanceResult{
		DistanceMeters: dist,
		DurationSecs:   dist,
	}, nil
}

func (calc stableDistanceCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	matrix := make([][]distance.DistanceResult, len(points))
	for i := range points {
		matrix[i] = make([]distance.DistanceResult, len(points))
		for j := range points {
			dist, err := calc.GetDistance(ctx, points[i], points[j])
			if err != nil {
				return nil, err
			}
			matrix[i][j] = *dist
		}
	}
	return matrix, nil
}

func (calc stableDistanceCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	results := make([]distance.DistanceResult, len(destinations))
	for i, dest := range destinations {
		dist, err := calc.GetDistance(ctx, origin, dest)
		if err != nil {
			return nil, err
		}
		results[i] = *dist
	}
	return results, nil
}

func (stableDistanceCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	return nil
}

type countingDistanceCalculator struct {
	stableDistanceCalculator
	calls int
}

func (c *countingDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	c.calls++
	return c.stableDistanceCalculator.GetDistance(ctx, origin, dest)
}

func TestRouteContextRiderScoreWeightsCumulativeStopTimes(t *testing.T) {
	tests := []struct {
		name   string
		mode   RouteMode
		driver *models.Driver
		want   float64
	}{
		{
			name:   "dropoff uses cumulative home arrival time",
			mode:   RouteModeDropoff,
			driver: &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0, VehicleCapacity: 3},
			want:   5000,
		},
		{
			name:   "pickup uses cumulative pickup time",
			mode:   RouteModePickup,
			driver: &models.Driver{ID: 1, Name: "Driver", Lat: 0, Lng: 0, VehicleCapacity: 3},
			want:   5000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := newRouteContext(stableDistanceCalculator{}, models.Coordinates{Lat: 0, Lng: 0}, tt.mode)
			stops := []*models.Participant{
				{ID: 1, Name: "Sibling A", Lat: 1, Lng: 0},
				{ID: 2, Name: "Sibling B", Lat: 1, Lng: 0},
				{ID: 3, Name: "Farther", Lat: 3, Lng: 0},
			}

			got, err := rc.riderScore(context.Background(), tt.driver, stops)
			if err != nil {
				t.Fatalf("riderScore() error = %v", err)
			}

			if got != tt.want {
				t.Fatalf("riderScore() = %.0f, want %.0f", got, tt.want)
			}
		})
	}
}

func TestGroupInsertionDeltaRiderScorePrefersEarlierDropoff(t *testing.T) {
	rc := newRouteContext(stableDistanceCalculator{}, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff)
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 20, Lng: 0, VehicleCapacity: 2}
	stops := []*models.Participant{
		{ID: 1, Name: "Far", Lat: 10, Lng: 0},
	}
	group := &participantGroup{
		members: []*models.Participant{{ID: 2, Name: "Near", Lat: 1, Lng: 0}},
	}

	startDelta, err := rc.groupInsertionDeltaRiderScore(context.Background(), driver, stops, group, 0)
	if err != nil {
		t.Fatalf("start delta error = %v", err)
	}
	endDelta, err := rc.groupInsertionDeltaRiderScore(context.Background(), driver, stops, group, 1)
	if err != nil {
		t.Fatalf("end delta error = %v", err)
	}

	if startDelta >= endDelta {
		t.Fatalf("start delta %.0f should be less than end delta %.0f", startDelta, endDelta)
	}
}

func TestGroupInsertionDeltaRiderScorePrefersEarlierPickup(t *testing.T) {
	rc := newRouteContext(stableDistanceCalculator{}, models.Coordinates{Lat: 20, Lng: 0}, RouteModePickup)
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 0, Lng: 0, VehicleCapacity: 2}
	stops := []*models.Participant{
		{ID: 1, Name: "Far", Lat: 10, Lng: 0},
	}
	group := &participantGroup{
		members: []*models.Participant{{ID: 2, Name: "Near", Lat: 1, Lng: 0}},
	}

	startDelta, err := rc.groupInsertionDeltaRiderScore(context.Background(), driver, stops, group, 0)
	if err != nil {
		t.Fatalf("start delta error = %v", err)
	}
	endDelta, err := rc.groupInsertionDeltaRiderScore(context.Background(), driver, stops, group, 1)
	if err != nil {
		t.Fatalf("end delta error = %v", err)
	}

	if startDelta >= endDelta {
		t.Fatalf("start delta %.0f should be less than end delta %.0f", startDelta, endDelta)
	}
}

func TestInsertionDeltaDistance_DropoffInsertAtEndPreservesLegacyBehavior(t *testing.T) {
	rc := newRouteContext(stableDistanceCalculator{}, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff)
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0}
	existingStops := []*models.Participant{
		{ID: 1, Name: "Existing", Lat: 7, Lng: 0},
	}
	inserted := &models.Participant{ID: 2, Name: "Inserted", Lat: 9, Lng: 0}

	delta, err := rc.insertionDeltaDistance(context.Background(), driver, existingStops, inserted, len(existingStops))
	if err != nil {
		t.Fatalf("insertionDeltaDistance() error = %v", err)
	}

	if delta != 2000 {
		t.Fatalf("dropoff end-insertion delta = %.0f, want 2000", delta)
	}
}

func TestInsertionDeltaDuration_PickupInsertAtEndUsesActivityDestination(t *testing.T) {
	rc := newRouteContext(stableDistanceCalculator{}, models.Coordinates{Lat: 0, Lng: 0}, RouteModePickup)
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0}
	existingStops := []*models.Participant{
		{ID: 1, Name: "Existing", Lat: 7, Lng: 0},
	}
	inserted := &models.Participant{ID: 2, Name: "Inserted", Lat: 9, Lng: 0}

	delta, err := rc.insertionDeltaDuration(context.Background(), driver, existingStops, inserted, len(existingStops))
	if err != nil {
		t.Fatalf("insertionDeltaDuration() error = %v", err)
	}

	if delta != 4000 {
		t.Fatalf("pickup end-insertion delta = %.0f, want 4000", delta)
	}
}

func TestPopulateRouteMetrics_PickupIncludesActivityDestination(t *testing.T) {
	route := &models.CalculatedRoute{
		Driver: &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0, VehicleCapacity: 4},
		Stops: []models.RouteStop{
			{Participant: &models.Participant{ID: 1, Name: "P1", Lat: 8, Lng: 0}},
			{Participant: &models.Participant{ID: 2, Name: "P2", Lat: 3, Lng: 0}},
		},
	}

	err := PopulateRouteMetrics(
		context.Background(),
		stableDistanceCalculator{},
		models.Coordinates{Lat: 0, Lng: 0},
		RouteModePickup,
		route,
	)
	if err != nil {
		t.Fatalf("PopulateRouteMetrics() error = %v", err)
	}

	if route.Mode != "pickup" {
		t.Fatalf("route.Mode = %q, want pickup", route.Mode)
	}
	if route.TotalDropoffDistanceMeters != 7000 {
		t.Fatalf("TotalDropoffDistanceMeters = %.0f, want 7000", route.TotalDropoffDistanceMeters)
	}
	if route.DistanceToDriverHomeMeters != 3000 {
		t.Fatalf("DistanceToDriverHomeMeters = %.0f, want 3000", route.DistanceToDriverHomeMeters)
	}
	if route.TotalDistanceMeters != 10000 {
		t.Fatalf("TotalDistanceMeters = %.0f, want 10000", route.TotalDistanceMeters)
	}
	if route.RouteDurationSecs != 10000 {
		t.Fatalf("RouteDurationSecs = %.0f, want 10000", route.RouteDurationSecs)
	}
	if route.BaselineDurationSecs != 10000 {
		t.Fatalf("BaselineDurationSecs = %.0f, want 10000", route.BaselineDurationSecs)
	}
	if route.DetourSecs != 0 {
		t.Fatalf("DetourSecs = %.0f, want 0", route.DetourSecs)
	}
	if len(route.Stops) != 2 {
		t.Fatalf("len(route.Stops) = %d, want 2", len(route.Stops))
	}
	if route.Stops[0].DistanceFromPrevMeters != 2000 {
		t.Fatalf("first stop DistanceFromPrevMeters = %.0f, want 2000", route.Stops[0].DistanceFromPrevMeters)
	}
	if route.Stops[1].DistanceFromPrevMeters != 5000 {
		t.Fatalf("second stop DistanceFromPrevMeters = %.0f, want 5000", route.Stops[1].DistanceFromPrevMeters)
	}
	if route.Stops[1].CumulativeDistanceMeters != 7000 {
		t.Fatalf("second stop CumulativeDistanceMeters = %.0f, want 7000", route.Stops[1].CumulativeDistanceMeters)
	}
}

func TestOptimizeRouteOrder_ReordersAndRefreshesMetrics(t *testing.T) {
	route := &models.CalculatedRoute{
		Driver: &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0},
		Stops: []models.RouteStop{
			{Participant: &models.Participant{ID: 1, Name: "Destination Side", Lat: 9, Lng: 0}},
			{Participant: &models.Participant{ID: 2, Name: "Origin Detour", Lat: 1, Lng: 100}},
		},
	}

	if err := OptimizeRouteOrder(context.Background(), stableDistanceCalculator{}, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff, route); err != nil {
		t.Fatalf("OptimizeRouteOrder() error = %v", err)
	}

	if route.Stops[0].Participant.Name != "Origin Detour" {
		t.Fatalf("first stop = %q, want Origin Detour", route.Stops[0].Participant.Name)
	}
	if route.Stops[0].Order != 0 || route.Stops[1].Order != 1 {
		t.Fatalf("orders = [%d %d], want [0 1]", route.Stops[0].Order, route.Stops[1].Order)
	}
	if route.TotalDistanceMeters == 0 || route.RouteDurationSecs == 0 {
		t.Fatalf("route metrics were not refreshed: total=%.0f duration=%.0f", route.TotalDistanceMeters, route.RouteDurationSecs)
	}
}

func TestBalancedRouterCalculateRouteDuration_PickupIncludesActivityLeg(t *testing.T) {
	calc := stableDistanceCalculator{}
	router := &BalancedRouter{distanceCalc: calc}
	rc := newRouteContext(calc, models.Coordinates{Lat: 0, Lng: 0}, RouteModePickup)
	route := &balancedRoute{
		driver: &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0, VehicleCapacity: 4},
		stops: []*models.Participant{
			{ID: 1, Name: "P1", Lat: 8, Lng: 0},
			{ID: 2, Name: "P2", Lat: 3, Lng: 0},
		},
	}

	duration, err := router.calculateRouteDuration(context.Background(), rc, route)
	if err != nil {
		t.Fatalf("calculateRouteDuration() error = %v", err)
	}

	if duration != 10000 {
		t.Fatalf("pickup route duration = %.0f, want 10000", duration)
	}
}

func TestBalancedRouterConcurrentMixedModes(t *testing.T) {
	router := NewBalancedRouter(stableDistanceCalculator{})

	runRequest := func(mode RouteMode) error {
		result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
			InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
			Participants: []models.Participant{
				{ID: 1, Name: "Passenger", Lat: 8, Lng: 0},
			},
			Drivers: []models.Driver{
				{ID: 1, Name: "Driver", Lat: 10, Lng: 0, VehicleCapacity: 1},
			},
			Mode: mode,
		})
		if err != nil {
			return err
		}
		if result.Mode != mode {
			return &ErrRoutingFailed{Reason: "result mode mismatch"}
		}
		if len(result.Routes) != 1 {
			return &ErrRoutingFailed{Reason: "unexpected route count"}
		}

		route := result.Routes[0]
		if route.Mode != mode {
			return &ErrRoutingFailed{Reason: "route mode mismatch"}
		}

		wantStopDistance := 8000.0
		wantFinalLeg := 2000.0
		if mode == RouteModePickup {
			wantStopDistance = 2000
			wantFinalLeg = 8000
		}

		if route.TotalDropoffDistanceMeters != wantStopDistance {
			return &ErrRoutingFailed{Reason: "unexpected stop distance"}
		}
		if route.DistanceToDriverHomeMeters != wantFinalLeg {
			return &ErrRoutingFailed{Reason: "unexpected final leg distance"}
		}
		if route.TotalDistanceMeters != 10000 {
			return &ErrRoutingFailed{Reason: "unexpected total distance"}
		}
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 40)

	for i := range 40 {
		mode := RouteModeDropoff
		if i%2 == 1 {
			mode = RouteModePickup
		}

		wg.Add(1)
		go func(mode RouteMode) {
			defer wg.Done()
			errs <- runRequest(mode)
		}(mode)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTwoOptDistance_UsesLocalEdgeDeltaEvaluation(t *testing.T) {
	calc := &countingDistanceCalculator{}
	rc := newRouteContext(calc, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff)
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0}
	stops := []*models.Participant{
		{ID: 1, Name: "P1", Lat: 1, Lng: 0},
		{ID: 2, Name: "P2", Lat: 2, Lng: 0},
		{ID: 3, Name: "P3", Lat: 3, Lng: 0},
		{ID: 4, Name: "P4", Lat: 4, Lng: 0},
		{ID: 5, Name: "P5", Lat: 5, Lng: 0},
		{ID: 6, Name: "P6", Lat: 6, Lng: 0},
		{ID: 7, Name: "P7", Lat: 7, Lng: 0},
		{ID: 8, Name: "P8", Lat: 8, Lng: 0},
	}

	if _, err := rc.twoOptDistance(context.Background(), driver, stops); err != nil {
		t.Fatalf("twoOptDistance() error = %v", err)
	}

	if calc.calls > 100 {
		t.Fatalf("twoOptDistance() made %d distance calls, want <= 100 for local-edge evaluation", calc.calls)
	}
}

func TestTwoOptRouteDuration_UsesLocalEdgeDeltaEvaluation(t *testing.T) {
	calc := &countingDistanceCalculator{}
	rc := newRouteContext(calc, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff)
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0}
	stops := []*models.Participant{
		{ID: 1, Name: "P1", Lat: 1, Lng: 0},
		{ID: 2, Name: "P2", Lat: 2, Lng: 0},
		{ID: 3, Name: "P3", Lat: 3, Lng: 0},
		{ID: 4, Name: "P4", Lat: 4, Lng: 0},
		{ID: 5, Name: "P5", Lat: 5, Lng: 0},
		{ID: 6, Name: "P6", Lat: 6, Lng: 0},
	}

	optimized, err := rc.twoOptRouteDuration(context.Background(), driver, stops)
	if err != nil {
		t.Fatalf("twoOptRouteDuration() error = %v", err)
	}
	if len(optimized) != len(stops) {
		t.Fatalf("optimized stop count = %d, want %d", len(optimized), len(stops))
	}
	if calc.calls > 100 {
		t.Fatalf("twoOptRouteDuration() made %d distance calls, want <= 100 for local-edge evaluation", calc.calls)
	}
}

func TestTwoOptRouteDurationMatchesLegacyFullScoreForDirectedDistances(t *testing.T) {
	for _, mode := range []RouteMode{RouteModeDropoff, RouteModePickup} {
		t.Run(string(mode), func(t *testing.T) {
			calc := newOverrideDistanceAdapter(100)
			institute := models.Coordinates{Lat: 0, Lng: 0}
			driver := &models.Driver{ID: 1, Name: "Driver", Lat: 4, Lng: 0}
			stops := []*models.Participant{
				{ID: 1, Name: "A", Lat: 1, Lng: 0},
				{ID: 2, Name: "B", Lat: 2, Lng: 0},
				{ID: 3, Name: "C", Lat: 3, Lng: 0},
			}
			rc := newRouteContext(calc, institute, mode)
			origin := rc.origin(driver)
			destination := rc.destination(driver)

			calc.setDuration(origin, stops[0].GetCoords(), 100)
			calc.setDuration(stops[0].GetCoords(), stops[1].GetCoords(), 10)
			calc.setDuration(stops[1].GetCoords(), stops[2].GetCoords(), 100)
			calc.setDuration(stops[2].GetCoords(), destination, 100)
			calc.setDuration(origin, stops[1].GetCoords(), 10)
			calc.setDuration(stops[1].GetCoords(), stops[0].GetCoords(), 500)
			calc.setDuration(stops[0].GetCoords(), stops[2].GetCoords(), 10)

			want, err := legacyTwoOptRouteDurationForTest(context.Background(), rc, driver, stops)
			if err != nil {
				t.Fatalf("legacy two-opt error = %v", err)
			}
			got, err := rc.twoOptRouteDuration(context.Background(), driver, stops)
			if err != nil {
				t.Fatalf("twoOptRouteDuration() error = %v", err)
			}

			if gotIDs, wantIDs := participantIDs(got), participantIDs(want); gotIDs != wantIDs {
				t.Fatalf("optimized order = %s, want legacy full-score order %s", gotIDs, wantIDs)
			}
			if gotIDs := participantIDs(got); gotIDs != "1,2,3" {
				t.Fatalf("directed internal edge costs should keep original order, got %s", gotIDs)
			}
		})
	}
}

func TestTwoOptByDeltaRequiresLegacyImprovementEpsilon(t *testing.T) {
	stops := []*models.Participant{
		{ID: 1, Name: "A"},
		{ID: 2, Name: "B"},
		{ID: 3, Name: "C"},
	}
	calls := 0

	got, err := twoOptByDelta(stops, func(candidate []*models.Participant, i, j int) (float64, error) {
		calls++
		if calls == 1 {
			return -scoreImprovementEpsilon / 2, nil
		}
		return 0, nil
	})
	if err != nil {
		t.Fatalf("twoOptByDelta() error = %v", err)
	}

	if gotIDs := participantIDs(got); gotIDs != "1,2,3" {
		t.Fatalf("sub-epsilon gain changed order to %s", gotIDs)
	}
}

func TestTwoOptBlockDelta_UsesLastMemberOfPreviousBlock(t *testing.T) {
	rc := newRouteContext(stableDistanceCalculator{}, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff)
	driver := &models.Driver{ID: 1, Lat: 10, Lng: 0}
	memberFirst := &models.Participant{ID: 1, Lat: 1.000001, Lng: 0}
	memberLast := &models.Participant{ID: 2, Lat: 1.000002, Lng: 0}
	blockA := &participantGroup{members: []*models.Participant{memberFirst, memberLast}}
	blockB := &participantGroup{members: []*models.Participant{{ID: 3, Lat: 3, Lng: 0}}}
	blockC := &participantGroup{members: []*models.Participant{{ID: 4, Lat: 5, Lng: 0}}}
	blocks := []*participantGroup{blockA, blockB, blockC}

	blockDelta, err := rc.twoOptBlockDelta(context.Background(), driver, blocks, 1, 3, func(r *distance.DistanceResult) float64 {
		return r.DurationSecs
	}, true)
	if err != nil {
		t.Fatalf("twoOptBlockDelta() error = %v", err)
	}

	repStops := []*models.Participant{memberFirst, blockB.members[0], blockC.members[0]}
	repDelta, err := rc.twoOptDelta(context.Background(), driver, repStops, 1, 3, func(r *distance.DistanceResult) float64 {
		return r.DurationSecs
	}, true)
	if err != nil {
		t.Fatalf("twoOptDelta() error = %v", err)
	}

	if blockDelta == repDelta {
		t.Fatalf("block delta %.0f should differ from first-member rep delta %.0f", blockDelta, repDelta)
	}
}

func legacyTwoOptRouteDurationForTest(ctx context.Context, rc routeContext, driver *models.Driver, stops []*models.Participant) ([]*models.Participant, error) {
	blocks := routeHouseholdBlocks(stops)
	if len(blocks) < 2 {
		return stops, nil
	}

	currentBlocks := append([]*participantGroup(nil), blocks...)
	currentStops := flattenParticipantGroups(currentBlocks)
	currentScore, err := rc.totalDriveDuration(ctx, driver, currentStops)
	if err != nil {
		return nil, err
	}

	improved := true
	for improved {
		improved = false
		for i := 0; i < len(currentBlocks)-1; i++ {
			for j := i + 2; j <= len(currentBlocks); j++ {
				candidateBlocks := append([]*participantGroup(nil), currentBlocks...)
				reverseParticipantGroups(candidateBlocks, i, j-1)
				candidateStops := flattenParticipantGroups(candidateBlocks)
				candidateScore, err := rc.totalDriveDuration(ctx, driver, candidateStops)
				if err != nil {
					return nil, err
				}
				if candidateScore < currentScore-scoreImprovementEpsilon {
					currentBlocks = candidateBlocks
					currentStops = candidateStops
					currentScore = candidateScore
					improved = true
				}
			}
		}
	}

	return currentStops, nil
}

func participantIDs(stops []*models.Participant) string {
	ids := ""
	for i, stop := range stops {
		if i > 0 {
			ids += ","
		}
		ids += fmt.Sprintf("%d", stop.ID)
	}
	return ids
}

func TestTwoOptRouteDuration_SplitHouseholdBlocksPreservesAllParticipants(t *testing.T) {
	calc := &countingDistanceCalculator{}
	rc := newRouteContext(calc, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff)
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0}
	householdLat, householdLng := 1.0, 0.0
	stops := []*models.Participant{
		{ID: 1, Name: "H1", Lat: householdLat, Lng: householdLng},
		{ID: 2, Name: "Other", Lat: 5, Lng: 0},
		{ID: 3, Name: "H2", Lat: householdLat, Lng: householdLng},
	}

	optimized, err := rc.twoOptRouteDuration(context.Background(), driver, stops)
	if err != nil {
		t.Fatalf("twoOptRouteDuration() error = %v", err)
	}
	if len(optimized) != len(stops) {
		t.Fatalf("optimized stop count = %d, want %d", len(optimized), len(stops))
	}

	seen := make(map[int64]struct{}, len(stops))
	for _, stop := range optimized {
		seen[stop.ID] = struct{}{}
	}
	for _, stop := range stops {
		if _, ok := seen[stop.ID]; !ok {
			t.Fatalf("optimized route lost participant %d", stop.ID)
		}
	}
}
