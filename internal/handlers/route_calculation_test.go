package handlers

import (
	"context"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"testing"
)

func TestRouteCalculation_AssignedVehicleSuccessCreatesRestorableSession(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	ctx := context.Background()

	participant, err := store.Participants().Create(ctx, &models.Participant{Name: "Rider", Address: "1 Rider Rd", Lat: 40.1, Lng: -73.9})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	driver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Driver", Address: "2 Driver Rd", Lat: 40.2, Lng: -73.8, VehicleCapacity: 2})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	location, err := store.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "3 Event Ave", Lat: 42, Lng: -75})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}
	van, err := store.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Blue Van", Capacity: 8})
	if err != nil {
		t.Fatalf("create organization vehicle: %v", err)
	}

	router := &captureRouter{result: &models.RoutingResult{
		Routes: []models.CalculatedRoute{{
			Driver: driver,
			Stops:  []models.RouteStop{{Participant: participant}},
		}},
		Summary: models.RoutingSummary{TotalDriversUsed: 1},
	}}
	calculation := newRouteCalculation(store, router, handler.RouteSession)

	outcome := calculation.calculate(ctx, routeCalculationInput{
		ParticipantIDs:        []int64{participant.ID},
		DriverIDs:             []int64{driver.ID},
		ActivityLocationID:    location.ID,
		RouteTime:             "18:30",
		Mode:                  models.RouteModeDropoff,
		OrgVehicleAssignments: map[int64]int64{driver.ID: van.ID},
	})

	if outcome.Kind != routeCalculationSuccess {
		t.Fatalf("outcome kind = %v, want success; err=%v", outcome.Kind, outcome.Err)
	}
	if router.lastRequest == nil {
		t.Fatal("expected router to receive a request")
	}
	if got := router.lastRequest.InstituteCoords; got != location.GetCoords() {
		t.Fatalf("router activity coordinates = %#v, want %#v", got, location.GetCoords())
	}
	if got := router.lastRequest.Mode; got != models.RouteModeDropoff {
		t.Fatalf("router mode = %q, want %q", got, models.RouteModeDropoff)
	}
	if got := router.lastRequest.Drivers[0].VehicleCapacity; got != van.Capacity {
		t.Fatalf("router driver capacity = %d, want assigned van capacity %d", got, van.Capacity)
	}
	if got := outcome.Result.Routes[0].OrgVehicleID; got != van.ID {
		t.Fatalf("route organization vehicle ID = %d, want %d", got, van.ID)
	}
	if got := outcome.Result.Routes[0].EffectiveCapacity; got != van.Capacity {
		t.Fatalf("route effective capacity = %d, want %d", got, van.Capacity)
	}
	if got := outcome.Result.Summary.OrgVehiclesUsed; got != 1 {
		t.Fatalf("organization vehicles used = %d, want 1", got)
	}

	session := handler.RouteSession.Get(outcome.Session.ID)
	if session == nil {
		t.Fatal("expected route session to be restorable")
	}
	if got := session.SelectedDrivers[0].VehicleCapacity; got != van.Capacity {
		t.Fatalf("session driver capacity = %d, want %d", got, van.Capacity)
	}
	if got := session.DriverOrgVehicles[driver.ID]; got == nil || got.ID != van.ID {
		t.Fatalf("session organization vehicle = %#v, want ID %d", got, van.ID)
	}
	if got := session.CurrentRoutes[0].OrgVehicleID; got != van.ID {
		t.Fatalf("session route organization vehicle ID = %d, want %d", got, van.ID)
	}
}

func TestRouteCalculation_CapacityShortageReturnsAssignmentsAndAvailableVehicles(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	ctx := context.Background()

	participant, err := store.Participants().Create(ctx, &models.Participant{Name: "Rider", Address: "1 Rider Rd", Lat: 40.1, Lng: -73.9})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	driver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Driver", Address: "2 Driver Rd", Lat: 40.2, Lng: -73.8, VehicleCapacity: 1})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	location, err := store.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "3 Event Ave", Lat: 42, Lng: -75})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}
	van, err := store.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Blue Van", Capacity: 2})
	if err != nil {
		t.Fatalf("create organization vehicle: %v", err)
	}
	if err := store.Settings().Update(ctx, &models.Settings{UseMiles: false}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	routingFailure := &routing.ErrRoutingFailed{
		Reason:            "not enough capacity",
		UnassignedCount:   1,
		TotalCapacity:     2,
		TotalParticipants: 3,
	}
	calculation := newRouteCalculation(store, &captureRouter{err: routingFailure}, handler.RouteSession)
	assignments := map[int64]int64{driver.ID: van.ID}

	outcome := calculation.calculate(ctx, routeCalculationInput{
		ParticipantIDs:        []int64{participant.ID},
		DriverIDs:             []int64{driver.ID},
		ActivityLocationID:    location.ID,
		RouteTime:             "18:30",
		Mode:                  models.RouteModeDropoff,
		OrgVehicleAssignments: assignments,
	})

	if outcome.Kind != routeCalculationShortage {
		t.Fatalf("outcome kind = %v, want shortage; err=%v", outcome.Kind, outcome.Err)
	}
	shortage := outcome.Shortage
	if shortage == nil || shortage.RoutingError != routingFailure {
		t.Fatalf("shortage routing error = %#v, want %#v", shortage, routingFailure)
	}
	if got := shortage.OrgVehicleAssignments[driver.ID]; got != van.ID {
		t.Fatalf("preserved assignment = %d, want %d", got, van.ID)
	}
	if len(shortage.AvailableOrgVehicles) != 1 || shortage.AvailableOrgVehicles[0].ID != van.ID {
		t.Fatalf("available organization vehicles = %#v, want vehicle %d", shortage.AvailableOrgVehicles, van.ID)
	}
	if got := shortage.DriverOrgVehicles[driver.ID]; got == nil || got.ID != van.ID {
		t.Fatalf("driver organization vehicle = %#v, want ID %d", got, van.ID)
	}
	if got := shortage.Drivers[0].VehicleCapacity; got != driver.VehicleCapacity {
		t.Fatalf("shortage driver capacity = %d, want original capacity %d", got, driver.VehicleCapacity)
	}
	if shortage.ActivityLocation.ID != location.ID || shortage.UseMiles || shortage.RouteTime != "18:30" {
		t.Fatalf("shortage context = %#v, want selected location, settings, and route time", shortage)
	}
}
