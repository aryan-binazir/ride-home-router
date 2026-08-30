package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/templates"
	"ride-home-router/web"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

const testEventTemplates = `
{{define "event_list"}}
FULL|{{.DisplayedCount}}/{{.Total}}{{range .Events}}<div class="event-item">{{.ID}}|{{.Notes}}</div>{{else}}<div class="empty">No events</div>{{end}}
{{end}}
{{define "event_list_page"}}
PAGE|{{.DisplayedCount}}/{{.Total}}{{range .Events}}<div class="event-item">{{.ID}}|{{.Notes}}</div>{{end}}<div id="event-list-status" hx-swap-oob="outerHTML">Showing {{.DisplayedCount}}</div><div id="event-list-pagination" hx-swap-oob="outerHTML"></div>
{{end}}
{{define "event_detail"}}
{{if .UseLegacyAssignments}}Legacy Detail{{range .Assignments}}|{{.DriverName}}{{range .Stops}}|{{.ParticipantName}}|{{printf "%.0f" .DistanceFromPrevMeters}}{{end}}{{end}}{{else}}Native Detail{{range .Routes}}|{{.DriverName}}|Final Leg {{printf "%.0f" .DistanceToDriverHomeMeters}}|Detour {{printf "%.0f" .DetourSecs}}{{end}}{{end}}
{{end}}
`

type failingEventRepository struct {
	database.EventRepository
	err error
}

func (r failingEventRepository) Create(context.Context, *models.Event, []models.EventRoute, *models.EventSummary) (*models.Event, error) {
	return nil, r.err
}

type eventRepositoryDataStore struct {
	database.DataStore
	events database.EventRepository
}

type blockingEventRepository struct {
	database.EventRepository
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingEventRepository) Create(ctx context.Context, event *models.Event, routes []models.EventRoute, summary *models.EventSummary) (*models.Event, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.EventRepository.Create(ctx, event, routes, summary)
}

func (s eventRepositoryDataStore) Events() database.EventRepository {
	return s.events
}

func TestHandleCreateEvent_MissingRoutesReturnsBadRequest(t *testing.T) {
	handler, _ := newTestEventHandler(t, false)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader("event_date=2026-03-14"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked: %v", r)
		}
	}()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("expected JSON error response, got %q", got)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Message != "Routes are required" {
		t.Fatalf("expected routes validation message, got %q", resp.Error.Message)
	}
}

func TestHandleCreateEvent_SessionSaveWithoutRoutesJSON(t *testing.T) {
	handler, store := newTestEventHandler(t, false)

	session := handler.RouteSession.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{
				Driver:              &models.Driver{ID: 1, Name: "Driver 1", VehicleCapacity: 2},
				EffectiveCapacity:   2,
				Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}},
				TotalDistanceMeters: 5000,
				Mode:                "dropoff",
			},
		},
		SelectedDrivers:  []models.Driver{{ID: 1, Name: "Driver 1", VehicleCapacity: 2}},
		ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ", Address: "1 Main", Lat: 0, Lng: 0},
		RouteTime:        "18:30",
		Mode:             "dropoff",
	})

	form := "event_date=2026-03-14&session_id=" + session.ID
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, rr.Code, rr.Body.String())
	}

	events, _, err := store.Events().List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 saved event, got %d", len(events))
	}
	if _, ok := handler.RouteSession.Snapshot(session.ID); ok {
		t.Fatal("session remains available after successful event persistence")
	}
}

func TestHandleCreateEvent_ConcurrentRetryWithFallbackDoesNotCreateDuplicate(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	result := models.RoutingResult{
		Mode: models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{{
			Driver:            &models.Driver{ID: 1, Name: "Driver 1", VehicleCapacity: 2},
			EffectiveCapacity: 2,
			Stops:             []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}},
			Mode:              models.RouteModeDropoff,
		}},
	}
	session := handler.RouteSession.Create(routesession.CreateInput{Routes: result.Routes, Mode: result.Mode})
	started := make(chan struct{})
	release := make(chan struct{})
	blockingRepo := &blockingEventRepository{EventRepository: store.Events(), started: started, release: release}
	handler.DB = eventRepositoryDataStore{DataStore: store, events: blockingRepo}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal routing payload: %v", err)
	}
	form := "event_date=2026-03-14&session_id=" + session.ID + "&routes_json=" + url.QueryEscape(string(payload))

	save := func() *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		handler.HandleCreateEvent(rr, req)
		return rr
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- save() }()
	<-started
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- save() }()
	close(release)

	first := <-firstDone
	second := <-secondDone
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201; body=%s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusNotFound {
		t.Fatalf("retry status = %d, want 404; body=%s", second.Code, second.Body.String())
	}
	events, _, err := store.Events().List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("saved events = %d, want 1", len(events))
	}
}

