package routing

import (
	"context"
	"testing"

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

func TestCalculateRoutes_EmptyParticipants(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewDistanceMinimizer(mock)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants:    []models.Participant{},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 1, Lng: 1, VehicleCapacity: 4},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(result.Routes))
	}
	if result.Summary.TotalParticipants != 0 {
		t.Errorf("expected 0 participants, got %d", result.Summary.TotalParticipants)
	}
}

func TestCalculateRoutes_SingleParticipantSingleDriver(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewDistanceMinimizer(mock)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "Alice", Lat: 0.01, Lng: 0.01},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.02, Lng: 0.02, VehicleCapacity: 4},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(result.Routes))
	}
	if len(result.Routes[0].Stops) != 1 {
		t.Errorf("expected 1 stop, got %d", len(result.Routes[0].Stops))
	}
	if result.Routes[0].Stops[0].Participant.Name != "Alice" {
		t.Errorf("expected Alice, got %s", result.Routes[0].Stops[0].Participant.Name)
	}
	if result.Summary.TotalDriversUsed != 1 {
		t.Errorf("expected 1 driver used, got %d", result.Summary.TotalDriversUsed)
	}
}

func TestCalculateRoutes_MultipleParticipantsOptimalAssignment(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewDistanceMinimizer(mock)

	// Two drivers at different locations, two participants
	// Participant1 at (0.1, 0) - closer to Driver1 at (0.2, 0)
	// Participant2 at (0, 0.1) - closer to Driver2 at (0, 0.2)
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "Participant1", Lat: 0.1, Lng: 0},
			{ID: 2, Name: "Participant2", Lat: 0, Lng: 0.1},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.2, Lng: 0, VehicleCapacity: 4},
			{ID: 2, Name: "Driver2", Lat: 0, Lng: 0.2, VehicleCapacity: 4},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all participants assigned
	totalStops := 0
	for _, route := range result.Routes {
		totalStops += len(route.Stops)
	}
	if totalStops != 2 {
		t.Errorf("expected 2 total stops, got %d", totalStops)
	}

	if result.Summary.TotalParticipants != 2 {
		t.Errorf("expected 2 participants, got %d", result.Summary.TotalParticipants)
	}
}

func TestCalculateRoutes_CapacityExceeded(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewDistanceMinimizer(mock)

	// 3 participants, 1 driver with capacity 2
	_, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 0.01, Lng: 0},
			{ID: 2, Name: "P2", Lat: 0.02, Lng: 0},
			{ID: 3, Name: "P3", Lat: 0.03, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.1, Lng: 0, VehicleCapacity: 2},
		},
	})

	if err == nil {
		t.Fatal("expected error when capacity exceeded, got nil")
	}

	routingErr, ok := err.(*ErrRoutingFailed)
	if !ok {
		t.Fatalf("expected ErrRoutingFailed, got %T", err)
	}
	if routingErr.UnassignedCount != 1 {
		t.Errorf("expected 1 unassigned, got %d", routingErr.UnassignedCount)
	}
	if routingErr.TotalCapacity != 2 {
		t.Errorf("expected total capacity 2, got %d", routingErr.TotalCapacity)
	}
}

func TestCalculateRoutes_TwoOptImprovesCrossing(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewDistanceMinimizer(mock)

	// Create a crossing scenario that 2-opt should fix:
	// Institute at (0,0), participants arranged so initial insertion creates crossing
	// A at (0.1, 0), B at (0.1, 0.1), C at (0, 0.1)
	// Optimal order from (0,0) is A -> B -> C or A -> C -> B depending on driver location
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "A", Lat: 0.1, Lng: 0},
			{ID: 2, Name: "B", Lat: 0.1, Lng: 0.1},
			{ID: 3, Name: "C", Lat: 0, Lng: 0.1},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.2, Lng: 0.2, VehicleCapacity: 4},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(result.Routes))
	}
	if len(result.Routes[0].Stops) != 3 {
		t.Fatalf("expected 3 stops, got %d", len(result.Routes[0].Stops))
	}

	// The algorithm should have optimized the route
	// We verify total distance is reasonable (non-zero)
	if result.Summary.TotalDropoffDistanceMeters <= 0 {
		t.Error("expected positive total distance")
	}
}

