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

func TestCalculateOverCapacity(t *testing.T) {
	routes := []models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, VehicleCapacity: 2},
			Stops: []models.RouteStop{
				{Participant: &models.Participant{ID: 1}},
				{Participant: &models.Participant{ID: 2}},
			},
		},
		{
			Driver:              &models.Driver{ID: 2, VehicleCapacity: 3},
			EffectiveCapacity:   3,
			Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 3}}, {Participant: &models.Participant{ID: 4}}, {Participant: &models.Participant{ID: 5}}, {Participant: &models.Participant{ID: 6}}},
			TotalDistanceMeters: 1234,
		},
	}

	overCapacity, outOfBalance := calculateOverCapacity(routes)

	if !slices.Equal(overCapacity, []bool{false, true}) {
		t.Fatalf("overCapacity = %v, want [false true]", overCapacity)
	}
	if !outOfBalance {
		t.Fatal("outOfBalance = false, want true")
	}
}

func TestRecalculateDirtyRoutes(t *testing.T) {
	handler := &Handler{DistanceCalc: routeEditDistanceCalculator{}}
	session := &RouteSession{
		ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 0, Lng: 0},
		Mode:             "pickup",
		CurrentRoutes: []models.CalculatedRoute{
			{
				Driver: &models.Driver{ID: 1, Name: "Driver 1", Lat: 10, Lng: 0, VehicleCapacity: 4},
				Stops: []models.RouteStop{
					{Participant: &models.Participant{ID: 1, Name: "P1", Lat: 9, Lng: 0}},
				},
			},
			{
				Driver: &models.Driver{ID: 2, Name: "Driver 2", Lat: 8, Lng: 0, VehicleCapacity: 4},
				Stops: []models.RouteStop{
					{Participant: &models.Participant{ID: 2, Name: "P2", Lat: 7, Lng: 0}},
				},
			},
		},
		DirtyRouteIndexes: map[int]struct{}{
			0: {},
			1: {},
		},
	}

	backup := deepCopyRoutes(session.CurrentRoutes)
	if err := handler.recalculateDirtyRoutes(context.Background(), session, backup); err != nil {
		t.Fatalf("recalculateDirtyRoutes() error = %v", err)
	}

	if session.CurrentRoutes[0].TotalDistanceMeters == 0 {
		t.Fatal("route 0 total distance was not recalculated")
	}
	if session.CurrentRoutes[1].TotalDistanceMeters == 0 {
		t.Fatal("route 1 total distance was not recalculated")
	}
	if len(session.DirtyRouteIndexes) != 0 {
		t.Fatalf("dirty routes not cleared after recalculation: %v", session.DirtyRouteIndexes)
	}
}

func TestHandleMoveParticipant_RecalculatesAllDirtyRoutesWhenBalancedAgain(t *testing.T) {
	handler := &Handler{
		DistanceCalc: routeEditDistanceCalculator{},
		RouteSession: NewRouteSessionStore(),
	}
	t.Cleanup(handler.RouteSession.Close)

	activityLocation := &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 0, Lng: 0}
	routes := []models.CalculatedRoute{
		{
			Driver:              &models.Driver{ID: 1, Name: "Driver 1", Lat: 10, Lng: 0, VehicleCapacity: 2},
			EffectiveCapacity:   2,
			Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 101, Name: "P101", Lat: 9.5, Lng: 0}}, {Participant: &models.Participant{ID: 102, Name: "P102", Lat: 9.0, Lng: 0}}},
			TotalDistanceMeters: 111, // stale sentinel value
		},
		{
			Driver:              &models.Driver{ID: 2, Name: "Driver 2", Lat: 8, Lng: 0, VehicleCapacity: 1},
			EffectiveCapacity:   1,
			Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 201, Name: "P201", Lat: 7.5, Lng: 0}}},
			TotalDistanceMeters: 222, // stale sentinel value
		},
	}
	session := handler.RouteSession.Create(routes, []models.Driver{}, activityLocation, false, "18:30", "pickup", nil)

	move := func(participantID int64, fromRoute, toRoute int) {
		body, err := json.Marshal(map[string]any{
			"session_id":         session.ID,
			"participant_id":     participantID,
			"from_route_index":   fromRoute,
			"to_route_index":     toRoute,
			"insert_at_position": -1,
		})
		if err != nil {
			t.Fatalf("marshal move request: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/edit/move-participant", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		handler.HandleMoveParticipant(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("move participant status=%d body=%q", rr.Code, rr.Body.String())
		}
	}

	// Make routes imbalanced: route 2 goes from 1/1 to 2/1.
	move(101, 0, 1)
	// Restore balance: route 2 back to 1/1; this should recalculate all dirty routes.
	move(101, 1, 0)

	updatedSession := handler.RouteSession.Get(session.ID)
	if updatedSession == nil {
		t.Fatal("expected session to exist")
	}
	updatedSession.mu.Lock()
	defer updatedSession.mu.Unlock()

	if len(updatedSession.DirtyRouteIndexes) != 0 {
		t.Fatalf("expected dirty routes to be cleared, got %v", updatedSession.DirtyRouteIndexes)
	}
	if updatedSession.CurrentRoutes[0].TotalDistanceMeters == 111 {
		t.Fatal("route 0 metrics were not recalculated after returning to balanced")
	}
	if updatedSession.CurrentRoutes[1].TotalDistanceMeters == 222 {
		t.Fatal("route 1 metrics were not recalculated after returning to balanced")
	}
}
