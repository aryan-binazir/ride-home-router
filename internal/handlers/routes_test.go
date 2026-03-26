package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

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
