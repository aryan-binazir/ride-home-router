//go:build !race

package routing_test

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"testing"
)

// This resource budget protects the public warm-cache calculation, independently
// of its memo representation. Wall-clock performance belongs in benchmarks.
func TestWarmCalculationAllocationBudget(t *testing.T) {
	discardRoutingLogs(t)
	req, source := performanceFixture(8, 3, false)
	router := routing.NewBalancedRouter(source)
	allocations := testing.AllocsPerRun(2, func() {
		if _, err := router.CalculateRoutes(t.Context(), &req); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("warm allocations: %.0f", allocations)
	if allocations > 10000 {
		t.Fatalf("warm calculation allocated %.0f objects; budget is 10000", allocations)
	}
}

// Exercise provider prewarming through the same public routing seam. The cache
// supplies all requested pairs and returns an error for any unknown location.
type warmProviderCache struct {
	database.DistanceCacheRepository
	warmDistances
}

func (s warmProviderCache) Get(ctx context.Context, a, b models.Coordinates) (*models.DistanceCacheEntry, error) {
	v, err := s.GetDistance(ctx, a, b)
	if err != nil {
		return nil, err
	}
	return &models.DistanceCacheEntry{Origin: a, Destination: b, DistanceMeters: v.DistanceMeters, DurationSecs: v.DurationSecs}, nil
}

func (s warmProviderCache) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	entries := make(map[string]*models.DistanceCacheEntry, len(pairs))
	for _, pair := range pairs {
		entry, err := s.Get(ctx, pair.Origin, pair.Dest)
		if err != nil {
			return nil, err
		}
		entries[distance.PairCacheKey(pair.Origin, pair.Dest)] = entry
	}
	return entries, nil
}

func TestWarmProviderCalculationAllocationBudget(t *testing.T) {
	discardRoutingLogs(t)
	req, source := performanceFixture(8, 3, false)
	provider := distance.NewGoogleCalculator(warmProviderCache{warmDistances: source}, func(context.Context) (string, error) { return "synthetic-key", nil })
	router := routing.NewBalancedRouter(provider)
	allocations := testing.AllocsPerRun(2, func() {
		if _, err := router.CalculateRoutes(t.Context(), &req); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("warm provider allocations: %.0f", allocations)
	if allocations > 10400 {
		t.Fatalf("warm provider calculation allocated %.0f objects; budget is 10400", allocations)
	}
}
