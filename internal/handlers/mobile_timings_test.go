package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

func twoCarMobileFixture(t *testing.T) (*Handler, *stubMeasurer, *http.Cookie, routesession.Snapshot) {
	t.Helper()
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	drafts := plandraft.NewStore()
	t.Cleanup(drafts.Close)
	first := models.Driver{ID: 1, Name: "Dana Whitfield", Address: "4 Driver Rd", Lat: 40.3, Lng: -73.3, VehicleCapacity: 3}
	second := models.Driver{ID: 2, Name: "Marcus Hill", Address: "5 Driver Rd", Lat: 40.4, Lng: -73.4, VehicleCapacity: 3}
	riderOne := models.Participant{ID: 10, Name: "Maya Chen", Address: "1 Rider Rd", Lat: 40.1, Lng: -73.1}
	riderTwo := models.Participant{ID: 11, Name: "Leo Park", Address: "2 Rider Rd", Lat: 40.2, Lng: -73.2}
	session := mustCreateRouteSession(t, store, routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{Driver: &first, EffectiveCapacity: 3, Stops: []models.RouteStop{{Participant: &riderOne}}},
			{Driver: &second, EffectiveCapacity: 3, Stops: []models.RouteStop{{Participant: &riderTwo}}},
		},
		SelectedDrivers:  []models.Driver{first, second},
		ActivityLocation: &models.ActivityLocation{Name: "Center", Address: "9 Event Ave", Lat: 41, Lng: -74},
		RouteTime:        "18:30", Mode: models.RouteModeDropoff,
	})
	id := drafts.NewID()
	drafts.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })
	measurer := &stubMeasurer{}
	handler := &Handler{Renderer: loadEmbeddedTemplates(t), PlanDraft: drafts, RouteSession: store, Measurer: measurer}
	return handler, measurer, mobileTestCookie(id), session
}

func getMobileRoutes(t *testing.T, handler *Handler, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/routes", nil)
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	response := httptest.NewRecorder()
	handler.HandleMobileRoutes(response, request)
	return response
}

func TestMobileRoutesRenderUnmeasuredCarsUnderDefaultEngine(t *testing.T) {
	handler, measurer, cookie, _ := twoCarMobileFixture(t)
	response := getMobileRoutes(t, handler, cookie)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, body)
	}
	for _, want := range []string{"Maya Chen", "Leo Park", "Save event"} {
		if !strings.Contains(body, want) {
			t.Fatalf("routes page missing %q: %s", want, body)
		}
	}
	if strings.Count(body, "Show timings") != 2 {
		t.Fatalf("expected a Show timings action per car: %s", body)
	}
	if measurer.count() != 0 {
		t.Fatalf("plain routes view measured %d requests", measurer.count())
	}
}

func TestMobileMoveMeasuresOnlyChangedCarsOnRedirect(t *testing.T) {
	handler, measurer, cookie, session := twoCarMobileFixture(t)
	move := postMobileForm(t, cookie, "/m/routes/move", url.Values{
		"session_id": {session.ID}, "participant_id": {"10"}, "from_route_index": {"0"}, "to_route_index": {"1"},
	}, handler.HandleMobileMove)
	assertMobileRedirect(t, move, "/m/routes")
	var pending *http.Cookie
	for _, c := range move.Result().Cookies() {
		if c.Name == mobileTimingsCookie {
			pending = c
		}
	}
	if pending == nil {
		t.Fatalf("move did not queue timings for the redirect: %#v", move.Result().Cookies())
	}

	response := getMobileRoutes(t, handler, cookie, pending)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, body)
	}
	if measurer.count() != 2 {
		t.Fatalf("measured %d requests, want 2 for the changed car", measurer.count())
	}
	if strings.Contains(body, "Show timings") {
		t.Fatalf("changed car should already show timings: %s", body)
	}
	cleared := false
	for _, c := range response.Result().Cookies() {
		if c.Name == mobileTimingsCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("routes page did not clear the queued timings")
	}
	again := getMobileRoutes(t, handler, cookie)
	if again.Code != http.StatusOK || measurer.count() != 2 {
		t.Fatalf("refresh status=%d measured=%d", again.Code, measurer.count())
	}
}

func TestMobileQueuedTimingsIgnoreOtherSessions(t *testing.T) {
	handler, measurer, cookie, _ := twoCarMobileFixture(t)
	stale := &http.Cookie{Name: mobileTimingsCookie, Value: "deadbeef.0-1", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	response := getMobileRoutes(t, handler, cookie, stale)
	if response.Code != http.StatusOK || measurer.count() != 0 {
		t.Fatalf("status=%d measured=%d", response.Code, measurer.count())
	}
}
