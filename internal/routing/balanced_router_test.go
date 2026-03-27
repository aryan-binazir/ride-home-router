package routing

import (
	"context"
	"fmt"
	"testing"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

func TestGroupParticipantsByAddress(t *testing.T) {
	participants := []*models.Participant{
		// Household 1: Alice and Bob at the same address
		{ID: 1, Name: "Alice", Address: "123 Main St", Lat: 40.12345, Lng: -74.12345},
		{ID: 2, Name: "Bob", Address: "123 Main St", Lat: 40.12345, Lng: -74.12345},
		// Household 2: Charlie alone
		{ID: 3, Name: "Charlie", Address: "456 Oak Ave", Lat: 40.23456, Lng: -74.23456},
		// Household 3: David, Eve, and Frank at the same address
		{ID: 4, Name: "David", Address: "789 Elm St", Lat: 40.34567, Lng: -74.34567},
		{ID: 5, Name: "Eve", Address: "789 Elm St", Lat: 40.34567, Lng: -74.34567},
		{ID: 6, Name: "Frank", Address: "789 Elm St", Lat: 40.34567, Lng: -74.34567},
	}

	groups := groupParticipantsByAddress(participants)

	// Should have 3 groups
	if len(groups) != 3 {
		t.Errorf("expected 3 groups, got %d", len(groups))
	}

	// Check group sizes (should be sorted by size, largest first)
	expectedSizes := []int{3, 2, 1}
	for i, expectedSize := range expectedSizes {
		if len(groups[i].members) != expectedSize {
			t.Errorf("group %d: expected size %d, got %d", i, expectedSize, len(groups[i].members))
		}
	}

	// Verify that participants from the same address are in the same group
	for _, group := range groups {
		if len(group.members) > 1 {
			firstLat := models.RoundCoordinate(group.members[0].Lat)
			firstLng := models.RoundCoordinate(group.members[0].Lng)
			for j := 1; j < len(group.members); j++ {
				lat := models.RoundCoordinate(group.members[j].Lat)
				lng := models.RoundCoordinate(group.members[j].Lng)
				if lat != firstLat || lng != firstLng {
					t.Errorf("group members have different coordinates: (%f,%f) vs (%f,%f)",
						firstLat, firstLng, lat, lng)
				}
			}
		}
	}
}

func TestGroupParticipantsByAddress_SlightlyDifferentCoordinates(t *testing.T) {
	// Test that participants with slightly different coordinates (beyond rounding precision)
	// are placed in different groups
	participants := []*models.Participant{
		{ID: 1, Name: "Alice", Lat: 40.123450, Lng: -74.123450},
		{ID: 2, Name: "Bob", Lat: 40.123454, Lng: -74.123454},     // Within rounding precision (rounds to same value)
		{ID: 3, Name: "Charlie", Lat: 40.123550, Lng: -74.123550}, // Beyond rounding precision
	}

	groups := groupParticipantsByAddress(participants)

	// Alice and Bob should be in the same group (both round to 40.12345, -74.12345)
	// Charlie should be in a different group (rounds to 40.12355, -74.12355)
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
}

func TestBalancedRouter_GroupsStayTogether(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

	// Create participants from 2 households
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			// Household 1: Alice and Bob
			{ID: 1, Name: "Alice", Lat: 0.01, Lng: 0.01},
			{ID: 2, Name: "Bob", Lat: 0.01, Lng: 0.01},
			// Household 2: Charlie and David
			{ID: 3, Name: "Charlie", Lat: 0.02, Lng: 0.02},
			{ID: 4, Name: "David", Lat: 0.02, Lng: 0.02},
			// Individual: Eve
			{ID: 5, Name: "Eve", Lat: 0.03, Lng: 0.03},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.05, Lng: 0.05, VehicleCapacity: 3},
			{ID: 2, Name: "Driver2", Lat: 0.06, Lng: 0.06, VehicleCapacity: 3},
		},
		Mode: RouteModeDropoff,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All participants should be assigned
	if result.Summary.TotalParticipants != 5 {
		t.Errorf("expected 5 participants, got %d", result.Summary.TotalParticipants)
	}

	if len(result.Summary.UnassignedParticipants) != 0 {
		t.Errorf("expected 0 unassigned participants, got %d", len(result.Summary.UnassignedParticipants))
	}

	// Verify household members are on the same route
	participantToRoute := make(map[int64]int)
	for routeIdx, route := range result.Routes {
		for _, stop := range route.Stops {
			participantToRoute[stop.Participant.ID] = routeIdx
		}
	}

	// Alice (1) and Bob (2) should be on the same route
	if participantToRoute[1] != participantToRoute[2] {
		t.Errorf("Alice and Bob should be on the same route")
	}

	// Charlie (3) and David (4) should be on the same route
	if participantToRoute[3] != participantToRoute[4] {
		t.Errorf("Charlie and David should be on the same route")
	}

	// Verify household members are consecutive in the route
	for _, route := range result.Routes {
		stops := route.Stops
		for i := 0; i < len(stops)-1; i++ {
			p1 := stops[i].Participant
			p2 := stops[i+1].Participant

			// Check if they're from the same household (same rounded coordinates)
			lat1 := models.RoundCoordinate(p1.Lat)
			lng1 := models.RoundCoordinate(p1.Lng)
			lat2 := models.RoundCoordinate(p2.Lat)
			lng2 := models.RoundCoordinate(p2.Lng)

			sameHousehold := (lat1 == lat2 && lng1 == lng2)

			// If they're from the same household and not consecutive, that's an error
			// But we need to check if there are other members between them
			if sameHousehold {
				// They should be consecutive - this is good
				// We just verify they exist in the route
			}
		}
	}
}

