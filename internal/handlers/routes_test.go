package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"ride-home-router/internal/sqlite"
)

type captureRouter struct {
	lastRequest *routing.RoutingRequest
	result      *models.RoutingResult
}

func (r *captureRouter) CalculateRoutes(_ context.Context, req *routing.RoutingRequest) (*models.RoutingResult, error) {
	r.lastRequest = req
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

func newTestRouteHandler(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "routes-test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	handler := &Handler{
		DB:           store,
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
