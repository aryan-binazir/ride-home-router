package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"strconv"
	"strings"
	"testing"
	"time"
)

func routeEditorFormRequest(form url.Values) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/choose", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestStandaloneRouteEditorActions(t *testing.T) {
	for _, action := range []string{"move", "swap", "add"} {
		t.Run(action, func(t *testing.T) {
			h, session := newRouteEditHandler(t)
			measurer := &stubMeasurer{}
			h.Measurer = measurer
			form := url.Values{"session_id": {session.ID}, "action": {action}, "from_route_index": {"0"}, "participant_id": {"10"}, "destination": {"1"}}
			if action == "add" {
				form.Set("destination", "3")
				form.Set("from_route_index", "ignored")
			}
			w := httptest.NewRecorder()
			h.HandleRouteEditorAction(w, routeEditorFormRequest(form))
			if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `data-session-id="`+session.ID+`"`) {
				t.Fatalf("standalone response: status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
			}
			state := httptest.NewRecorder()
			h.HandleGetRouteSession(state, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil))
			response := decodeRouteResponse(t, state)
			wantMeasurements := 1
			switch action {
			case "move":
				if len(response.Routes[0].Stops) != 0 || len(response.Routes[1].Stops) != 1 || response.Routes[1].Stops[0].Participant.ID != 10 {
					t.Fatal("rider did not move")
				}
			case "swap":
				if response.Routes[0].Driver.ID != 2 || response.Routes[1].Driver.ID != 1 {
					t.Fatal("drivers did not swap")
				}
			case "add":
				wantMeasurements = 0
				if len(response.Routes) != 3 || response.Routes[2].Driver.ID != 3 {
					t.Fatal("unused driver was not added")
				}
			}
			if got := measurer.count(); got != wantMeasurements {
				t.Fatalf("measurements=%d, want %d", got, wantMeasurements)
			}
		})
	}
}

func TestStandaloneRouteEditorValidationOrderAndResponse(t *testing.T) {
	for _, tc := range []struct {
		name, action, destination, from, participant, want string
		forced                                             bool
		status                                             int
	}{
		{"malformed destination", "move", "bad", "0", "0", messageInvalidRequestBody, false, 400},
		{"destination overflow", "add", "9223372036854775808", "", "", messageInvalidRequestBody, false, 400},
		{"invalid source", "move", "1", "bad", "0", messageInvalidRequestBody, false, 400},
		{"invalid rider", "move", "1", "0", "bad", messageInvalidRequestBody, false, 400},
		{"unsupported reset", "reset", "0", "0", "10", messageInvalidRequestBody, false, 400},
		{"unknown action", "unknown", "0", "0", "10", messageInvalidRequestBody, false, 400},
		{"zero rider before missing session", "move", "1", "0", "0", messageInvalidParticipantID, true, 400},
		{"negative rider reaches session lookup", "move", "1", "0", "-1", messageSessionNotFound, true, 404},
		{"missing swap session", "swap", "1", "0", "", messageSessionNotFound, true, 404},
		{"missing add session ignores source", "add", "3", "bad", "", messageSessionNotFound, true, 404},
	} {
		for _, hx := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/htmx=%t", tc.name, hx), func(t *testing.T) {
				h, _ := newRouteEditHandler(t)
				r := routeEditorFormRequest(url.Values{"session_id": {"missing"}, "action": {tc.action}, "destination": {tc.destination}, "from_route_index": {tc.from}, "participant_id": {tc.participant}})
				if hx {
					r.Header.Set("HX-Request", "true")
				}
				w := httptest.NewRecorder()
				h.HandleRouteEditorAction(w, r)
				html := hx || tc.forced
				if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) || strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") != html {
					t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
				}
				if (w.Header().Get("HX-Trigger") != "") != html {
					t.Fatalf("toast headers=%v", w.Header())
				}
				if tc.status == 404 && w.Header().Get("HX-Reswap") != "none" {
					t.Fatal("missing no-swap header")
				}
			})
		}
	}
}

func TestStandaloneRouteEditorRequiresClaimedSource(t *testing.T) {
	h, session := newRouteEditHandler(t)
	r := routeEditorFormRequest(url.Values{"session_id": {session.ID}, "action": {"move"}, "destination": {"1"}, "from_route_index": {"1"}, "participant_id": {"10"}})
	w := httptest.NewRecorder()
	h.HandleRouteEditorAction(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "That rider is no longer on this route. Refresh the page and try again.") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	state := httptest.NewRecorder()
	h.HandleGetRouteSession(state, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil))
	if len(decodeRouteResponse(t, state).Routes[0].Stops) != 1 {
		t.Fatal("rejected edit changed routes")
	}
}

