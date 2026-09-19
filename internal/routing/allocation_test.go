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
// Budgets were raised on 2026-09-13 when the assignment search gained whole-car
// driver swaps: on this fixture a swap is accepted, which costs one more search
// iteration (about 2,100 objects). Lowered on 2026-09-19 when household blocks
// in the search became index ranges over the stops instead of heap objects:
// each block had cost a group struct plus a one-element member slice per
// candidate move, and this fixture went from 9,088 objects to 4,900. Budgets
// keep about a third of headroom over the measured count.
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
	if allocations > 6500 {
		t.Fatalf("warm calculation allocated %.0f objects; budget is 6500", allocations)
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

// Measured at 6,186 objects on 2026-09-19 after the household block change.
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
	if allocations > 8200 {
		t.Fatalf("warm provider calculation allocated %.0f objects; budget is 8200", allocations)
	}
}
