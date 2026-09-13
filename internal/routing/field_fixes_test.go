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