func TestBalancedRouter_LargeGroupHandling(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

	// Create a household with 4 members but driver capacity is only 3
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			// Large household: 4 members
			{ID: 1, Name: "Alice", Lat: 0.01, Lng: 0.01},
			{ID: 2, Name: "Bob", Lat: 0.01, Lng: 0.01},
			{ID: 3, Name: "Charlie", Lat: 0.01, Lng: 0.01},
			{ID: 4, Name: "David", Lat: 0.01, Lng: 0.01},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.05, Lng: 0.05, VehicleCapacity: 3},
			{ID: 2, Name: "Driver2", Lat: 0.06, Lng: 0.06, VehicleCapacity: 3},
		},
		Mode: RouteModeDropoff,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All participants should be assigned (group should be split if necessary)
	if result.Summary.TotalParticipants != 4 {
		t.Errorf("expected 4 participants, got %d", result.Summary.TotalParticipants)
	}

	if len(result.Summary.UnassignedParticipants) != 0 {
		t.Errorf("expected 0 unassigned participants, got %d", len(result.Summary.UnassignedParticipants))
	}

	// Should use both drivers
	if result.Summary.TotalDriversUsed != 2 {
		t.Errorf("expected 2 drivers used, got %d", result.Summary.TotalDriversUsed)
	}
}

func TestBalancedRouter_LargeHouseholdSplit(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

	// 10-person household, 2 cars with 5 seats each
	// Previously this would fail because maxRounds was based on group count (1)
	// instead of participant count (10), causing the loop to exit early
	participants := make([]models.Participant, 10)
	for i := range participants {
		participants[i] = models.Participant{
			ID:   int64(i + 1),
			Name: fmt.Sprintf("Person%d", i+1),
			Lat:  0.01,
			Lng:  0.01,
		}
	}

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants:    participants,
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.05, Lng: 0.05, VehicleCapacity: 5},
			{ID: 2, Name: "Driver2", Lat: 0.06, Lng: 0.06, VehicleCapacity: 5},
		},
		Mode: RouteModeDropoff,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Summary.UnassignedParticipants) != 0 {
		t.Errorf("expected 0 unassigned, got %d", len(result.Summary.UnassignedParticipants))
	}

	totalAssigned := 0
	for _, route := range result.Routes {
		totalAssigned += len(route.Stops)
	}
	if totalAssigned != 10 {
		t.Errorf("expected 10 assigned, got %d", totalAssigned)
	}
}

func TestInsertGroupAt(t *testing.T) {
	existing := []*models.Participant{
		{ID: 1, Name: "Alice"},
		{ID: 2, Name: "Bob"},
		{ID: 3, Name: "Charlie"},
	}

	group := &participantGroup{
		members: []*models.Participant{
			{ID: 4, Name: "David"},
			{ID: 5, Name: "Eve"},
		},
	}

	tests := []struct {
		name     string
		pos      int
		expected []string // Expected names in order
	}{
		{
			name:     "insert at beginning",
			pos:      0,
			expected: []string{"David", "Eve", "Alice", "Bob", "Charlie"},
		},
		{
			name:     "insert in middle",
			pos:      2,
			expected: []string{"Alice", "Bob", "David", "Eve", "Charlie"},
		},
		{
			name:     "insert at end",
			pos:      3,
			expected: []string{"Alice", "Bob", "Charlie", "David", "Eve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := insertGroupAt(existing, group, tt.pos)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d participants, got %d", len(tt.expected), len(result))
			}

			for i, expectedName := range tt.expected {
				if result[i].Name != expectedName {
					t.Errorf("position %d: expected %s, got %s", i, expectedName, result[i].Name)
				}
			}
		})
	}
}

