package routing

import (
	"context"
	"errors"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"testing"
)

// recordingSolveSource records calls made through the public routing seam.
type recordingSolveSource struct {
	stableDistanceCalculator
	prewarmPairsCalls int
	prewarmed         []distance.DistancePair
	lookups           map[string]struct{}
	prewarmErr        error
	returnNil         bool
}

func (c *recordingSolveSource) PrewarmPairs(ctx context.Context, pairs []distance.DistancePair) error {
	c.prewarmPairsCalls++
	c.prewarmed = append(c.prewarmed, pairs...)
	return c.prewarmErr
}

func (c *recordingSolveSource) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	if !distance.SamePoint(origin, dest) {
		if c.lookups == nil {
			c.lookups = make(map[string]struct{})
		}
		c.lookups[distance.PairCacheKey(origin, dest)] = struct{}{}
	}
	if c.returnNil {
		return nil, nil //nolint:nilnil // Exercise defensive handling of an invalid lookup implementation.
	}
	return c.stableDistanceCalculator.GetDistance(ctx, origin, dest)
}

func BenchmarkCollectSolveDistancePairs(b *testing.B) {
	req := &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants:    make([]models.Participant, 20),
		Drivers:         make([]models.Driver, 5),
	}
	for i := range req.Participants {
		req.Participants[i] = models.Participant{ID: int64(i + 1), Lat: float64(i + 1), Lng: 0}
	}
	for i := range req.Drivers {
		req.Drivers[i] = models.Driver{ID: int64(i + 1), Lat: 10 + float64(i), Lng: 0, VehicleCapacity: 4}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = collectSolveDistancePairs(context.Background(), RouteModePickup, req.InstituteCoords, req.Participants, req.Drivers)
	}
}

func TestBalancedRouter_PrewarmsEveryDirectedSolvePair(t *testing.T) {
	for _, mode := range []RouteMode{RouteModePickup, RouteModeDropoff} {
		t.Run(string(mode), func(t *testing.T) {
			calc := &recordingSolveSource{}
			router := NewBalancedRouter(calc)
			_, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
				InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
				Participants: []models.Participant{
					{ID: 1, Name: "P1", Lat: 1, Lng: 0},
					{ID: 2, Name: "P2", Lat: 2, Lng: 0},
				},
				Drivers: []models.Driver{
					{ID: 1, Name: "D1", Lat: 10, Lng: 0, VehicleCapacity: 4},
					{ID: 2, Name: "D2", Lat: 11, Lng: 0, VehicleCapacity: 4},
				},
				Mode: mode,
			})
			if err != nil {
				t.Fatalf("CalculateRoutes() error = %v", err)
			}
			if calc.prewarmPairsCalls != 1 {
				t.Fatalf("PrewarmPairs calls = %d, want 1", calc.prewarmPairsCalls)
			}
			if len(calc.prewarmed) != 10 {
				t.Fatalf("prewarmed pair count = %d, want 10", len(calc.prewarmed))
			}

			prewarmed := make(map[string]struct{}, len(calc.prewarmed))
			for _, pair := range calc.prewarmed {
				if distance.SamePoint(pair.Origin, pair.Destination) {
					t.Fatalf("unexpected identity pair: %+v", pair)
				}
				key := distance.PairCacheKey(pair.Origin, pair.Destination)
				if _, exists := prewarmed[key]; exists {
					t.Fatalf("duplicate pair in prewarm set: %s", key)
				}
				prewarmed[key] = struct{}{}
			}
			for key := range calc.lookups {
				if _, ok := prewarmed[key]; !ok {
					t.Fatalf("solve looked up pair that was not prewarmed: %s", key)
				}
			}
		})
	}
}