func TestHandleCreateEvent_PersistenceFailureRetainsSession(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	session := handler.RouteSession.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{{
			Driver:            &models.Driver{ID: 1, Name: "Driver 1", VehicleCapacity: 2},
			EffectiveCapacity: 2,
			Stops:             []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}},
			Mode:              models.RouteModeDropoff,
		}},
		Mode: models.RouteModeDropoff,
	})
	wantErr := errors.New("event persistence failed")
	handler.DB = eventRepositoryDataStore{
		DataStore: store,
		events: failingEventRepository{
			EventRepository: store.Events(),
			err:             wantErr,
		},
	}

	form := "event_date=2026-03-14&session_id=" + session.ID
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := handler.RouteSession.Snapshot(session.ID); !ok {
		t.Fatal("session was removed after event persistence failed")
	}
}

func TestHandleCreateEvent_AllEmptyRoutesReturnsLowercaseMessage(t *testing.T) {
	handler, _ := newTestEventHandler(t, false)
	session := handler.RouteSession.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{{
			Driver:            &models.Driver{ID: 1, Name: "Driver 1", VehicleCapacity: 2},
			EffectiveCapacity: 2,
			Mode:              models.RouteModeDropoff,
		}},
		Mode: models.RouteModeDropoff,
	})

	form := "event_date=2026-03-14&session_id=" + session.ID
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	var response ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Message != "routes are required" {
		t.Fatalf("message = %q, want %q", response.Error.Message, "routes are required")
	}
}

func TestHandleCreateEvent_ExpiredSessionReturnsNotFound(t *testing.T) {
	handler, _ := newTestEventHandler(t, false)

	form := "event_date=2026-03-14&session_id=expired-session-id"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rr.Code, rr.Body.String())
	}
}

func TestHandleCreateEvent_ExpiredSessionFallsBackToPostedRoutesJSON(t *testing.T) {
	handler, store := newTestEventHandler(t, false)

	routingPayload := models.RoutingResult{
		Routes: []models.CalculatedRoute{
			{
				Driver:            &models.Driver{ID: 7, Name: "Fallback Driver", VehicleCapacity: 2},
				EffectiveCapacity: 2,
				Stops:             []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}},
				Mode:              "dropoff",
			},
		},
		Summary: models.RoutingSummary{
			TotalParticipants: 1,
			TotalDriversUsed:  1,
		},
		Mode: "dropoff",
	}
	payloadBytes, err := json.Marshal(routingPayload)
	if err != nil {
		t.Fatalf("marshal routing payload: %v", err)
	}

	form := "event_date=2026-03-14&session_id=expired-session-id&routes_json=" + url.QueryEscape(string(payloadBytes))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, rr.Code, rr.Body.String())
	}
	events, _, err := store.Events().List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 saved event, got %d", len(events))
	}
	_, routes, _, err := store.Events().GetByID(context.Background(), events[0].ID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(routes))
	}
	if routes[0].DriverName != "Fallback Driver" {
		t.Fatalf("saved driver = %q, want fallback posted route driver", routes[0].DriverName)
	}
}

func TestHandleCreateEvent_InvalidFallbackModeReturnsFriendlyMessage(t *testing.T) {
	handler, _ := newTestEventHandler(t, false)
	routingPayload := models.RoutingResult{
		Mode: models.RouteMode("invalid"),
		Routes: []models.CalculatedRoute{{
			Driver: &models.Driver{ID: 7, Name: "Fallback Driver", VehicleCapacity: 2},
			Stops:  []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}},
		}},
	}
	payloadBytes, err := json.Marshal(routingPayload)
	if err != nil {
		t.Fatalf("marshal routing payload: %v", err)
	}
	form := "event_date=2026-03-14&session_id=expired-session-id&routes_json=" + url.QueryEscape(string(payloadBytes))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	var response ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Message != messageInvalidRouteMode {
		t.Fatalf("message = %q, want %q", response.Error.Message, messageInvalidRouteMode)
	}
}