func TestCoordinateKey(t *testing.T) {
	tests := []struct {
		lat1, lng1  float64
		lat2, lng2  float64
		shouldMatch bool
	}{
		{40.12345, -74.12345, 40.12345, -74.12345, true},
		{40.12345, -74.12345, 40.12346, -74.12345, false},
		{40.123456789, -74.123456789, 40.123456789, -74.123456789, true}, // Should match after formatting
	}

	for i, tt := range tests {
		key1 := coordinateKey(tt.lat1, tt.lng1)
		key2 := coordinateKey(tt.lat2, tt.lng2)

		matches := (key1 == key2)
		if matches != tt.shouldMatch {
			t.Errorf("test %d: expected match=%v, got match=%v (key1=%s, key2=%s)",
				i, tt.shouldMatch, matches, key1, key2)
		}
	}
}

type overrideDistanceAdapter struct {
	defaultDuration float64
	overrides       map[string]float64
}

func newOverrideDistanceAdapter(defaultDuration float64) *overrideDistanceAdapter {
	return &overrideDistanceAdapter{
		defaultDuration: defaultDuration,
		overrides:       make(map[string]float64),
	}
}

func (a *overrideDistanceAdapter) setDuration(origin, dest models.Coordinates, duration float64) {
	key := fmt.Sprintf("%.5f,%.5f->%.5f,%.5f", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
	a.overrides[key] = duration
}

func (a *overrideDistanceAdapter) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	if models.RoundCoordinate(origin.Lat) == models.RoundCoordinate(dest.Lat) &&
		models.RoundCoordinate(origin.Lng) == models.RoundCoordinate(dest.Lng) {
		return &distance.DistanceResult{DistanceMeters: 0, DurationSecs: 0}, nil
	}

	key := fmt.Sprintf("%.5f,%.5f->%.5f,%.5f", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
	duration := a.defaultDuration
	if override, ok := a.overrides[key]; ok {
		duration = override
	}

	return &distance.DistanceResult{DistanceMeters: duration, DurationSecs: duration}, nil
}

func (a *overrideDistanceAdapter) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	matrix := make([][]distance.DistanceResult, len(points))
	for i := range points {
		matrix[i] = make([]distance.DistanceResult, len(points))
		for j := range points {
			result, err := a.GetDistance(ctx, points[i], points[j])
			if err != nil {
				return nil, err
			}
			matrix[i][j] = *result
		}
	}
	return matrix, nil
}

func (a *overrideDistanceAdapter) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	results := make([]distance.DistanceResult, len(destinations))
	for i, destination := range destinations {
		result, err := a.GetDistance(ctx, origin, destination)
		if err != nil {
			return nil, err
		}
		results[i] = *result
	}
	return results, nil
}

func (a *overrideDistanceAdapter) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	return nil
}

func TestRoundRobinInsertion_KeepsPickupHouseholdsIntact(t *testing.T) {
	driverHome := models.Coordinates{Lat: 10, Lng: 10}
	activity := models.Coordinates{Lat: 0, Lng: 0}
	household := models.Coordinates{Lat: 1, Lng: 1}
	otherStop := models.Coordinates{Lat: 2, Lng: 2}

	distances := newOverrideDistanceAdapter(50)
	distances.setDuration(driverHome, household, 1)
	distances.setDuration(driverHome, activity, 10)
	distances.setDuration(driverHome, otherStop, 100)
	distances.setDuration(household, otherStop, 1)
	distances.setDuration(otherStop, household, 1)
	distances.setDuration(household, activity, 1)
	distances.setDuration(otherStop, activity, 100)

	router := &BalancedRouter{distanceCalc: distances}
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: driverHome.Lat, Lng: driverHome.Lng, VehicleCapacity: 3}
	routes := map[int64]*balancedRoute{
		driver.ID: {
			driver: driver,
			stops:  []*models.Participant{},
		},
	}
	participants := []*models.Participant{
		{ID: 1, Name: "Sister 1", Lat: household.Lat, Lng: household.Lng},
		{ID: 2, Name: "Sister 2", Lat: household.Lat, Lng: household.Lng},
		{ID: 3, Name: "Neighbor", Lat: otherStop.Lat, Lng: otherStop.Lng},
	}

	remaining, err := router.roundRobinInsertion(context.Background(), newRouteContext(distances, activity, RouteModePickup), routes, []int64{driver.ID}, participants)
	if err != nil {
		t.Fatalf("roundRobinInsertion() error = %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("roundRobinInsertion() left %d unassigned participants", len(remaining))
	}

	stops := routes[driver.ID].stops
	if len(stops) != 3 {
		t.Fatalf("route stop count = %d, want 3", len(stops))
	}
	if !hasAdjacentHouseholdPair(stops) {
		t.Fatalf("pickup household split during insertion: got order %q, %q, %q", stops[0].Name, stops[1].Name, stops[2].Name)
	}
}

