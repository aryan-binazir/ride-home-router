package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"strings"
	"testing"
	"time"
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
	if err := store.Drivers().UpdateCoordinates(ctx, driver.ID, driver.Address, driver.GetCoords(), time.Now().Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	geocoder := &refreshGeocoder{}
	calculation := newRouteCalculation(store, router, handler.RouteSession, geocoder)

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
	if got := router.lastRequest.Drivers[0].GetCoords(); got != (models.Coordinates{Lat: 41, Lng: -72}) || geocoder.calls.Load() != 1 {
		t.Fatalf("refreshed assigned driver coords=%#v calls=%d", got, geocoder.calls.Load())
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

	session, ok := mustLoadRouteSession(t, handler.RouteSession, outcome.Session.ID)
	if !ok {
		t.Fatal("expected route session to be restorable")
	}
	if got := session.Routes[0].EffectiveCapacity; got != van.Capacity {
		t.Fatalf("session route capacity = %d, want %d", got, van.Capacity)
	}
	if got := session.Routes[0].OrgVehicleID; got != van.ID {
		t.Fatalf("session route organization vehicle ID = %d, want %d", got, van.ID)
	}
}

func TestRouteCalculation_CanceledAfterSolveDoesNotReturnSession(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	ctx, cancel := context.WithCancel(context.Background())

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

	calculation := newRouteCalculation(store, &captureRouter{
		result:     &models.RoutingResult{Routes: []models.CalculatedRoute{{Driver: driver}}},
		afterSolve: cancel,
	}, handler.RouteSession, handler.Geocoder)
	outcome := calculation.calculate(ctx, routeCalculationInput{
		ParticipantIDs:     []int64{participant.ID},
		DriverIDs:          []int64{driver.ID},
		ActivityLocationID: location.ID,
		RouteTime:          "18:30",
		Mode:               models.RouteModeDropoff,
	})

	if outcome.Kind != routeCalculationRouteFailure || !errors.Is(outcome.Err, context.Canceled) {
		t.Fatalf("outcome = kind %v err %v, want canceled route failure", outcome.Kind, outcome.Err)
	}
	if outcome.Session.ID != "" {
		t.Fatalf("outcome session ID = %q, want no session", outcome.Session.ID)
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
	calculation := newRouteCalculation(store, &captureRouter{err: routingFailure}, handler.RouteSession, handler.Geocoder)
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

func TestRouteCalculation_MissingAssignedVanRejectsBeforeRouting(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	location, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: "Gym", Address: "2 Test St"})
	if err != nil {
		t.Fatal(err)
	}
	van, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Deleted van", Capacity: 12})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OrganizationVehicles().Delete(t.Context(), van.ID); err != nil {
		t.Fatal(err)
	}
	participant, err := store.Participants().Create(t.Context(), &models.Participant{Name: "Rider", Address: "3 Test St"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Drivers().UpdateCoordinates(t.Context(), driver.ID, driver.Address, driver.GetCoords(), time.Now().Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	geocoder := &refreshGeocoder{}
	for _, id := range []int64{van.ID, van.ID + 1000} {
		router := &captureRouter{}
		calculation := newRouteCalculation(store, router, handler.RouteSession, geocoder)
		outcome := calculation.calculate(t.Context(), routeCalculationInput{ParticipantIDs: []int64{participant.ID}, DriverIDs: []int64{driver.ID}, ActivityLocationID: location.ID, OrgVehicleAssignments: map[int64]int64{driver.ID: id}})
		if outcome.Kind != routeCalculationValidationFailure || !errors.Is(outcome.Err, errSelectedVanNotFound) || outcome.Err.Error() != selectedVanNotFoundMessage || outcome.Session.ID != "" || router.lastRequest != nil {
			t.Fatalf("missing assigned van %d: outcome=%#v request=%#v", id, outcome, router.lastRequest)
		}
		if geocoder.calls.Load() != 0 {
			t.Fatal("missing van triggered coordinate refresh")
		}
		handler.Router = router
		for _, endpoint := range []struct {
			path   string
			handle http.HandlerFunc
		}{
			{"/api/v1/routes/calculate", handler.HandleCalculateRoutes},
			{"/api/v1/routes/calculate-with-org-vehicles", handler.HandleCalculateRoutesWithOrgVehicles},
		} {
			body := fmt.Sprintf("participant_ids=%d&driver_ids=%d&activity_location_id=%d&route_time=18%%3A30&mode=dropoff&org_vehicle_%d=%d", participant.ID, driver.ID, location.ID, driver.ID, id)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			endpoint.handle(w, r)
			wantTrigger := `{"showToast":{"message":"` + selectedVanNotFoundMessage + `","type":"error"}}`
			if w.Code != http.StatusBadRequest || w.Header().Get("HX-Trigger") != wantTrigger || router.lastRequest != nil {
				t.Fatalf("missing van HTTP %s: status=%d trigger=%q", endpoint.path, w.Code, w.Header().Get("HX-Trigger"))
			}
		}
	}
}

func TestRouteCalculation_AssignedVanLookupErrorsPropagate(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	location, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: "Gym", Address: "2 Test St"})
	if err != nil {
		t.Fatal(err)
	}
	for _, lookupErr := range []error{errors.New("van lookup unavailable"), context.Canceled} {
		router := &captureRouter{}
		db := testDataStore{DataStore: store, orgVehicleRepo: orgVehicleRepoWithError{OrganizationVehicleRepository: store.OrganizationVehicles(), err: lookupErr}}
		outcome := newRouteCalculation(db, router, handler.RouteSession, nil).calculate(t.Context(), routeCalculationInput{DriverIDs: []int64{driver.ID}, ActivityLocationID: location.ID, OrgVehicleAssignments: map[int64]int64{driver.ID: 1}})
		if outcome.Kind != routeCalculationInternalFailure || !errors.Is(outcome.Err, lookupErr) || router.lastRequest != nil || outcome.Session.ID != "" {
			t.Fatalf("lookup failure: outcome=%#v request=%#v", outcome, router.lastRequest)
		}
	}
}

func TestRouteCalculation_SharedVanAndPersonalDriverMetadata(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	var drivers []models.Driver
	for _, name := range []string{"Van driver one", "Van driver two", "Personal driver"} {
		driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: name, Address: "1 Test St", VehicleCapacity: 4})
		if err != nil {
			t.Fatal(err)
		}
		drivers = append(drivers, *driver)
	}
	location, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: "Gym", Address: "2 Test St"})
	if err != nil {
		t.Fatal(err)
	}
	van, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Shared van", Capacity: 12})
	if err != nil {
		t.Fatal(err)
	}
	router := &captureRouter{result: &models.RoutingResult{Routes: []models.CalculatedRoute{
		{Driver: &drivers[0], Stops: []models.RouteStop{{}}},
		{Driver: &drivers[1], Stops: []models.RouteStop{{}}},
		{Driver: &drivers[2]},
	}}}
	outcome := newRouteCalculation(store, router, handler.RouteSession, nil).calculate(t.Context(), routeCalculationInput{DriverIDs: []int64{drivers[0].ID, drivers[1].ID, drivers[2].ID}, ActivityLocationID: location.ID, OrgVehicleAssignments: map[int64]int64{drivers[0].ID: van.ID, drivers[1].ID: van.ID}})
	if outcome.Kind != routeCalculationSuccess {
		t.Fatalf("outcome=%#v", outcome)
	}
	capacities := map[int64]int{}
	for _, driver := range router.lastRequest.Drivers {
		capacities[driver.ID] = driver.VehicleCapacity
	}
	if capacities[drivers[0].ID] != 12 || capacities[drivers[1].ID] != 12 || capacities[drivers[2].ID] != 4 {
		t.Fatalf("effective capacities=%v", capacities)
	}
	for _, route := range outcome.Result.Routes[:2] {
		if route.OrgVehicleID != van.ID || route.OrgVehicleName != "Shared van" || route.EffectiveCapacity != 12 {
			t.Fatalf("van route=%#v", route)
		}
	}
	personal := outcome.Result.Routes[2]
	if personal.OrgVehicleID != 0 || personal.OrgVehicleName != "" || personal.EffectiveCapacity != 4 || outcome.Result.Summary.OrgVehiclesUsed != 1 {
		t.Fatalf("personal route=%#v summary=%#v", personal, outcome.Result.Summary)
	}
	session, ok := mustLoadRouteSession(t, handler.RouteSession, outcome.Session.ID)
	if !ok || session.Routes[0].OrgVehicleName != "Shared van" || session.Routes[1].EffectiveCapacity != 12 || session.Routes[2].EffectiveCapacity != 4 {
		t.Fatalf("restored session=%#v", session)
	}
}

type cancelVanLookupRepository struct {
	database.OrganizationVehicleRepository
	cancel context.CancelFunc
}

func (r cancelVanLookupRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.OrganizationVehicle, error) {
	r.cancel()
	return r.OrganizationVehicleRepository.GetByIDs(ctx, ids)
}

func TestRouteCalculation_CanceledDuringCurrentVanLookup(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	location, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: "Gym", Address: "2 Test St"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := testDataStore{DataStore: store, orgVehicleRepo: cancelVanLookupRepository{OrganizationVehicleRepository: store.OrganizationVehicles(), cancel: cancel}}
	router := &captureRouter{}
	outcome := newRouteCalculation(db, router, handler.RouteSession, nil).calculate(ctx, routeCalculationInput{DriverIDs: []int64{driver.ID}, ActivityLocationID: location.ID, OrgVehicleAssignments: map[int64]int64{driver.ID: 1}})
	if outcome.Kind != routeCalculationInternalFailure || !errors.Is(outcome.Err, context.Canceled) || router.lastRequest != nil || outcome.Session.ID != "" {
		t.Fatalf("canceled lookup: outcome=%#v request=%#v", outcome, router.lastRequest)
	}
}
