package routing

import (
	"context"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"testing"
)

// recordingSolveSource observes the public routing seam without duplicating
// solve-distance behavior in the fake.
type recordingSolveSource struct {
	stableDistanceCalculator
	prewarmPairsCalls int
	prewarmed         []distance.DistancePair
	lookups           map[string]int
}

func (c *recordingSolveSource) PrewarmPairs(ctx context.Context, pairs []distance.DistancePair) error {
	c.prewarmPairsCalls++
	c.prewarmed = append(c.prewarmed, pairs...)
	return nil
}

func (c *recordingSolveSource) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	if c.lookups == nil {
		c.lookups = make(map[string]int)
	}
	c.lookups[distance.PairCacheKey(origin, dest)]++
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
		_ = collectSolveDistancePairs(RouteModePickup, req.InstituteCoords, req.Participants, req.Drivers)
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
				if models.RoundCoordinate(pair.Origin.Lat) == models.RoundCoordinate(pair.Destination.Lat) &&
					models.RoundCoordinate(pair.Origin.Lng) == models.RoundCoordinate(pair.Destination.Lng) {
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

func TestBalancedRouter_SkipsPrewarmWhenNoPairsAreNeeded(t *testing.T) {
	tests := []struct {
		name string
		req  RoutingRequest
	}{
		{
			name: "no participants preserves default mode",
			req: RoutingRequest{
				InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
				Drivers:         []models.Driver{{ID: 1, Name: "D1", VehicleCapacity: 1}},
			},
		},
		{
			name: "identical coordinates",
			req: RoutingRequest{
				InstituteCoords: models.Coordinates{Lat: 1, Lng: 1},
				Participants:    []models.Participant{{ID: 1, Name: "P1", Lat: 1, Lng: 1}},
				Drivers:         []models.Driver{{ID: 1, Name: "D1", Lat: 1, Lng: 1, VehicleCapacity: 1}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := &recordingSolveSource{}
			result, err := NewBalancedRouter(calc).CalculateRoutes(context.Background(), &tt.req)
			if err != nil {
				t.Fatalf("CalculateRoutes() error = %v", err)
			}
			if calc.prewarmPairsCalls != 0 {
				t.Fatalf("PrewarmPairs calls = %d, want 0", calc.prewarmPairsCalls)
			}
			if tt.name == "no participants preserves default mode" && result.Mode != RouteModeDropoff {
				t.Fatalf("result mode = %q, want %q", result.Mode, RouteModeDropoff)
			}
		})
	}
}
