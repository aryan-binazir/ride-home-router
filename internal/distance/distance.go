package distance

import (
	"context"
	"ride-home-router/internal/models"
)

// DistanceResult contains the result of a distance calculation.
type DistanceResult struct {
	DistanceMeters float64
	DurationSecs   float64
}

// Lookup provides point-to-point distance results.
type Lookup interface {
	GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error)
}

// SolveSource provides the distance operations needed to prepare and run one
// routing solve.
type SolveSource interface {
	Lookup
	PrewarmPairs(ctx context.Context, pairs []DistancePair) error
}

// DistanceCalculator provides the full distance-provider surface.
type DistanceCalculator interface {
	SolveSource
	GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error)
	GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error)
}
