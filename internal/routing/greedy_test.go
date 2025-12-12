package routing

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// Mock distance calculator for testing
type mockDistanceCalculator struct {
	distances map[string]float64
}

func newMockDistanceCalculator() *mockDistanceCalculator {
	return &mockDistanceCalculator{
		distances: make(map[string]float64),
	}
}

func (m *mockDistanceCalculator) setDistance(origin, dest models.Coordinates, distanceMeters float64) {
	key := makeKey(origin, dest)
	m.distances[key] = distanceMeters
}

func makeKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.6f,%.6f->%.6f,%.6f", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
}

func (m *mockDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	key := makeKey(origin, dest)
	if dist, ok := m.distances[key]; ok {
		return &distance.DistanceResult{
			DistanceMeters: dist,
			DurationSecs:   dist / 10, // Mock duration
		}, nil
	}
	// Default distance if not set
	return &distance.DistanceResult{
		DistanceMeters: 1000.0,
		DurationSecs:   100.0,
	}, nil
}

func (m *mockDistanceCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	n := len(points)
	matrix := make([][]distance.DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]distance.DistanceResult, n)
		for j := range matrix[i] {
			if i == j {
				matrix[i][j] = distance.DistanceResult{DistanceMeters: 0, DurationSecs: 0}
			} else {
				result, _ := m.GetDistance(ctx, points[i], points[j])
				matrix[i][j] = *result
			}
		}
	}
	return matrix, nil
}

func (m *mockDistanceCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	results := make([]distance.DistanceResult, len(destinations))
	for i, dest := range destinations {
		result, err := m.GetDistance(ctx, origin, dest)
		if err != nil {
			return nil, err
		}
		results[i] = *result
	}
	return results, nil
}

func (m *mockDistanceCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	return nil
}

func TestGreedyRouterSingleDriverFewParticipants(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	participants := []models.Participant{
		{ID: 1, Name: "P1", Lat: 40.1, Lng: -75.1},
		{ID: 2, Name: "P2", Lat: 40.2, Lng: -75.2},
	}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.5, Lng: -75.5, VehicleCapacity: 4},
	}

	// Set up mock distances
	calc.setDistance(institute, participants[0].GetCoords(), 1000.0)
	calc.setDistance(institute, participants[1].GetCoords(), 2000.0)
	calc.setDistance(participants[0].GetCoords(), participants[1].GetCoords(), 1500.0)
	calc.setDistance(participants[1].GetCoords(), drivers[0].GetCoords(), 3000.0)

	req := &RoutingRequest{
		InstituteCoords: institute,
		Participants:    participants,
		Drivers:         drivers,
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.Routes, 1)
	assert.Len(t, result.Routes[0].Stops, 2)
	assert.Equal(t, 2, result.Summary.TotalParticipants)
	assert.Equal(t, 1, result.Summary.TotalDriversUsed)
	assert.False(t, result.Summary.UsedInstituteVehicle)
}

func TestGreedyRouterMultipleDrivers(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	participants := []models.Participant{
		{ID: 1, Name: "P1", Lat: 40.1, Lng: -75.1},
		{ID: 2, Name: "P2", Lat: 40.2, Lng: -75.2},
		{ID: 3, Name: "P3", Lat: 40.3, Lng: -75.3},
		{ID: 4, Name: "P4", Lat: 40.4, Lng: -75.4},
	}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.5, Lng: -75.5, VehicleCapacity: 2},
		{ID: 2, Name: "D2", Lat: 40.6, Lng: -75.6, VehicleCapacity: 2},
	}

	req := &RoutingRequest{
		InstituteCoords: institute,
		Participants:    participants,
		Drivers:         drivers,
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.Routes, 2)
	assert.Equal(t, 4, result.Summary.TotalParticipants)
	assert.Equal(t, 2, result.Summary.TotalDriversUsed)

	// Each driver should have 2 participants (their capacity)
	assert.Len(t, result.Routes[0].Stops, 2)
	assert.Len(t, result.Routes[1].Stops, 2)
}

