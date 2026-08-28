package routing

import (
	"context"
	"fmt"
	"math"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"slices"
	"testing"
	"time"
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
		{ID: 1, Name: "Alice", Address: "123 Main St", Lat: 40.12345, Lng: -74.12345},
		{ID: 2, Name: "Bob", Address: "123 Main St", Lat: 40.12345, Lng: -74.12345},
		{ID: 3, Name: "Charlie", Address: "456 Oak Ave", Lat: 40.23456, Lng: -74.23456},
		{ID: 4, Name: "David", Address: "789 Elm St", Lat: 40.34567, Lng: -74.34567},
		{ID: 5, Name: "Eve", Address: "789 Elm St", Lat: 40.34567, Lng: -74.34567},
		{ID: 6, Name: "Frank", Address: "789 Elm St", Lat: 40.34567, Lng: -74.34567},
	}

	groups := groupParticipantsByAddress(participants)

	if len(groups) != 3 {
		t.Errorf("expected 3 groups, got %d", len(groups))
	}

	expectedSizes := []int{3, 2, 1}
	for i, expectedSize := range expectedSizes {
		if len(groups[i].members) != expectedSize {
			t.Errorf("group %d: expected size %d, got %d", i, expectedSize, len(groups[i].members))
		}
	}

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
	participants := []*models.Participant{
		{ID: 1, Name: "Alice", Lat: 40.123450, Lng: -74.123450},
		{ID: 2, Name: "Bob", Lat: 40.123454, Lng: -74.123454},     // Within rounding precision (rounds to same value)
		{ID: 3, Name: "Charlie", Lat: 40.123550, Lng: -74.123550}, // Beyond rounding precision
	}

	groups := groupParticipantsByAddress(participants)

	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
}

func TestGroupParticipantsByAddress_SameNormalizedAddressWithDifferentCoordinates(t *testing.T) {
	participants := []*models.Participant{
		{ID: 1, Name: "Alice", Address: "123 Main St", Lat: 40.12345, Lng: -74.12345},
		{ID: 2, Name: "Bob", Address: "123 Main St", Lat: 40.22345, Lng: -74.22345},
	}

	groups := groupParticipantsByAddress(participants)
	if len(groups) != 1 {
		t.Fatalf("group count = %d, want 1", len(groups))
	}
	if len(groups[0].members) != 2 {
		t.Fatalf("group member count = %d, want 2", len(groups[0].members))
	}
	if groups[0].lat != models.RoundCoordinate(participants[0].Lat) || groups[0].lng != models.RoundCoordinate(participants[0].Lng) {
		t.Fatalf("group coordinates = (%f, %f), want first member coordinates", groups[0].lat, groups[0].lng)
	}

	router := NewBalancedRouter(newMockDistanceAdapter())
	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{},
		Participants: []models.Participant{
			*participants[0],
			*participants[1],
		},
		Drivers: []models.Driver{
			{ID: 1, Name: "Driver1", VehicleCapacity: 2},
			{ID: 2, Name: "Driver2", VehicleCapacity: 2},
		},
		Mode: RouteModeDropoff,
	})
	if err != nil {
		t.Fatalf("CalculateRoutes() error = %v", err)
	}

	assignedDriver := make(map[int64]int64)
	for _, route := range result.Routes {
		for _, stop := range route.Stops {
			assignedDriver[stop.Participant.ID] = route.Driver.ID
		}
	}
	if assignedDriver[1] == 0 || assignedDriver[1] != assignedDriver[2] {
		t.Fatalf("same-address participants assigned to drivers %d and %d, want one driver", assignedDriver[1], assignedDriver[2])
	}
}

func TestGroupParticipantsByAddress_DifferentAddressesWithIdenticalCoordinates(t *testing.T) {
	participants := []*models.Participant{
		{ID: 1, Address: "Apartment 1", Lat: 40.12345, Lng: -74.12345},
		{ID: 2, Address: "Apartment 2", Lat: 40.12345, Lng: -74.12345},
	}

	groups := groupParticipantsByAddress(participants)
	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2", len(groups))
	}
	if householdKey(participants[0]) == householdKey(participants[1]) {
		t.Fatal("different non-blank addresses produced the same household key")
	}
}

