package distance

import (
	"context"
	"fmt"

	"ride-home-router/internal/models"
)

// DistancePair is a directed origin-destination pair for cache prewarming.
type DistancePair struct {
	Origin      models.Coordinates
	Destination models.Coordinates
}

// PairPrewarmedDistanceCalculator can prewarm only the directed pairs required
// for a routing solve instead of a full coordinate matrix.
type PairPrewarmedDistanceCalculator interface {
	PrewarmPairs(ctx context.Context, pairs []DistancePair) error
}

// PrewarmRoutingPairs calls PrewarmPairs when supported, otherwise falls back to
// PrewarmCache with the unique coordinates from the pair list.
func PrewarmRoutingPairs(ctx context.Context, calc DistanceCalculator, pairs []DistancePair) error {
	if len(pairs) == 0 {
		return nil
	}
	if pairPrewarmer, ok := calc.(PairPrewarmedDistanceCalculator); ok {
		return pairPrewarmer.PrewarmPairs(ctx, pairs)
	}

	seen := make(map[string]struct{}, len(pairs)*2)
	points := make([]models.Coordinates, 0, len(pairs)*2)
	for _, pair := range pairs {
		for _, coord := range []models.Coordinates{pair.Origin, pair.Destination} {
			key := coordinatePointKey(coord)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			points = append(points, coord)
		}
	}
	return calc.PrewarmCache(ctx, points)
}

func coordinatePointKey(coord models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f",
		models.RoundCoordinate(coord.Lat),
		models.RoundCoordinate(coord.Lng),
	)
}

func PairCacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat),
		models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat),
		models.RoundCoordinate(dest.Lng),
	)
}
