package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/orderedroute"
	"ride-home-router/internal/plandraft"
)

const (
	stubLegDistanceMeters = 1000
	stubLegDurationSecs   = 60
)

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
			results[i].Legs = append(results[i].Legs, orderedroute.Leg{DistanceMeters: stubLegDistanceMeters, DurationSecs: stubLegDurationSecs})
		}
	}
	return results
}

func (m *stubMeasurer) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.requests) }

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
	for _, want := range []string{`data-timings="measured"`, `data-route-duration-secs="` + strconv.Itoa(2*stubLegDurationSecs) + `"`, `data-stop-cumulative-duration-secs="` + strconv.Itoa(stubLegDurationSecs) + `"`, `google-maps-attribution`, `Current estimates for this itinerary`} {
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
	if len(decoded.Timings) != 1 || decoded.Timings[0].Status != "measured" || decoded.Timings[0].RouteDurationSecs != 2*stubLegDurationSecs {
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

func TestRouteTimingsPauseEveryCarWhilePlanIsOutOfBalance(t *testing.T) {
	handler, measurer, form := measuredCalculateFixture(t)
	second, err := handler.DB.Participants().Create(context.Background(), &models.Participant{Name: "Rider Two", Address: "9 Rider Rd", Lat: 40.11, Lng: -73.91})
	if err != nil {
		t.Fatal(err)
	}
	form.Add("participant_ids", int64ToString(second.ID))
	result := handler.Router.(*captureRouter).result
	result.Routes[0].EffectiveCapacity = 1
	result.Routes[0].Driver.VehicleCapacity = 1
	result.Routes[0].Stops = append(result.Routes[0].Stops, models.RouteStop{Participant: second})
	result.Summary.TotalParticipants = 2
	rr := postCalculate(handler, form, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if measurer.count() != 0 {
		t.Fatalf("out-of-balance plans must not be measured; requests = %d", measurer.count())
	}
	if !strings.Contains(rr.Body.String(), `data-timings="paused"`) || strings.Contains(rr.Body.String(), `data-timings="measured"`) {
		t.Fatalf("expected every car paused:\n%s", rr.Body.String())
	}
}

func TestRouteSessionTimingsMeasureOnlyTheRequestedCar(t *testing.T) {
	handler, measurer, form := measuredCalculateFixture(t)
	body := postCalculate(handler, form, true).Body.String()
	start := strings.Index(body, `data-session-id="`) + len(`data-session-id="`)
	sessionID := body[start : start+strings.Index(body[start:], `"`)]
	before := measurer.count()

	restore := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+sessionID, nil)
	restore.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	handler.HandleGetRouteSession(rr, restore)
	if rr.Code != http.StatusOK || measurer.count() != before || !strings.Contains(rr.Body.String(), `data-timings="stale"`) || !strings.Contains(rr.Body.String(), "Show timings") {
		t.Fatalf("restore: status=%d requests=%d body=%s", rr.Code, measurer.count()-before, rr.Body.String())
	}

	payload := strings.NewReader(`{"session_id":"` + sessionID + `","route_index":0}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/session/timings", payload)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	handler.HandleRouteTimings(rr, req)
	if rr.Code != http.StatusOK || measurer.count()-before != 2 {
		t.Fatalf("timings: status=%d requests=%d body=%s", rr.Code, measurer.count()-before, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `data-timings="measured"`) || strings.Contains(rr.Body.String(), estimatorSentinel) {
		t.Fatalf("timings body:\n%s", rr.Body.String())
	}

	bad := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/session/timings", strings.NewReader(`{"session_id":"`+sessionID+`","route_index":9}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.Header.Set("HX-Request", "true")
	rr = httptest.NewRecorder()
	handler.HandleRouteTimings(rr, bad)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range route index: status=%d", rr.Code)
	}
}

func TestMobileRouteTimingsRedirectsWholePageOnExpiredPlan(t *testing.T) {
	handler, _, _ := measuredCalculateFixture(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = "gone" })
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/m/routes/timings", strings.NewReader(url.Values{"session_id": {"gone"}, "route_index": {"0"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(mobileTestCookie(id))
	rr := httptest.NewRecorder()
	handler.HandleMobileRouteTimings(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("HX-Redirect") == "" || rr.Header().Get("Location") != "" {
		t.Fatalf("htmx expiry must navigate via HX-Redirect: status=%d headers=%v", rr.Code, rr.Header())
	}
}