func TestGroupParticipantsByAddress_NormalizesAddressCaseAndWhitespace(t *testing.T) {
	participants := []*models.Participant{
		{ID: 1, Address: "  123  MAIN\tSt\n", Lat: 1, Lng: 1},
		{ID: 2, Address: "123 main st", Lat: 2, Lng: 2},
	}

	groups := groupParticipantsByAddress(participants)
	if len(groups) != 1 {
		t.Fatalf("group count = %d, want 1", len(groups))
	}
	if got := householdKey(participants[0]); got != "addr:123 main st" {
		t.Fatalf("household key = %q, want %q", got, "addr:123 main st")
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

func TestBalancedRouter_LargeGroupHandling(t *testing.T) {
	mock := newMockDistanceAdapter()
	router := NewBalancedRouter(mock)

	result, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 0, Lng: 0},
		Participants: []models.Participant{
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

	if result.Summary.TotalParticipants != 4 {
		t.Errorf("expected 4 participants, got %d", result.Summary.TotalParticipants)
	}

	if len(result.Summary.UnassignedParticipants) != 0 {
		t.Errorf("expected 0 unassigned participants, got %d", len(result.Summary.UnassignedParticipants))
	}

	if result.Summary.TotalDriversUsed != 2 {
		t.Errorf("expected 2 drivers used, got %d", result.Summary.TotalDriversUsed)
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

func TestRoundRobinInsertion_ReservesOnlyFittingVehicleForHousehold(t *testing.T) {
	distances := stableDistanceCalculator{}
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

	remaining, err := roundRobinInsertion(context.Background(), newRouteContext(distances, institute, RouteModeDropoff), routes, []int64{largeDriver.ID, smallDriver.ID}, participants)
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

func TestBearingSweepInsertion_AssignsClearBearingClustersToMatchingDrivers(t *testing.T) {
	routes, participants := bearingSweepFixture(
		[]float64{85, 95, 265, 275},
		[]bearingSweepDriver{{id: 1, bearing: 90, capacity: 2}, {id: 2, bearing: 270, capacity: 2}},
	)

	if ok := (&BalancedRouter{}).bearingSweepInsertion(models.Coordinates{}, routes, []int64{1, 2}, participants); !ok {
		t.Fatal("bearingSweepInsertion() failed, want a complete sweep seed")
	}
	assertSweepRouteParticipantIDs(t, routes[1], 1, 2)
	assertSweepRouteParticipantIDs(t, routes[2], 3, 4)
}

func TestBearingSweepInsertion_MatchesDriverByHomeBearingNotDriverOrder(t *testing.T) {
	routes, participants := bearingSweepFixture(
		[]float64{85, 95, 265, 275},
		[]bearingSweepDriver{{id: 1, bearing: 270, capacity: 2}, {id: 2, bearing: 90, capacity: 2}},
	)

	if ok := (&BalancedRouter{}).bearingSweepInsertion(models.Coordinates{}, routes, []int64{1, 2}, participants); !ok {
		t.Fatal("bearingSweepInsertion() failed, want a complete sweep seed")
	}
	assertSweepRouteParticipantIDs(t, routes[2], 1, 2)
	assertSweepRouteParticipantIDs(t, routes[1], 3, 4)
}

func TestBearingSweepInsertion_KeepsWraparoundClusterContiguous(t *testing.T) {
	routes, participants := bearingSweepFixture(
		[]float64{350, 355, 5, 10},
		[]bearingSweepDriver{{id: 1, bearing: 355, capacity: 2}, {id: 2, bearing: 5, capacity: 2}},
	)

	if ok := (&BalancedRouter{}).bearingSweepInsertion(models.Coordinates{}, routes, []int64{1, 2}, participants); !ok {
		t.Fatal("bearingSweepInsertion() failed, want a complete sweep seed")
	}
	assertSweepRouteParticipantIDs(t, routes[1], 1, 2)
	assertSweepRouteParticipantIDs(t, routes[2], 3, 4)
}

func TestBearingSweepInsertion_ReservesOneGroupPerRemainingDriver(t *testing.T) {
	routes, participants := bearingSweepFixture(
		[]float64{0, 10, 20},
		[]bearingSweepDriver{{id: 1, bearing: 0, capacity: 3}, {id: 2, bearing: 10, capacity: 3}, {id: 3, bearing: 20, capacity: 3}},
	)

	if ok := (&BalancedRouter{}).bearingSweepInsertion(models.Coordinates{}, routes, []int64{1, 2, 3}, participants); !ok {
		t.Fatal("bearingSweepInsertion() failed, want a complete sweep seed")
	}
	for _, driverID := range []int64{1, 2, 3} {
		if got := len(routes[driverID].stops); got != 1 {
			t.Fatalf("driver %d stop count = %d, want 1", driverID, got)
		}
	}
}

func TestBearingSweepInsertion_DeterministicTieBreaks(t *testing.T) {
	participants := participantsAtBearings(0, 90, 180, 270)
	for i, participant := range participants {
		participant.Address = fmt.Sprintf("Household %d", i+1)
	}
	groups := bearingSweepGroups(models.Coordinates{}, participants)
	for i, group := range groups {
		if got, want := group.members[0].ID, int64(i+1); got != want {
			t.Fatalf("equal-gap sweep group %d ID = %d, want household-key order ID %d", i, got, want)
		}
	}

	routes, oneGroup := bearingSweepFixture(
		[]float64{0},
		[]bearingSweepDriver{{id: 2, bearing: 0, capacity: 1}, {id: 1, bearing: 0, capacity: 1}},
	)
	if ok := (&BalancedRouter{}).bearingSweepInsertion(models.Coordinates{}, routes, []int64{2, 1}, oneGroup); !ok {
		t.Fatal("bearingSweepInsertion() failed, want a complete sweep seed")
	}
	assertSweepRouteParticipantIDs(t, routes[1], 1)
}

func TestBearingSweepInsertion_DoesNotBurnDriverOnUnfittingGroup(t *testing.T) {
	institute := models.Coordinates{}
	groupCoords := participantAtBearing(1, 0).GetCoords()
	soloCoords := participantAtBearing(3, 10).GetCoords()
	participants := []*models.Participant{
		{ID: 1, Name: "Group 1", Address: "Shared", Lat: groupCoords.Lat, Lng: groupCoords.Lng},
		{ID: 2, Name: "Group 2", Address: "Shared", Lat: groupCoords.Lat, Lng: groupCoords.Lng},
		{ID: 3, Name: "Solo", Address: "Solo", Lat: soloCoords.Lat, Lng: soloCoords.Lng},
	}
	small := driverAtBearing(1, 0, 1)
	large := driverAtBearing(2, 10, 2)
	routes := map[int64]*balancedRoute{
		small.ID: {driver: small},
		large.ID: {driver: large},
	}
	if ok := (&BalancedRouter{}).bearingSweepInsertion(institute, routes, []int64{small.ID, large.ID}, participants); !ok {
		t.Fatal("bearingSweepInsertion() failed after the small driver rejected only the oversized group")
	}
	assertSweepRouteParticipantIDs(t, routes[small.ID], 3)
	assertSweepRouteParticipantIDs(t, routes[large.ID], 1, 2)
}

func TestMaximizeNonemptyRoutes_UsesAugmentingRelocationChain(t *testing.T) {
	for _, mode := range []RouteMode{RouteModeDropoff, RouteModePickup} {
		t.Run(string(mode), func(t *testing.T) {
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
				driverAtBearing(1, 30, 4),
				driverAtBearing(2, 20, 2),
				driverAtBearing(3, 0, 1),
			}
			routes := map[int64]*balancedRoute{
				1: {driver: drivers[0], stops: participants[:4]},
				2: {driver: drivers[1], stops: participants[4:]},
				3: {driver: drivers[2]},
			}

			repairs, err := (&BalancedRouter{}).maximizeNonemptyRoutes(
				context.Background(),
				newRouteContext(stableDistanceCalculator{}, models.Coordinates{}, mode),
				routes,
				[]int64{1, 2, 3},
			)
			if err != nil {
				t.Fatalf("maximizeNonemptyRoutes() error = %v", err)
			}
			if repairs != 1 {
				t.Fatalf("repair count = %d, want 1", repairs)
			}
			if len(routes[1].stops) != 2 || len(routes[2].stops) != 2 || len(routes[3].stops) != 1 {
				t.Fatalf("route sizes = %d/%d/%d, want 2/2/1", len(routes[1].stops), len(routes[2].stops), len(routes[3].stops))
			}
			if routes[3].stops[0].Address != "C" {
				t.Fatalf("cap-1 driver received household %q, want C", routes[3].stops[0].Address)
			}
			if routes[2].stops[0].Address != routes[2].stops[1].Address {
				t.Fatalf("cap-2 driver household split: %q/%q", routes[2].stops[0].Address, routes[2].stops[1].Address)
			}
		})
	}
}

func TestMaximizeNonemptyRoutes_AllSingletonRoutesReturnQuickly(t *testing.T) {
	const singletonCount = 6

	routes := make(map[int64]*balancedRoute, singletonCount+1)
	driverIDs := make([]int64, 0, singletonCount+1)
	for id := int64(1); id <= singletonCount+1; id++ {
		driver := driverAtBearing(id, float64(id), 1)
		route := &balancedRoute{driver: driver}
		if id <= singletonCount {
			route.stops = []*models.Participant{{
				ID:      id,
				Name:    fmt.Sprintf("Rider %d", id),
				Address: fmt.Sprintf("Household %d", id),
			}}
		}
		routes[id] = route
		driverIDs = append(driverIDs, id)
	}

	started := time.Now()
	repairs, err := (&BalancedRouter{}).maximizeNonemptyRoutes(
		context.Background(),
		newRouteContext(stableDistanceCalculator{}, models.Coordinates{}, RouteModeDropoff),
		routes,
		driverIDs,
	)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("maximizeNonemptyRoutes() error = %v", err)
	}
	if repairs != 0 {
		t.Fatalf("repair count = %d, want 0", repairs)
	}
	if elapsed >= time.Second {
		t.Fatalf("maximizeNonemptyRoutes() took %v, want under 1s", elapsed)
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

func TestBalancedRouter_HeterogeneousCapacityForcedFallbackUsesAllDrivers(t *testing.T) {
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
	routes := map[int64]*balancedRoute{
		drivers[0].ID: {driver: drivers[0]},
		drivers[1].ID: {driver: drivers[1]},
	}
	if ok := (&BalancedRouter{}).bearingSweepInsertion(institute, routes, []int64{1, 2}, participants); ok {
		t.Fatal("bearingSweepInsertion() succeeded, want a forced fallback for noncontiguous capacity packing")
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

type bearingSweepDriver struct {
	id       int64
	bearing  float64
	capacity int
}

func bearingSweepFixture(groupBearings []float64, drivers []bearingSweepDriver) (map[int64]*balancedRoute, []*models.Participant) {
	participants := make([]*models.Participant, len(groupBearings))
	for i, bearing := range groupBearings {
		participant := participantAtBearing(int64(i+1), bearing)
		participant.Address = fmt.Sprintf("Household %d", i+1)
		participants[i] = participant
	}

	routes := make(map[int64]*balancedRoute, len(drivers))
	for _, spec := range drivers {
		driver := driverAtBearing(spec.id, spec.bearing, spec.capacity)
		routes[spec.id] = &balancedRoute{driver: driver}
	}
	return routes, participants
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

func assertSweepRouteParticipantIDs(t *testing.T, route *balancedRoute, want ...int64) {
	t.Helper()
	got := make([]int64, len(route.stops))
	for i, stop := range route.stops {
		got[i] = stop.ID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("driver %d participant IDs = %v, want %v", route.driver.ID, got, want)
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

func TestRouteCorridorSpread(t *testing.T) {
	institute := models.Coordinates{}
	tests := []struct {
		name     string
		bearings []float64
		want     float64
	}{
		{name: "empty route", want: 0},
		{name: "single stop", bearings: []float64{42}, want: 0},
		{name: "wraparound", bearings: []float64{10, 350}, want: 20},
		{name: "half circle", bearings: []float64{0, 90, 180}, want: 180},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stops := make([]*models.Participant, 0, len(tt.bearings))
			for i, bearing := range tt.bearings {
				stops = append(stops, participantAtBearing(int64(i+1), bearing))
			}

			got := routeCorridorSpread(institute, stops)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("routeCorridorSpread() = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestBearingFromInstitute_NormalizesAntimeridianLongitudeDelta(t *testing.T) {
	got := bearingFromInstitute(
		models.Coordinates{Lat: 0, Lng: 179},
		models.Coordinates{Lat: 1, Lng: -179},
	)
	const want = 63.43494882292201
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("bearingFromInstitute() = %.12f, want %.12f", got, want)
	}
}

func TestRouteCorridorSpread_StraddlesAntimeridian(t *testing.T) {
	institute := models.Coordinates{Lat: 0, Lng: 179}
	stops := []*models.Participant{
		{ID: 1, Address: "West", Lat: 1, Lng: 178.5},
		{ID: 2, Address: "East", Lat: 1, Lng: -179.5},
	}

	got := routeCorridorSpread(institute, stops)
	const want = 82.8749836510982
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("routeCorridorSpread() = %.12f, want %.12f", got, want)
	}
}

func TestSolutionObjective_PrefersLowerCorridorSpreadOverFasterRoutes(t *testing.T) {
	institute := models.Coordinates{}
	participants := participantsAtBearings(0, 10, 90, 100)
	driver1 := &models.Driver{ID: 1, Lat: -1, Lng: -1}
	driver2 := &models.Driver{ID: 2, Lat: -2, Lng: -2}
	distances := newOverrideDistanceAdapter(1)
	distances.setDuration(participants[0].GetCoords(), participants[1].GetCoords(), 100)
	distances.setDuration(participants[2].GetCoords(), participants[3].GetCoords(), 100)
	rc := newRouteContext(distances, institute, RouteModeDropoff)

	coherent := objectiveScoreForTest(t, rc,
		objectiveTestRoute{driver: driver1, stops: participants[0:2]},
		objectiveTestRoute{driver: driver2, stops: participants[2:4]},
	)
	zigzag := objectiveScoreForTest(t, rc,
		objectiveTestRoute{driver: driver1, stops: []*models.Participant{participants[0], participants[2]}},
		objectiveTestRoute{driver: driver2, stops: []*models.Participant{participants[1], participants[3]}},
	)

	if coherent.corridorSpread >= zigzag.corridorSpread {
		t.Fatalf("coherent corridor spread = %d, want less than zigzag spread %d", coherent.corridorSpread, zigzag.corridorSpread)
	}
	if coherent.latestParticipantCompletion <= zigzag.latestParticipantCompletion {
		t.Fatalf("coherent latest completion = %.0f, want slower than zigzag completion %.0f", coherent.latestParticipantCompletion, zigzag.latestParticipantCompletion)
	}
	if !coherent.betterThan(zigzag) {
		t.Fatal("lower-spread solution did not beat faster zigzag solution")
	}
}

func TestSolutionObjective_UsedDriversDominatesCorridorSpread(t *testing.T) {
	institute := models.Coordinates{}
	participants := participantsAtBearings(0, 10, 90, 100)
	driver1 := &models.Driver{ID: 1, Lat: -1, Lng: -1}
	driver2 := &models.Driver{ID: 2, Lat: -2, Lng: -2}
	rc := newRouteContext(newOverrideDistanceAdapter(1), institute, RouteModeDropoff)

	oneDriver := objectiveScoreForTest(t, rc,
		objectiveTestRoute{driver: driver1, stops: participants},
	)
	twoDrivers := objectiveScoreForTest(t, rc,
		objectiveTestRoute{driver: driver1, stops: []*models.Participant{participants[0], participants[2]}},
		objectiveTestRoute{driver: driver2, stops: []*models.Participant{participants[1], participants[3]}},
	)

	if twoDrivers.corridorSpread <= oneDriver.corridorSpread {
		t.Fatalf("two-driver corridor spread = %d, want greater than one-driver spread %d", twoDrivers.corridorSpread, oneDriver.corridorSpread)
	}
	if !twoDrivers.betterThan(oneDriver) {
		t.Fatal("solution using more drivers did not win over its lower-spread alternative")
	}
}

func TestSolutionObjective_SameCorridorBucketFallsThroughToTime(t *testing.T) {
	institute := models.Coordinates{}
	slowerStops := participantsAtBearings(0, 20)
	fasterStops := participantsAtBearings(180, 204)
	driver := &models.Driver{ID: 1, Lat: -1, Lng: -1}
	distances := newOverrideDistanceAdapter(1)
	distances.setDuration(slowerStops[0].GetCoords(), slowerStops[1].GetCoords(), 100)
	rc := newRouteContext(distances, institute, RouteModeDropoff)

	slower := objectiveScoreForTest(t, rc, objectiveTestRoute{driver: driver, stops: slowerStops})
	faster := objectiveScoreForTest(t, rc, objectiveTestRoute{driver: driver, stops: fasterStops})

	spreadDifference := routeCorridorSpread(institute, fasterStops) - routeCorridorSpread(institute, slowerStops)
	if spreadDifference <= 0 || spreadDifference >= 10 {
		t.Fatalf("raw spread difference = %.2f, want between 0 and 10 degrees", spreadDifference)
	}
	if slower.corridorSpread != faster.corridorSpread {
		t.Fatalf("corridor buckets differ: slower = %d, faster = %d", slower.corridorSpread, faster.corridorSpread)
	}
	if !faster.betterThan(slower) {
		t.Fatal("faster solution did not win after equal corridor buckets")
	}
}

type objectiveTestRoute struct {
	driver *models.Driver
	stops  []*models.Participant
}

func objectiveScoreForTest(t *testing.T, rc routeContext, routes ...objectiveTestRoute) solutionScore {
	t.Helper()

	metrics := make(map[int64]routeObjectiveMetrics, len(routes))
	driverIDs := make([]int64, 0, len(routes))
	for _, route := range routes {
		routeMetrics, err := rc.evaluateRouteObjective(context.Background(), route.driver, route.stops)
		if err != nil {
			t.Fatalf("evaluateRouteObjective() error = %v", err)
		}
		metrics[route.driver.ID] = routeMetrics
		driverIDs = append(driverIDs, route.driver.ID)
	}
	return scoreSolution(metrics, driverIDs)
}

func participantsAtBearings(bearings ...float64) []*models.Participant {
	participants := make([]*models.Participant, 0, len(bearings))
	for i, bearing := range bearings {
		participants = append(participants, participantAtBearing(int64(i+1), bearing))
	}
	return participants
}

func participantAtBearing(id int64, bearing float64) *models.Participant {
	radians := bearing * math.Pi / 180
	return &models.Participant{ID: id, Lat: math.Cos(radians), Lng: math.Sin(radians)}
}

func TestOptimizeAssignments_UsesTimeTieBreakerWhenDriverCountIsEqual(t *testing.T) {
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

	routes := map[int64]*balancedRoute{
		driver1.ID: {driver: driver1, stops: []*models.Participant{x1, y1}},
		driver2.ID: {driver: driver2, stops: []*models.Participant{x2, y2}},
		driver3.ID: {driver: driver3, stops: []*models.Participant{c1, c2}},
	}
	driverIDs := []int64{driver1.ID, driver2.ID, driver3.ID}
	rc := newRouteContext(distances, activity, RouteModeDropoff)

	if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
		t.Fatalf("optimizeRouteOrders() error = %v", err)
	}
	if routes[driver3.ID].stops[0].ID != c1.ID {
		t.Fatalf("peer route changed before the global maximum dropped")
	}

	if _, err := optimizeAssignments(ctx, rc, routes, driverIDs); err != nil {
		t.Fatalf("optimizeAssignments() error = %v", err)
	}
	if routes[driver3.ID].stops[0].ID != c2.ID {
		t.Fatalf("peer route starts with participant %d, want %d after the assignment lowered the global maximum", routes[driver3.ID].stops[0].ID, c2.ID)
	}
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

	remaining, err := roundRobinInsertion(context.Background(), newRouteContext(distances, activity, RouteModeDropoff), routes, []int64{driver.ID}, participants)
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
