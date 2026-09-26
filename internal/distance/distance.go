package distance

import (
	"context"
	"ride-home-router/internal/models"
)

type ErrDistanceCalculationFailed struct {
	Origin models.Coordinates
	Dest   models.Coordinates
	Reason string
	Cause  error
}

func (e *ErrDistanceCalculationFailed) Error() string {
	return e.Reason
}

func (e *ErrDistanceCalculationFailed) Unwrap() error { return e.Cause }

type DistanceResult struct {
	DistanceMeters float64
	DurationSecs   float64
}

type Lookup interface {
	GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error)
}

type SolveSource interface {
	Lookup
	PrewarmPairs(ctx context.Context, pairs []DistancePair) error
}

type DistanceCalculator interface {
	SolveSource
	GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error)
	GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error)
}
