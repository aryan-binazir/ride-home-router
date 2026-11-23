package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
	"strconv"
	"strings"
	"testing"
)

type captureRouter struct {
	lastRequest *routing.RoutingRequest
	lastContext context.Context
	result      *models.RoutingResult
	err         error
	afterSolve  func()
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

func TestRouteCalculationEndpoints_RejectInvalidFormSelectionsBeforeRouting(t *testing.T) {
	tooMany := make([]string, maxSelectionSize+1)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(i + 1)
	}

	tests := []struct {
		name           string
		participantIDs []string
		driverIDs      []string
		message        string
	}{
		{name: "mixed valid and malformed participant IDs", participantIDs: []string{"12", "garbage"}, driverIDs: []string{"1"}},
		{name: "malformed driver ID", participantIDs: []string{"1"}, driverIDs: []string{"garbage"}},
		{name: "duplicate participant ID", participantIDs: []string{"12", "12"}, driverIDs: []string{"1"}},
		{name: "duplicate driver ID", participantIDs: []string{"1"}, driverIDs: []string{"9", "9"}},
		{name: "zero participant ID", participantIDs: []string{"0"}, driverIDs: []string{"1"}},
		{name: "negative driver ID", participantIDs: []string{"1"}, driverIDs: []string{"-1"}},
		{name: "too many participant IDs", participantIDs: tooMany, driverIDs: []string{"1"}, message: routeSelectionLimitMessage("participants")},
		{name: "too many driver IDs", participantIDs: []string{"1"}, driverIDs: tooMany, message: routeSelectionLimitMessage("drivers")},
	}
	endpoints := []struct {
		name   string
		path   string
		handle func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{name: "calculate", path: "/api/v1/routes/calculate", handle: (*Handler).HandleCalculateRoutes},
		{name: "calculate with organization vehicles", path: "/api/v1/routes/calculate-with-org-vehicles", handle: (*Handler).HandleCalculateRoutesWithOrgVehicles},
	}

	for _, endpoint := range endpoints {
		for _, test := range tests {
			t.Run(endpoint.name+"/"+test.name, func(t *testing.T) {
				router := &captureRouter{}
				handler := &Handler{Router: router}
				form := url.Values{
					"participant_ids":      test.participantIDs,
					"driver_ids":           test.driverIDs,
					"activity_location_id": {"1"},
					"route_time":           {"18:30"},
					"mode":                 {"dropoff"},
				}
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, endpoint.path, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("HX-Request", "true")
				rr := httptest.NewRecorder()

				endpoint.handle(handler, rr, req)

				if rr.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
				}
				want := test.message
				if want == "" {
					want = messageInvalidFormData
				}
				if got := rr.Header().Get("HX-Trigger"); !strings.Contains(got, want) {
					t.Fatalf("HX-Trigger = %q, want %q", got, want)
				}
				if router.lastRequest != nil {
					t.Fatalf("router received request %#v", router.lastRequest)
				}
			})
		}
	}
}

func (s testDataStore) OrganizationVehicles() database.OrganizationVehicleRepository {
	return s.orgVehicleRepo
}

