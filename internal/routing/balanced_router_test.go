package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
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

func TestGroupParticipantsByAddress_SameNormalizedAddressWithDifferentCoordinates(t *testing.T) {
	participants := []*models.Participant{{ID: 1, Address: "123 Main St", Lat: 40.12345, Lng: -74.12345}, {ID: 2, Address: "123 Main St", Lat: 40.22345, Lng: -74.22345}}
	if groups := groupParticipantsByAddress(participants); len(groups) != 2 {
		t.Fatalf("different coordinates grouped together: %d", len(groups))
	}
}

func TestGroupParticipantsByAddress_DifferentAddressesWithIdenticalCoordinates(t *testing.T) {
	for _, addresses := range [][2]string{
		{"Apartment 1", "Apartment 2"},
		{"12 Oak St Apt 1", "12 Oak St Apt 2"},
	} {
		t.Run(addresses[0], func(t *testing.T) {
			participants := []*models.Participant{
				{ID: 1, Address: addresses[0], Lat: 40.12345, Lng: -74.12345},
				{ID: 2, Address: addresses[1], Lat: 40.12345, Lng: -74.12345},
			}
			if groups := groupParticipantsByAddress(participants); len(groups) != 2 {
				t.Fatalf("group count = %d, want 2 separate households", len(groups))
			}
		})
	}
}

func TestGroupParticipantsByAddress_NormalizesStreetAndUnitMarkers(t *testing.T) {
	for _, pair := range [][2]string{
		{"St", "Street"},
		{"Ave", "Avenue"},
		{"Rd", "Road"},
		{"Dr", "Drive"},
		{"Blvd", "Boulevard"},
		{"Ln", "Lane"},
		{"Ct", "Court"},
		{"Pl", "Place"},
		{"N", "North"},
		{"S", "South"},
		{"E", "East"},
		{"W", "West"},
	} {
		t.Run(pair[0], func(t *testing.T) {
			participants := []*models.Participant{
				{ID: 1, Address: " 12  Oak " + pair[0] + "., Apt. 1", Lat: 40.123450, Lng: -74.123450},
				{ID: 2, Address: "12 OAK " + pair[1] + " #1", Lat: 40.123454, Lng: -74.123454},
				{ID: 3, Address: "12 Oak " + pair[1] + " Unit 1", Lat: 40.123450, Lng: -74.123450},
				{ID: 4, Address: "12 Oak " + pair[1] + " Apt #1", Lat: 40.123450, Lng: -74.123450},
			}
			if groups := groupParticipantsByAddress(participants); len(groups) != 1 {
				t.Fatalf("group count = %d, want 1", len(groups))
			}
		})
	}
}

func TestGroupParticipantsByAddress_EmptyAddressFallsBackToCoordinates(t *testing.T) {
	participants := []*models.Participant{
		{ID: 1, Address: "", Lat: 40.123450, Lng: -74.123450},
		{ID: 2, Address: " \t\n", Lat: 40.123454, Lng: -74.123454},
	}

	groups := groupParticipantsByAddress(participants)
	if len(groups) != 1 {
		t.Fatalf("group count = %d, want 1", len(groups))
	}
	coordinateHouseholdKey := householdKey(participants[0])
	if coordinateHouseholdKey != coordinateKey(40.12345, -74.12345) {
		got := coordinateHouseholdKey
		t.Fatalf("household key = %q, want coordinate fallback", got)
	}
	if householdKey(&models.Participant{Address: coordinateKey(40.12345, -74.12345)}) == coordinateHouseholdKey {
		t.Fatal("address and coordinate household key spaces collided")
	}
}

