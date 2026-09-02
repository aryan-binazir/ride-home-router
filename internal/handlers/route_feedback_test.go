package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routefeedback"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type failingRouteFeedbackRepository struct {
	err    error
	called int
}

func (r *failingRouteFeedbackRepository) Create(context.Context, *models.RouteFeedbackRecord) error {
	r.called++
	return r.err
}

type routeFeedbackDataStore struct {
	database.DataStore
	feedback database.RouteFeedbackRepository
}

func (s routeFeedbackDataStore) RouteFeedback() database.RouteFeedbackRepository { return s.feedback }

func TestHandleCreateEvent_CapturesEditedSMERouteFeedback(t *testing.T) {
	handler, store, conn := newRouteFeedbackHandler(t)
	setSMEEmail(t, store, "SME@Example.com")
	session := createFeedbackSession(handler)
	if _, err := handler.RouteSession.ApplyMoves(context.Background(), session.ID, []routesession.Move{{
		ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1,
	}}, routesession.ApplyMovesOptions{}); err != nil {
		t.Fatalf("move participant: %v", err)
	}

	rr := saveLiveFeedbackSession(handler, session.ID, " sme@example.COM ")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%q", rr.Code, rr.Body.String())
	}

	var count, schemaVersion int
	var eventID int64
	var email, sessionID string
	var inputJSON, proposedJSON, finalJSON []byte
	if err := conn.QueryRow(context.Background(), `
		SELECT COUNT(*), min(event_id), min(session_id), min(sme_email), min(schema_version),
		       min(input::text), min(proposed::text), min(final::text)
		FROM route_feedback`).Scan(&count, &eventID, &sessionID, &email, &schemaVersion, &inputJSON, &proposedJSON, &finalJSON); err != nil {
		t.Fatalf("query route feedback: %v", err)
	}
	if count != 1 || eventID == 0 || sessionID != session.ID || email != "SME@Example.com" || schemaVersion != routefeedback.SchemaVersion {
		t.Fatalf("feedback metadata = count:%d event:%d session:%q email:%q version:%d", count, eventID, sessionID, email, schemaVersion)
	}
	var input routefeedback.Input
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if len(input.Drivers) != 3 {
		t.Fatalf("input drivers = %#v, want all 3 selected drivers", input.Drivers)
	}
	if bytes.Equal(proposedJSON, finalJSON) {
		t.Fatalf("edited proposed and final JSON are equal: %s", proposedJSON)
	}
	for label, payload := range map[string][]byte{"input": inputJSON, "proposed": proposedJSON, "final": finalJSON} {
		for _, forbidden := range []string{`"name":`, `"address_name":`, `"created_at":`, `"updated_at":`} {
			if bytes.Contains(payload, []byte(forbidden)) {
				t.Fatalf("%s contains forbidden key %s: %s", label, forbidden, payload)
			}
		}
	}
}

