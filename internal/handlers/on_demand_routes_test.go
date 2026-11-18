package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/database/workflowtest"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
	"time"
)

func largeRouteFixture(t *testing.T) (*Handler, routesession.Snapshot) {
	t.Helper()
	store := routesession.NewPersistentStore(routeEditDistanceCalculator{}, workflowtest.New())

	drivers := make([]models.Driver, 500)
	routes := make([]models.CalculatedRoute, 500)
	for i := range drivers {
		drivers[i] = models.Driver{ID: int64(i + 1), Name: fmt.Sprintf("Driver %03d", i+1), VehicleCapacity: 4, Address: "2 Test St"}
		routes[i] = models.CalculatedRoute{Driver: &drivers[i], EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: &models.Participant{ID: int64(i + 1), Name: fmt.Sprintf("Rider %03d", i+1), Address: "3 Test St"}}}}
	}
	snapshot := mustCreateRouteSession(t, store, routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{Name: "Venue", Address: "1 Test St"}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	return &Handler{Renderer: loadEmbeddedTemplates(t), RouteSession: store, Measurer: &stubMeasurer{}}, snapshot
}

func TestMovingOneRiderReturnsOnlyChangedRouteCards(t *testing.T) {
	h, snapshot := largeRouteFixture(t)
	r := newRouteEditJSONRequest("/api/v1/routes/edit/move-participant", []byte(`{"session_id":"`+snapshot.ID+`","participant_id":1,"from_route_index":0,"to_route_index":499,"insert_at_position":-1}`))
	r.Header.Set("HX-Request", "true")
	r.Header.Set("X-Route-Fragment", "true")
	r.Header.Set("X-Route-Balance", "false")
	w := httptest.NewRecorder()
	h.HandleMoveParticipant(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	if len(body) > 30_000 || strings.Count(body, `data-route-index=`) != 2 || strings.Contains(body, "Driver 250") || strings.Contains(body, `id="save-event-panel"`) {
		t.Fatalf("one move returned whole/unrelated routes: bytes=%d cards=%d", len(body), strings.Count(body, `data-route-index=`))
	}
	if !strings.Contains(body, "Reset changes") || !strings.Contains(body, `data-route-patch="`+snapshot.ID+`"`) {
		t.Fatal("missing first-edit actions/session patch identity")
	}
}

func TestUnusedDriverChoicesAreFetchedOnlyWhenRequested(t *testing.T) {
	h, snapshot := newRouteEditHandler(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/routes/session?session_id="+snapshot.ID, nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleGetRouteSession(w, r)
	if strings.Contains(w.Body.String(), "Three") {
		t.Fatal("unused driver details were eagerly rendered")
	}
	r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/routes/editor?session_id="+snapshot.ID+"&action=add", nil)
	r.Header.Set("HX-Request", "true")
	w = httptest.NewRecorder()
	h.HandleRouteEditor(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Three") {
		t.Fatalf("editor status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteEditorReturnsOnlyABoundedPageOfDestinations(t *testing.T) {
	h, snapshot := largeRouteFixture(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/routes/editor?session_id="+snapshot.ID+"&action=move&from_route_index=0&participant_id=1", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleRouteEditor(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if got := strings.Count(body, `name="destination"`); got != 25 {
		t.Fatalf("rendered %d destination controls; want 25", got)
	}
	if len(body) > 35_000 || strings.Contains(body, "Driver 500") || strings.Contains(body, "Driver 001") {
		t.Fatalf("editor contains unrelated destinations (%d bytes)", len(body))
	}
	if !strings.Contains(body, "Next") || !strings.Contains(body, "Rider 001") {
		t.Fatal("missing paging or source identity")
	}
	if h.Measurer.(*stubMeasurer).count() != 0 {
		t.Fatal("opening an editor measured routes")
	}
}

func TestLargeRoutesLoadActionsWithoutEagerDestinationCatalogs(t *testing.T) {
	h, snapshot := largeRouteFixture(t)
	w := httptest.NewRecorder()
	started := time.Now()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/routes/session?session_id="+snapshot.ID, nil)
	r.Header.Set("HX-Request", "true")
	h.HandleGetRouteSession(w, r)
	body := w.Body.String()
	t.Logf("500 routes: bytes=%d options=%d server_elapsed=%s", len(body), strings.Count(body, "<option"), time.Since(started))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if got := strings.Count(body, "<option"); got != 0 {
		t.Fatalf("initial routes render %d eager destination options; want none", got)
	}
	if len(body) > 4_000_000 {
		t.Fatalf("route HTML is %d bytes; budget 4 MB for 500 visible cards", len(body))
	}
	if !strings.Contains(body, "Rider 500") || !strings.Contains(body, "Driver 500") {
		t.Fatal("visible itinerary was lost")
	}
	if !strings.Contains(body, "routes/editor?") {
		t.Fatal("missing on-demand editor links")
	}
}
