package routing

import (
	"context"
	"testing"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

type countingPrewarmCalculator struct {
	stableDistanceCalculator
	prewarmPairsCalls int
	prewarmPairCount  int
	prewarmCacheCalls int
}

func (c *countingPrewarmCalculator) PrewarmPairs(ctx context.Context, pairs []distance.DistancePair) error {
	c.prewarmPairsCalls++
	c.prewarmPairCount += len(pairs)
	return nil
}

func (c *countingPrewarmCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	c.prewarmCacheCalls++
	return nil
}

func BenchmarkCollectRoutingPrewarmPairs(b *testing.B) {
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
		_ = collectRoutingPrewarmPairs(RouteModePickup, req.InstituteCoords, req.Participants, req.Drivers)
	}
}

func TestCollectRoutingPrewarmPairs_PickupUsesDirectedLegsOnly(t *testing.T) {
	req := &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 1, Lng: 0},
			{ID: 2, Name: "P2", Lat: 2, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "D1", Lat: 10, Lng: 0, VehicleCapacity: 4},
			{ID: 2, Name: "D2", Lat: 11, Lng: 0, VehicleCapacity: 4},
		},
	}

	pairs := collectRoutingPrewarmPairs(RouteModePickup, req.InstituteCoords, req.Participants, req.Drivers)
	seen := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		key := distance.PairCacheKey(pair.Origin, pair.Destination)
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate pair in prewarm set: %s", key)
		}
		seen[key] = struct{}{}
	}

	wantDirected := 10
	if len(pairs) != wantDirected {
		t.Fatalf("pickup pair count = %d, want %d", len(pairs), wantDirected)
	}
}

func TestCollectRoutingPrewarmPairs_DropoffUsesDirectedLegsOnly(t *testing.T) {
	req := &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 1, Lng: 0},
			{ID: 2, Name: "P2", Lat: 2, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "D1", Lat: 10, Lng: 0, VehicleCapacity: 4},
			{ID: 2, Name: "D2", Lat: 11, Lng: 0, VehicleCapacity: 4},
		},
	}

	pairs := collectRoutingPrewarmPairs(RouteModeDropoff, req.InstituteCoords, req.Participants, req.Drivers)
	seen := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		if models.RoundCoordinate(pair.Origin.Lat) == models.RoundCoordinate(pair.Destination.Lat) &&
			models.RoundCoordinate(pair.Origin.Lng) == models.RoundCoordinate(pair.Destination.Lng) {
			t.Fatalf("unexpected identity pair: %+v", pair)
		}
		key := distance.PairCacheKey(pair.Origin, pair.Destination)
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate pair in prewarm set: %s", key)
		}
		seen[key] = struct{}{}
	}

	wantDirected := 10
	if len(pairs) != wantDirected {
		t.Fatalf("dropoff pair count = %d, want %d", len(pairs), wantDirected)
	}
}

func TestPrewarmRoutingDistances_UsesPairInterfaceWhenAvailable(t *testing.T) {
	calc := &countingPrewarmCalculator{}
	req := &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 1, Lng: 0},
			{ID: 2, Name: "P2", Lat: 2, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "D1", Lat: 10, Lng: 0, VehicleCapacity: 4},
		},
	}

	if err := prewarmRoutingDistances(context.Background(), calc, req, RouteModePickup); err != nil {
		t.Fatalf("prewarmRoutingDistances() error = %v", err)
	}
	if calc.prewarmPairsCalls != 1 {
		t.Fatalf("PrewarmPairs calls = %d, want 1", calc.prewarmPairsCalls)
	}
	if calc.prewarmCacheCalls != 0 {
		t.Fatalf("PrewarmCache calls = %d, want 0", calc.prewarmCacheCalls)
	}
	if calc.prewarmPairCount == 0 {
		t.Fatal("expected non-zero pair prewarm count")
	}
}

func TestPrewarmRoutingDistances_FallsBackToCachePrewarm(t *testing.T) {
	calc := &cacheOnlyPrewarmCalculator{}
	req := &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 1, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "D1", Lat: 10, Lng: 0, VehicleCapacity: 4},
		},
	}

	var calcIface distance.DistanceCalculator = calc
	if err := prewarmRoutingDistances(context.Background(), calcIface, req, RouteModeDropoff); err != nil {
		t.Fatalf("prewarmRoutingDistances() error = %v", err)
	}
	if calc.prewarmCacheCalls != 1 {
		t.Fatalf("PrewarmCache calls = %d, want 1", calc.prewarmCacheCalls)
	}
}

type cacheOnlyPrewarmCalculator struct {
	stableDistanceCalculator
	prewarmCacheCalls int
}

func (c *cacheOnlyPrewarmCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	c.prewarmCacheCalls++
	return nil
}
