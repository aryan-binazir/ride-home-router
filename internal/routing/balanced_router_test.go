package routing

import (
	"context"
	"fmt"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"testing"
)

type countingSolveDistanceCalculator struct {
	stableDistanceCalculator
	calls map[string]int
}

func (c *countingSolveDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	if c.calls == nil {
		c.calls = make(map[string]int)
	}
	c.calls[distance.PairCacheKey(origin, dest)]++
	return c.stableDistanceCalculator.GetDistance(ctx, origin, dest)
}

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

func TestBalancedRouter_LargeHouseholdStaysTogetherWhenAnyVehicleFits(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "Household 1", Lat: 0.01, Lng: 0.01},
			{ID: 2, Name: "Household 2", Lat: 0.01, Lng: 0.01},
			{ID: 3, Name: "Household 3", Lat: 0.01, Lng: 0.01},
			{ID: 4, Name: "Household 4", Lat: 0.01, Lng: 0.01},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Small", Lat: 0.05, Lng: 0.05, VehicleCapacity: 3},
			{ID: 2, Name: "Large", Lat: 0.06, Lng: 0.06, VehicleCapacity: 4},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if len(result.Routes) != 1 {
		t.Fatalf("route count = %d, want 1 household route", len(result.Routes))
	}
	if got := len(result.Routes[0].Stops); got != 4 {
		t.Fatalf("household route stop count = %d, want 4", got)
	}
	if result.Routes[0].Driver.ID != 2 {
		t.Fatalf("household assigned to driver %d, want large-capacity driver 2", result.Routes[0].Driver.ID)
	}
}

func TestRoundRobinInsertion_ReservesOnlyFittingVehicleForHousehold(t *testing.T) {
	distances := stableDistanceCalculator{}
	router := &BalancedRouter{distanceCalc: distances}
	institute := models.Coordinates{Lat: 0, Lng: 0}
	household := models.Coordinates{Lat: 10, Lng: 0}
	solo := models.Coordinates{Lat: 0.1, Lng: 0}

	largeDriver := &models.Driver{ID: 1, Name: "LargeCar", Lat: 0, Lng: 0, VehicleCapacity: 4}
	smallDriver := &models.Driver{ID: 2, Name: "SmallCar", Lat: 0, Lng: 0, VehicleCapacity: 2}
	routes := map[int64]*balancedRoute{
		largeDriver.ID: {driver: largeDriver, stops: []*models.Participant{}},
		smallDriver.ID: {driver: smallDriver, stops: []*models.Participant{}},
	}
	participants := []*models.Participant{
		{ID: 1, Name: "Household 1", Lat: household.Lat, Lng: household.Lng},
		{ID: 2, Name: "Household 2", Lat: household.Lat, Lng: household.Lng},
		{ID: 3, Name: "Household 3", Lat: household.Lat, Lng: household.Lng},
		{ID: 4, Name: "Household 4", Lat: household.Lat, Lng: household.Lng},
		{ID: 5, Name: "Solo", Lat: solo.Lat, Lng: solo.Lng},
	}

	remaining, err := router.roundRobinInsertion(context.Background(), newRouteContext(distances, institute, RouteModeDropoff), routes, []int64{largeDriver.ID, smallDriver.ID}, participants)
	if err != nil {
		t.Fatalf("roundRobinInsertion() error = %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("roundRobinInsertion() left %d unassigned participants", len(remaining))
	}
	if got := len(routes[largeDriver.ID].stops); got != 4 {
		t.Fatalf("large driver stop count = %d, want reserved 4-person household", got)
	}
	if got := len(routes[smallDriver.ID].stops); got != 1 {
		t.Fatalf("small driver stop count = %d, want solo rider", got)
	}
}

func TestBalancedRouter_OversizedHouseholdMaySplitOnlyWhenNoVehicleFits(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "Household 1", Lat: 0.01, Lng: 0.01},
			{ID: 2, Name: "Household 2", Lat: 0.01, Lng: 0.01},
			{ID: 3, Name: "Household 3", Lat: 0.01, Lng: 0.01},
			{ID: 4, Name: "Household 4", Lat: 0.01, Lng: 0.01},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", Lat: 0.05, Lng: 0.05, VehicleCapacity: 3},
			{ID: 2, Name: "Driver2", Lat: 0.06, Lng: 0.06, VehicleCapacity: 3},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if len(result.Routes) != 2 {
		t.Fatalf("route count = %d, want split across 2 routes", len(result.Routes))
	}
	totalAssigned := 0
	for _, route := range result.Routes {
		if len(route.Stops) > route.Driver.VehicleCapacity {
			t.Fatalf("route for %s has %d stops over capacity %d", route.Driver.Name, len(route.Stops), route.Driver.VehicleCapacity)
		}
		totalAssigned += len(route.Stops)
	}
	if totalAssigned != 4 {
		t.Fatalf("assigned stops = %d, want 4", totalAssigned)
	}
}

