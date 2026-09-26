package distance

import (
	"context"
	"errors"
	"math"

	"ride-home-router/internal/models"
)

const (
	estimateEarthRadiusMeters      = 6371000.0
	estimateRoadFactor             = 1.3
	estimateSpeed25MPHMetersPerSec = 11.18
)

var errInvalidCoordinates = errors.New("distance: invalid coordinates")

type estimator struct{}

func NewEstimator() SolveSource { return estimator{} }

func (estimator) NoPrewarm() bool { return true }

func (estimator) PrewarmPairs(context.Context, []DistancePair) error { return nil }

func (estimator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validCoordinate(origin) || !validCoordinate(dest) {
		return nil, errInvalidCoordinates
	}
	if SamePoint(origin, dest) {
		return &DistanceResult{}, nil
	}
	meters := haversineMeters(origin, dest) * estimateRoadFactor
	return &DistanceResult{DistanceMeters: meters, DurationSecs: meters / estimateSpeed25MPHMetersPerSec}, nil
}

func validCoordinate(c models.Coordinates) bool {
	return !math.IsNaN(c.Lat) && !math.IsNaN(c.Lng) && !math.IsInf(c.Lat, 0) && !math.IsInf(c.Lng, 0) &&
		c.Lat >= -90 && c.Lat <= 90 && c.Lng >= -180 && c.Lng <= 180
}

func haversineMeters(a, b models.Coordinates) float64 {
	toRad := math.Pi / 180
	dLat := (b.Lat - a.Lat) * toRad
	dLng := (b.Lng - a.Lng) * toRad
	sinLat := math.Sin(dLat / 2)
	sinLng := math.Sin(dLng / 2)
	h := sinLat*sinLat + math.Cos(a.Lat*toRad)*math.Cos(b.Lat*toRad)*sinLng*sinLng
	return 2 * estimateEarthRadiusMeters * math.Asin(math.Min(1, math.Sqrt(h)))
}
