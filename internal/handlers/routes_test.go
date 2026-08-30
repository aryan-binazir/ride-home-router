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
	"strings"
	"testing"
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
	if router.lastRequest.Mode != models.RouteModePickup {
		t.Fatalf("expected pickup mode, got %q", router.lastRequest.Mode)
	}

	var resp RouteCalculationResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	session, ok := handler.RouteSession.Snapshot(resp.SessionID)
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

func TestRouteCalculationEndpoints_PreserveValidationOrder(t *testing.T) {
	form := url.Values{}
	form.Add("driver_ids", "1")
	form.Set("activity_location_id", "1")
	form.Set("route_time", "not-a-time")

	tests := []struct {
		name        string
		path        string
		handle      func(*Handler, http.ResponseWriter, *http.Request)
		wantMessage string
	}{
		{
			name:        "initial validates selections before route options",
			path:        "/api/v1/routes/calculate",
			handle:      (*Handler).HandleCalculateRoutes,
			wantMessage: messageSelectAtLeastOneParticipant,
		},
		{
			name:        "retry validates route options before selections",
			path:        "/api/v1/routes/calculate-with-org-vehicles",
			handle:      (*Handler).HandleCalculateRoutesWithOrgVehicles,
			wantMessage: messageChooseValidRouteTime,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &Handler{}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, test.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			rr := httptest.NewRecorder()

			test.handle(handler, rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			wantTrigger := `{"showToast":{"message":"` + test.wantMessage + `","type":"error"}}`
			if got := rr.Header().Get("HX-Trigger"); got != wantTrigger {
				t.Fatalf("HX-Trigger = %q, want %q", got, wantTrigger)
			}
		})
	}
}

func TestRouteCalculationEndpoints_PreserveAssignmentValidationOrder(t *testing.T) {
	form := url.Values{}
	form.Set("activity_location_id", "1")
	form.Set("route_time", "18:30")
	form.Set("mode", "dropoff")
	form.Set("org_vehicle_1", "1")

	tests := []struct {
		name        string
		path        string
		handle      func(*Handler, http.ResponseWriter, *http.Request)
		wantMessage string
	}{
		{
			name:        "initial validates selections before assignments",
			path:        "/api/v1/routes/calculate",
			handle:      (*Handler).HandleCalculateRoutes,
			wantMessage: messageSelectAtLeastOneParticipant,
		},
		{
			name:        "retry validates assignments before selections",
			path:        "/api/v1/routes/calculate-with-org-vehicles",
			handle:      (*Handler).HandleCalculateRoutesWithOrgVehicles,
			wantMessage: unselectedDriverVanAssignmentMessage,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &Handler{}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, test.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			rr := httptest.NewRecorder()

			test.handle(handler, rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			wantTrigger := `{"showToast":{"message":"` + test.wantMessage + `","type":"error"}}`
			if got := rr.Header().Get("HX-Trigger"); got != wantTrigger {
				t.Fatalf("HX-Trigger = %q, want %q", got, wantTrigger)
			}
		})
	}
}

func TestRouteCalculationEndpoints_InvalidActivityLocationMessage(t *testing.T) {
	form := url.Values{}
	form.Add("participant_ids", "1")
	form.Add("driver_ids", "1")
	form.Set("activity_location_id", "not-an-id")
	form.Set("route_time", "18:30")

	for _, endpoint := range []struct {
		path   string
		handle func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{path: "/api/v1/routes/calculate", handle: (*Handler).HandleCalculateRoutes},
		{path: "/api/v1/routes/calculate-with-org-vehicles", handle: (*Handler).HandleCalculateRoutesWithOrgVehicles},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			handler := &Handler{}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, endpoint.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			rr := httptest.NewRecorder()

			endpoint.handle(handler, rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			wantTrigger := `{"showToast":{"message":"` + messageChooseValidActivityLocation + `","type":"error"}}`
			if got := rr.Header().Get("HX-Trigger"); got != wantTrigger {
				t.Fatalf("HX-Trigger = %q, want %q", got, wantTrigger)
			}
		})
	}
}

