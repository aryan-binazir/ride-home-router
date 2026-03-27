package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"ride-home-router/internal/sqlite"
)

type captureRouter struct {
	lastRequest *routing.RoutingRequest
	result      *models.RoutingResult
	err         error
}

type orgVehicleRepoWithError struct {
	database.OrganizationVehicleRepository
	err error
}

func (r orgVehicleRepoWithError) GetByIDs(_ context.Context, _ []int64) ([]models.OrganizationVehicle, error) {
	return nil, r.err
}

type testDataStore struct {
	database.DataStore
	orgVehicleRepo database.OrganizationVehicleRepository
}

func (s testDataStore) OrganizationVehicles() database.OrganizationVehicleRepository {
	return s.orgVehicleRepo
}

func (r *captureRouter) CalculateRoutes(_ context.Context, req *routing.RoutingRequest) (*models.RoutingResult, error) {
	r.lastRequest = req
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return &models.RoutingResult{}, nil
}

func TestHandleCalculateRoutes_UsesRequestedActivityLocation(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Rd",
		Lat:     40.10,
		Lng:     -73.90,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}

	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}

	fallbackLocation, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Fallback",
		Address: "3 Default Ave",
		Lat:     41.00,
		Lng:     -74.00,
	})
	if err != nil {
		t.Fatalf("create fallback location: %v", err)
	}

	requestedLocation, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Requested",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create requested location: %v", err)
	}

	if err := store.Settings().Update(context.Background(), &models.Settings{
		SelectedActivityLocationID: fallbackLocation.ID,
		UseMiles:                   false,
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	router := &captureRouter{
		result: &models.RoutingResult{
			Routes: []models.CalculatedRoute{},
			Summary: models.RoutingSummary{
				TotalDriversUsed:           1,
				TotalDropoffDistanceMeters: 1200,
			},
		},
	}
	handler.Router = router

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(requestedLocation.ID))
	form.Set("route_time", "18:30")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusOK, rr.Code, rr.Body.String())
	}
	if router.lastRequest == nil {
		t.Fatal("expected router to receive a request")
	}
	if router.lastRequest.InstituteCoords != requestedLocation.GetCoords() {
		t.Fatalf("expected requested location coords %+v, got %+v", requestedLocation.GetCoords(), router.lastRequest.InstituteCoords)
	}
	if router.lastRequest.Mode != models.RouteModeDropoff {
		t.Fatalf("expected default mode %q, got %q", models.RouteModeDropoff, router.lastRequest.Mode)
	}
}

func TestHandleCalculateRoutes_JSONPickupPropagatesTypedMode(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Rd",
		Lat:     40.10,
		Lng:     -73.90,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}

	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	router := &captureRouter{
		result: &models.RoutingResult{
			Routes: []models.CalculatedRoute{},
			Summary: models.RoutingSummary{
				TotalDriversUsed: 1,
			},
		},
	}
	handler.Router = router

	body := `{"participant_ids":[` + int64ToString(participant.ID) + `],"driver_ids":[` + int64ToString(driver.ID) + `],"activity_location_id":` + int64ToString(location.ID) + `,"route_time":"18:30","mode":"pickup"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusOK, rr.Code, rr.Body.String())
	}
	if router.lastRequest == nil {
		t.Fatal("expected router to receive a request")
	}
	if router.lastRequest.Mode != models.RouteModePickup {
		t.Fatalf("expected pickup mode, got %q", router.lastRequest.Mode)
	}

	var resp RouteCalculationResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	session := handler.RouteSession.Get(resp.SessionID)
	if session == nil {
		t.Fatal("expected route session to be created")
	}
	if session.Mode != models.RouteModePickup {
		t.Fatalf("expected session mode %q, got %q", models.RouteModePickup, session.Mode)
	}
}

func TestHandleCalculateRoutes_InvalidModeReturnsValidationError(t *testing.T) {
	handler, _ := newTestRouteHandler(t)
	router := &captureRouter{}
	handler.Router = router

	form := url.Values{}
	form.Add("participant_ids", "1")
	form.Add("driver_ids", "1")
	form.Set("activity_location_id", "1")
	form.Set("route_time", "18:30")
	form.Set("mode", "sideways")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	expected := `{"showToast":{"message":"Please choose a valid route mode.","type":"error"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expected {
		t.Fatalf("HX-Trigger = %q, want %q", got, expected)
	}
	if router.lastRequest != nil {
		t.Fatalf("expected router to not receive a request, got %#v", router.lastRequest)
	}
}

func TestHandleCalculateRoutesWithOrgVehicles_InvalidModeReturnsValidationError(t *testing.T) {
	handler, _ := newTestRouteHandler(t)
	router := &captureRouter{}
	handler.Router = router

	form := url.Values{}
	form.Add("participant_ids", "1")
	form.Add("driver_ids", "1")
	form.Set("activity_location_id", "1")
	form.Set("route_time", "18:30")
	form.Set("mode", "sideways")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate-with-org-vehicles", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutesWithOrgVehicles(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	expected := `{"showToast":{"message":"Please choose a valid route mode.","type":"error"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expected {
		t.Fatalf("HX-Trigger = %q, want %q", got, expected)
	}
	if router.lastRequest != nil {
		t.Fatalf("expected router to not receive a request, got %#v", router.lastRequest)
	}
}