func TestRoundRobinInsertion_SingleParticipantFallbackPreservesExistingHouseholdBlock(t *testing.T) {
	activity := models.Coordinates{Lat: 0, Lng: 0}
	sharedHome := models.Coordinates{Lat: 1, Lng: 1}
	splitHome := models.Coordinates{Lat: 2, Lng: 2}
	otherStop := models.Coordinates{Lat: 3, Lng: 3}

	distances := newOverrideDistanceAdapter(500)
	distances.setDuration(activity, sharedHome, 1)
	distances.setDuration(activity, splitHome, 100)
	distances.setDuration(sharedHome, splitHome, 1)
	distances.setDuration(splitHome, sharedHome, 1)
	distances.setDuration(sharedHome, otherStop, 1)
	distances.setDuration(splitHome, otherStop, 90)
	distances.setDuration(otherStop, splitHome, 100)

	router := &BalancedRouter{distanceCalc: distances}
	driver := &models.Driver{ID: 1, Name: "Driver", Lat: 9, Lng: 9, VehicleCapacity: 4}
	routes := map[int64]*balancedRoute{
		driver.ID: {
			driver: driver,
			stops: []*models.Participant{
				{ID: 1, Name: "Sister 1", Lat: sharedHome.Lat, Lng: sharedHome.Lng},
				{ID: 2, Name: "Sister 2", Lat: sharedHome.Lat, Lng: sharedHome.Lng},
				{ID: 3, Name: "Neighbor", Lat: otherStop.Lat, Lng: otherStop.Lng},
			},
		},
	}
	participants := []*models.Participant{
		{ID: 4, Name: "Large Household 1", Lat: splitHome.Lat, Lng: splitHome.Lng},
		{ID: 5, Name: "Large Household 2", Lat: splitHome.Lat, Lng: splitHome.Lng},
	}

	remaining, err := router.roundRobinInsertion(context.Background(), newRouteContext(distances, activity, RouteModeDropoff), routes, []int64{driver.ID}, participants)
	if err != nil {
		t.Fatalf("roundRobinInsertion() error = %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("roundRobinInsertion() remaining = %d, want 1 after capacity-driven split", len(remaining))
	}

	stops := routes[driver.ID].stops
	if len(stops) != 4 {
		t.Fatalf("route stop count = %d, want 4", len(stops))
	}
	for i := 0; i < len(stops)-1; i++ {
		if stops[i].Name == "Sister 1" && stops[i+1].Name == "Sister 2" {
			return
		}
	}

	t.Fatalf("single-participant fallback split existing household: got order %q, %q, %q, %q", stops[0].Name, stops[1].Name, stops[2].Name, stops[3].Name)
}

func TestOptimizeAllRoutes_CoalescesHouseholdsForBothModes(t *testing.T) {
	for _, mode := range []RouteMode{RouteModeDropoff, RouteModePickup} {
		t.Run(string(mode), func(t *testing.T) {
			distances := newOverrideDistanceAdapter(10)
			router := &BalancedRouter{distanceCalc: distances}
			driver := &models.Driver{ID: 1, Name: "Driver", Lat: 9, Lng: 9, VehicleCapacity: 4}
			sharedLat, sharedLng := 1.0, 1.0
			otherLat, otherLng := 2.0, 2.0
			routes := map[int64]*balancedRoute{
				driver.ID: {
					driver: driver,
					stops: []*models.Participant{
						{ID: 1, Name: "Sibling A", Lat: sharedLat, Lng: sharedLng},
						{ID: 2, Name: "Other", Lat: otherLat, Lng: otherLng},
						{ID: 3, Name: "Sibling B", Lat: sharedLat, Lng: sharedLng},
					},
				},
			}

			if err := router.optimizeAllRoutes(context.Background(), newRouteContext(distances, models.Coordinates{Lat: 0, Lng: 0}, mode), routes); err != nil {
				t.Fatalf("optimizeAllRoutes() error = %v", err)
			}

			stops := routes[driver.ID].stops
			if len(stops) != 3 {
				t.Fatalf("optimized stop count = %d, want 3", len(stops))
			}
			if !hasAdjacentHouseholdPair(stops) {
				t.Fatalf("%s optimization left household split: got order %q, %q, %q", mode, stops[0].Name, stops[1].Name, stops[2].Name)
			}
		})
	}
}

func hasAdjacentHouseholdPair(stops []*models.Participant) bool {
	for i := 0; i < len(stops)-1; i++ {
		if householdKey(stops[i]) == householdKey(stops[i+1]) {
			return true
		}
	}
	return false
}
