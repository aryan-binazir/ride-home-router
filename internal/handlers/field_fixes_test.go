package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/aryan-binazir/ride-home-router/internal/database/workflowtest"
	"github.com/aryan-binazir/ride-home-router/internal/models"
	"github.com/aryan-binazir/ride-home-router/internal/routesession"
)

func TestCalculateOverLimitSelectionNamesTheLimit(t *testing.T) {
	handler, _ := newTestRouteHandler(t)
	form := url.Values{"activity_location_id": {"1"}, "route_time": {"18:30"}, "mode": {"dropoff"}, "driver_ids": {"1"}}
	for i := range maxSelectionSize + 1 {
		form.Add("participant_ids", strconv.Itoa(i+1))
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	handler.HandleCalculateRoutes(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Choose no more than 500 participants.") {
		t.Fatalf("over-limit response = %d %q", response.Code, response.Body.String())
	}
}

func TestEmptyCarIsLabelledEmptyNotMeasured(t *testing.T) {
	store := routesession.NewPersistentStore(routeEditDistanceCalculator{}, workflowtest.New())

	rider := models.Participant{ID: 10, Name: "Rider", Address: "1 Main", Lat: 1, Lng: 1}
	session := mustCreateRouteSession(t, store, routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{Driver: &models.Driver{ID: 1, Name: "Full", VehicleCapacity: 3}, EffectiveCapacity: 3, Stops: []models.RouteStop{{Participant: &rider}}},
			{Driver: &models.Driver{ID: 2, Name: "Spare", VehicleCapacity: 3}, EffectiveCapacity: 3},
		},
		ActivityLocation: &models.ActivityLocation{Name: "Center", Lat: 0, Lng: 0}, RouteTime: "18:30", Mode: models.RouteModeDropoff,
	})
	measurer := &stubMeasurer{}
	handler := &Handler{Renderer: loadEmbeddedTemplates(t), RouteSession: store, Measurer: measurer}
	for _, indexes := range [][]int{allRouteIndexes(session), nil} {
		timings := handler.routeTimings(context.Background(), session, indexes)
		if timings[1].Status != timingEmpty || timings[1].Route != nil || timings[1].Message != messageTimingsEmpty {
			t.Fatalf("empty car timing for indexes %v = %+v", indexes, timings[1])
		}
	}
	timings := handler.routeTimings(context.Background(), session, allRouteIndexes(session))
	if _, complete := handler.itinerarySummary(session.Summary, timings); !complete {
		t.Fatal("an empty car must not block the plan totals")
	}
	if measurer.count() != 2 {
		t.Fatalf("requests = %d, want 2 for the one occupied car", measurer.count())
	}
	view := handler.buildTimedRouteResultsView(session, timings)
	var body strings.Builder
	if err := handler.Renderer.Render(&body, "route_results", view); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), messageTimingsEmpty) || strings.Contains(body.String(), "0 ft") {
		t.Fatalf("empty car card: %s", body.String())
	}
}
