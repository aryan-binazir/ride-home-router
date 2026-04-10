package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ride-home-router/internal/models"
	"ride-home-router/internal/sqlite"
	"ride-home-router/web"

	_ "modernc.org/sqlite"
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

func TestHandleCreateEvent_MissingRoutesReturnsBadRequest(t *testing.T) {
	handler, _ := newTestEventHandler(t, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader("event_date=2026-03-14"))
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

func TestHandleCreateEvent_OutOfBalanceSessionReturnsBadRequest(t *testing.T) {
	handler, _ := newTestEventHandler(t, false)

	session := handler.RouteSession.Create(
		[]models.CalculatedRoute{
			{
				Driver:              &models.Driver{ID: 1, Name: "Driver 1", VehicleCapacity: 1},
				EffectiveCapacity:   1,
				Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10}}, {Participant: &models.Participant{ID: 11}}},
				TotalDistanceMeters: 5000,
			},
		},
		[]models.Driver{{ID: 1, Name: "Driver 1", VehicleCapacity: 1}},
		&models.ActivityLocation{ID: 1, Name: "HQ", Address: "1 Main", Lat: 0, Lng: 0},
		false,
		"18:30",
		"dropoff",
		nil,
	)

	routingPayload := models.RoutingResult{
		Routes: session.CurrentRoutes,
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(form))
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?limit=1", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+int64ToString(event.ID), nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/1", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+int64ToString(event.ID), nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?offset=20&limit=20", nil)
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
}

func TestHandleDeleteEvent_HTMXRerendersEventList(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	deleted := createTestEvent(t, store, "2026-03-13", "delete me")
	createTestEvent(t, store, "2026-03-14", "keep me")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/events/"+int64ToString(deleted.ID), nil)
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

func TestBuildEventSnapshots_RejectsMixedRouteModes(t *testing.T) {
	result := &models.RoutingResult{
		Mode: models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{
			{
				Driver: &models.Driver{ID: 1, Name: "Driver One", VehicleCapacity: 4},
				Mode:   models.RouteModeDropoff,
				Stops: []models.RouteStop{
					{Participant: &models.Participant{ID: 1, Name: "Passenger One"}},
				},
			},
			{
				Driver: &models.Driver{ID: 2, Name: "Driver Two", VehicleCapacity: 4},
				Mode:   models.RouteModePickup,
				Stops: []models.RouteStop{
					{Participant: &models.Participant{ID: 2, Name: "Passenger Two"}},
				},
			},
		},
	}

	_, _, _, err := buildEventSnapshots(result)
	if err == nil || err.Error() != "all routes must use the same mode" {
		t.Fatalf("expected mixed-mode validation error, got %v", err)
	}
}

func TestBuildEventSnapshots_RejectsInvalidMode(t *testing.T) {
	result := &models.RoutingResult{
		Mode: "sideways",
		Routes: []models.CalculatedRoute{
			{
				Driver: &models.Driver{ID: 1, Name: "Driver One", VehicleCapacity: 4},
				Stops: []models.RouteStop{
					{Participant: &models.Participant{ID: 1, Name: "Passenger One"}},
				},
			},
		},
	}

	_, _, _, err := buildEventSnapshots(result)
	if err == nil || err.Error() != messageInvalidRouteMode {
		t.Fatalf("expected invalid mode error %q, got %v", messageInvalidRouteMode, err)
	}
}

func newTestEventHandler(t *testing.T, withLegacyHistory bool) (*Handler, *sqlite.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "events-test.db")
	if withLegacyHistory {
		createLegacyHistoryDB(t, dbPath)
	}

	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	handler := &Handler{
		DB:           store,
		Templates:    newTestTemplates(t),
		RouteSession: NewRouteSessionStore(),
	}

	t.Cleanup(func() {
		handler.RouteSession.Close()
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	return handler, store
}

func newTestTemplates(t *testing.T) *TemplateSet {
	t.Helper()

	base, err := template.New("test").Parse(testEventTemplates)
	if err != nil {
		t.Fatalf("parse test templates: %v", err)
	}

	return &TemplateSet{
		Base:  base,
		Pages: map[string]string{},
	}
}

func createTestEvent(t *testing.T, store *sqlite.Store, eventDate, notes string) *models.Event {
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

func createLegacyHistoryDB(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()

	statements := []string{
		`CREATE TABLE schema_version (version INTEGER PRIMARY KEY)`,
		`INSERT INTO schema_version (version) VALUES (1)`,
		`CREATE TABLE settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			selected_activity_location_id INTEGER,
			use_miles INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO settings (id, use_miles) VALUES (1, 0)`,
		`CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_date DATETIME NOT NULL,
			notes TEXT,
			mode TEXT NOT NULL DEFAULT 'dropoff',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE event_assignments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id INTEGER NOT NULL,
			driver_id INTEGER NOT NULL,
			driver_name TEXT NOT NULL,
			driver_address TEXT NOT NULL,
			route_order INTEGER NOT NULL,
			participant_id INTEGER NOT NULL,
			participant_name TEXT NOT NULL,
			participant_address TEXT NOT NULL,
			distance_from_prev_meters REAL NOT NULL DEFAULT 0,
			org_vehicle_id INTEGER,
			org_vehicle_name TEXT
		)`,
		`CREATE TABLE event_summaries (
			event_id INTEGER PRIMARY KEY,
			total_participants INTEGER NOT NULL DEFAULT 0,
			total_drivers INTEGER NOT NULL DEFAULT 0,
			total_distance_meters REAL NOT NULL DEFAULT 0,
			org_vehicles_used INTEGER NOT NULL DEFAULT 0,
			mode TEXT NOT NULL DEFAULT 'dropoff'
		)`,
		`INSERT INTO events (id, event_date, notes, mode, created_at)
		 VALUES (1, '2026-03-13T00:00:00Z', 'legacy event', 'dropoff', '2026-03-13T00:00:00Z')`,
		`INSERT INTO event_assignments (
			event_id, driver_id, driver_name, driver_address, route_order,
			participant_id, participant_name, participant_address, distance_from_prev_meters,
			org_vehicle_id, org_vehicle_name
		) VALUES
			(1, 10, 'Legacy Driver', '10 Driver Lane', 0, 11, 'Legacy Rider One', '11 Rider Lane', 1500, 5, 'Org Van'),
			(1, 10, 'Legacy Driver', '10 Driver Lane', 1, 12, 'Legacy Rider Two', '12 Rider Lane', 600, 5, 'Org Van')`,
		`INSERT INTO event_summaries (
			event_id, total_participants, total_drivers, total_distance_meters, org_vehicles_used, mode
		) VALUES (1, 2, 1, 2100, 1, 'dropoff')`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy statement %q: %v", stmt, err)
		}
	}
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
