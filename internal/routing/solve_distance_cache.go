package routing

import (
	"context"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// solveDistanceCache keeps repeated optimizer scoring from reloading the same
// prewarmed pair through the persistent cache during one calculation.
type solveDistanceCache struct {
	distance.DistanceCalculator
	values map[string]distance.DistanceResult
}

func newSolveDistanceCache(calc distance.DistanceCalculator) *solveDistanceCache {
	return &solveDistanceCache{
		DistanceCalculator: calc,
		values:             make(map[string]distance.DistanceResult),
	}
}

func (c *solveDistanceCache) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	key := distance.PairCacheKey(origin, dest)
	if cached, ok := c.values[key]; ok {
		result := cached
		return &result, nil
	}

	result, err := c.DistanceCalculator.GetDistance(ctx, origin, dest)
	if err != nil {
		return nil, err
	}
	c.values[key] = *result
	copy := *result
	return &copy, nil
}
