package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

func TestDeletedRosterPaginationKeepsOlderRecordsRestorable(t *testing.T) {
	h, db := newTestPageHandler(t)
	var firstDriver, firstParticipant int64
	for i := range 60 {
		name := fmt.Sprintf("Deleted %03d", i)
		driver, err := db.Drivers().Create(t.Context(), &models.Driver{Name: name, Address: "1 Main St", VehicleCapacity: 4})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Drivers().Delete(t.Context(), driver.ID); err != nil {
			t.Fatal(err)
		}
		person, err := db.Participants().Create(t.Context(), &models.Participant{Name: name, Address: "1 Main St"})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Participants().Delete(t.Context(), person.ID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstDriver = driver.ID
			firstParticipant = person.ID
		}
	}
	cases := []struct {
		kind          string
		id            int64
		list, restore http.HandlerFunc
	}{{"drivers", firstDriver, h.HandleListDeletedDrivers, h.HandleRestoreDriver}, {"participants", firstParticipant, h.HandleListDeletedParticipants, h.HandleRestoreParticipant}}
	for _, tc := range cases {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/"+tc.kind+"/deleted", nil)
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		tc.list(w, r)
		if !strings.Contains(w.Body.String(), "Next") || !strings.Contains(w.Body.String(), `hx-target="#`+tc.kind+`-deleted"`) {
			t.Fatalf("%s deleted page lacks navigation", tc.kind)
		}
		r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/"+tc.kind+"/deleted?offset=50", nil)
		r.Header.Set("HX-Request", "true")
		w = httptest.NewRecorder()
		tc.list(w, r)
		if !strings.Contains(w.Body.String(), "Deleted 000") || strings.Count(w.Body.String(), `<tr id="deleted-`) != 10 {
			t.Fatalf("%s older deleted records inaccessible", tc.kind)
		}
		r = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/"+tc.kind+"/restore", strings.NewReader(fmt.Sprintf("id=%d", tc.id)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w = httptest.NewRecorder()
		tc.restore(w, r)
		if w.Code != 200 {
			t.Fatalf("restore %s: %d", tc.kind, w.Code)
		}
	}
	if _, err := db.Drivers().GetByID(t.Context(), firstDriver); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Participants().GetByID(t.Context(), firstParticipant); err != nil {
		t.Fatal(err)
	}
}

func TestVehicleEditorReportsDeletedRecordsAsUserErrors(t *testing.T) {
	h, db := newTestPageHandler(t)
	driver, err := db.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Main St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		driver int64
		body   string
		status int
	}{{999999, "", http.StatusNotFound}, {driver.ID, "choose=1&vehicle_id=999999", http.StatusBadRequest}} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, fmt.Sprintf("/api/v1/planner/vehicle-editor?driver_id=%d", tc.driver), strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		h.HandleVehicleEditor(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d want=%d", w.Code, tc.status)
		}
	}
}

func TestStandaloneRouteEditorSearchAndPagingKeepFormNavigation(t *testing.T) {
	h, snapshot := largeRouteFixture(t)
	for _, query := range []string{"", "&search=Driver+02", "&offset=25"} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/routes/editor?session_id="+snapshot.ID+"&action=move&from_route_index=0&participant_id=1"+query, nil)
		w := httptest.NewRecorder()
		h.HandleRouteEditor(w, r)
		body := w.Body.String()
		if w.Code != http.StatusOK || strings.Contains(body, `hx-get="/api/v1/routes/editor`) || strings.Contains(body, `onsubmit="submitRouteEditor(event)"`) || !strings.Contains(body, `action="/api/v1/routes/choose"`) {
			t.Fatalf("navigation fallback: status=%d body=%s", w.Code, body)
		}
		if !strings.Contains(body, `href="/" aria-label="Close editor"`) {
			t.Fatal("standalone editor is missing its return link")
		}
	}

	bad := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/routes/editor?session_id="+snapshot.ID+"&action=invalid", nil)
	w := httptest.NewRecorder()
	h.HandleRouteEditor(w, bad)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), messageInvalidRequestBody) {
		t.Fatalf("invalid action: status=%d body=%s", w.Code, w.Body.String())
	}
}

func oneRouteWithUnusedDriver(t *testing.T) (*Handler, routesession.Snapshot) {
	t.Helper()
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	drivers := []models.Driver{{ID: 1, Name: "First driver", VehicleCapacity: 4}, {ID: 2, Name: "Unused driver", VehicleCapacity: 4}}
	snapshot := mustCreateRouteSession(t, store, routesession.CreateInput{Routes: []models.CalculatedRoute{{Driver: &drivers[0], EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 1, Name: "Rider"}}}}}, SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{Name: "Venue"}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	return &Handler{Renderer: loadEmbeddedTemplates(t), RouteSession: store, Measurer: &stubMeasurer{}}, snapshot
}

func TestAddSecondDriverRefreshesOriginalCardActions(t *testing.T) {
	h, snapshot := oneRouteWithUnusedDriver(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/routes/edit/add-driver", strings.NewReader(`{"session_id":"`+snapshot.ID+`","driver_id":2}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("HX-Request", "true")
	r.Header.Set("X-Route-Fragment", "true")
	r.Header.Set("X-Route-Balance", "false")
	h.HandleAddDriver(w, r)
	if !strings.Contains(w.Body.String(), `data-route-index="0"`) {
		t.Fatal("original card lacks newly available edit actions")
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "routes/editor?") {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	if h.Measurer.(*stubMeasurer).count() != 0 {
		t.Fatal("adding an empty car should not buy extra measurements")
	}
}

func TestResetRoutesRemovesAddedCard(t *testing.T) {
	h, snapshot := oneRouteWithUnusedDriver(t)
	add := newRouteEditJSONRequest("/api/v1/routes/edit/add-driver", []byte(`{"session_id":"`+snapshot.ID+`","driver_id":2}`))
	add.Header.Set("HX-Request", "true")
	added := httptest.NewRecorder()
	h.HandleAddDriver(added, add)
	if added.Code != http.StatusOK || !strings.Contains(added.Body.String(), `data-route-index="1"`) {
		t.Fatalf("add: %d %s", added.Code, added.Body.String())
	}
	reset := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/routes/edit/reset?session_id="+snapshot.ID, nil)
	reset.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleResetRoutes(w, reset)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `data-route-index="1"`) || !strings.Contains(w.Body.String(), `data-route-index="0"`) {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
}
