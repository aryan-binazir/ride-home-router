package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/eventsnapshot"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func replayInput() routesession.CreateInput {
	return routesession.CreateInput{Mode: models.RouteModeDropoff, ActivityLocation: &models.ActivityLocation{Name: "HQ"}, Routes: []models.CalculatedRoute{{Driver: &models.Driver{ID: 1, Name: "D", VehicleCapacity: 2}, EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Alice"}}}}}}
}

func TestClearedPayloadRejectionReason(t *testing.T) {
	_, err := eventsnapshot.Build(models.RoutingResult{})
	if !errors.Is(err, eventsnapshot.ErrRoutesRequired) {
		t.Fatalf("cleared payload: %v", err)
	}
	mode, err := models.ParseRouteMode("")
	if err != nil || mode != models.RouteModeDropoff {
		t.Fatalf("blank mode: %q %v", mode, err)
	}
}

func TestCallbackContractDoesNotRequireEventValidation(t *testing.T) {
	s := routesession.NewStore(distance.NewEstimator())
	defer s.Close()
	c, err := s.CreateContext(t.Context(), routesession.CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	err = s.CommitEvent(t.Context(), c.ID, func(_ context.Context, snap routesession.CommitSnapshot, _ database.WorkflowWrites) error {
		called++
		if len(snap.Final) != 0 {
			t.Fatal("unexpected routes")
		}
		return nil
	})
	if err != nil || called != 1 {
		t.Fatalf("callback: %d %v", called, err)
	}
}

func replayPost(h *handlers.Handler, id, date string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events", strings.NewReader(url.Values{"session_id": {id}, "event_date": {date}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCreateEvent(w, r)
	return w
}

func TestPersistentHandlerResetIdentityDeleteAndRetry(t *testing.T) {
	db := postgrestest.Open(t)
	s := routesession.NewPersistentStore(distance.NewEstimator(), db.Workflows())
	h := &handlers.Handler{DB: db, RouteSession: s}
	a, err := s.CreateContext(t.Context(), replayInput())
	if err != nil {
		t.Fatal(err)
	}
	other := replayInput()
	other.Routes[0].Driver.ID = 2
	other.Routes[0].Stops[0].Participant.ID = 20
	b, err := s.CreateContext(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetReviewerNote(t.Context(), a.ID, " keep "); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResetContext(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := s.Load(t.Context(), a.ID)
	if err != nil || !ok || loaded.ReviewerNote != "keep" {
		t.Fatalf("reset lost note: %+v %v", loaded, err)
	}
	before, err := db.Workflows().Load(t.Context(), "route", a.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w := replayPost(h, a.ID, "bad-date"); w.Code != 400 {
		t.Fatalf("invalid date: %d %s", w.Code, w.Body.String())
	}
	after, err := db.Workflows().Load(t.Context(), "route", a.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("validation failure changed payload/revision/consumed")
	}
	if w := replayPost(h, a.ID, "2026-11-08"); w.Code != 201 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	events, total, err := db.Events().List(t.Context(), 10, 0)
	if err != nil || total != 1 || len(events) != 1 || events[0].RouteSessionID != a.ID {
		t.Fatalf("event identity: %+v %d %v", events, total, err)
	}
	e := events[0]
	_, routes, summary, err := db.Events().GetByID(t.Context(), e.ID)
	if err != nil || len(routes) != 1 || routes[0].DriverID != 1 || len(routes[0].Stops) != 1 || routes[0].Stops[0].ParticipantID != 10 || summary.TotalParticipants != 1 {
		t.Fatalf("event oracle: %+v %+v %v", routes, summary, err)
	}
	live, ok, err := s.Load(t.Context(), b.ID)
	if err != nil || !ok || live.ID != b.ID || live.ReviewerNote != "" || live.Routes[0].Driver.ID != 2 || live.Routes[0].Stops[0].Participant.ID != 20 {
		t.Fatalf("other session affected: %+v %v", live, err)
	}
	rec, err := db.Workflows().Load(t.Context(), "route", a.ID, time.Hour)
	if err != nil || !rec.Consumed || string(rec.Data) != "{}" {
		t.Fatalf("tombstone: %+v %v", rec, err)
	}
	if _, err = s.SetReviewerNote(t.Context(), a.ID, "late"); !errors.Is(err, routesession.ErrNotFound) {
		t.Fatalf("late note: %v", err)
	}
	if err = db.Events().Delete(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	if w := replayPost(h, a.ID, "2026-11-08"); w.Code != 409 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	if _, n, err := db.Events().List(t.Context(), 10, 0); err != nil || n != 0 {
		t.Fatalf("resurrection: %d %v", n, err)
	}
}

func TestDeferredCommitFailureLeavesRetryableSession(t *testing.T) {
	u := postgrestest.DatabaseURL(t)
	db := postgrestest.OpenURL(t, u)
	conn, err := pgx.Connect(t.Context(), u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	_, err = conn.Exec(t.Context(), `CREATE FUNCTION reject_event_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'owned commit fault'; END $$;
 CREATE CONSTRAINT TRIGGER reject_event_commit AFTER UPDATE ON workflow_sessions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.consumed) EXECUTE FUNCTION reject_event_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	s := routesession.NewPersistentStore(distance.NewEstimator(), db.Workflows())
	h := &handlers.Handler{DB: db, RouteSession: s}
	a, err := s.CreateContext(t.Context(), replayInput())
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.Workflows().Load(t.Context(), "route", a.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w := replayPost(h, a.ID, "2026-11-08"); w.Code != 500 {
		t.Fatalf("commit fault: %d %s", w.Code, w.Body.String())
	}
	after, err := db.Workflows().Load(t.Context(), "route", a.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("commit failure changed session")
	}
	if _, n, err := db.Events().List(t.Context(), 10, 0); err != nil || n != 0 {
		t.Fatalf("partial event: %d %v", n, err)
	}
	if _, err = conn.Exec(t.Context(), `DROP TRIGGER reject_event_commit ON workflow_sessions`); err != nil {
		t.Fatal(err)
	}
	if w := replayPost(h, a.ID, "2026-11-08"); w.Code != 201 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	if _, n, err := db.Events().List(t.Context(), 10, 0); err != nil || n != 1 {
		t.Fatalf("retry event count: %d %v", n, err)
	}
}