func TestBalancedRouter_SwapsFullRoutesToMinimizeLatestDropoff(t *testing.T) {
	router := NewBalancedRouter(stableDistanceCalculator{})

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "Near Positive", Lat: 1, Lng: 0},
			{ID: 2, Name: "Far Positive", Lat: 2, Lng: 0},
			{ID: 3, Name: "Near Negative", Lat: -10, Lng: 0},
			{ID: 4, Name: "Far Negative", Lat: -11, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Positive Driver", Lat: 12, Lng: 0, VehicleCapacity: 2},
			{ID: 2, Name: "Negative Driver", Lat: -12, Lng: 0, VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	wantParticipantIDs := map[int64]map[int64]bool{
		1: {1: true, 2: true},
		2: {3: true, 4: true},
	}
	seenParticipants := make(map[int64]bool)
	latestDropoff := 0.0
	householdDriver := make(map[string]int64)
	for _, route := range result.Routes {
		if len(route.Stops) > route.Driver.VehicleCapacity {
			t.Fatalf("driver %d has %d stops over capacity %d", route.Driver.ID, len(route.Stops), route.Driver.VehicleCapacity)
		}
		for _, stop := range route.Stops {
			if !wantParticipantIDs[route.Driver.ID][stop.Participant.ID] {
				t.Fatalf("participant %d assigned to driver %d, want geographic partition", stop.Participant.ID, route.Driver.ID)
			}
			if seenParticipants[stop.Participant.ID] {
				t.Fatalf("participant %d assigned more than once", stop.Participant.ID)
			}
			seenParticipants[stop.Participant.ID] = true
			key := householdKey(stop.Participant)
			if driverID, ok := householdDriver[key]; ok && driverID != route.Driver.ID {
				t.Fatalf("household %s split across drivers %d and %d", key, driverID, route.Driver.ID)
			}
			householdDriver[key] = route.Driver.ID
			latestDropoff = max(latestDropoff, stop.CumulativeDurationSecs)
		}
	}

	if len(seenParticipants) != 4 {
		t.Fatalf("assigned participant count = %d, want 4", len(seenParticipants))
	}
	if latestDropoff > 11000 {
		t.Fatalf("latest dropoff = %.0f, want at most 11000", latestDropoff)
	}
}

func TestBalancedRouter_MemoizesDistancePairsForOneSolve(t *testing.T) {
	calc := &countingSolveDistanceCalculator{}
	router := NewBalancedRouter(calc)

	_, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "P1", Lat: 1, Lng: 0},
			{ID: 2, Name: "P2", Lat: 2, Lng: 0},
			{ID: 3, Name: "P3", Lat: -1, Lng: 0},
			{ID: 4, Name: "P4", Lat: -2, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "D1", Lat: 3, Lng: 0, VehicleCapacity: 2},
			{ID: 2, Name: "D2", Lat: -3, Lng: 0, VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	for pair, calls := range calc.calls {
		if calls > 1 {
			t.Fatalf("distance pair %s loaded %d times in one solve, want at most once", pair, calls)
		}
	}
}

func TestBalancedRouter_OrdersRoutesAgainstTheFullSolutionObjective(t *testing.T) {
	activity := models.Coordinates{Lat: 0, Lng: 0}
	first := models.Coordinates{Lat: 1, Lng: 0}
	peerFirst := models.Coordinates{Lat: 2, Lng: 0}
	second := models.Coordinates{Lat: 3, Lng: 0}
	peerSecond := models.Coordinates{Lat: 4, Lng: 0}
	firstDriverHome := models.Coordinates{Lat: 5, Lng: 0}
	peerDriverHome := models.Coordinates{Lat: 6, Lng: 0}
	distances := newOverrideDistanceAdapter(1000)

	distances.setDuration(activity, first, 1)
	distances.setDuration(activity, peerFirst, 2)
	distances.setDuration(activity, second, 3)
	distances.setDuration(activity, peerSecond, 4)
	distances.setDuration(first, second, 1)
	distances.setDuration(second, first, 1)
	distances.setDuration(second, firstDriverHome, 200)
	distances.setDuration(first, firstDriverHome, 1)
	distances.setDuration(activity, firstDriverHome, 2)
	distances.setDuration(peerFirst, peerSecond, 10)
	distances.setDuration(peerSecond, peerFirst, 100)
	distances.setDuration(peerSecond, peerDriverHome, 1)
	distances.setDuration(activity, peerDriverHome, 13)

	router := NewBalancedRouter(distances)
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: activity,
		Participants: []models.Participant{
			{ID: 1, Name: "First", Lat: first.Lat, Lng: first.Lng},
			{ID: 2, Name: "Peer First", Lat: peerFirst.Lat, Lng: peerFirst.Lng},
			{ID: 3, Name: "Second", Lat: second.Lat, Lng: second.Lng},
			{ID: 4, Name: "Peer Second", Lat: peerSecond.Lat, Lng: peerSecond.Lng},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "First Driver", Lat: firstDriverHome.Lat, Lng: firstDriverHome.Lng, VehicleCapacity: 2},
			{ID: 2, Name: "Peer Driver", Lat: peerDriverHome.Lat, Lng: peerDriverHome.Lng, VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	var firstRoute *models.CalculatedRoute
	latestDropoff := 0.0
	for i := range result.Routes {
		route := &result.Routes[i]
		for _, stop := range route.Stops {
			latestDropoff = max(latestDropoff, stop.CumulativeDurationSecs)
		}
		if route.Driver.ID == 1 {
			firstRoute = route
		}
	}
	if firstRoute == nil {
		t.Fatal("first driver route is missing")
	}
	if firstRoute.Stops[0].Participant.ID != 3 {
		t.Fatalf("first driver starts with participant %d, want 3 to reduce the global detour tie-breaker", firstRoute.Stops[0].Participant.ID)
	}
	if firstRoute.DetourSecs != 3 {
		t.Fatalf("first driver detour = %.0f, want 3", firstRoute.DetourSecs)
	}
	if latestDropoff != 12 {
		t.Fatalf("latest dropoff = %.0f, want peer-route maximum 12", latestDropoff)
	}
}

func TestOptimizeAssignments_ReordersUntouchedPeerAfterGlobalMaximumChanges(t *testing.T) {
	ctx := context.Background()
	activity := models.Coordinates{Lat: 0, Lng: 0}
	x1 := &models.Participant{ID: 1, Name: "X1", Lat: 1, Lng: 0}
	y1 := &models.Participant{ID: 2, Name: "Y1", Lat: 2, Lng: 0}
	x2 := &models.Participant{ID: 3, Name: "X2", Lat: 3, Lng: 0}
	y2 := &models.Participant{ID: 4, Name: "Y2", Lat: 4, Lng: 0}
	c1 := &models.Participant{ID: 5, Name: "C1", Lat: 5, Lng: 0}
	c2 := &models.Participant{ID: 6, Name: "C2", Lat: 6, Lng: 0}
	driver1 := &models.Driver{ID: 1, Name: "Driver 1", Lat: 11, Lng: 0, VehicleCapacity: 2}
	driver2 := &models.Driver{ID: 2, Name: "Driver 2", Lat: 12, Lng: 0, VehicleCapacity: 2}
	driver3 := &models.Driver{ID: 3, Name: "Driver 3", Lat: 13, Lng: 0, VehicleCapacity: 2}

	distances := newOverrideDistanceAdapter(1000)
	for _, participant := range []*models.Participant{x1, y1, x2, y2, c1, c2} {
		distances.setDuration(activity, participant.GetCoords(), 10)
	}
	distances.setDuration(x1.GetCoords(), y1.GetCoords(), 90)
	distances.setDuration(y1.GetCoords(), x1.GetCoords(), 90)
	distances.setDuration(x2.GetCoords(), y2.GetCoords(), 90)
	distances.setDuration(y2.GetCoords(), x2.GetCoords(), 90)
	distances.setDuration(x1.GetCoords(), x2.GetCoords(), 10)
	distances.setDuration(x2.GetCoords(), x1.GetCoords(), 10)
	distances.setDuration(y1.GetCoords(), y2.GetCoords(), 10)
	distances.setDuration(y2.GetCoords(), y1.GetCoords(), 10)
	distances.setDuration(c1.GetCoords(), c2.GetCoords(), 30)
	distances.setDuration(c2.GetCoords(), c1.GetCoords(), 20)

	for _, driver := range []*models.Driver{driver1, driver2} {
		distances.setDuration(activity, driver.GetCoords(), 100)
		for _, participant := range []*models.Participant{x1, y1, x2, y2} {
			distances.setDuration(participant.GetCoords(), driver.GetCoords(), 80)
		}
	}
	distances.setDuration(activity, driver3.GetCoords(), 40)
	distances.setDuration(c2.GetCoords(), driver3.GetCoords(), 0)
	distances.setDuration(c1.GetCoords(), driver3.GetCoords(), 100)

	router := &BalancedRouter{distanceCalc: distances}
	routes := map[int64]*balancedRoute{
		driver1.ID: {driver: driver1, stops: []*models.Participant{x1, y1}},
		driver2.ID: {driver: driver2, stops: []*models.Participant{x2, y2}},
		driver3.ID: {driver: driver3, stops: []*models.Participant{c1, c2}},
	}
	driverIDs := []int64{driver1.ID, driver2.ID, driver3.ID}
	rc := newRouteContext(distances, activity, RouteModeDropoff)

	if err := router.optimizeRouteOrders(ctx, rc, routes, driverIDs); err != nil {
		t.Fatalf("optimizeRouteOrders() error = %v", err)
	}
	if routes[driver3.ID].stops[0].ID != c1.ID {
		t.Fatalf("peer route changed before the global maximum dropped")
	}

	if _, err := router.optimizeAssignments(ctx, rc, routes, driverIDs); err != nil {
		t.Fatalf("optimizeAssignments() error = %v", err)
	}
	if routes[driver3.ID].stops[0].ID != c2.ID {
		t.Fatalf("peer route starts with participant %d, want %d after the assignment lowered the global maximum", routes[driver3.ID].stops[0].ID, c2.ID)
	}
}

func TestBalancedRouter_CanLeaveASelectedDriverUnusedForAHigherPriorityObjective(t *testing.T) {
	router := NewBalancedRouter(stableDistanceCalculator{})

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "First", Lat: 1, Lng: 0},
			{ID: 2, Name: "Second", Lat: 2, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Nearby Driver", Lat: 2, Lng: 0, VehicleCapacity: 2},
			{ID: 2, Name: "Opposite Driver", Lat: -100, Lng: 0, VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if result.Summary.TotalDriversUsed != 1 {
		t.Fatalf("drivers used = %d, want 1 because an empty route removes the higher-priority detour", result.Summary.TotalDriversUsed)
	}
	if len(result.Routes) != 1 || result.Routes[0].Driver.ID != 1 {
		t.Fatalf("routes = %+v, want only nearby driver 1", result.Routes)
	}
	if len(result.Routes[0].Stops) != 2 {
		t.Fatalf("nearby driver stops = %d, want 2", len(result.Routes[0].Stops))
	}
}

func TestBalancedRouter_PrefersUsingMoreDriversOnlyAfterObjectiveTies(t *testing.T) {
	router := NewBalancedRouter(newOverrideDistanceAdapter(0))

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "First", Lat: 1, Lng: 0},
			{ID: 2, Name: "Second", Lat: 2, Lng: 0},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "First Driver", Lat: 10, Lng: 0, VehicleCapacity: 2},
			{ID: 2, Name: "Second Driver", Lat: 20, Lng: 0, VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if result.Summary.TotalDriversUsed != 2 {
		t.Fatalf("drivers used = %d, want 2 when every higher-priority objective ties", result.Summary.TotalDriversUsed)
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
	if len(remaining) != 2 {
		t.Fatalf("roundRobinInsertion() remaining = %d, want 2 because the household fits the max selected vehicle capacity", len(remaining))
	}

	stops := routes[driver.ID].stops
	if len(stops) != 3 {
		t.Fatalf("route stop count = %d, want unchanged 3", len(stops))
	}
	for i := 0; i < len(stops)-1; i++ {
		if stops[i].Name == "Sister 1" && stops[i+1].Name == "Sister 2" {
			return
		}
	}

	t.Fatalf("single-participant fallback split existing household: got order %q, %q, %q", stops[0].Name, stops[1].Name, stops[2].Name)
}

func hasAdjacentHouseholdPair(stops []*models.Participant) bool {
	for i := 0; i < len(stops)-1; i++ {
		if householdKey(stops[i]) == householdKey(stops[i+1]) {
			return true
		}
	}
	return false
}
