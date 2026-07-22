package routing

import (
	"context"
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
			{Participant: &models.Participant{ID: 2, Name: "Origin Detour", Lat: 1, Lng: 100}},
			{Participant: &models.Participant{ID: 1, Name: "Destination Side", Lat: 9, Lng: 0}},
		},
	}

	if err := OptimizeRouteOrder(context.Background(), stableDistanceCalculator{}, models.Coordinates{Lat: 0, Lng: 0}, RouteModeDropoff, route); err != nil {
		t.Fatalf("OptimizeRouteOrder() error = %v", err)
	}

	if route.Stops[0].Participant.Name != "Destination Side" {
		t.Fatalf("first stop = %q, want Destination Side", route.Stops[0].Participant.Name)
	}
	if route.Stops[0].Order != 0 || route.Stops[1].Order != 1 {
		t.Fatalf("orders = [%d %d], want [0 1]", route.Stops[0].Order, route.Stops[1].Order)
	}
	if route.TotalDistanceMeters == 0 || route.RouteDurationSecs == 0 {
		t.Fatalf("route metrics were not refreshed: total=%.0f duration=%.0f", route.TotalDistanceMeters, route.RouteDurationSecs)
	}
}

func TestOptimizeRouteOrder_MatchesSolverOrdering(t *testing.T) {
	ctx := context.Background()
	calc := stableDistanceCalculator{}
	activity := models.Coordinates{Lat: 0, Lng: 0}
	driver := &models.Driver{ID: 7, Name: "Driver", Lat: 10, Lng: 0}
	participants := []*models.Participant{
		{ID: 1, Name: "Far", Lat: 2, Lng: 8},
		{ID: 2, Name: "Near Home", Lat: 9, Lng: 0},
		{ID: 3, Name: "Near Activity", Lat: 1, Lng: 0},
	}
	route := &models.CalculatedRoute{Driver: driver, Stops: make([]models.RouteStop, len(participants))}
	for i, participant := range participants {
		route.Stops[i].Participant = participant
	}

	if err := OptimizeRouteOrder(ctx, calc, activity, RouteModeDropoff, route); err != nil {
		t.Fatalf("OptimizeRouteOrder() error = %v", err)
	}

	internalRoutes := map[int64]*balancedRoute{
		driver.ID: {driver: driver, stops: append([]*models.Participant(nil), participants...)},
	}
	router := &BalancedRouter{distanceCalc: calc}
	if err := router.optimizeRouteOrders(ctx, newRouteContext(calc, activity, RouteModeDropoff), internalRoutes, []int64{driver.ID}); err != nil {
		t.Fatalf("optimizeRouteOrders() error = %v", err)
	}

	for i, stop := range route.Stops {
		if got, want := stop.Participant.ID, internalRoutes[driver.ID].stops[i].ID; got != want {
			t.Fatalf("stop %d participant = %d, want solver order %d", i, got, want)
		}
	}
}

func TestOptimizeRouteOrder_DropoffPrioritizesLastParticipantOverDriverHomeLeg(t *testing.T) {
	activity := models.Coordinates{Lat: 0, Lng: 0}
	firstHome := models.Coordinates{Lat: 1, Lng: 0}
	secondHome := models.Coordinates{Lat: 2, Lng: 0}
	driverHome := models.Coordinates{Lat: 3, Lng: 0}
	distances := newOverrideDistanceAdapter(50)
	distances.setDuration(activity, firstHome, 1)
	distances.setDuration(firstHome, secondHome, 1)
	distances.setDuration(secondHome, driverHome, 100)
	distances.setDuration(activity, secondHome, 10)
	distances.setDuration(secondHome, firstHome, 1)
	distances.setDuration(firstHome, driverHome, 1)

	route := &models.CalculatedRoute{
		Driver: &models.Driver{ID: 1, Name: "Driver", Lat: driverHome.Lat, Lng: driverHome.Lng},
		Stops: []models.RouteStop{
			{Participant: &models.Participant{ID: 2, Name: "Second", Lat: secondHome.Lat, Lng: secondHome.Lng}},
			{Participant: &models.Participant{ID: 1, Name: "First", Lat: firstHome.Lat, Lng: firstHome.Lng}},
		},
	}

	if err := OptimizeRouteOrder(context.Background(), distances, activity, RouteModeDropoff, route); err != nil {
		t.Fatalf("OptimizeRouteOrder() error = %v", err)
	}

	if route.Stops[0].Participant.ID != 1 {
		t.Fatalf("first participant = %d, want 1 so the last dropoff occurs after 2 seconds", route.Stops[0].Participant.ID)
	}
	if route.Stops[1].CumulativeDurationSecs != 2 {
		t.Fatalf("last participant completion = %.0f, want 2", route.Stops[1].CumulativeDurationSecs)
	}
	if route.RouteDurationSecs != 102 {
		t.Fatalf("full driver route = %.0f, want 102 as the lower-priority tradeoff", route.RouteDurationSecs)
	}
}

func TestOptimizeRouteOrder_PickupIncludesFinalActivityLeg(t *testing.T) {
	activity := models.Coordinates{Lat: 0, Lng: 0}
	firstHome := models.Coordinates{Lat: 1, Lng: 0}
	secondHome := models.Coordinates{Lat: 2, Lng: 0}
	driverHome := models.Coordinates{Lat: 3, Lng: 0}
	distances := newOverrideDistanceAdapter(50)
	distances.setDuration(driverHome, firstHome, 1)
	distances.setDuration(firstHome, secondHome, 1)
	distances.setDuration(secondHome, activity, 100)
	distances.setDuration(driverHome, secondHome, 10)
	distances.setDuration(secondHome, firstHome, 1)
	distances.setDuration(firstHome, activity, 1)

	route := &models.CalculatedRoute{
		Driver: &models.Driver{ID: 1, Name: "Driver", Lat: driverHome.Lat, Lng: driverHome.Lng},
		Stops: []models.RouteStop{
			{Participant: &models.Participant{ID: 1, Name: "First", Lat: firstHome.Lat, Lng: firstHome.Lng}},
			{Participant: &models.Participant{ID: 2, Name: "Second", Lat: secondHome.Lat, Lng: secondHome.Lng}},
		},
	}

	if err := OptimizeRouteOrder(context.Background(), distances, activity, RouteModePickup, route); err != nil {
		t.Fatalf("OptimizeRouteOrder() error = %v", err)
	}

	if route.Stops[0].Participant.ID != 2 {
		t.Fatalf("first pickup = %d, want 2 so all participants reach the activity after 12 seconds", route.Stops[0].Participant.ID)
	}
	if route.RouteDurationSecs != 12 {
		t.Fatalf("participant completion = %.0f, want 12 including the final activity leg", route.RouteDurationSecs)
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