func TestHandleCreateEvent_SessionSaveIgnoresClientSuppliedRoutes(t *testing.T) {
	handler, store := newTestEventHandler(t, false)

	session := handler.RouteSession.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{
				Driver:              &models.Driver{ID: 1, Name: "Driver 1", VehicleCapacity: 2},
				EffectiveCapacity:   2,
				Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}},
				TotalDistanceMeters: 5000,
				Mode:                "dropoff",
			},
		},
		SelectedDrivers:  []models.Driver{{ID: 1, Name: "Driver 1", VehicleCapacity: 2}},
		ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ", Address: "1 Main", Lat: 0, Lng: 0},
		RouteTime:        "18:30",
		Mode:             "dropoff",
	})

	body := map[string]any{
		"event_date": "2026-03-14",
		"session_id": session.ID,
		"routes": map[string]any{
			"routes": []map[string]any{
				{
					"driver": map[string]any{"id": 99, "name": "Forged Driver", "vehicle_capacity": 1},
					"stops":  []map[string]any{},
					"mode":   "dropoff",
				},
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, rr.Code, rr.Body.String())
	}

	events, _, err := store.Events().List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 saved event, got %d", len(events))
	}

	event, routes, _, err := store.Events().GetByID(context.Background(), events[0].ID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if event == nil {
		t.Fatal("expected event")
	}
	if len(routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(routes))
	}
	if routes[0].DriverName != "Driver 1" {
		t.Fatalf("saved driver = %q, want server session driver", routes[0].DriverName)
	}
}

func TestHandleCreateEvent_SessionSaveIgnoresMalformedRoutesJSON(t *testing.T) {
	handler, store := newTestEventHandler(t, false)

	session := handler.RouteSession.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{
				Driver:            &models.Driver{ID: 1, Name: "Session Driver", VehicleCapacity: 2},
				EffectiveCapacity: 2,
				Stops:             []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}},
				Mode:              "dropoff",
			},
		},
		SelectedDrivers:  []models.Driver{{ID: 1, Name: "Session Driver", VehicleCapacity: 2}},
		ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ", Address: "1 Main", Lat: 0, Lng: 0},
		RouteTime:        "18:30",
		Mode:             "dropoff",
	})

	form := "event_date=2026-03-14&session_id=" + url.QueryEscape(session.ID) + "&routes_json=%7Bnot-json"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, rr.Code, rr.Body.String())
	}
	events, _, err := store.Events().List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 saved event, got %d", len(events))
	}
	_, routes, _, err := store.Events().GetByID(context.Background(), events[0].ID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if routes[0].DriverName != "Session Driver" {
		t.Fatalf("saved driver = %q, want server session driver", routes[0].DriverName)
	}
}

func TestHandleCreateEvent_OutOfBalanceSessionReturnsBadRequest(t *testing.T) {
	handler, _ := newTestEventHandler(t, false)

	session := handler.RouteSession.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{
				Driver:              &models.Driver{ID: 1, Name: "Driver 1", VehicleCapacity: 1},
				EffectiveCapacity:   1,
				Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10}}, {Participant: &models.Participant{ID: 11}}},
				TotalDistanceMeters: 5000,
			},
		},
		SelectedDrivers:  []models.Driver{{ID: 1, Name: "Driver 1", VehicleCapacity: 1}},
		ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ", Address: "1 Main", Lat: 0, Lng: 0},
		RouteTime:        "18:30",
		Mode:             "dropoff",
	})

	routingPayload := models.RoutingResult{
		Routes: session.Routes,
		Summary: models.RoutingSummary{
			TotalParticipants:   2,
			TotalDriversUsed:    1,
			TotalDistanceMeters: 5000,
		},
		Mode: "dropoff",
	}
	payloadBytes, err := json.Marshal(routingPayload)
	if err != nil {
		t.Fatalf("marshal routing payload: %v", err)
	}

	form := "event_date=2026-03-14&session_id=" + session.ID + "&routes_json=" + url.QueryEscape(string(payloadBytes))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.HandleCreateEvent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Message != messageRoutesMustBeBalancedBeforeSaving {
		t.Fatalf("expected %q, got %q", messageRoutesMustBeBalancedBeforeSaving, resp.Error.Message)
	}
}