func TestGreedyRouterCapacityConstraint(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	participants := []models.Participant{
		{ID: 1, Name: "P1", Lat: 40.1, Lng: -75.1},
		{ID: 2, Name: "P2", Lat: 40.2, Lng: -75.2},
		{ID: 3, Name: "P3", Lat: 40.3, Lng: -75.3},
		{ID: 4, Name: "P4", Lat: 40.4, Lng: -75.4},
		{ID: 5, Name: "P5", Lat: 40.5, Lng: -75.5},
	}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.9, Lng: -75.9, VehicleCapacity: 2},
	}

	req := &RoutingRequest{
		InstituteCoords: institute,
		Participants:    participants,
		Drivers:         drivers,
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	// Should fail because we have 5 participants but only capacity for 2
	require.Error(t, err)
	assert.Nil(t, result)

	routingErr, ok := err.(*ErrRoutingFailed)
	require.True(t, ok)
	assert.Equal(t, 3, routingErr.UnassignedCount)
	assert.Equal(t, 2, routingErr.TotalCapacity)
	assert.Equal(t, 5, routingErr.TotalParticipants)
}

func TestGreedyRouterInstituteVehicleFallback(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	participants := []models.Participant{
		{ID: 1, Name: "P1", Lat: 40.1, Lng: -75.1},
		{ID: 2, Name: "P2", Lat: 40.2, Lng: -75.2},
		{ID: 3, Name: "P3", Lat: 40.3, Lng: -75.3},
	}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.5, Lng: -75.5, VehicleCapacity: 2},
	}

	instituteVehicle := &models.Driver{
		ID:                 100,
		Name:               "Institute Van",
		Lat:                institute.Lat,
		Lng:                institute.Lng,
		VehicleCapacity:    8,
		IsInstituteVehicle: true,
	}

	req := &RoutingRequest{
		InstituteCoords:          institute,
		Participants:             participants,
		Drivers:                  drivers,
		InstituteVehicle:         instituteVehicle,
		InstituteVehicleDriverID: 50, // Driver assigned to institute vehicle
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.Routes, 2)

	// First driver takes 2 participants, institute vehicle takes 1
	assert.Equal(t, 3, result.Summary.TotalParticipants)
	assert.Equal(t, 2, result.Summary.TotalDriversUsed)
	assert.True(t, result.Summary.UsedInstituteVehicle)

	// Find institute vehicle route
	var instituteRoute *models.CalculatedRoute
	for i := range result.Routes {
		if result.Routes[i].UsedInstituteVehicle {
			instituteRoute = &result.Routes[i]
			break
		}
	}

	require.NotNil(t, instituteRoute)
	assert.True(t, instituteRoute.UsedInstituteVehicle)
	assert.Equal(t, int64(50), instituteRoute.InstituteVehicleDriverID)
	assert.Len(t, instituteRoute.Stops, 1)
}

func TestGreedyRouterInsufficientCapacityEvenWithInstituteVehicle(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	participants := []models.Participant{
		{ID: 1, Name: "P1", Lat: 40.1, Lng: -75.1},
		{ID: 2, Name: "P2", Lat: 40.2, Lng: -75.2},
		{ID: 3, Name: "P3", Lat: 40.3, Lng: -75.3},
		{ID: 4, Name: "P4", Lat: 40.4, Lng: -75.4},
		{ID: 5, Name: "P5", Lat: 40.5, Lng: -75.5},
	}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.9, Lng: -75.9, VehicleCapacity: 2},
	}

	instituteVehicle := &models.Driver{
		ID:                 100,
		Name:               "Institute Van",
		Lat:                institute.Lat,
		Lng:                institute.Lng,
		VehicleCapacity:    2,
		IsInstituteVehicle: true,
	}

	req := &RoutingRequest{
		InstituteCoords:          institute,
		Participants:             participants,
		Drivers:                  drivers,
		InstituteVehicle:         instituteVehicle,
		InstituteVehicleDriverID: 50,
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	// Should fail: 5 participants, total capacity = 2 + 2 = 4
	require.Error(t, err)
	assert.Nil(t, result)

	routingErr, ok := err.(*ErrRoutingFailed)
	require.True(t, ok)
	assert.Equal(t, 1, routingErr.UnassignedCount)
	assert.Equal(t, 4, routingErr.TotalCapacity)
}

