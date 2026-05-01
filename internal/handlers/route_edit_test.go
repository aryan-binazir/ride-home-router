package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestGetUnusedDrivers_ExcludesDriversAlreadyRenderedAsRoutes(t *testing.T) {
	session := &RouteSession{
		SelectedDrivers: []models.Driver{
			{ID: 1, Name: "Driver1", VehicleCapacity: 4},
			{ID: 2, Name: "Driver2", VehicleCapacity: 4},
			{ID: 3, Name: "Driver3", VehicleCapacity: 4},
		},
		CurrentRoutes: []models.CalculatedRoute{
			{
				Driver: &models.Driver{ID: 1, Name: "Driver1", VehicleCapacity: 4},
				Stops: []models.RouteStop{
					{Participant: &models.Participant{ID: 10, Name: "Alice"}},
				},
			},
			{
				Driver: &models.Driver{ID: 2, Name: "Driver2", VehicleCapacity: 4},
				Stops:  []models.RouteStop{},
			},
		},
	}

	unused := getUnusedDrivers(session)
	gotIDs := make([]int64, 0, len(unused))
	for _, driver := range unused {
		gotIDs = append(gotIDs, driver.ID)
	}

	if !slices.Equal(gotIDs, []int64{3}) {
		t.Fatalf("getUnusedDrivers() IDs = %v, want [3]", gotIDs)
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

func TestHandleMoveParticipantOptimizesDestinationRoute(t *testing.T) {
	store := NewRouteSessionStore()
	defer store.Close()

	handler := &Handler{
		DistanceCalc: routeEditDistanceCalculator{},
		RouteSession: store,
	}
	activityLocation := &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 0, Lng: 0}
	session := store.Create([]models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, Name: "From", Lat: 10, Lng: 0, VehicleCapacity: 2},
			Stops: []models.RouteStop{
				{Participant: &models.Participant{ID: 2, Name: "Origin Detour", Lat: 1, Lng: 100}},
			},
		},
		{
			Driver: &models.Driver{ID: 2, Name: "To", Lat: 10, Lng: 0, VehicleCapacity: 3},
			Stops: []models.RouteStop{
				{Participant: &models.Participant{ID: 1, Name: "Destination Side", Lat: 9, Lng: 0}},
			},
		},
	}, []models.Driver{}, activityLocation, false, "18:30", models.RouteModeDropoff, nil)

	body, err := json.Marshal(map[string]any{
		"session_id":         session.ID,
		"participant_id":     int64(2),
		"from_route_index":   0,
		"to_route_index":     1,
		"insert_at_position": -1,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/edit/move-participant", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.HandleMoveParticipant(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	updated := store.Get(session.ID)
	if updated == nil {
		t.Fatal("expected route session to remain available")
	}
	toRoute := updated.CurrentRoutes[1]
	if len(toRoute.Stops) != 2 {
		t.Fatalf("destination stops = %d, want 2", len(toRoute.Stops))
	}
	if toRoute.Stops[0].Participant.Name != "Origin Detour" {
		t.Fatalf("first destination stop = %q, want Origin Detour", toRoute.Stops[0].Participant.Name)
	}
	if toRoute.Stops[0].Order != 0 || toRoute.Stops[1].Order != 1 {
		t.Fatalf("destination orders = [%d %d], want [0 1]", toRoute.Stops[0].Order, toRoute.Stops[1].Order)
	}
	if toRoute.RouteDurationSecs == 0 {
		t.Fatal("destination route metrics were not refreshed")
	}
}
