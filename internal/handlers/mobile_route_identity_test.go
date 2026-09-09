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

func TestMobileRouteActionsRejectStaleOrMissingSession(t *testing.T) {
	for _, action := range []string{"save", "move", "swap", "reset", "add-driver"} {
		for _, submitted := range []string{"stale", "missing"} {
			t.Run(action+"/"+submitted, func(t *testing.T) {
				h, _ := newTestManagementHandler(t)
				h.PlanDraft = plandraft.NewStore()
				t.Cleanup(h.PlanDraft.Close)
				old := h.RouteSession.Create(routesession.CreateInput{})
				current := h.RouteSession.Create(routesession.CreateInput{Mode: models.RouteModeDropoff, ActivityLocation: &models.ActivityLocation{Name: "Activity", Address: "Activity Road"}, Routes: []models.CalculatedRoute{
					{Driver: &models.Driver{ID: 1, Name: "New driver", Address: "Driver Road", VehicleCapacity: 4}, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 1, Name: "Rider", Address: "Rider Road"}}}, EffectiveCapacity: 4},
					{Driver: &models.Driver{ID: 2, Name: "Second driver", Address: "Other Road", VehicleCapacity: 4}, EffectiveCapacity: 4},
				}})
				id := h.PlanDraft.NewID()
				h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = current.ID })
				getRoutes := func() string {
					r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/routes", nil)
					r.AddCookie(mobileTestCookie(id))
					w := httptest.NewRecorder()
					h.HandleMobileRoutes(w, r)
					if w.Code != 200 {
						t.Fatalf("get routes: %d", w.Code)
					}
					return w.Body.String()
				}
				before := getRoutes()
				values := url.Values{"event_date": {"2026-09-08"}, "participant_id": {"1"}, "from_route_index": {"0"}, "to_route_index": {"1"}, "route_index_1": {"0"}, "route_index_2": {"1"}, "driver_id": {"2"}}
				if submitted == "stale" {
					values.Set("session_id", old.ID)
				}
				fn := map[string]http.HandlerFunc{"save": h.HandleMobileSave, "move": h.HandleMobileMove, "swap": h.HandleMobileSwap, "reset": h.HandleMobileReset, "add-driver": h.HandleMobileAddDriver}[action]
				response := postMobileForm(t, mobileTestCookie(id), "/m/routes/"+action, values, fn)
				if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "error=") {
					t.Fatalf("old page accepted: %d %s", response.Code, response.Header().Get("Location"))
				}
				if after := getRoutes(); after != before {
					t.Fatal("stale action changed replacement routes")
				}
				if !strings.Contains(before, `name="session_id" value="`+current.ID+`"`) {
					t.Fatal("rendered forms do not identify the displayed session")
				}
			})
		}
	}
}