func TestHandleListEvents_ReturnsJSONForStandardRequest(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	createTestEvent(t, store, "2026-03-10", "older")
	createTestEvent(t, store, "2026-03-12", "newer")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events?limit=1", nil)
	rr := httptest.NewRecorder()

	handler.HandleListEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("expected JSON response, got %q", got)
	}

	var resp EventListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("expected total 2, got %d", resp.Total)
	}
	if resp.Limit != 1 {
		t.Fatalf("expected limit 1, got %d", resp.Limit)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 listed event, got %d", len(resp.Events))
	}
	if resp.Events[0].Notes != "newer" {
		t.Fatalf("expected newest event first, got %q", resp.Events[0].Notes)
	}
	if resp.Events[0].Summary == nil || resp.Events[0].Summary.TotalDistanceMeters != 1500 {
		t.Fatalf("expected event summary total distance 1500, got %#v", resp.Events[0].Summary)
	}
}

func TestHandleListEvents_HTMXRendersHTMLWithoutLegacyNoticeAndIncludesMigratedEvents(t *testing.T) {
	handler, _ := newTestEventHandler(t, true)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleListEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected HTML response, got %q", got)
	}

	body := rr.Body.String()
	if strings.Contains(body, "archived and is no longer shown here") {
		t.Fatalf("expected no legacy archive notice, got %q", body)
	}
	if !strings.Contains(body, "legacy event") {
		t.Fatalf("expected migrated legacy event to be rendered, got %q", body)
	}
}

func TestHandleGetEvent_ReturnsLegacyCompatibleJSON(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	event := createTestEvent(t, store, "2026-03-14", "current event")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events/"+int64ToString(event.ID), nil)
	rr := httptest.NewRecorder()

	handler.HandleGetEvent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("expected JSON response, got %q", got)
	}

	var raw map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := raw["assignments"]; !ok {
		t.Fatalf("expected assignments field in response, got %#v", raw)
	}
	if _, ok := raw["routes"]; ok {
		t.Fatalf("expected legacy JSON contract without routes field, got %#v", raw)
	}

	assignments, ok := raw["assignments"].([]any)
	if !ok || len(assignments) != 1 {
		t.Fatalf("expected one grouped assignment, got %#v", raw["assignments"])
	}
	group, ok := assignments[0].(map[string]any)
	if !ok {
		t.Fatalf("expected grouped assignment object, got %#v", assignments[0])
	}
	if group["driver_name"] != "Driver One" {
		t.Fatalf("expected driver_name Driver One, got %#v", group["driver_name"])
	}
	stops, ok := group["stops"].([]any)
	if !ok || len(stops) != 1 {
		t.Fatalf("expected one stop, got %#v", group["stops"])
	}
}

func TestHandleGetEvent_HTMXUsesLegacyDetailForMigratedHistory(t *testing.T) {
	handler, _ := newTestEventHandler(t, true)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events/1", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleGetEvent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Legacy Detail") {
		t.Fatalf("expected legacy detail rendering, got %q", body)
	}
	if strings.Contains(body, "Final Leg") {
		t.Fatalf("expected legacy detail to suppress final-leg metrics, got %q", body)
	}
	if !strings.Contains(body, "Legacy Rider One") {
		t.Fatalf("expected migrated legacy stop in detail view, got %q", body)
	}
}

func TestHandleGetEvent_HTMXUsesNativeDetailForCurrentHistory(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	event := createTestEvent(t, store, "2026-03-14", "current event")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events/"+int64ToString(event.ID), nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleGetEvent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Native Detail") {
		t.Fatalf("expected native detail rendering, got %q", body)
	}
	if !strings.Contains(body, "Final Leg 300") {
		t.Fatalf("expected native detail to include final leg metrics, got %q", body)
	}
}

func TestHandleListEvents_HTMXLoadMoreRendersAppendPartial(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	for i := range 25 {
		createTestEvent(t, store, time.Date(2026, time.March, i+1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), "event "+strconv.Itoa(i))
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events?offset=20&limit=20", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleListEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "PAGE|25/25") {
		t.Fatalf("expected append partial with updated counts, got %q", body)
	}
	if strings.Contains(body, "FULL|") {
		t.Fatalf("expected append partial instead of full list, got %q", body)
	}
	if !strings.Contains(body, `hx-swap-oob="outerHTML"`) {
		t.Fatalf("expected OOB updates in append partial, got %q", body)
	}
}