func (r *captureRouter) CalculateRoutes(ctx context.Context, req *routing.RoutingRequest) (*models.RoutingResult, error) {
	r.lastContext = ctx
	r.lastRequest = req
	if r.afterSolve != nil {
		r.afterSolve()
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return &models.RoutingResult{}, nil
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusOK, rr.Code, rr.Body.String())
	}
	if router.lastRequest == nil {
		t.Fatal("expected router to receive a request")
	}
	if _, ok := router.lastContext.Deadline(); !ok {
		t.Fatal("expected route calculation context to have a deadline")
	}
	if router.lastRequest.Mode != models.RouteModePickup {
		t.Fatalf("expected pickup mode, got %q", router.lastRequest.Mode)
	}

	var resp RouteCalculationResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	session, ok := mustLoadRouteSession(t, handler.RouteSession, resp.SessionID)
	if !ok {
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
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

func TestHandleCalculateRoutes_PreCanceledContextReturnsTimeoutError(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	participant, err := store.Participants().Create(context.Background(), &models.Participant{Name: "Rider", Address: "1 Rider Rd", Lat: 1, Lng: 1})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	driver, err := store.Drivers().Create(context.Background(), &models.Driver{Name: "Driver", Address: "2 Driver Rd", Lat: 2, Lng: 2, VehicleCapacity: 1})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{Name: "Gym", Address: "3 Event Rd", Lat: 3, Lng: 3})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	body, err := json.Marshal(CalculateRoutesRequest{
		ParticipantIDs:     []int64{participant.ID},
		DriverIDs:          []int64{driver.ID},
		ActivityLocationID: location.ID,
		RouteTime:          "18:30",
		Mode:               "dropoff",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	var response ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "CALCULATION_TIMED_OUT" || response.Error.Message != messageCalculationTimedOut {
		t.Fatalf("error = %#v, want timeout error", response.Error)
	}
}

func TestHandleCalculateRoutes_DistanceProviderFailureReturnsVisibleError(t *testing.T) {
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
		Name:    "Event",
		Address: "3 Event Ave",
		Lat:     42.00,
		Lng:     -75.00,
	})
	if err != nil {
		t.Fatalf("create location: %v", err)
	}

	handler.Router = &captureRouter{err: fmt.Errorf("%w: missing key", distance.ErrProviderNotConfigured)}

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), messageRouteCalculationNotConfigured) {
		t.Fatalf("body = %q, want provider setup message", rr.Body.String())
	}
}

func TestHandleCalculateRoutes_JSONCapacityShortageReturnsRoutingFailure(t *testing.T) {
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
	handler.Router = &captureRouter{err: &routing.ErrRoutingFailed{
		Reason:            "not enough capacity",
		UnassignedCount:   2,
		TotalCapacity:     1,
		TotalParticipants: 3,
	}}

	body := fmt.Sprintf(`{"participant_ids":[%d],"driver_ids":[%d],"activity_location_id":%d,"route_time":"18:30","mode":"dropoff"}`, participant.ID, driver.ID, location.ID)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
	var response struct {
		Error struct {
			Code    string              `json:"code"`
			Message string              `json:"message"`
			Details RoutingErrorDetails `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "ROUTING_FAILED" || response.Error.Message != messageHouseholdsDoNotFit {
		t.Fatalf("error = %#v, want ROUTING_FAILED capacity error", response.Error)
	}
	if got, want := response.Error.Details, (RoutingErrorDetails{UnassignedCount: 2, TotalCapacity: 1, TotalParticipants: 3}); got != want {
		t.Fatalf("routing details = %#v, want %#v", got, want)
	}
}

func TestHandleCalculateRoutesWithOrgVehicles_SuccessRendersHTMLAndCreatesSession(t *testing.T) {
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
	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("mode", "dropoff")
	form.Set("org_vehicle_"+int64ToString(driver.ID), int64ToString(van.ID))
	for _, htmx := range []bool{true, false} {
		t.Run(fmt.Sprintf("htmx=%t", htmx), func(t *testing.T) {
			handler.Router = &captureRouter{result: &models.RoutingResult{
				Routes: []models.CalculatedRoute{{
					Driver: driver,
					Stops:  []models.RouteStop{{Participant: participant}},
					Mode:   models.RouteModeDropoff,
				}},
				Summary: models.RoutingSummary{TotalParticipants: 1, TotalDriversUsed: 1},
			}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate-with-org-vehicles", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if htmx {
				req.Header.Set("HX-Request", "true")
			}
			rr := httptest.NewRecorder()

			handler.HandleCalculateRoutesWithOrgVehicles(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
			}
			if got, want := rr.Header().Get("HX-Trigger"), `{"showToast":{"message":"`+messageRoutesCalculated(1)+`","type":"success"}}`; got != want {
				t.Fatalf("HX-Trigger = %q, want %q", got, want)
			}
			body := rr.Body.String()
			if !strings.Contains(body, `class="routes-container"`) {
				t.Fatalf("expected route-result HTML, body=%q", body)
			}
			sessionIDMarker := `data-session-id="`
			start := strings.Index(body, sessionIDMarker)
			if start < 0 {
				t.Fatalf("expected rendered session ID, body=%q", body)
			}
			start += len(sessionIDMarker)
			end := strings.Index(body[start:], `"`)
			if end < 0 {
				t.Fatalf("expected rendered session ID terminator, body=%q", body)
			}
			session, ok, err := handler.RouteSession.Load(ctx, body[start:start+end])
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("expected route session to be restorable")
			}
			if got := session.Routes[0].OrgVehicleID; got != van.ID {
				t.Fatalf("session organization vehicle ID = %d, want %d", got, van.ID)
			}
		})
	}
}

