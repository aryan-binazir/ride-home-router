package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

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

func TestMobileRouteEditorValidationRedirectsNormalNavigation(t *testing.T) {
	h, snapshot, cookie := oneRouteWithUnusedDriver(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/m/routes/editor?session_id="+snapshot.ID+"&action=invalid", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.HandleRouteEditor(w, r)
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/m/routes?error=") {
		t.Fatalf("invalid editor should return to routes: status=%d location=%s", w.Code, w.Header().Get("Location"))
	}
}

func oneRouteWithUnusedDriver(t *testing.T) (*Handler, routesession.Snapshot, *http.Cookie) {
	t.Helper()
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	drafts := plandraft.NewStore()
	t.Cleanup(drafts.Close)
	drivers := []models.Driver{{ID: 1, Name: "First driver", VehicleCapacity: 4}, {ID: 2, Name: "Unused driver", VehicleCapacity: 4}}
	snapshot := store.Create(routesession.CreateInput{Routes: []models.CalculatedRoute{{Driver: &drivers[0], EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 1, Name: "Rider"}}}}}, SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{Name: "Venue"}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	id := drafts.NewID()
	drafts.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = snapshot.ID })
	return &Handler{Renderer: loadEmbeddedTemplates(t), RouteSession: store, PlanDraft: drafts, Measurer: &stubMeasurer{}}, snapshot, mobileTestCookie(id)
}

func TestAddSecondDriverRefreshesOriginalCardActions(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		t.Run(map[bool]string{false: "desktop", true: "mobile"}[mobile], func(t *testing.T) {
			h, snapshot, cookie := oneRouteWithUnusedDriver(t)
			w := httptest.NewRecorder()
			if mobile {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/m/routes/add-driver", strings.NewReader("session_id="+snapshot.ID+"&driver_id=2&rendered_balance=false"))
				r.AddCookie(cookie)
				r.Header.Set("HX-Request", "true")
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				h.HandleMobileAddDriver(w, r)
				if !strings.Contains(w.Body.String(), `id="mobile-route-0"`) {
					t.Fatal("original card lacks newly available edit actions")
				}
			} else {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/routes/edit/add-driver", strings.NewReader(`{"session_id":"`+snapshot.ID+`","driver_id":2}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("HX-Request", "true")
				r.Header.Set("X-Route-Fragment", "true")
				r.Header.Set("X-Route-Balance", "false")
				h.HandleAddDriver(w, r)
				if !strings.Contains(w.Body.String(), `data-route-index="0"`) {
					t.Fatal("original card lacks newly available edit actions")
				}
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), "routes/editor?") {
				t.Fatalf("response: %d %s", w.Code, w.Body.String())
			}
			if h.Measurer.(*stubMeasurer).count() != 0 {
				t.Fatal("adding an empty car should not buy extra measurements")
			}
		})
	}
}

func TestMobileResetReplacesScreenToRemoveAddedCards(t *testing.T) {
	h, snapshot, cookie := oneRouteWithUnusedDriver(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/m/routes/add-driver", strings.NewReader("session_id="+snapshot.ID+"&driver_id=2"))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.HandleMobileAddDriver(httptest.NewRecorder(), r)
	r = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/m/routes/reset", strings.NewReader("session_id="+snapshot.ID+"&rendered_balance=false"))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleMobileReset(w, r)
	if w.Code != 200 || w.Header().Get("HX-Retarget") != ".mobile-routes" || strings.Contains(w.Body.String(), `id="mobile-route-1"`) {
		t.Fatalf("reset failed to remove stale cards: status=%d target=%s", w.Code, w.Header().Get("HX-Retarget"))
	}
}