func TestBalancedRouter_GroupsStayTogether(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
			{ID: 1, Name: "Alice", Lat: 0.01, Lng: 0.01},
			{ID: 2, Name: "Bob", Lat: 0.01, Lng: 0.01},
			{ID: 3, Name: "Charlie", Lat: 0.02, Lng: 0.02},
			{ID: 4, Name: "David", Lat: 0.02, Lng: 0.02},
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

	if result.Summary.TotalParticipants != 5 {
		t.Errorf("expected 5 participants, got %d", result.Summary.TotalParticipants)
	}

	if len(result.Summary.UnassignedParticipants) != 0 {
		t.Errorf("expected 0 unassigned participants, got %d", len(result.Summary.UnassignedParticipants))
	}

	participantToRoute := make(map[int64]int)
	for routeIdx, route := range result.Routes {
		for _, stop := range route.Stops {
			participantToRoute[stop.Participant.ID] = routeIdx
		}
	}

	if participantToRoute[1] != participantToRoute[2] {
		t.Errorf("Alice and Bob should be on the same route")
	}

	if participantToRoute[3] != participantToRoute[4] {
		t.Errorf("Charlie and David should be on the same route")
	}
}

func TestBalancedRouter_LargeHouseholdSplit(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

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

func TestMaximizeNonemptyRoutes_StopsForCanceledContext(t *testing.T) {
	driverOne := &models.Driver{ID: 1, VehicleCapacity: 2}
	driverTwo := &models.Driver{ID: 2, VehicleCapacity: 1}
	routes := map[int64]*balancedRoute{
		1: {
			driver: driverOne,
			stops: []*models.Participant{
				{ID: 1, Address: "First"},
				{ID: 2, Address: "Second"},
			},
		},
		2: {driver: driverTwo},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (&BalancedRouter{}).maximizeNonemptyRoutes(
		ctx,
		newRouteContext(stableDistanceCalculator{}, models.Coordinates{}, RouteModeDropoff),
		routes,
		[]int64{1, 2},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("maximizeNonemptyRoutes() error = %v, want %v", err, context.Canceled)
	}
}

func TestOptimizeAssignments_StopsForCanceledContext(t *testing.T) {
	driverOne := &models.Driver{ID: 1, VehicleCapacity: 1}
	driverTwo := &models.Driver{ID: 2, VehicleCapacity: 1}
	routes := map[int64]*balancedRoute{
		1: {driver: driverOne},
		2: {driver: driverTwo},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := optimizeAssignments(ctx, newRouteContext(stableDistanceCalculator{}, models.Coordinates{}, RouteModeDropoff), routes, []int64{1, 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("optimizeAssignments() error = %v, want %v", err, context.Canceled)
	}
}

func TestBalancedRouter_MaximizesDriversWithHeterogeneousCapacities(t *testing.T) {
	for _, mode := range []RouteMode{RouteModeDropoff, RouteModePickup} {
		t.Run(string(mode), func(t *testing.T) {
			institute := models.Coordinates{}
			groupA := participantAtBearing(1, 0).GetCoords()
			groupB := participantAtBearing(3, 10).GetCoords()
			groupC := participantAtBearing(5, 20).GetCoords()
			drivers := []models.Driver{
				*driverAtBearing(1, 30, 4),
				*driverAtBearing(2, 20, 2),
				*driverAtBearing(3, 0, 1),
			}
			distances := newOverrideDistanceAdapter(1000)
			if mode == RouteModeDropoff {
				distances.setDuration(institute, groupA, 1)
				distances.setDuration(institute, groupB, 100)
				distances.setDuration(institute, groupC, 3)
			} else {
				distances.setDuration(drivers[0].GetCoords(), groupA, 1)
				distances.setDuration(drivers[0].GetCoords(), groupB, 100)
				distances.setDuration(drivers[0].GetCoords(), groupC, 3)
				distances.setDuration(drivers[1].GetCoords(), groupB, 100)
				distances.setDuration(drivers[2].GetCoords(), groupC, 5000)
				distances.setDuration(groupA, groupB, 1)
				distances.setDuration(groupB, groupA, 1)
				distances.setDuration(groupA, institute, 1)
				distances.setDuration(groupB, institute, 1)
				distances.setDuration(groupC, institute, 1)
			}

			result, err := NewBalancedRouter(distances).CalculateRoutes(context.Background(), &RoutingRequest{
				InstituteCoords: institute,
				Participants: []models.Participant{
					{ID: 1, Name: "A1", Address: "A", Lat: groupA.Lat, Lng: groupA.Lng},
					{ID: 2, Name: "A2", Address: "A", Lat: groupA.Lat, Lng: groupA.Lng},
					{ID: 3, Name: "B1", Address: "B", Lat: groupB.Lat, Lng: groupB.Lng},
					{ID: 4, Name: "B2", Address: "B", Lat: groupB.Lat, Lng: groupB.Lng},
					{ID: 5, Name: "C", Address: "C", Lat: groupC.Lat, Lng: groupC.Lng},
				},
				Drivers: drivers,
				Mode:    mode,
			})
			if err != nil {
				t.Fatalf("CalculateRoutes() error = %v", err)
			}

			assertAllDriversUsedAndHouseholdsIntact(t, result, 3, map[string]int{"A": 2, "B": 2, "C": 1})
		})
	}
}

func TestBalancedRouter_HeterogeneousCapacityNoncontiguousPackingUsesAllDrivers(t *testing.T) {
	institute := models.Coordinates{}
	groupA := participantAtBearing(1, 0).GetCoords()
	groupB := participantAtBearing(3, 10).GetCoords()
	groupC := participantAtBearing(5, 20).GetCoords()
	participants := []*models.Participant{
		{ID: 1, Name: "A1", Address: "A", Lat: groupA.Lat, Lng: groupA.Lng},
		{ID: 2, Name: "A2", Address: "A", Lat: groupA.Lat, Lng: groupA.Lng},
		{ID: 3, Name: "B1", Address: "B", Lat: groupB.Lat, Lng: groupB.Lng},
		{ID: 4, Name: "B2", Address: "B", Lat: groupB.Lat, Lng: groupB.Lng},
		{ID: 5, Name: "C", Address: "C", Lat: groupC.Lat, Lng: groupC.Lng},
	}
	drivers := []*models.Driver{
		driverAtBearing(1, 0, 3),
		driverAtBearing(2, 10, 2),
	}

	result, err := NewBalancedRouter(stableDistanceCalculator{}).CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: institute,
		Participants: []models.Participant{
			*participants[0], *participants[1], *participants[2], *participants[3], *participants[4],
		},
		Drivers: []models.Driver{*drivers[0], *drivers[1]},
		Mode:    RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}
	assertAllDriversUsedAndHouseholdsIntact(t, result, 2, map[string]int{"A": 2, "B": 2, "C": 1})
}

func assertAllDriversUsedAndHouseholdsIntact(t *testing.T, result *models.RoutingResult, wantDrivers int, householdSizes map[string]int) {
	t.Helper()
	if result.Summary.TotalDriversUsed != wantDrivers {
		t.Fatalf("drivers used = %d, want %d", result.Summary.TotalDriversUsed, wantDrivers)
	}

	householdDriver := make(map[string]int64, len(householdSizes))
	householdCounts := make(map[string]int, len(householdSizes))
	for _, route := range result.Routes {
		if len(route.Stops) > route.Driver.VehicleCapacity {
			t.Fatalf("driver %d has %d stops over capacity %d", route.Driver.ID, len(route.Stops), route.Driver.VehicleCapacity)
		}
		for _, stop := range route.Stops {
			address := stop.Participant.Address
			if previousDriver, seen := householdDriver[address]; seen && previousDriver != route.Driver.ID {
				t.Fatalf("household %q split between drivers %d and %d", address, previousDriver, route.Driver.ID)
			}
			householdDriver[address] = route.Driver.ID
			householdCounts[address]++
		}
	}
	for address, want := range householdSizes {
		if got := householdCounts[address]; got != want {
			t.Fatalf("household %q assigned count = %d, want %d", address, got, want)
		}
	}
}

func driverAtBearing(id int64, bearing float64, capacity int) *models.Driver {
	radians := bearing * math.Pi / 180
	return &models.Driver{
		ID:              id,
		Name:            fmt.Sprintf("Driver %d", id),
		Lat:             math.Cos(radians),
		Lng:             math.Sin(radians),
		VehicleCapacity: capacity,
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
	maxDetour := 0.0
	sumDetour := 0.0
	for i := range result.Routes {
		route := &result.Routes[i]
		maxDetour = max(maxDetour, route.DetourSecs)
		sumDetour += route.DetourSecs
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
	if maxDetour != 3 || sumDetour != 3 {
		t.Fatalf("route detours: max=%.1f sum=%.1f, want max=3 sum=3", maxDetour, sumDetour)
	}
	if result.Summary.MaxDetourSecs != 3 {
		t.Fatalf("summary max detour = %.1f, want 3", result.Summary.MaxDetourSecs)
	}
	if result.Summary.SumDetourSecs != 3 {
		t.Fatalf("summary sum detour = %.1f, want 3", result.Summary.SumDetourSecs)
	}
	if result.Summary.AverageDetourSecs != 1.5 {
		t.Fatalf("summary average detour = %.1f, want 1.5", result.Summary.AverageDetourSecs)
	}
	if result.Summary.MaxDetourSecs != maxDetour || result.Summary.SumDetourSecs != sumDetour {
		t.Fatalf("summary detours %+v do not match returned routes", result.Summary)
	}
}

func participantAtBearing(id int64, bearing float64) *models.Participant {
	radians := bearing * math.Pi / 180
	return &models.Participant{ID: id, Lat: math.Cos(radians), Lng: math.Sin(radians)}
}

func TestBalancedRouter_UsesAllDriversEvenWhenConsolidationIsFaster(t *testing.T) {
	activity := models.Coordinates{Lat: 0, Lng: 0}
	first := models.Coordinates{Lat: 1, Lng: 0}
	second := models.Coordinates{Lat: 2, Lng: 0}
	nearbyHome := models.Coordinates{Lat: 3, Lng: 0}
	oppositeHome := models.Coordinates{Lat: -100, Lng: 0}
	distances := newOverrideDistanceAdapter(1000)
	distances.setDuration(activity, first, 1)
	distances.setDuration(activity, second, 2)
	distances.setDuration(first, second, 1)
	distances.setDuration(second, first, 1)
	distances.setDuration(first, nearbyHome, 1)
	distances.setDuration(second, nearbyHome, 2)
	distances.setDuration(activity, nearbyHome, 2)
	distances.setDuration(first, oppositeHome, 101)
	distances.setDuration(second, oppositeHome, 102)
	distances.setDuration(activity, oppositeHome, 100)
	router := NewBalancedRouter(distances)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: activity,
		Participants: []models.Participant{
			{ID: 1, Name: "First", Address: "1 First Street", Lat: first.Lat, Lng: first.Lng},
			{ID: 2, Name: "Second", Address: "2 Second Street", Lat: second.Lat, Lng: second.Lng},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Nearby Driver", Lat: nearbyHome.Lat, Lng: nearbyHome.Lng, VehicleCapacity: 2},
			{ID: 2, Name: "Opposite Driver", Lat: oppositeHome.Lat, Lng: oppositeHome.Lng, VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if result.Summary.TotalDriversUsed != 2 {
		t.Fatalf("drivers used = %d, want 2 despite the second driver's worse route", result.Summary.TotalDriversUsed)
	}
	if len(result.Routes) != 2 {
		t.Fatalf("route count = %d, want 2", len(result.Routes))
	}
	for _, route := range result.Routes {
		if len(route.Stops) != 1 {
			t.Fatalf("driver %d stops = %d, want exactly 1", route.Driver.ID, len(route.Stops))
		}
	}
	if result.Summary.MaxDetourSecs != 2 || result.Summary.SumDetourSecs != 4 || result.Summary.AverageDetourSecs != 2 {
		t.Fatalf("detour summary = %+v, want max=2 sum=4 average=2", result.Summary)
	}
}

func TestBalancedRouter_UsedDriversDominatesLatestCompletionAndDetour(t *testing.T) {
	activity := models.Coordinates{Lat: 0, Lng: 0}
	first := models.Coordinates{Lat: 1, Lng: 0}
	second := models.Coordinates{Lat: 2, Lng: 0}
	firstDriverHome := models.Coordinates{Lat: 10, Lng: 0}
	secondDriverHome := models.Coordinates{Lat: 20, Lng: 0}
	distances := newOverrideDistanceAdapter(1000)
	distances.setDuration(activity, first, 1)
	distances.setDuration(activity, second, 100)
	distances.setDuration(first, second, 1)
	distances.setDuration(second, first, 100)
	distances.setDuration(activity, firstDriverHome, 10)
	distances.setDuration(first, firstDriverHome, 9)
	distances.setDuration(second, firstDriverHome, 8)
	distances.setDuration(activity, secondDriverHome, 20)
	distances.setDuration(second, secondDriverHome, 100)
	router := NewBalancedRouter(distances)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: activity,
		Participants: []models.Participant{
			{ID: 1, Name: "First", Address: "1 First Street", Lat: first.Lat, Lng: first.Lng},
			{ID: 2, Name: "Second", Address: "2 Second Street", Lat: second.Lat, Lng: second.Lng},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "First Driver", Lat: firstDriverHome.Lat, Lng: firstDriverHome.Lng, VehicleCapacity: 2},
			{ID: 2, Name: "Second Driver", Lat: secondDriverHome.Lat, Lng: secondDriverHome.Lng, VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if result.Summary.TotalDriversUsed != 2 {
		t.Fatalf("drivers used = %d, want 2 despite worse completion and detour", result.Summary.TotalDriversUsed)
	}

	latestCompletion := 0.0
	maxDetour := 0.0
	for _, route := range result.Routes {
		for _, stop := range route.Stops {
			latestCompletion = max(latestCompletion, stop.CumulativeDurationSecs)
		}
		maxDetour = max(maxDetour, route.DetourSecs)
	}
	if latestCompletion <= 2 {
		t.Fatalf("latest completion = %.0f, want greater than consolidated completion 2", latestCompletion)
	}
	if maxDetour <= 0 {
		t.Fatalf("max detour = %.0f, want greater than consolidated detour 0", maxDetour)
	}
}

func TestBalancedRouter_SingleHouseholdUsesOnlyOneDriver(t *testing.T) {
	router := NewBalancedRouter(stableDistanceCalculator{})

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{},
		Participants: []models.Participant{
			{ID: 1, Name: "First", Address: "1 Shared Street", Lat: 1},
			{ID: 2, Name: "Second", Address: "1 Shared Street", Lat: 1},
			{ID: 3, Name: "Third", Address: "1 Shared Street", Lat: 1},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "First Driver", Lat: 10, VehicleCapacity: 3},
			{ID: 2, Name: "Second Driver", Lat: 20, VehicleCapacity: 3},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if result.Summary.TotalDriversUsed != 1 {
		t.Fatalf("drivers used = %d, want 1 because the only household is atomic", result.Summary.TotalDriversUsed)
	}
	if len(result.Routes) != 1 || len(result.Routes[0].Stops) != 3 {
		t.Fatalf("routes = %+v, want one route containing the whole household", result.Routes)
	}
}

func TestBalancedRouter_ThreeHouseholdsUseBothDrivers(t *testing.T) {
	router := NewBalancedRouter(stableDistanceCalculator{})

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{},
		Participants: []models.Participant{
			{ID: 1, Name: "First", Address: "1 First Street", Lat: 1},
			{ID: 2, Name: "Second", Address: "2 Second Street", Lat: 2},
			{ID: 3, Name: "Third", Address: "3 Third Street", Lat: 3},
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "First Driver", Lat: 10, VehicleCapacity: 3},
			{ID: 2, Name: "Second Driver", Lat: 20, VehicleCapacity: 3},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	if result.Summary.TotalDriversUsed != 2 {
		t.Fatalf("drivers used = %d, want 2 for three household groups", result.Summary.TotalDriversUsed)
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

func (a *overrideDistanceAdapter) PrewarmPairs(context.Context, []distance.DistancePair) error {
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

	remaining, err := roundRobinInsertion(context.Background(), newRouteContext(distances, activity, RouteModePickup), routes, []int64{driver.ID}, participants)
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

func hasAdjacentHouseholdPair(stops []*models.Participant) bool {
	for i := 0; i < len(stops)-1; i++ {
		if householdKey(stops[i]) == householdKey(stops[i+1]) {
			return true
		}
	}
	return false
}