func TestRouteCalculationEndpoints_PreserveMalformedFormResponses(t *testing.T) {
	// Compatibility pin: the initial endpoint's JSON response is existing behavior,
	// not the desired HTMX error experience.
	tests := []struct {
		name            string
		path            string
		handle          func(*Handler, http.ResponseWriter, *http.Request)
		wantContentType string
		wantTrigger     string
	}{
		{
			name:            "initial returns JSON without a toast",
			path:            "/api/v1/routes/calculate",
			handle:          (*Handler).HandleCalculateRoutes,
			wantContentType: "application/json",
		},
		{
			name:            "retry returns HTML with a toast",
			path:            "/api/v1/routes/calculate-with-org-vehicles",
			handle:          (*Handler).HandleCalculateRoutesWithOrgVehicles,
			wantContentType: "text/html",
			wantTrigger:     `{"showToast":{"message":"Invalid form data","type":"error"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &Handler{}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, test.path, strings.NewReader("participant_ids=%zz"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			rr := httptest.NewRecorder()

			test.handle(handler, rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); !strings.Contains(got, test.wantContentType) {
				t.Fatalf("Content-Type = %q, want %q", got, test.wantContentType)
			}
			if got := rr.Header().Get("HX-Trigger"); got != test.wantTrigger {
				t.Fatalf("HX-Trigger = %q, want %q", got, test.wantTrigger)
			}
		})
	}
}

func TestRouteCalculationEndpoints_EquivalentFormInputReachesRouter(t *testing.T) {
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
	van, err := store.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Blue Van", Capacity: 8})
	if err != nil {
		t.Fatalf("create organization vehicle: %v", err)
	}

	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("mode", "pickup")
	form.Set("org_vehicle_"+int64ToString(driver.ID), int64ToString(van.ID))

	requests := make([]*routing.RoutingRequest, 0, 2)
	for _, endpoint := range []struct {
		path   string
		handle func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{path: "/api/v1/routes/calculate", handle: (*Handler).HandleCalculateRoutes},
		{path: "/api/v1/routes/calculate-with-org-vehicles", handle: (*Handler).HandleCalculateRoutesWithOrgVehicles},
	} {
		router := &captureRouter{result: &models.RoutingResult{}}
		handler.Router = router
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, endpoint.path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()

		endpoint.handle(handler, rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d body=%q", endpoint.path, rr.Code, http.StatusOK, rr.Body.String())
		}
		if router.lastRequest == nil {
			t.Fatalf("%s did not reach router", endpoint.path)
		}
		requests = append(requests, router.lastRequest)
	}

	initial, retry := requests[0], requests[1]
	if initial.InstituteCoords != retry.InstituteCoords || initial.Mode != retry.Mode {
		t.Fatalf("router request metadata differs:\ninitial = %#v\nretry = %#v", initial, retry)
	}
	if len(initial.Participants) != len(retry.Participants) || len(initial.Drivers) != len(retry.Drivers) {
		t.Fatalf("router request selection counts differ:\ninitial = %#v\nretry = %#v", initial, retry)
	}
	for i := range initial.Participants {
		if initial.Participants[i].ID != retry.Participants[i].ID {
			t.Fatalf("participant %d differs: initial=%d retry=%d", i, initial.Participants[i].ID, retry.Participants[i].ID)
		}
	}
	for i := range initial.Drivers {
		if initial.Drivers[i].ID != retry.Drivers[i].ID || initial.Drivers[i].VehicleCapacity != retry.Drivers[i].VehicleCapacity {
			t.Fatalf("driver %d differs: initial=%#v retry=%#v", i, initial.Drivers[i], retry.Drivers[i])
		}
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

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Google Maps API key is not configured") {
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
	if response.Error.Code != "ROUTING_FAILED" || response.Error.Message != "not enough capacity" {
		t.Fatalf("error = %#v, want ROUTING_FAILED capacity error", response.Error)
	}
	if got, want := response.Error.Details, (RoutingErrorDetails{UnassignedCount: 2, TotalCapacity: 1, TotalParticipants: 3}); got != want {
		t.Fatalf("routing details = %#v, want %#v", got, want)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate-with-org-vehicles", strings.NewReader(form.Encode()))
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
				"Not Enough Available Capacity",
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
			if got, want := rr.Header().Get("HX-Trigger"), `{"showToast":{"message":"Routes calculated! 1 driver assigned.","type":"success"}}`; got != want {
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
			session, ok := handler.RouteSession.Snapshot(body[start : start+end])
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
		{name: "unknown participant", participantID: participant.ID + 1000, driverID: driver.ID, activityLocationID: location.ID, wantMessage: "Some participants not found"},
		{name: "unknown driver", participantID: participant.ID, driverID: driver.ID + 1000, activityLocationID: location.ID, wantMessage: "Some drivers not found"},
		{name: "archived participant", participantID: archivedParticipant.ID, driverID: driver.ID, activityLocationID: location.ID, wantMessage: "Some participants not found"},
		{name: "archived driver", participantID: participant.ID, driverID: archivedDriver.ID, activityLocationID: location.ID, wantMessage: "Some drivers not found"},
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

func TestHandleCalculateRoutes_HTMXStaleSelectedEntitiesReturnJSONWithoutToast(t *testing.T) {
	// Compatibility pin: the initial endpoint's JSON response is existing behavior,
	// not the desired HTMX error experience.
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
	tests := []struct {
		name          string
		participantID int64
		driverID      int64
		wantMessage   string
	}{
		{name: "participant", participantID: participant.ID + 1000, driverID: driver.ID, wantMessage: "Some participants not found"},
		{name: "driver", participantID: participant.ID, driverID: driver.ID + 1000, wantMessage: "Some drivers not found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler.Router = &captureRouter{}
			form := url.Values{}
			form.Add("participant_ids", int64ToString(test.participantID))
			form.Add("driver_ids", int64ToString(test.driverID))
			form.Set("activity_location_id", int64ToString(location.ID))
			form.Set("route_time", "18:30")
			form.Set("mode", "dropoff")
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			rr := httptest.NewRecorder()

			handler.HandleCalculateRoutes(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			if got := rr.Header().Get("HX-Trigger"); got != "" {
				t.Fatalf("HX-Trigger = %q, want no toast", got)
			}
			if !strings.Contains(rr.Body.String(), `"message":"`+test.wantMessage+`"`) {
				t.Fatalf("body = %q, want %q", rr.Body.String(), test.wantMessage)
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

	session, ok := handler.RouteSession.Snapshot(body[start : start+end])
	if !ok {
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expected := `{"showToast":{"message":"a van can only be assigned to one driver per event","type":"error"}}`
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

	expected := `{"showToast":{"message":"please choose a route time","type":"error"}}`
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCalculateRoutes(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}

	expected := `{"showToast":{"message":"only selected drivers can be assigned vans","type":"error"}}`
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
	expectedTrigger := `{"showToast":{"message":"Not enough capacity - need 1 more seat","type":"warning"}}`
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

	session := handler.RouteSession.Create(routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: activityLoc, RouteTime: "18:30", Mode: "dropoff"})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id=nonexistent", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for missing session, got %d", w.Code)
	}
}

func TestHandleGetRouteSession_EmptyParam(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session", nil)
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

	session := handler.RouteSession.Create(routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: activityLoc, UseMiles: true, RouteTime: "08:15", Mode: "pickup"})

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

	session := handler.RouteSession.Create(routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: activityLoc, RouteTime: "18:30", Mode: "dropoff"})

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
	if !strings.Contains(body, "Reset to Original") {
		t.Error("edited session should show Reset to Original button")
	}
}

func TestHandleGetRouteSession_DeletedSessionReturnsNoContent(t *testing.T) {
	handler, _ := newTestRouteHandler(t)

	session := handler.RouteSession.Create(routesession.CreateInput{ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ"}, RouteTime: "18:30", Mode: "dropoff"})
	handler.RouteSession.Delete(session.ID)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	handler.HandleGetRouteSession(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for expired session, got %d", w.Code)
	}
	if _, ok := handler.RouteSession.Snapshot(session.ID); ok {
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

	session := handler.RouteSession.Create(routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: activityLoc, RouteTime: "08:15", Mode: "pickup"})

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
		"Unused Driver (1)",
		"<dt>Passenger</dt>",
		`class="label">Participant`,
		`class="label">Driver`,
		"Driver2",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected pickup route results to contain %q, body=%q", fragment, body)
		}
	}
}

func newTestRouteHandler(t *testing.T) (*Handler, *postgres.Store) {
	t.Helper()

	store := postgrestest.Open(t)

	handler := &Handler{
		DB:           store,
		Renderer:     loadEmbeddedTemplates(t),
		RouteSession: routesession.NewStore(routeEditDistanceCalculator{}),
	}

	t.Cleanup(handler.RouteSession.Close)

	return handler, store
}