func TestCalculateRoutes_InterRouteMovesParticipant(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewDistanceMinimizer(mock)

	// Setup: P1 close to D2's route, P2 close to D1's route
	// After inter-route optimization, they might swap
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 0.05, Lng: 0.1},  // closer to east side
			{ID: 2, Name: "P2", Lat: 0.1, Lng: 0.05},  // closer to north side
			{ID: 3, Name: "P3", Lat: 0.02, Lng: 0.15}, // east side
			{ID: 4, Name: "P4", Lat: 0.15, Lng: 0.02}, // north side
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "D1_East", Lat: 0.1, Lng: 0.2, VehicleCapacity: 4},  // east
			{ID: 2, Name: "D2_North", Lat: 0.2, Lng: 0.1, VehicleCapacity: 4}, // north
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All 4 participants should be assigned
	totalStops := 0
	for _, route := range result.Routes {
		totalStops += len(route.Stops)
	}
	if totalStops != 4 {
		t.Errorf("expected 4 total stops, got %d", totalStops)
	}

	// Verify optimization ran (we can't guarantee specific outcome but distance should be reasonable)
	if result.Summary.TotalDropoffDistanceMeters <= 0 {
		t.Error("expected positive total distance")
	}
}

func TestInsertionCost_VariousPositions(t *testing.T) {
	mock := newMockDistanceAdapter()
	dm := &distanceMinimizer{distanceCalc: mock}

	institute := models.Coordinates{Lat: 0, Lng: 0}
	driver := &models.Driver{ID: 1, Name: "D1", Lat: 1, Lng: 1, VehicleCapacity: 4}

	p1 := &models.Participant{ID: 1, Name: "P1", Lat: 0.2, Lng: 0}
	p2 := &models.Participant{ID: 2, Name: "P2", Lat: 0.4, Lng: 0}
	pNew := &models.Participant{ID: 3, Name: "NewP", Lat: 0.3, Lng: 0}

	route := &dmRoute{
		driver: driver,
		stops:  []*models.Participant{p1, p2},
	}

	tests := []struct {
		name string
		pos  int
	}{
		{"insert_at_beginning", 0},
		{"insert_in_middle", 1},
		{"insert_at_end", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := dm.insertionCost(context.Background(), institute, route, pNew, tt.pos)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cost < 0 {
				t.Errorf("insertion cost should be non-negative, got %f", cost)
			}
		})
	}
}

func TestTotalDropoffDistance_Accumulation(t *testing.T) {
	mock := newMockDistanceAdapter()
	dm := &distanceMinimizer{distanceCalc: mock}

	institute := models.Coordinates{Lat: 0, Lng: 0}

	// Create routes with known positions
	routes := map[int64]*dmRoute{
		1: {
			driver: &models.Driver{ID: 1, Name: "D1", Lat: 1, Lng: 0, VehicleCapacity: 4},
			stops: []*models.Participant{
				{ID: 1, Name: "P1", Lat: 0.1, Lng: 0}, // 0.1 degrees from institute
				{ID: 2, Name: "P2", Lat: 0.2, Lng: 0}, // 0.1 degrees from P1
			},
		},
		2: {
			driver: &models.Driver{ID: 2, Name: "D2", Lat: 0, Lng: 1, VehicleCapacity: 4},
			stops: []*models.Participant{
				{ID: 3, Name: "P3", Lat: 0, Lng: 0.1}, // 0.1 degrees from institute
			},
		},
	}

	total, err := dm.totalDropoffDistance(context.Background(), institute, routes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Total should be sum of all segment distances
	// Route 1: institute->P1 (0.1°) + P1->P2 (0.1°) = 0.2° * scale
	// Route 2: institute->P3 (0.1°) = 0.1° * scale
	// Total = 0.3° * 111000 m/° ≈ 33300m
	expectedApprox := 0.3 * 111000

	// Allow 1% tolerance for floating point
	tolerance := expectedApprox * 0.01
	if total < expectedApprox-tolerance || total > expectedApprox+tolerance {
		t.Errorf("expected total distance ~%.0f, got %.0f", expectedApprox, total)
	}
}

func TestCalculateRoutes_MultipleDriversDifferentCapacities(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewDistanceMinimizer(mock)

	// 5 participants, driver1 has capacity 2, driver2 has capacity 3
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 0.01, Lng: 0},
			{ID: 2, Name: "P2", Lat: 0.02, Lng: 0},
			{ID: 3, Name: "P3", Lat: 0.03, Lng: 0},
			{ID: 4, Name: "P4", Lat: 0.04, Lng: 0},
			{ID: 5, Name: "P5", Lat: 0.05, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.1, Lng: 0, VehicleCapacity: 2},
			{ID: 2, Name: "Driver2", Lat: 0.1, Lng: 0.1, VehicleCapacity: 3},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all 5 participants assigned
	totalStops := 0
	for _, route := range result.Routes {
		totalStops += len(route.Stops)
		// Verify capacity not exceeded
		if len(route.Stops) > route.Driver.VehicleCapacity {
			t.Errorf("route for %s has %d stops but capacity is %d",
				route.Driver.Name, len(route.Stops), route.Driver.VehicleCapacity)
		}
	}
	if totalStops != 5 {
		t.Errorf("expected 5 total stops, got %d", totalStops)
	}
}