func TestActualEventDetailTemplateRendersFloatDetourComparison(t *testing.T) {
	content, err := fs.ReadFile(web.Templates, "templates/partials/event_detail.html")
	if err != nil {
		t.Fatalf("read event_detail template: %v", err)
	}

	tmpl, err := template.New("event_detail").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"formatDistance": func(meters float64, useMiles bool) string {
			if useMiles {
				return "0.00 mi"
			}
			return "0.00 km"
		},
		"formatDuration": func(seconds float64) string {
			return strconv.Itoa(int(seconds))
		},
		"initials": func(name string) string {
			return name[:1]
		},
		"displayAddress": func(addressName, address string) string {
			if addressName == "" {
				return address
			}
			return addressName + " (" + address + ")"
		},
		"pluralize": func(count int, singular string) string {
			if count == 1 {
				return singular
			}
			return singular + "s"
		},
	}).Parse(string(content))
	if err != nil {
		t.Fatalf("parse event_detail template: %v", err)
	}

	data := EventDetailViewData{
		Routes: []models.EventRoute{
			{
				DriverName:                 "Driver One",
				DriverAddress:              "1 Driver Way",
				TotalDropoffDistanceMeters: 1200,
				DistanceToDriverHomeMeters: 300,
				TotalDistanceMeters:        1500,
				DetourSecs:                 300,
				Mode:                       "dropoff",
				Stops: []models.EventRouteStop{
					{
						Order:                  0,
						ParticipantName:        "Passenger One",
						ParticipantAddress:     "2 Rider Road",
						DistanceFromPrevMeters: 1200,
					},
				},
			},
		},
		Summary: &models.EventSummary{
			TotalParticipants:   1,
			TotalDrivers:        1,
			TotalDistanceMeters: 1500,
		},
		UseMiles: false,
	}

	var rendered bytes.Buffer
	if err := tmpl.ExecuteTemplate(&rendered, "event_detail", data); err != nil {
		t.Fatalf("execute event_detail template: %v", err)
	}
	if body := rendered.String(); !strings.Contains(body, `<div class="label">Participant</div>`) || !strings.Contains(body, `<div class="label">Driver</div>`) {
		t.Fatalf("singular event summary labels missing: %s", body)
	}
}

func TestHandleDeleteEvent_HTMXRerendersEventList(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	deleted := createTestEvent(t, store, "2026-03-13", "delete me")
	createTestEvent(t, store, "2026-03-14", "keep me")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/events/"+int64ToString(deleted.ID), nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleDeleteEvent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected HTML response, got %q", got)
	}

	body := rr.Body.String()
	if strings.Contains(body, "delete me") {
		t.Fatalf("expected deleted event to be absent, got %q", body)
	}
	if !strings.Contains(body, "keep me") {
		t.Fatalf("expected remaining event to be rendered, got %q", body)
	}
}

func newTestEventHandler(t *testing.T, withLegacyHistory bool) (*Handler, *postgres.Store) {
	t.Helper()

	store := postgrestest.Open(t)
	if withLegacyHistory {
		createLegacyEvent(t, store)
	}

	handler := &Handler{
		DB:           store,
		Renderer:     newTestTemplates(t),
		RouteSession: routesession.NewStore(routeEditDistanceCalculator{}),
	}

	t.Cleanup(handler.RouteSession.Close)

	return handler, store
}

