package routing

import (
	"context"
	"math"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

type mockDistanceAdapter struct{}

func newMockDistanceAdapter() *mockDistanceAdapter {
	return &mockDistanceAdapter{}
}

func (a *mockDistanceAdapter) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	if distance.SamePoint(origin, dest) {
		return &distance.DistanceResult{}, nil
	}
	distanceMeters := math.Hypot(dest.Lat-origin.Lat, dest.Lng-origin.Lng) * 111000
	return &distance.DistanceResult{
		DistanceMeters: distanceMeters,
		DurationSecs:   distanceMeters / 50000 * 3600,
	}, nil
}

func (a *mockDistanceAdapter) PrewarmPairs(ctx context.Context, pairs []distance.DistancePair) error {
	return nil
}
