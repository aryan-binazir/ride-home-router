package handlers

import (
	"context"
	"math"
	"testing"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

type routeEditDistanceCalculator struct{}

func (routeEditDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	dist := math.Hypot(dest.Lat-origin.Lat, dest.Lng-origin.Lng) * 1000
	return &distance.DistanceResult{
		DistanceMeters: dist,
		DurationSecs:   dist,
	}, nil
}

func (calc routeEditDistanceCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	matrix := make([][]distance.DistanceResult, len(points))
	for i := range points {
		matrix[i] = make([]distance.DistanceResult, len(points))
		for j := range points {
			dist, err := calc.GetDistance(ctx, points[i], points[j])
			if err != nil {
				return nil, err
			}
			matrix[i][j] = *dist
		}
	}
	return matrix, nil
}

func (calc routeEditDistanceCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	results := make([]distance.DistanceResult, len(destinations))
	for i, dest := range destinations {
		dist, err := calc.GetDistance(ctx, origin, dest)
		if err != nil {
			return nil, err
		}
		results[i] = *dist
	}
	return results, nil
}

func (routeEditDistanceCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	return nil
}

func TestRoutesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []models.CalculatedRoute
		want bool
	}{
		{
			name: "both empty",
			a:    []models.CalculatedRoute{},
			b:    []models.CalculatedRoute{},
			want: true,
		},
		{
			name: "both nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "different lengths",
			a:    []models.CalculatedRoute{{}},
			b:    []models.CalculatedRoute{{}, {}},
			want: false,
		},
		{
			name: "same structure",
			a: []models.CalculatedRoute{
				{
					Driver: &models.Driver{ID: 1},
					Stops: []models.RouteStop{
						{Participant: &models.Participant{ID: 10}},
						{Participant: &models.Participant{ID: 20}},
					},
				},
			},
			b: []models.CalculatedRoute{
				{
					Driver: &models.Driver{ID: 1},
					Stops: []models.RouteStop{
						{Participant: &models.Participant{ID: 10}},
						{Participant: &models.Participant{ID: 20}},
					},
				},
			},
			want: true,
		},
		{
			name: "different driver IDs",
			a: []models.CalculatedRoute{
				{Driver: &models.Driver{ID: 1}, Stops: []models.RouteStop{}},
			},
			b: []models.CalculatedRoute{
				{Driver: &models.Driver{ID: 2}, Stops: []models.RouteStop{}},
			},
			want: false,
		},
		{
			name: "different participant order",
			a: []models.CalculatedRoute{
				{
					Driver: &models.Driver{ID: 1},
					Stops: []models.RouteStop{
						{Participant: &models.Participant{ID: 10}},
						{Participant: &models.Participant{ID: 20}},
					},
				},
			},
			b: []models.CalculatedRoute{
				{
					Driver: &models.Driver{ID: 1},
					Stops: []models.RouteStop{
						{Participant: &models.Participant{ID: 20}},
						{Participant: &models.Participant{ID: 10}},
					},
				},
			},
			want: false,
		},
		{
			name: "different stop counts",
			a: []models.CalculatedRoute{
				{
					Driver: &models.Driver{ID: 1},
					Stops:  []models.RouteStop{{Participant: &models.Participant{ID: 10}}},
				},
			},
			b: []models.CalculatedRoute{
				{
					Driver: &models.Driver{ID: 1},
					Stops:  []models.RouteStop{},
				},
			},
			want: false,
		},
		{
			name: "nil drivers both sides",
			a: []models.CalculatedRoute{
				{Driver: nil, Stops: []models.RouteStop{}},
			},
			b: []models.CalculatedRoute{
				{Driver: nil, Stops: []models.RouteStop{}},
			},
			want: true,
		},
		{
			name: "nil vs non-nil driver",
			a: []models.CalculatedRoute{
				{Driver: nil, Stops: []models.RouteStop{}},
			},
			b: []models.CalculatedRoute{
				{Driver: &models.Driver{ID: 1}, Stops: []models.RouteStop{}},
			},
			want: false,
		},
		{
			name: "ignores metric differences",
			a: []models.CalculatedRoute{
				{
					Driver:              &models.Driver{ID: 1},
					Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10}}},
					TotalDistanceMeters: 1000,
				},
			},
			b: []models.CalculatedRoute{
				{
					Driver:              &models.Driver{ID: 1},
					Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10}}},
					TotalDistanceMeters: 9999,
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routesEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("routesEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecalculateRoutePickupUsesModeAwareMetrics(t *testing.T) {
	handler := &Handler{DistanceCalc: routeEditDistanceCalculator{}}
	activityLocation := &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 0, Lng: 0}
	route := &models.CalculatedRoute{
		Driver: &models.Driver{ID: 1, Name: "Driver", Lat: 10, Lng: 0, VehicleCapacity: 4},
		Stops: []models.RouteStop{
			{Participant: &models.Participant{ID: 1, Name: "P1", Lat: 8, Lng: 0}},
			{Participant: &models.Participant{ID: 2, Name: "P2", Lat: 3, Lng: 0}},
		},
	}

	if err := handler.recalculateRoute(context.Background(), activityLocation, "pickup", route); err != nil {
		t.Fatalf("recalculateRoute() error = %v", err)
	}

	if route.Mode != "pickup" {
		t.Fatalf("route.Mode = %q, want pickup", route.Mode)
	}
	if route.TotalDropoffDistanceMeters != 7000 {
		t.Fatalf("TotalDropoffDistanceMeters = %.0f, want 7000", route.TotalDropoffDistanceMeters)
	}
	if route.DistanceToDriverHomeMeters != 3000 {
		t.Fatalf("DistanceToDriverHomeMeters = %.0f, want 3000", route.DistanceToDriverHomeMeters)
	}
	if route.TotalDistanceMeters != 10000 {
		t.Fatalf("TotalDistanceMeters = %.0f, want 10000", route.TotalDistanceMeters)
	}
	if route.Stops[0].DistanceFromPrevMeters != 2000 {
		t.Fatalf("first stop distance = %.0f, want 2000", route.Stops[0].DistanceFromPrevMeters)
	}
	if route.Stops[1].DistanceFromPrevMeters != 5000 {
		t.Fatalf("second stop distance = %.0f, want 5000", route.Stops[1].DistanceFromPrevMeters)
	}
}