func newTestTemplates(t *testing.T) *templates.Renderer {
	t.Helper()

	templatesFS := fstest.MapFS{
		"templates/layout.html":             {Data: []byte(`{{template "content" .}}`)},
		"templates/mobile/layout.html":      {Data: []byte(`{{template "mobile_content" .}}`)},
		"templates/partials/events.html":    {Data: []byte(testEventTemplates)},
		"templates/index.html":              {Data: []byte(`{{define "content"}}test{{end}}`)},
		"templates/participants.html":       {Data: []byte(`{{define "content"}}test{{end}}`)},
		"templates/drivers.html":            {Data: []byte(`{{define "content"}}test{{end}}`)},
		"templates/labels.html":             {Data: []byte(`{{define "content"}}test{{end}}`)},
		"templates/activity_locations.html": {Data: []byte(`{{define "content"}}test{{end}}`)},
		"templates/vans.html":               {Data: []byte(`{{define "content"}}test{{end}}`)},
		"templates/settings.html":           {Data: []byte(`{{define "content"}}test{{end}}`)},
		"templates/history.html":            {Data: []byte(`{{define "content"}}test{{end}}`)},
	}
	for _, name := range []string{
		"plan.html", "location.html", "riders.html", "drivers.html", "when.html", "routes.html",
		"people.html", "person_form.html", "places.html", "place_form.html", "history.html", "history_detail.html", "error.html",
	} {
		templatesFS["templates/mobile/"+name] = &fstest.MapFile{Data: []byte(`{{define "mobile_content"}}test{{end}}`)}
	}
	renderer, err := templates.New(templatesFS)
	if err != nil {
		t.Fatalf("load test templates: %v", err)
	}
	return renderer
}

func createTestEvent(t *testing.T, store *postgres.Store, eventDate, notes string) *models.Event {
	t.Helper()

	date, err := time.Parse("2006-01-02", eventDate)
	if err != nil {
		t.Fatalf("parse date %q: %v", eventDate, err)
	}

	event := &models.Event{
		EventDate: date,
		Notes:     notes,
		Mode:      "dropoff",
	}

	routes := []models.EventRoute{
		{
			RouteOrder:                 0,
			DriverID:                   11,
			DriverName:                 "Driver One",
			DriverAddress:              "1 Driver Way",
			EffectiveCapacity:          4,
			TotalDropoffDistanceMeters: 1200,
			DistanceToDriverHomeMeters: 300,
			TotalDistanceMeters:        1500,
			BaselineDurationSecs:       600,
			RouteDurationSecs:          900,
			DetourSecs:                 300,
			Mode:                       "dropoff",
			Stops: []models.EventRouteStop{
				{
					Order:                    0,
					ParticipantID:            21,
					ParticipantName:          "Passenger One",
					ParticipantAddress:       "2 Rider Road",
					DistanceFromPrevMeters:   1200,
					CumulativeDistanceMeters: 1200,
					DurationFromPrevSecs:     720,
					CumulativeDurationSecs:   720,
				},
			},
		},
	}

	summary := &models.EventSummary{
		TotalParticipants:   1,
		TotalDrivers:        1,
		TotalDistanceMeters: 1500,
		Mode:                "dropoff",
	}

	created, err := store.Events().Create(context.Background(), event, routes, summary)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	return created
}

// createLegacyEvent seeds the incomplete version 1 snapshot format.
func createLegacyEvent(t *testing.T, store *postgres.Store) {
	t.Helper()
	orgVehicleID := int64(5)
	routes := []models.EventRoute{{
		RouteOrder: 0, DriverID: 10, DriverName: "Legacy Driver", DriverAddress: "10 Driver Lane",
		OrgVehicleID: orgVehicleID, OrgVehicleName: "Org Van",
		TotalDropoffDistanceMeters: 2100, TotalDistanceMeters: 2100, Mode: "dropoff",
		SnapshotVersion: 1, MetricsComplete: false,
		Stops: []models.EventRouteStop{
			{Order: 0, ParticipantID: 11, ParticipantName: "Legacy Rider One", ParticipantAddress: "11 Rider Lane", DistanceFromPrevMeters: 1500, CumulativeDistanceMeters: 1500},
			{Order: 1, ParticipantID: 12, ParticipantName: "Legacy Rider Two", ParticipantAddress: "12 Rider Lane", DistanceFromPrevMeters: 600, CumulativeDistanceMeters: 2100},
		},
	}}
	summary := &models.EventSummary{TotalParticipants: 2, TotalDrivers: 1, TotalDistanceMeters: 2100, OrgVehiclesUsed: 1, Mode: "dropoff"}
	if _, err := store.Events().Create(context.Background(), &models.Event{
		EventDate: time.Date(2026, time.March, 13, 0, 0, 0, 0, time.UTC), Notes: "legacy event", Mode: "dropoff",
	}, routes, summary); err != nil {
		t.Fatalf("create legacy event: %v", err)
	}
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
