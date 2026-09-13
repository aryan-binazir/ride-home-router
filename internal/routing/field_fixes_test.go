package routing

import (
	"context"
	"errors"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"testing"
)

// A roster that cannot fit is reported immediately, before any distance work,
// so a 500-rider shortage does not run into the calculation timeout.
func TestCalculateRoutesReportsSeatShortageBeforeSearching(t *testing.T) {
	calc := &countingSolveDistanceCalculator{}
	router := NewBalancedRouter(calc)
	participants := make([]models.Participant, 5)
	for i := range participants {
		participants[i] = models.Participant{ID: int64(i + 1), Name: "Rider", Address: "1 Rider Rd", Lat: 40 + float64(i)*0.01, Lng: -74}
	}
	_, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 40, Lng: -74},
		Participants:    participants,
		Drivers:         []models.Driver{{ID: 1, Name: "Small Car", Lat: 40.1, Lng: -74, VehicleCapacity: 3}},
		Mode:            "dropoff",
	})
	var failed *ErrRoutingFailed
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *ErrRoutingFailed", err)
	}
	if failed.UnassignedCount != 2 || failed.TotalCapacity != 3 || failed.TotalParticipants != 5 {
		t.Fatalf("shortage = %+v, want 2 unassigned of 5 with 3 seats", failed)
	}
	if len(calc.calls) != 0 {
		t.Fatalf("distance lookups before the shortage check = %d, want 0", len(calc.calls))
	}
}

// Two drivers who each hold the other's neighbourhood must end up swapped:
// the driver phase exchanges whole cars, not households.
func TestAssignmentSearchSwapsDriversWhoseHomesAreCrossed(t *testing.T) {
	institute := models.Coordinates{Lat: 0, Lng: 0}
	north := &models.Driver{ID: 1, Name: "North Driver", Lat: 0.10, Lng: 0, VehicleCapacity: 2}
	south := &models.Driver{ID: 2, Name: "South Driver", Lat: -0.10, Lng: 0, VehicleCapacity: 2}
	riders := []*models.Participant{
		{ID: 11, Name: "North One", Address: "1 North St", Lat: 0.08, Lng: 0.005},
		{ID: 12, Name: "North Two", Address: "2 North St", Lat: 0.09, Lng: -0.005},
		{ID: 21, Name: "South One", Address: "1 South St", Lat: -0.08, Lng: 0.005},
		{ID: 22, Name: "South Two", Address: "2 South St", Lat: -0.09, Lng: -0.005},
	}
	req := &RoutingRequest{InstituteCoords: institute, Drivers: []models.Driver{*north, *south}, Mode: "dropoff"}
	for _, rider := range riders {
		req.Participants = append(req.Participants, *rider)
	}
	lookup, err := prepareSolveDistances(context.Background(), distance.NewEstimator(), req)
	if err != nil {
		t.Fatal(err)
	}
	rc := newRouteContext(lookup, institute, normalizeRouteMode("dropoff"))
	rc.prepareParticipants(riders)
	routes := map[int64]*balancedRoute{
		north.ID: {driver: north, stops: []*models.Participant{riders[2], riders[3]}},
		south.ID: {driver: south, stops: []*models.Participant{riders[0], riders[1]}},
	}
	if _, err := optimizeDriverAssignments(context.Background(), rc, routes, []int64{north.ID, south.ID}); err != nil {
		t.Fatal(err)
	}
	for _, stop := range routes[north.ID].stops {
		if stop.Lat < 0 {
			t.Fatalf("north driver still carries %s: %v", stop.Name, names(routes[north.ID].stops))
		}
	}
	for _, stop := range routes[south.ID].stops {
		if stop.Lat > 0 {
			t.Fatalf("south driver still carries %s: %v", stop.Name, names(routes[south.ID].stops))
		}
	}
}

func names(stops []*models.Participant) []string {
	out := make([]string, len(stops))
	for i, stop := range stops {
		out[i] = stop.Name
	}
	return out
}

// A swap the comparator would prefer (it lowers the worst detour) is still
// refused when it adds driving overall, and refusing it leaves both cars as
// they were.
func TestDriverSwapThatAddsDrivingIsRefused(t *testing.T) {
	institute := models.Coordinates{Lat: 0, Lng: 0}
	a := &models.Driver{ID: 1, Name: "A", Lat: 0.029, Lng: 0.099, VehicleCapacity: 2}
	b := &models.Driver{ID: 2, Name: "B", Lat: 0.064, Lng: -0.043, VehicleCapacity: 2}
	g1 := &models.Participant{ID: 11, Name: "G1", Address: "1 First St", Lat: -0.023, Lng: 0.034}
	g2 := &models.Participant{ID: 21, Name: "G2", Address: "1 Second St", Lat: -0.095, Lng: -0.008}
	req := &RoutingRequest{InstituteCoords: institute, Drivers: []models.Driver{*a, *b}, Participants: []models.Participant{*g1, *g2}, Mode: "dropoff"}
	lookup, err := prepareSolveDistances(context.Background(), distance.NewEstimator(), req)
	if err != nil {
		t.Fatal(err)
	}
	rc := newRouteContext(lookup, institute, normalizeRouteMode("dropoff"))
	rc.prepareParticipants([]*models.Participant{g1, g2})
	routes := map[int64]*balancedRoute{a.ID: {driver: a, stops: []*models.Participant{g1}}, b.ID: {driver: b, stops: []*models.Participant{g2}}}
	before := scoreSolution(mustMetrics(t, rc, routes), []int64{a.ID, b.ID})
	swapped := map[int64]*balancedRoute{a.ID: {driver: a, stops: []*models.Participant{g2}}, b.ID: {driver: b, stops: []*models.Participant{g1}}}
	after := scoreSolution(mustMetrics(t, rc, swapped), []int64{a.ID, b.ID})
	if !(after.maxDriverDetour < before.maxDriverDetour && after.aggregateDriveDuration > before.aggregateDriveDuration) {
		t.Fatalf("fixture no longer discriminates: before=%+v after=%+v", before, after)
	}
	swaps, err := optimizeDriverAssignments(context.Background(), rc, routes, []int64{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if swaps != 0 || routes[a.ID].stops[0] != g1 || routes[b.ID].stops[0] != g2 {
		t.Fatalf("swap accepted despite extra driving: swaps=%d a=%v b=%v", swaps, names(routes[a.ID].stops), names(routes[b.ID].stops))
	}
}

func mustMetrics(t *testing.T, rc routeContext, routes map[int64]*balancedRoute) map[int64]routeObjectiveMetrics {
	t.Helper()
	out := make(map[int64]routeObjectiveMetrics, len(routes))
	for id, route := range routes {
		metrics, err := rc.evaluateRouteObjective(context.Background(), route.driver, route.stops)
		if err != nil {
			t.Fatal(err)
		}
		out[id] = metrics
	}
	return out
}
