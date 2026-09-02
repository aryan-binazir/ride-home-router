package distance

import (
	"context"
	"fmt"
	"ride-home-router/internal/models"
)

// ErrDistanceCalculationFailed reports a distance-provider failure.
type ErrDistanceCalculationFailed struct {
	Origin models.Coordinates
	Dest   models.Coordinates
	Reason string
}

func (e *ErrDistanceCalculationFailed) Error() string {
	return fmt.Sprintf("distance calculation failed: %s", e.Reason)
}

// DistanceResult holds a distance and travel time.
type DistanceResult struct {
	DistanceMeters float64
	DurationSecs   float64
}

// Lookup provides point-to-point distance results.
type Lookup interface {
	GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error)
}

// SolveSource prepares distances for one routing solve.
type SolveSource interface {
	Lookup
	PrewarmPairs(ctx context.Context, pairs []DistancePair) error
}

// DistanceCalculator calculates and prewarms distances.
type DistanceCalculator interface {
	SolveSource
	GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error)
	GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error)
}
