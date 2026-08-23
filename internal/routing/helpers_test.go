package routing

import (
	"context"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/testutil"
)

// mockDistanceAdapter adapts testutil.MockDistanceCalculator to distance.SolveSource.
type mockDistanceAdapter struct {
	mock *testutil.MockDistanceCalculator
}

func newMockDistanceAdapter() *mockDistanceAdapter {
	return &mockDistanceAdapter{mock: testutil.NewMockDistanceCalculator()}
}

func (a *mockDistanceAdapter) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	r, err := a.mock.GetDistance(ctx, origin, dest)
	if err != nil {
		return nil, err
	}
	return &distance.DistanceResult{DistanceMeters: r.DistanceMeters, DurationSecs: r.DurationSecs}, nil
}

func (a *mockDistanceAdapter) PrewarmPairs(ctx context.Context, pairs []distance.DistancePair) error {
	return a.mock.PrewarmPairs(ctx, pairs)
}
