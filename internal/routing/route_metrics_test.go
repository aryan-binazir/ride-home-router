package routing

import (
	"context"
	"math"
	"sync"
	"testing"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
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