func TestHandleMobileSaveCapturesSMERouteFeedback(t *testing.T) {
	handler, store, conn := newRouteFeedbackHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	setSMEEmail(t, store, "sme@example.com")
	session := createFeedbackSession(handler)
	draftID := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(draftID, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })

	form := url.Values{"event_date": {"2026-08-29"}, "notes": {"Mobile feedback"}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/m/routes/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(routefeedback.AuthenticatedUserEmailHeader, "SME@Example.com")
	req.AddCookie(mobileTestCookie(draftID))
	rr := httptest.NewRecorder()
	handler.HandleMobileSave(rr, req)

	if rr.Code != http.StatusSeeOther || !strings.HasPrefix(rr.Header().Get("Location"), "/m/history/") {
		t.Fatalf("status = %d location=%q body=%q", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	if count := countFeedbackRows(t, conn); count != 1 {
		t.Fatalf("route feedback rows = %d, want 1", count)
	}
}

func TestHandleCreateEvent_CapturesUnchangedSMERouteFeedback(t *testing.T) {
	handler, store, conn := newRouteFeedbackHandler(t)
	setSMEEmail(t, store, "sme@example.com")
	session := createFeedbackSession(handler)

	rr := saveLiveFeedbackSession(handler, session.ID, "sme@example.com")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%q", rr.Code, rr.Body.String())
	}
	var proposedJSON, finalJSON []byte
	if err := conn.QueryRow(context.Background(), `SELECT proposed::text, final::text FROM route_feedback WHERE session_id = $1`, session.ID).
		Scan(&proposedJSON, &finalJSON); err != nil {
		t.Fatalf("query route feedback: %v", err)
	}
	if !bytes.Equal(proposedJSON, finalJSON) {
		t.Fatalf("unchanged proposed and final differ: proposed=%s final=%s", proposedJSON, finalJSON)
	}
}

func TestHandleCreateEvent_DoesNotCaptureWithoutMatchingSME(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		header  string
	}{
		{name: "mismatched header", setting: "sme@example.com", header: "other@example.com"},
		{name: "missing header", setting: "sme@example.com"},
		{name: "empty setting", header: "sme@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, store, conn := newRouteFeedbackHandler(t)
			setSMEEmail(t, store, tt.setting)
			session := createFeedbackSession(handler)
			rr := saveLiveFeedbackSession(handler, session.ID, tt.header)
			if rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201 body=%q", rr.Code, rr.Body.String())
			}
			if countFeedbackRows(t, conn) != 0 {
				t.Fatal("feedback captured without matching SME")
			}
			events, total, err := store.Events().List(context.Background(), 10, 0)
			if err != nil || total != 1 || len(events) != 1 {
				t.Fatalf("saved events = %#v total=%d err=%v", events, total, err)
			}
		})
	}
}

func TestHandleCreateEvent_RejectsPostedRoutesBeforeCapturingFeedback(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
	}{
		{name: "direct routes"},
		{name: "expired session fallback", sessionID: "expired-session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, store, conn := newRouteFeedbackHandler(t)
			setSMEEmail(t, store, "sme@example.com")
			form := url.Values{"event_date": {"2026-08-29"}, "routes_json": {`{"mode":"dropoff","routes":[]}`}}
			if tt.sessionID != "" {
				form.Set("session_id", tt.sessionID)
			}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set(routefeedback.AuthenticatedUserEmailHeader, "sme@example.com")
			rr := httptest.NewRecorder()
			handler.HandleCreateEvent(rr, req)
			if rr.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 body=%q", rr.Code, rr.Body.String())
			}
			if countFeedbackRows(t, conn) != 0 {
				t.Fatal("feedback captured for posted routes")
			}
			events, total, err := store.Events().List(context.Background(), 10, 0)
			if err != nil || total != 0 || len(events) != 0 {
				t.Fatalf("saved events = %#v total=%d err=%v, want none", events, total, err)
			}
		})
	}
}

func TestHandleCreateEvent_FeedbackFailureStillCommitsEventAndSession(t *testing.T) {
	handler, store, _ := newRouteFeedbackHandler(t)
	setSMEEmail(t, store, "sme@example.com")
	wantErr := errors.New("feedback unavailable")
	failing := &failingRouteFeedbackRepository{err: wantErr}
	handler.DB = routeFeedbackDataStore{DataStore: store, feedback: failing}
	session := createFeedbackSession(handler)

	rr := saveLiveFeedbackSession(handler, session.ID, "sme@example.com")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%q", rr.Code, rr.Body.String())
	}
	if failing.called != 1 {
		t.Fatalf("feedback Create calls = %d, want 1", failing.called)
	}
	events, total, err := store.Events().List(context.Background(), 10, 0)
	if err != nil || total != 1 || len(events) != 1 {
		t.Fatalf("saved events = %#v total=%d err=%v", events, total, err)
	}
	if _, ok := handler.RouteSession.Snapshot(session.ID); ok {
		t.Fatal("session remained retryable after feedback failure")
	}
	if err := handler.RouteSession.Commit(context.Background(), session.ID, func(context.Context, routesession.CommitSnapshot) error { return nil }); !errors.Is(err, routesession.ErrAlreadyCommitted) {
		t.Fatalf("retry Commit error = %v, want ErrAlreadyCommitted", err)
	}
}