func TestHandleCalculateRoutes_HTMXMissingActivityLocationReturnsEventPlanningMessage(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Old Default",
		Address: "5 Legacy Ave",
		Lat:     43.00,
		Lng:     -76.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	if err := store.Settings().Update(context.Background(), &models.Settings{
		SelectedActivityLocationID: location.ID,
		UseMiles:                   false,
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	form := url.Values{}
	form.Add("participant_ids", "1")
	form.Add("driver_ids", "1")
	form.Set("route_time", "18:30")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expectedTrigger := `{"showToast":{"message":"Please choose an activity location for this event.","type":"error"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expectedTrigger {
		t.Fatalf("HX-Trigger = %q, want %q", got, expectedTrigger)
	}
}

func TestHandleCalculateRoutes_AppliesAssignedVanCapacityBeforeRouting(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Rd",
		Lat:     40.10,
		Lng:     -73.90,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}

	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	van, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 8,
	})
	if err != nil {
		t.Fatalf("create van: %v", err)
	}

	router := &captureRouter{
		result: &models.RoutingResult{
			Routes: []models.CalculatedRoute{},
			Summary: models.RoutingSummary{
				TotalDriversUsed:           1,
				TotalDropoffDistanceMeters: 1200,
			},
		},
	}
	handler.Router = router

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("org_vehicle_"+int64ToString(driver.ID), int64ToString(van.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusOK, rr.Code, rr.Body.String())
	}
	if router.lastRequest == nil {
		t.Fatal("expected router to receive a request")
	}
	if got := router.lastRequest.Drivers[0].VehicleCapacity; got != van.Capacity {
		t.Fatalf("driver capacity = %d, want %d", got, van.Capacity)
	}
}

func TestHandleCalculateRoutes_HTMXRendersRouteTimeMetadataAndParentCopyButton(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Rd",
		Lat:     40.10,
		Lng:     -73.90,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}

	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	handler.Router = &captureRouter{
		result: &models.RoutingResult{
			Routes: []models.CalculatedRoute{
				{
					Driver:            driver,
					EffectiveCapacity: driver.VehicleCapacity,
					RouteDurationSecs: 900,
					Mode:              "dropoff",
					Stops: []models.RouteStop{
						{
							Order:                  0,
							Participant:            participant,
							DistanceFromPrevMeters: 1200,
							DurationFromPrevSecs:   600,
							CumulativeDurationSecs: 600,
						},
					},
				},
			},
			Summary: models.RoutingSummary{
				TotalParticipants: 1,
				TotalDriversUsed:  1,
			},
		},
	}

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("mode", "dropoff")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusOK, rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	for _, fragment := range []string{
		`data-route-time="18:30"`,
		`data-route-duration-secs="900"`,
		`data-stop-cumulative-duration-secs="600"`,
		`Copy for Parents`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected rendered route results to contain %q, body=%q", fragment, body)
		}
	}

	sessionIDMarker := `data-session-id="`
	start := strings.Index(body, sessionIDMarker)
	if start < 0 {
		t.Fatalf("expected rendered route results to include session id, body=%q", body)
	}
	start += len(sessionIDMarker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("expected rendered route results to include session id terminator, body=%q", body)
	}

	session := handler.RouteSession.Get(body[start : start+end])
	if session == nil {
		t.Fatal("expected route session to be created")
	}
	if session.RouteTime != "18:30" {
		t.Fatalf("session.RouteTime = %q, want %q", session.RouteTime, "18:30")
	}
}

func TestHandleCalculateRoutes_RejectsDuplicateVanAssignments(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	driverOne, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver one: %v", err)
	}
	driverTwo, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver Two",
		Address:         "3 Driver Rd",
		Lat:             40.30,
		Lng:             -73.70,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver two: %v", err)
	}
	van, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 8,
	})
	if err != nil {
		t.Fatalf("create van: %v", err)
	}

	form := url.Values{}
	form.Add("participant_ids", "1")
	form.Add("driver_ids", int64ToString(driverOne.ID))
	form.Add("driver_ids", int64ToString(driverTwo.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("org_vehicle_"+int64ToString(driverOne.ID), int64ToString(van.ID))
	form.Set("org_vehicle_"+int64ToString(driverTwo.ID), int64ToString(van.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expected := `{"showToast":{"message":"A van can only be assigned to one driver per event.","type":"error"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expected {
		t.Fatalf("HX-Trigger = %q, want %q", got, expected)
	}
}

func TestHandleCalculateRoutes_RequiresRouteTime(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	form := url.Values{}
	form.Add("participant_ids", "1")
	form.Add("driver_ids", "1")
	form.Set("activity_location_id", int64ToString(location.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expected := `{"showToast":{"message":"Please choose a route time.","type":"error"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expected {
		t.Fatalf("HX-Trigger = %q, want %q", got, expected)
	}
}

func TestHandleCalculateRoutes_RejectsAssignmentsForUnselectedDrivers(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	selectedDriver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Selected Driver",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create selected driver: %v", err)
	}
	unselectedDriver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Unselected Driver",
		Address:         "3 Driver Rd",
		Lat:             40.30,
		Lng:             -73.70,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create unselected driver: %v", err)
	}
	van, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 8,
	})
	if err != nil {
		t.Fatalf("create van: %v", err)
	}

	form := url.Values{}
	form.Add("participant_ids", "1")
	form.Add("driver_ids", int64ToString(selectedDriver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("org_vehicle_"+int64ToString(unselectedDriver.ID), int64ToString(van.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expected := `{"showToast":{"message":"Only selected drivers can be assigned vans.","type":"error"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expected {
		t.Fatalf("HX-Trigger = %q, want %q", got, expected)
	}
}

func TestHandleCalculateRoutes_PreservesVanAssignmentsInShortageFlow(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	participantOne, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Rd",
		Lat:     40.10,
		Lng:     -73.90,
	})
	if err != nil {
		t.Fatalf("create participant one: %v", err)
	}
	participantTwo, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant Two",
		Address: "2 Rider Rd",
		Lat:     40.11,
		Lng:     -73.91,
	})
	if err != nil {
		t.Fatalf("create participant two: %v", err)
	}

	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 1,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	van, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 2,
	})
	if err != nil {
		t.Fatalf("create van: %v", err)
	}

	handler.Router = &captureRouter{
		err: &routing.ErrRoutingFailed{
			Reason:            "still short",
			TotalParticipants: 3,
			TotalCapacity:     2,
			UnassignedCount:   1,
		},
	}

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participantOne.ID))
	form.Add("participant_ids", int64ToString(participantTwo.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("org_vehicle_"+int64ToString(driver.ID), int64ToString(van.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusOK, rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	expectedSelected := `option value="` + int64ToString(van.ID) + `" data-capacity="2" selected`
	if !strings.Contains(body, expectedSelected) {
		t.Fatalf("expected shortage flow to preserve selected van assignment, body=%q", body)
	}
	if !strings.Contains(body, "2 available seats") {
		t.Fatalf("expected shortage flow to render updated capacity, body=%q", body)
	}
}

func TestHandleCalculateRoutes_OrgVehicleRepositoryFailureReturnsInternalError(t *testing.T) {
	handler, store := newTestRouteHandler(t)

	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Rd",
		Lat:     40.10,
		Lng:     -73.90,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}

	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Rd",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "4 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	van, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 8,
	})
	if err != nil {
		t.Fatalf("create van: %v", err)
	}

	handler.DB = testDataStore{
		DataStore: store,
		orgVehicleRepo: orgVehicleRepoWithError{
			OrganizationVehicleRepository: store.OrganizationVehicles(),
			err:                           errors.New("database unavailable"),
		},
	}

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("org_vehicle_"+int64ToString(driver.ID), int64ToString(van.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusInternalServerError, rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("HX-Trigger"); got != "" {
		t.Fatalf("expected no validation toast for internal error, got %q", got)
	}
}

func TestCountUsedOrgVehicles_IgnoresUnusedOrDuplicateRoutes(t *testing.T) {
	routes := []models.CalculatedRoute{
		{
			OrgVehicleID: 1,
			Stops: []models.RouteStop{
				{Order: 0, Participant: &models.Participant{ID: 1, Name: "Alice"}},
			},
		},
		{
			OrgVehicleID: 2,
			Stops:        []models.RouteStop{},
		},
		{
			OrgVehicleID: 1,
			Stops: []models.RouteStop{
				{Order: 0, Participant: &models.Participant{ID: 2, Name: "Bob"}},
			},
		},
	}

	if got := countUsedOrgVehicles(routes); got != 1 {
		t.Fatalf("countUsedOrgVehicles = %d, want 1", got)
	}
}

func TestHandleGetRouteSession_ValidSession(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	routes := []models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, Name: "Driver1", VehicleCapacity: 4},
			Stops: []models.RouteStop{
				{Order: 0, Participant: &models.Participant{ID: 1, Name: "Alice"}},
			},
			TotalDistanceMeters: 1000,
			Mode:                "dropoff",
		},
	}
	drivers := []models.Driver{{ID: 1, Name: "Driver1", VehicleCapacity: 4}}
	activityLoc := &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 1.0, Lng: 2.0}

	session := handler.RouteSession.Create(routes, drivers, activityLoc, false, "18:30", "dropoff", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, session.ID) {
		t.Error("response should contain the session ID")
	}
	if !strings.Contains(body, "Driver1") {
		t.Error("response should contain driver name")
	}
	if strings.Contains(body, "Reset to Original") {
		t.Error("unedited session should not show Reset to Original button")
	}
}

func TestHandleGetRouteSession_MissingSession(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/session?session_id=nonexistent", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for missing session, got %d", w.Code)
	}
}

func TestHandleGetRouteSession_EmptyParam(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/session", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for empty session_id, got %d", w.Code)
	}
}

func TestHandleGetRouteSession_JSONResponse(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	routes := []models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, Name: "Driver1", VehicleCapacity: 4},
			Stops: []models.RouteStop{
				{Order: 0, Participant: &models.Participant{ID: 1, Name: "Alice"}},
			},
			TotalDropoffDistanceMeters: 1200,
			DistanceToDriverHomeMeters: 800,
			TotalDistanceMeters:        2000,
			DetourSecs:                 300,
			Mode:                       "pickup",
		},
		{
			Driver: &models.Driver{ID: 2, Name: "Driver2", VehicleCapacity: 4},
			Stops:  []models.RouteStop{},
			Mode:   "pickup",
		},
	}
	drivers := []models.Driver{
		{ID: 1, Name: "Driver1", VehicleCapacity: 4},
		{ID: 2, Name: "Driver2", VehicleCapacity: 4},
	}
	activityLoc := &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 1.0, Lng: 2.0}

	session := handler.RouteSession.Create(routes, drivers, activityLoc, true, "08:15", "pickup", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var resp RouteCalculationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.SessionID != session.ID {
		t.Fatalf("SessionID = %q, want %q", resp.SessionID, session.ID)
	}
	if resp.Mode != models.RouteModePickup {
		t.Fatalf("Mode = %q, want %q", resp.Mode, models.RouteModePickup)
	}
	if resp.Summary.TotalParticipants != 1 {
		t.Fatalf("TotalParticipants = %d, want 1", resp.Summary.TotalParticipants)
	}
	if resp.Summary.TotalDriversUsed != 1 {
		t.Fatalf("TotalDriversUsed = %d, want 1", resp.Summary.TotalDriversUsed)
	}
	if resp.Summary.TotalDistanceMeters != 2000 {
		t.Fatalf("TotalDistanceMeters = %f, want 2000", resp.Summary.TotalDistanceMeters)
	}
	if len(resp.Routes) != 2 {
		t.Fatalf("len(Routes) = %d, want 2", len(resp.Routes))
	}
}

func TestHandleGetRouteSession_DetectsEditing(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	routes := []models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, Name: "Driver1", VehicleCapacity: 4},
			Stops: []models.RouteStop{
				{Order: 0, Participant: &models.Participant{ID: 1, Name: "Alice"}},
				{Order: 1, Participant: &models.Participant{ID: 2, Name: "Bob"}},
			},
			Mode: "dropoff",
		},
		{
			Driver: &models.Driver{ID: 2, Name: "Driver2", VehicleCapacity: 4},
			Stops:  []models.RouteStop{},
			Mode:   "dropoff",
		},
	}
	drivers := []models.Driver{
		{ID: 1, Name: "Driver1", VehicleCapacity: 4},
		{ID: 2, Name: "Driver2", VehicleCapacity: 4},
	}
	activityLoc := &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 1.0, Lng: 2.0}

	session := handler.RouteSession.Create(routes, drivers, activityLoc, false, "18:30", "dropoff", nil)

	// Modify current routes to simulate editing (move a participant)
	handler.RouteSession.Update(session.ID, func(s *RouteSession) {
		moved := s.CurrentRoutes[0].Stops[1]
		s.CurrentRoutes[0].Stops = s.CurrentRoutes[0].Stops[:1]
		s.CurrentRoutes[1].Stops = append(s.CurrentRoutes[1].Stops, moved)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Reset to Original") {
		t.Error("edited session should show Reset to Original button")
	}
}

func TestHandleGetRouteSession_ExpiredSessionReturnsNoContent(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	session := handler.RouteSession.Create(nil, nil, &models.ActivityLocation{ID: 1, Name: "HQ"}, false, "18:30", "dropoff", nil)
	session.LastAccessedAt = time.Now().Add(-3 * time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for expired session, got %d", w.Code)
	}
	if got := handler.RouteSession.Get(session.ID); got != nil {
		t.Fatal("expected expired session to be removed from store")
	}
}

func TestHandleGetRouteSession_PickupSessionRendersPickupLabelsAndUnusedDrivers(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	routes := []models.CalculatedRoute{
		{
			Driver:                     &models.Driver{ID: 1, Name: "Driver1", Address: "1 Main St", VehicleCapacity: 4, Lat: 10.0, Lng: 10.0},
			EffectiveCapacity:          4,
			Stops:                      []models.RouteStop{{Order: 0, Participant: &models.Participant{ID: 1, Name: "Alice", Address: "2 Oak Ave", Lat: 11.0, Lng: 11.0}, DistanceFromPrevMeters: 1500}},
			TotalDropoffDistanceMeters: 1500,
			DistanceToDriverHomeMeters: 700,
			TotalDistanceMeters:        2200,
			RouteDurationSecs:          900,
			Mode:                       "pickup",
		},
	}
	drivers := []models.Driver{
		{ID: 1, Name: "Driver1", Address: "1 Main St", VehicleCapacity: 4},
		{ID: 2, Name: "Driver2", Address: "3 Pine Rd", VehicleCapacity: 5},
	}
	activityLoc := &models.ActivityLocation{ID: 1, Name: "HQ", Address: "4 Event Way", Lat: 1.0, Lng: 2.0}

	session := handler.RouteSession.Create(routes, drivers, activityLoc, false, "08:15", "pickup", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, fragment := range []string{
		`data-route-mode="pickup"`,
		"Pickup Distance:",
		"To Activity:",
		"from Driver1's home",
		"Unused Drivers (1)",
		"Driver2",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected pickup route results to contain %q, body=%q", fragment, body)
		}
	}
}

func newTestRouteHandler(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "routes-test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	handler := &Handler{
		DB:           store,
		Templates:    loadEmbeddedTemplates(t),
		RouteSession: NewRouteSessionStore(),
	}

	t.Cleanup(func() {
		handler.RouteSession.Close()
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	return handler, store
}