func TestGreedyRouterNoParticipants(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.5, Lng: -75.5, VehicleCapacity: 4},
	}

	req := &RoutingRequest{
		InstituteCoords: institute,
		Participants:    []models.Participant{},
		Drivers:         drivers,
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.Routes)
	assert.Equal(t, 0, result.Summary.TotalParticipants)
	assert.Equal(t, 0, result.Summary.TotalDriversUsed)
}

func TestGreedyRouterGreedyNearestSelection(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	participants := []models.Participant{
		{ID: 1, Name: "Near", Lat: 40.01, Lng: -75.01},  // Very close
		{ID: 2, Name: "Far", Lat: 40.5, Lng: -75.5},     // Far away
	}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.1, Lng: -75.1, VehicleCapacity: 2}, // Capacity for both
	}

	// Set explicit distances to test greedy selection
	calc.setDistance(institute, participants[0].GetCoords(), 100.0)  // Near participant
	calc.setDistance(institute, participants[1].GetCoords(), 5000.0) // Far participant
	calc.setDistance(participants[0].GetCoords(), participants[1].GetCoords(), 4500.0)
	calc.setDistance(participants[0].GetCoords(), drivers[0].GetCoords(), 1000.0)
	calc.setDistance(participants[1].GetCoords(), drivers[0].GetCoords(), 2000.0)

	req := &RoutingRequest{
		InstituteCoords: institute,
		Participants:    participants,
		Drivers:         drivers,
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	require.NoError(t, err)
	assert.Len(t, result.Routes, 1)
	assert.Len(t, result.Routes[0].Stops, 2)

	// Should select the nearest participant first (greedy algorithm)
	assert.Equal(t, int64(1), result.Routes[0].Stops[0].Participant.ID)
	assert.Equal(t, "Near", result.Routes[0].Stops[0].Participant.Name)
}

func TestGreedyRouterRouteStopOrdering(t *testing.T) {
	calc := newMockDistanceCalculator()
	router := NewGreedyRouter(calc)

	institute := models.Coordinates{Lat: 40.0, Lng: -75.0}

	participants := []models.Participant{
		{ID: 1, Name: "P1", Lat: 40.1, Lng: -75.1},
		{ID: 2, Name: "P2", Lat: 40.2, Lng: -75.2},
		{ID: 3, Name: "P3", Lat: 40.3, Lng: -75.3},
	}

	drivers := []models.Driver{
		{ID: 1, Name: "D1", Lat: 40.5, Lng: -75.5, VehicleCapacity: 3},
	}

	req := &RoutingRequest{
		InstituteCoords: institute,
		Participants:    participants,
		Drivers:         drivers,
	}

	ctx := context.Background()
	result, err := router.CalculateRoutes(ctx, req)

	require.NoError(t, err)
	assert.Len(t, result.Routes, 1)
	assert.Len(t, result.Routes[0].Stops, 3)

	// Verify route stop ordering
	assert.Equal(t, 0, result.Routes[0].Stops[0].Order)
	assert.Equal(t, 1, result.Routes[0].Stops[1].Order)
	assert.Equal(t, 2, result.Routes[0].Stops[2].Order)

	// Verify cumulative distances are increasing
	assert.True(t, result.Routes[0].Stops[1].CumulativeDistanceMeters >= result.Routes[0].Stops[0].CumulativeDistanceMeters)
	assert.True(t, result.Routes[0].Stops[2].CumulativeDistanceMeters >= result.Routes[0].Stops[1].CumulativeDistanceMeters)
}