func TestHandleCalculateRoutesWithOrgVehicles_RejectsStaleSelectedEntitiesBeforeRouting(t *testing.T) {
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
	archivedParticipant, err := store.Participants().Create(ctx, &models.Participant{Name: "Archived Rider", Address: "4 Rider Rd", Lat: 40.3, Lng: -73.7})
	if err != nil {
		t.Fatalf("create archived participant: %v", err)
	}
	archivedDriver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Archived Driver", Address: "5 Driver Rd", Lat: 40.4, Lng: -73.6, VehicleCapacity: 1})
	if err != nil {
		t.Fatalf("create archived driver: %v", err)
	}
	archivedLocation, err := store.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Archived Gym", Address: "6 Event Ave", Lat: 43, Lng: -76})
	if err != nil {
		t.Fatalf("create archived activity location: %v", err)
	}
	if err := store.Participants().Delete(ctx, archivedParticipant.ID); err != nil {
		t.Fatalf("archive participant: %v", err)
	}
	if err := store.Drivers().Delete(ctx, archivedDriver.ID); err != nil {
		t.Fatalf("archive driver: %v", err)
	}
	if err := store.ActivityLocations().Delete(ctx, archivedLocation.ID); err != nil {
		t.Fatalf("archive activity location: %v", err)
	}

	tests := []struct {
		name               string
		participantID      int64
		driverID           int64
		activityLocationID int64
		wantMessage        string
	}{
		{name: "unknown participant", participantID: participant.ID + 1000, driverID: driver.ID, activityLocationID: location.ID, wantMessage: messageStaleRiders},
		{name: "unknown driver", participantID: participant.ID, driverID: driver.ID + 1000, activityLocationID: location.ID, wantMessage: messageStaleDrivers},
		{name: "archived participant", participantID: archivedParticipant.ID, driverID: driver.ID, activityLocationID: location.ID, wantMessage: messageStaleRiders},
		{name: "archived driver", participantID: participant.ID, driverID: archivedDriver.ID, activityLocationID: location.ID, wantMessage: messageStaleDrivers},
		{name: "archived activity location", participantID: participant.ID, driverID: driver.ID, activityLocationID: archivedLocation.ID, wantMessage: messageSelectedActivityLocationNotFoundChooseAnother},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := &captureRouter{}
			handler.Router = router
			form := url.Values{}
			form.Add("participant_ids", int64ToString(test.participantID))
			form.Add("driver_ids", int64ToString(test.driverID))
			form.Set("activity_location_id", int64ToString(test.activityLocationID))
			form.Set("route_time", "18:30")
			form.Set("mode", "dropoff")
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate-with-org-vehicles", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			rr := httptest.NewRecorder()

			handler.HandleCalculateRoutesWithOrgVehicles(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			wantTrigger := `{"showToast":{"message":"` + test.wantMessage + `","type":"error"}}`
			if got := rr.Header().Get("HX-Trigger"); got != wantTrigger {
				t.Fatalf("HX-Trigger = %q, want %q", got, wantTrigger)
			}
			if router.lastRequest != nil {
				t.Fatalf("router received request %#v for stale %s", router.lastRequest, test.name)
			}
		})
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expected := `{"showToast":{"message":"` + duplicateVanAssignmentMessage + `","type":"error"}}`
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expected := `{"showToast":{"message":"` + messageChooseRouteTime + `","type":"error"}}`
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusOK, rr.Code, rr.Body.String())
	}
	expectedTrigger := `{"showToast":{"message":"` + messageNotEnoughCapacity(1) + `","type":"warning"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expectedTrigger {
		t.Fatalf("HX-Trigger = %q, want %q", got, expectedTrigger)
	}

	body := rr.Body.String()
	expectedSelected := `option value="` + int64ToString(van.ID) + `" data-capacity="2" selected`
	if !strings.Contains(body, expectedSelected) {
		t.Fatalf("expected shortage flow to preserve selected van assignment, body=%q", body)
	}
	if !strings.Contains(body, "2 available seats") {
		t.Fatalf("expected shortage flow to render updated capacity, body=%q", body)
	}
	for _, fragment := range []string{
		`name="participant_ids" value="` + int64ToString(participantOne.ID) + `"`,
		`name="participant_ids" value="` + int64ToString(participantTwo.ID) + `"`,
		`name="driver_ids" value="` + int64ToString(driver.ID) + `"`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected shortage flow to preserve %q, body=%q", fragment, body)
		}
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d body=%q", http.StatusInternalServerError, rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("HX-Trigger"); !strings.Contains(got, messageGenericInternalError) {
		t.Fatalf("expected internal error toast, got %q", got)
	}
}

func TestHandleGetRouteSession_DeletedSessionReturnsNoContent(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	session := mustCreateRouteSession(t, handler.RouteSession, routesession.CreateInput{ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ"}, RouteTime: "18:30", Mode: "dropoff"})
	if err := handler.RouteSession.DeleteContext(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for deleted session, got %d", w.Code)
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

	session := mustCreateRouteSession(t, handler.RouteSession, routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: activityLoc, UseMiles: true, RouteTime: "08:15", Mode: "pickup"})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
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

	session := mustCreateRouteSession(t, handler.RouteSession, routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: activityLoc, RouteTime: "18:30", Mode: "dropoff"})

	if _, err := handler.RouteSession.ApplyMoves(context.Background(), session.ID, []routesession.Move{{ParticipantID: 2, FromRouteIndex: 0, ToRouteIndex: 1, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{RequireClaimedSource: true}); err != nil {
		t.Fatalf("move participant: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Reset changes") {
		t.Error("edited session should show Reset changes button")
	}
}

func newTestRouteHandler(t *testing.T) (*Handler, *postgres.Store) {
	t.Helper()

	store := postgrestest.Open(t)

	handler := &Handler{
		DB:           store,
		Renderer:     loadEmbeddedTemplates(t),
		RouteSession: routesession.NewPersistentStore(routeEditDistanceCalculator{}, store.Workflows()),
	}

	return handler, store
}

func (routeEditDistanceCalculator) PrewarmPairs(context.Context, []distance.DistancePair) error {
	return nil
}

func TestHouseholdPackingFailureExplainsCapacityWithoutNegativeShortage(t *testing.T) {
	for _, count := range []int{9, 12} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			handler, db := newTestRouteHandler(t)
			handler.Router = routing.NewBalancedRouter(routeEditDistanceCalculator{})
			ctx := context.Background()
			location, err := db.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Club", Address: "Club", Lat: 35, Lng: -79})
			if err != nil {
				t.Fatal(err)
			}
			form := url.Values{"activity_location_id": {strconv.FormatInt(location.ID, 10)}, "route_time": {"18:30"}, "mode": {"dropoff"}}
			for i := range count {
				person, createErr := db.Participants().Create(ctx, &models.Participant{Name: fmt.Sprintf("Rider %d", i), Address: fmt.Sprintf("Household %d", i/3), Lat: 35 + float64(i/3+1)/100, Lng: -79})
				if createErr != nil {
					t.Fatal(createErr)
				}
				form.Add("participant_ids", strconv.FormatInt(person.ID, 10))
			}
			for i := range 2 {
				driver, createErr := db.Drivers().Create(ctx, &models.Driver{Name: fmt.Sprintf("Driver %d", i), Address: fmt.Sprintf("Driver home %d", i), Lat: 35.1 + float64(i)/100, Lng: -79, VehicleCapacity: 5})
				if createErr != nil {
					t.Fatal(createErr)
				}
				form.Add("driver_ids", strconv.FormatInt(driver.ID, 10))
			}
			if _, err := db.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Van", Capacity: 8}); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			handler.HandleCalculateRoutes(w, r)
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d: %s", w.Code, body)
			}
			if count == 9 {
				if !strings.Contains(body, "Households do not fit the selected vehicles") || strings.Contains(body, "Not enough seats") || strings.Contains(body, "Need -1") || strings.Contains(body, "OK - ") {
					t.Fatalf("household failure must explain placement rather than a seat shortage: %s", body)
				}
				if strings.Contains(w.Header().Get("HX-Trigger"), "-1") {
					t.Fatalf("negative toast: %s", w.Header().Get("HX-Trigger"))
				}
			} else if !strings.Contains(body, "Not enough seats") || !strings.Contains(body, "Need 2 more seats") {
				t.Fatalf("genuine shortage guidance missing: %s", body)
			}
		})
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

	session := mustCreateRouteSession(t, handler.RouteSession, routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: activityLoc, RouteTime: "08:15", Mode: "pickup"})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, fragment := range []string{
		`data-route-mode="pickup"`,
		"<dt>Pickup</dt>",
		"<dt>To Activity</dt>",
		"from Driver1's home",
		"Add unused driver (1)",
		`class="seat-pill" title="Passengers / available seats">1/4 seats</span>`,
		`class="label">Participant`,
		`class="label">Driver`,
		`&amp;action=add`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected pickup route results to contain %q, body=%q", fragment, body)
		}
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
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
		`Copy for parents`,
		`data-session-action="preview" onclick="previewRoute(this)"`,
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

	session, ok := mustLoadRouteSession(t, handler.RouteSession, body[start:start+end])
	if !ok {
		t.Fatal("expected route session to be created")
	}
	if session.RouteTime != "18:30" {
		t.Fatalf("session.RouteTime = %q, want %q", session.RouteTime, "18:30")
	}
}

func TestHandleCalculateRoutesWithOrgVehicles_ShortageRendersHTMLWithoutWarningToast(t *testing.T) {
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
	handler.Router = &captureRouter{err: &routing.ErrRoutingFailed{
		Reason:            "still short",
		UnassignedCount:   1,
		TotalCapacity:     2,
		TotalParticipants: 3,
	}}

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("mode", "dropoff")
	form.Set("org_vehicle_"+int64ToString(driver.ID), int64ToString(van.ID))
	for _, htmx := range []bool{true, false} {
		t.Run(fmt.Sprintf("htmx=%t", htmx), func(t *testing.T) {
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate-with-org-vehicles", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if htmx {
				req.Header.Set("HX-Request", "true")
			}
			rr := httptest.NewRecorder()

			handler.HandleCalculateRoutesWithOrgVehicles(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
			}
			if got := rr.Header().Get("HX-Trigger"); got != "" {
				t.Fatalf("HX-Trigger = %q, want no warning toast", got)
			}
			body := rr.Body.String()
			for _, fragment := range []string{
				`class="capacity-shortage-container"`,
				"Not enough seats",
				`name="participant_ids" value="` + int64ToString(participant.ID) + `"`,
				`name="driver_ids" value="` + int64ToString(driver.ID) + `"`,
			} {
				if !strings.Contains(body, fragment) {
					t.Fatalf("expected capacity shortage HTML to contain %q, body=%q", fragment, body)
				}
			}
		})
	}
}