func newRouteFeedbackHandler(t *testing.T) (*Handler, *postgres.Store, *pgx.Conn) {
	t.Helper()
	databaseURL := postgrestest.DatabaseURL(t)
	store, err := postgres.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect for feedback assertions: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	handler := &Handler{DB: store, Renderer: newTestTemplates(t), RouteSession: routesession.NewStore(routeEditDistanceCalculator{})}
	t.Cleanup(handler.RouteSession.Close)
	return handler, store, conn
}

func setSMEEmail(t *testing.T, store *postgres.Store, email string) {
	t.Helper()
	settings, err := store.Settings().Get(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	settings.SMEEmail = email
	if err := store.Settings().Update(context.Background(), settings); err != nil {
		t.Fatalf("update settings: %v", err)
	}
}

func createFeedbackSession(handler *Handler) routesession.Snapshot {
	result := feedbackRoutingResult()
	return handler.RouteSession.Create(routesession.CreateInput{
		Routes: result.Routes,
		SelectedDrivers: []models.Driver{
			{ID: 1, Name: "Driver One", Address: "1 Driver Road", AddressName: "Home One", Lat: 35.1, Lng: -78.1, VehicleCapacity: 4},
			{ID: 2, Name: "Driver Two", Address: "2 Driver Road", AddressName: "Home Two", Lat: 35.2, Lng: -78.2, VehicleCapacity: 4},
			{ID: 3, Name: "Unused Driver", Address: "3 Driver Road", AddressName: "Home Three", Lat: 35.3, Lng: -78.3, VehicleCapacity: 6},
		},
		DriverOrgVehicles: map[int64]*models.OrganizationVehicle{1: {ID: 50, Name: "Van", Capacity: 4}},
		ActivityLocation:  &models.ActivityLocation{ID: 100, Name: "HQ", Address: "100 Center", Lat: 35, Lng: -78},
		Mode:              result.Mode,
	})
}

func feedbackRoutingResult() models.RoutingResult {
	return models.RoutingResult{
		Mode: models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{
			{
				Driver:       &models.Driver{ID: 1, Name: "Driver One", Address: "1 Driver Road", AddressName: "Home One", Lat: 35.1, Lng: -78.1, VehicleCapacity: 4},
				OrgVehicleID: 50, EffectiveCapacity: 4, Mode: models.RouteModeDropoff,
				Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Rider One", Address: "10 Rider Road", AddressName: "Rider Home", Lat: 35.4, Lng: -78.4}}},
				TotalDistanceMeters: 1000, RouteDurationSecs: 600, DetourSecs: 120,
			},
			{
				Driver:            &models.Driver{ID: 2, Name: "Driver Two", Address: "2 Driver Road", AddressName: "Home Two", Lat: 35.2, Lng: -78.2, VehicleCapacity: 4},
				EffectiveCapacity: 4, Mode: models.RouteModeDropoff,
				Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 11, Name: "Rider Two", Address: "11 Rider Road", AddressName: "Other Home", Lat: 35.5, Lng: -78.5}}},
				TotalDistanceMeters: 1200, RouteDurationSecs: 700, DetourSecs: 140,
			},
		},
	}
}

func saveLiveFeedbackSession(handler *Handler, sessionID, authenticatedEmail string) *httptest.ResponseRecorder {
	form := url.Values{"event_date": {"2026-08-29"}, "session_id": {sessionID}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authenticatedEmail != "" {
		req.Header.Set(routefeedback.AuthenticatedUserEmailHeader, authenticatedEmail)
	}
	rr := httptest.NewRecorder()
	handler.HandleCreateEvent(rr, req)
	return rr
}

func countFeedbackRows(t *testing.T, conn *pgx.Conn) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM route_feedback`).Scan(&count); err != nil {
		t.Fatalf("count route feedback: %v", err)
	}
	return count
}