func TestBalancedRouter_PrewarmsNonIdentityPairsWhenStopMatchesActivity(t *testing.T) {
	calc := &recordingSolveSource{}
	_, err := NewBalancedRouter(calc).CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "At activity", Lat: 0, Lng: 0},
			{ID: 2, Name: "Away", Lat: 1, Lng: 0},
		},
		Drivers: []models.Driver{{ID: 1, Name: "D1", Lat: 2, Lng: 0, VehicleCapacity: 2}},
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	prewarmed := make(map[string]struct{}, len(calc.prewarmed))
	for _, pair := range calc.prewarmed {
		prewarmed[distance.PairCacheKey(pair.Origin, pair.Destination)] = struct{}{}
	}
	for key := range calc.lookups {
		if _, ok := prewarmed[key]; !ok {
			t.Fatalf("non-identity solve lookup was not prewarmed: %s", key)
		}
	}
}

func TestBalancedRouter_EmptyParticipantsPreserveDefaultMode(t *testing.T) {
	calc := &recordingSolveSource{}
	result, err := NewBalancedRouter(calc).CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Drivers:         []models.Driver{{ID: 1, Name: "D1", VehicleCapacity: 1}},
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}
	if calc.prewarmPairsCalls != 0 {
		t.Fatalf("PrewarmPairs calls = %d, want 0", calc.prewarmPairsCalls)
	}
	if result.Mode != RouteModeDropoff {
		t.Fatalf("result mode = %q, want %q", result.Mode, RouteModeDropoff)
	}
}

func TestBalancedRouter_SkipsPrewarmForIdentityOnlySolve(t *testing.T) {
	calc := &recordingSolveSource{}
	_, err := NewBalancedRouter(calc).CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 1, Lng: 1},
		Participants:    []models.Participant{{ID: 1, Name: "P1", Lat: 1, Lng: 1}},
		Drivers:         []models.Driver{{ID: 1, Name: "D1", Lat: 1, Lng: 1, VehicleCapacity: 1}},
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}
	if calc.prewarmPairsCalls != 0 {
		t.Fatalf("PrewarmPairs calls = %d, want 0", calc.prewarmPairsCalls)
	}
}

func TestBalancedRouter_PropagatesPrewarmError(t *testing.T) {
	wantErr := errors.New("prewarm failed")
	calc := &recordingSolveSource{prewarmErr: wantErr}
	_, err := NewBalancedRouter(calc).CalculateRoutes(context.Background(), solveDistanceTestRequest())
	if !errors.Is(err, wantErr) {
		t.Fatalf("CalculateRoutes() error = %v, want %v", err, wantErr)
	}
}

func TestBalancedRouter_ReturnsErrorForMissingDistanceResult(t *testing.T) {
	calc := &recordingSolveSource{returnNil: true}
	_, err := NewBalancedRouter(calc).CalculateRoutes(context.Background(), solveDistanceTestRequest())
	if err == nil {
		t.Fatal("CalculateRoutes() error = nil, want missing distance result error")
	}
}

func TestBalancedRouter_PreCanceledContextStopsCalculation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewBalancedRouter(&recordingSolveSource{}).CalculateRoutes(ctx, solveDistanceTestRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CalculateRoutes() error = %v, want %v", err, context.Canceled)
	}
}

func TestCollectSolveDistancePairs_StopsForCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pairs, err := collectSolveDistancePairs(ctx, RouteModeDropoff, models.Coordinates{}, make([]models.Participant, 500), make([]models.Driver, 500))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("collectSolveDistancePairs() error = %v, want %v", err, context.Canceled)
	}
	if pairs != nil {
		t.Fatalf("collectSolveDistancePairs() returned %d pairs after cancellation", len(pairs))
	}
}

func TestSolveDistanceLookup_CanceledContextOverridesMemoizedResult(t *testing.T) {
	origin := models.Coordinates{Lat: 1, Lng: 2}
	destination := models.Coordinates{Lat: 3, Lng: 4}
	lookup := &solveDistanceLookup{
		values: map[string]distance.DistanceResult{
			distance.PairCacheKey(origin, destination): {DistanceMeters: 100},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lookup.GetDistance(ctx, origin, destination)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetDistance() error = %v, want %v", err, context.Canceled)
	}
}

func solveDistanceTestRequest() *RoutingRequest {
	return &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants:    []models.Participant{{ID: 1, Name: "P1", Lat: 1, Lng: 0}},
		Drivers:         []models.Driver{{ID: 1, Name: "D1", Lat: 2, Lng: 0, VehicleCapacity: 1}},
	}
}
