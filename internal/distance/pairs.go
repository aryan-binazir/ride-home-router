package distance

import (
	"fmt"
	"ride-home-router/internal/models"
)

// DistancePair is a directed origin-destination pair for cache prewarming.
type DistancePair struct {
	Origin      models.Coordinates
	Destination models.Coordinates
}

func coordinatePointKey(coord models.Coordinates) string {
	return fmt.Sprintf(
		"%.5f,%.5f",
		models.RoundCoordinate(coord.Lat),
		models.RoundCoordinate(coord.Lng),
	)
}

// SamePoint reports whether two coordinates share the cache's rounded identity.
func SamePoint(a, b models.Coordinates) bool {
	return models.RoundCoordinate(a.Lat) == models.RoundCoordinate(b.Lat) &&
		models.RoundCoordinate(a.Lng) == models.RoundCoordinate(b.Lng)
}

func PairCacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf(
		"%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat),
		models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat),
		models.RoundCoordinate(dest.Lng),
	)
}