func TestStandaloneRouteEditorFragmentHeaders(t *testing.T) {
	for _, balance := range []string{"false", "true", ""} {
		t.Run("balance="+balance, func(t *testing.T) {
			h, session, _ := largeRouteFixture(t)
			r := routeEditorFormRequest(url.Values{"session_id": {session.ID}, "action": {"move"}, "from_route_index": {"0"}, "participant_id": {"1"}, "destination": {"499"}})
			r.Header.Set("X-Route-Fragment", "true")
			r.Header.Set("X-Route-Balance", balance)
			w := httptest.NewRecorder()
			h.HandleRouteEditorAction(w, r)
			fragment := balance == "false"
			if w.Code != 200 || strings.Contains(w.Body.String(), `data-route-patch="`+session.ID+`"`) != fragment || strings.Contains(w.Body.String(), `id="save-event-panel"`) == fragment {
				t.Fatalf("status=%d fragment=%t bytes=%d", w.Code, fragment, w.Body.Len())
			}
			if fragment && strings.Count(w.Body.String(), `data-route-index=`) != 2 {
				t.Fatal("fragment did not contain exactly changed cards")
			}
		})
	}
}

func TestStandaloneAddSecondDriverKeepsFragmentRendering(t *testing.T) {
	h, session, _ := oneRouteWithUnusedDriver(t)
	r := routeEditorFormRequest(url.Values{"session_id": {session.ID}, "action": {"add"}, "destination": {"2"}})
	r.Header.Set("X-Route-Fragment", "true")
	r.Header.Set("X-Route-Balance", "false")
	w := httptest.NewRecorder()
	h.HandleRouteEditorAction(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), `data-route-index="0"`) || !strings.Contains(w.Body.String(), `data-route-patch="`+session.ID+`"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if h.Measurer.(*stubMeasurer).count() != 0 {
		t.Fatal("empty driver addition measured routes")
	}
}

func TestStandaloneRouteEditorMoveMeasuresOnlyChangedOccupiedCar(t *testing.T) {
	h, m, _, session := twoCarMobileFixture(t)
	r := routeEditorFormRequest(url.Values{"session_id": {session.ID}, "action": {"move"}, "destination": {"1"}, "from_route_index": {"0"}, "participant_id": {"10"}})
	w := httptest.NewRecorder()
	h.HandleRouteEditorAction(w, r)
	if w.Code != 200 || m.count() != 2 || !strings.Contains(w.Body.String(), `data-timings="measured"`) {
		t.Fatalf("status=%d measurements=%d body=%s", w.Code, m.count(), w.Body.String())
	}
}

func TestStandaloneRouteEditorMalformedForm(t *testing.T) {
	h, _ := newRouteEditHandler(t)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/choose", strings.NewReader("destination=%xx"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleRouteEditorAction(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), messageInvalidRequestBody) || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
}

func TestStandaloneRouteEditorLargeDestinationReportsInvalidRouteIndex(t *testing.T) {
	h, session := newRouteEditHandler(t)
	destination := strconv.FormatInt(int64(^uint(0)>>1), 10)
	r := routeEditorFormRequest(url.Values{"session_id": {session.ID}, "action": {"swap"}, "from_route_index": {"0"}, "destination": {destination}})
	w := httptest.NewRecorder()
	h.HandleRouteEditorAction(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), messageInvalidRouteIndex) || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
}

type conflictingRouteEditWorkflows struct{ database.WorkflowRepository }

func (conflictingRouteEditWorkflows) CompareAndSwap(context.Context, string, string, int64, []byte, time.Duration) error {
	return database.ErrWorkflowConflict
}

func TestRouteEditAdaptersPreserveConflictAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		action     string
		standalone bool
	}{
		{"move", false}, {"move", true}, {"swap", false}, {"swap", true}, {"add", false}, {"add", true},
	} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/standalone=%t/canceled=%t", tc.action, tc.standalone, canceled), func(t *testing.T) {
				db := postgrestest.Open(t)
				store := routesession.NewPersistentStore(routeEditDistanceCalculator{}, conflictingRouteEditWorkflows{db.Workflows()})
				drivers := []models.Driver{{ID: 1, Name: "One", VehicleCapacity: 2}, {ID: 2, Name: "Two", VehicleCapacity: 2}, {ID: 3, Name: "Three", VehicleCapacity: 2}}
				session := mustCreateRouteSession(t, store, routesession.CreateInput{Routes: []models.CalculatedRoute{{Driver: &drivers[0], EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Rider", Lat: 1}}}}, {Driver: &drivers[1], EffectiveCapacity: 2}}, SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{Name: "HQ"}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
				m := &stubMeasurer{}
				h := &Handler{RouteSession: store, Measurer: m}
				var r *http.Request
				var handle http.HandlerFunc
				if tc.standalone {
					destination := "1"
					if tc.action == "add" {
						destination = "3"
					}
					r = routeEditorFormRequest(url.Values{"session_id": {session.ID}, "action": {tc.action}, "from_route_index": {"0"}, "participant_id": {"10"}, "destination": {destination}})
					handle = h.HandleRouteEditorAction
				} else {
					switch tc.action {
					case "move":
						r = newRouteEditJSONRequest("/api/v1/routes/edit/move-participant", []byte(fmt.Sprintf(`{"session_id":%q,"participant_id":10,"from_route_index":0,"to_route_index":1,"insert_at_position":-1}`, session.ID)))
						handle = h.HandleMoveParticipant
					case "swap":
						r = newRouteEditJSONRequest("/api/v1/routes/edit/swap-drivers", []byte(fmt.Sprintf(`{"session_id":%q,"route_index_1":0,"route_index_2":1}`, session.ID)))
						handle = h.HandleSwapDrivers
					case "add":
						r = newRouteEditJSONRequest("/api/v1/routes/edit/add-driver", []byte(fmt.Sprintf(`{"session_id":%q,"driver_id":3}`, session.ID)))
						handle = h.HandleAddDriver
					}
				}
				if canceled {
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					r = r.WithContext(ctx)
				}
				w := httptest.NewRecorder()
				handle(w, r)
				wantStatus, wantMessage := 409, "This route plan changed. Reload it and try again."
				if canceled {
					wantStatus, wantMessage = 500, messageGenericInternalError
				}
				if w.Code != wantStatus || !strings.Contains(w.Body.String(), wantMessage) || m.count() != 0 {
					t.Fatalf("status=%d headers=%v measurements=%d body=%s", w.Code, w.Header(), m.count(), w.Body.String())
				}
				if tc.standalone && (w.Header().Get("HX-Reswap") != "none" || w.Header().Get("HX-Trigger") == "") {
					t.Fatalf("forced HTMX headers=%v", w.Header())
				}
				state := httptest.NewRecorder()
				h.HandleGetRouteSession(state, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil))
				response := decodeRouteResponse(t, state)
				if len(response.Routes) != 2 || response.Routes[0].Driver.ID != 1 || len(response.Routes[0].Stops) != 1 || len(response.Routes[1].Stops) != 0 {
					t.Fatalf("failed edit changed session: %+v", response.Routes)
				}
			})
		}
	}
}

func TestStandaloneRouteEditorMalformedSessionIDRetainsNotFound(t *testing.T) {
	for _, action := range []string{"move", "swap", "add"} {
		t.Run(action, func(t *testing.T) {
			db := postgrestest.Open(t)
			h := &Handler{RouteSession: routesession.NewPersistentStore(routeEditDistanceCalculator{}, db.Workflows())}
			form := url.Values{"session_id": {"missing-\xff\xfe"}, "action": {action}, "destination": {"1"}, "from_route_index": {"0"}, "participant_id": {"10"}}
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/choose", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			h.HandleRouteEditorAction(w, r)
			if w.Code != 404 || !strings.Contains(w.Body.String(), messageSessionNotFound) || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("HX-Reswap") != "none" || w.Header().Get("HX-Trigger") == "" {
				t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
			}
		})
	}
}

func TestStandaloneRouteEditorMoveAppendsWhenDestinationIsOverCapacity(t *testing.T) {
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	drivers := []models.Driver{{ID: 1, VehicleCapacity: 2}, {ID: 2, VehicleCapacity: 2}}
	riders := []models.Participant{{ID: 10, Name: "Moving"}, {ID: 11, Name: "First"}, {ID: 12, Name: "Second"}}
	session := mustCreateRouteSession(t, store, routesession.CreateInput{Routes: []models.CalculatedRoute{{Driver: &drivers[0], EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &riders[0]}}}, {Driver: &drivers[1], EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &riders[1]}, {Participant: &riders[2]}}}}, SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{Name: "HQ"}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	m := &stubMeasurer{}
	h := &Handler{RouteSession: store, Renderer: loadEmbeddedTemplates(t), Measurer: m}
	w := httptest.NewRecorder()
	h.HandleRouteEditorAction(w, routeEditorFormRequest(url.Values{"session_id": {session.ID}, "action": {"move"}, "from_route_index": {"0"}, "participant_id": {"10"}, "destination": {"1"}}))
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	state := httptest.NewRecorder()
	h.HandleGetRouteSession(state, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+session.ID, nil))
	response := decodeRouteResponse(t, state)
	stops := response.Routes[1].Stops
	if len(response.Routes[0].Stops) != 0 || len(stops) != 3 || stops[0].Participant.ID != 11 || stops[1].Participant.ID != 12 || stops[2].Participant.ID != 10 {
		t.Fatalf("move did not append: %+v", response.Routes)
	}
	if m.count() != 0 {
		t.Fatal("over-capacity edit measured routes")
	}
}
