package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/orderedroute"
	"strings"
	"sync"
	"testing"
)

// stubMeasurer answers every hop with 60 s / 1000 m, or fails every request.
type stubMeasurer struct {
	mu       sync.Mutex
	requests [][]models.Coordinates
	err      error
}

func (m *stubMeasurer) Measure(_ context.Context, requests []orderedroute.Request) []orderedroute.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	results := make([]orderedroute.Result, len(requests))
	for i, request := range requests {
		m.requests = append(m.requests, request.Points)
		results[i].ID = request.ID
		if m.err != nil {
			results[i].Err = m.err
			continue
		}
		for range len(request.Points) - 1 {
			results[i].Legs = append(results[i].Legs, orderedroute.Leg{DistanceMeters: 1000, DurationSecs: 60})
		}
	}
	return results
}

func (m *stubMeasurer) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.requests) }

// A digit run that random hex session ids are astronomically unlikely to contain.
const estimatorSentinel = "424242"

func measuredCalculateFixture(t *testing.T) (*Handler, *stubMeasurer, url.Values) {
	t.Helper()
	handler, store := newTestRouteHandler(t)
	participant, err := store.Participants().Create(context.Background(), &models.Participant{Name: "Rider One", Address: "1 Rider Rd", Lat: 40.10, Lng: -73.90})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := store.Drivers().Create(context.Background(), &models.Driver{Name: "Driver One", Address: "2 Driver Rd", Lat: 40.20, Lng: -73.80, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{Name: "Gym", Address: "4 Event Ave", Lat: 42.00, Lng: -75.00})
	if err != nil {
		t.Fatal(err)
	}
	// The planner's result carries estimator numbers that must never reach a user.
	handler.Router = &captureRouter{result: &models.RoutingResult{
		Routes: []models.CalculatedRoute{{
			Driver: driver, EffectiveCapacity: 4, Mode: "dropoff", RouteDurationSecs: 424242.75, TotalDistanceMeters: 424242.75, DetourSecs: 424242.75,
			Stops: []models.RouteStop{{Participant: participant, DistanceFromPrevMeters: 424242.75, DurationFromPrevSecs: 424242.75, CumulativeDurationSecs: 424242.75}},
		}},
		Summary: models.RoutingSummary{TotalParticipants: 1, TotalDriversUsed: 1, TotalDistanceMeters: 424242.75, MaxDetourSecs: 424242.75},
	}}
	measurer := &stubMeasurer{}
	handler.Measurer = measurer
	form := url.Values{}
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Set("activity_location_id", int64ToString(location.ID))
	form.Set("route_time", "18:30")
	form.Set("mode", "dropoff")
	return handler, measurer, form
}

func postCalculate(handler *Handler, form url.Values, htmx bool) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rr := httptest.NewRecorder()
	handler.HandleCalculateRoutes(rr, req)
	return rr
}

func TestHandleCalculateRoutes_RendersMeasuredTimingsNeverEstimates(t *testing.T) {
	handler, measurer, form := measuredCalculateFixture(t)
	rr := postCalculate(handler, form, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, estimatorSentinel) {
		t.Fatalf("estimator numbers leaked into the page:\n%s", body)
	}
	// Institute → rider → driver home = two 60 s legs; baseline is one leg.
	for _, want := range []string{`data-timings="measured"`, `data-route-duration-secs="120"`, `data-stop-cumulative-duration-secs="60"`, `google-maps-attribution`, `Current estimates for this itinerary`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	if measurer.count() != 2 {
		t.Fatalf("provider requests = %d, want route + baseline", measurer.count())
	}
}

func TestHandleCalculateRoutes_JSONHidesEstimatesAndReportsTimings(t *testing.T) {
	handler, _, form := measuredCalculateFixture(t)
	rr := postCalculate(handler, form, false)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), estimatorSentinel) {
		t.Fatalf("estimator numbers leaked into JSON: %s", rr.Body.String())
	}
	var decoded struct {
		Routes  []models.CalculatedRoute `json:"routes"`
		Timings []struct {
			Status            string  `json:"status"`
			RouteDurationSecs float64 `json:"route_duration_secs"`
		} `json:"timings"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Routes) != 1 || decoded.Routes[0].RouteDurationSecs != 0 || len(decoded.Routes[0].Stops) != 1 {
		t.Fatalf("routes = %+v, want itinerary with zeroed metrics", decoded.Routes)
	}
	if len(decoded.Timings) != 1 || decoded.Timings[0].Status != "measured" || decoded.Timings[0].RouteDurationSecs != 120 {
		t.Fatalf("timings = %+v", decoded.Timings)
	}
}

func TestHandleCalculateRoutes_ExhaustedUsageKeepsThePlanWithoutTimings(t *testing.T) {
	handler, measurer, form := measuredCalculateFixture(t)
	measurer.err = database.ErrUsageExhausted
	rr := postCalculate(handler, form, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, estimatorSentinel) || strings.Contains(body, `data-timings="measured"`) {
		t.Fatalf("exhausted month must not show numbers:\n%s", body)
	}
	for _, want := range []string{`data-timings="exhausted"`, "Timings unavailable this month", "Rider One", "Driver One"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}
