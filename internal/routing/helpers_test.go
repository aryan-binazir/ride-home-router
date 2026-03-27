package routing

import (
	"context"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/testutil"
)

// mockDistanceAdapter adapts testutil.MockDistanceCalculator to distance.DistanceCalculator
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

func (a *mockDistanceAdapter) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	r, err := a.mock.GetDistanceMatrix(ctx, points)
	if err != nil {
		return nil, err
	}
	result := make([][]distance.DistanceResult, len(r))
	for i := range r {
		result[i] = make([]distance.DistanceResult, len(r[i]))
		for j := range r[i] {
			result[i][j] = distance.DistanceResult{DistanceMeters: r[i][j].DistanceMeters, DurationSecs: r[i][j].DurationSecs}
		}
	}
	return result, nil
}

func (a *mockDistanceAdapter) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	r, err := a.mock.GetDistancesFromPoint(ctx, origin, destinations)
	if err != nil {
		return nil, err
	}
	result := make([]distance.DistanceResult, len(r))
	for i := range r {
		result[i] = distance.DistanceResult{DistanceMeters: r[i].DistanceMeters, DurationSecs: r[i].DurationSecs}
	}
	return result, nil
}

func (a *mockDistanceAdapter) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	return a.mock.PrewarmCache(ctx, points)
}
