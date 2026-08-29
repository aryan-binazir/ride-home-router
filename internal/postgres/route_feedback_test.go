package postgres_test

import (
	"context"
	"encoding/json"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routefeedback"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRouteFeedbackCreateIsIdempotentAndCascadesWithEvent(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	store, err := postgres.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect for assertions: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	event, err := store.Events().Create(context.Background(), &models.Event{
		EventDate: time.Date(2026, time.August, 29, 0, 0, 0, 0, time.UTC), Mode: models.RouteModeDropoff,
	}, []models.EventRoute{{
		RouteOrder: 0, DriverID: 1, DriverName: "Driver", DriverAddress: "1 Road", EffectiveCapacity: 4, Mode: models.RouteModeDropoff,
		Stops: []models.EventRouteStop{{Order: 0, ParticipantID: 10, ParticipantName: "Rider", ParticipantAddress: "10 Road"}},
	}}, &models.EventSummary{TotalParticipants: 1, TotalDrivers: 1, Mode: models.RouteModeDropoff})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	record := &routefeedback.Record{
		EventID: event.ID, SessionID: "session-1", SMEEmail: "sme@example.com", SchemaVersion: routefeedback.SchemaVersion,
		Mode: models.RouteModeDropoff,
		Input: routefeedback.Input{
			Activity:     routefeedback.Activity{ID: 100, Lat: 35, Lng: -78},
			Drivers:      []routefeedback.Driver{{ID: 1, Address: "1 Road", Lat: 35.1, Lng: -78.1, Capacity: 4}},
			Participants: []routefeedback.Participant{{ID: 10, Address: "10 Road", Lat: 35.2, Lng: -78.2}},
		},
		Proposed: []routefeedback.Route{{DriverID: 1, ParticipantIDs: []int64{10}, TotalDistanceMeters: 1000}},
		Final:    []routefeedback.Route{{DriverID: 1, ParticipantIDs: []int64{10}, TotalDistanceMeters: 1000}},
	}
	if err := store.RouteFeedback().Create(context.Background(), record); err != nil {
		t.Fatalf("create feedback: %v", err)
	}
	record.Final[0].TotalDistanceMeters = 9999
	if err := store.RouteFeedback().Create(context.Background(), record); err != nil {
		t.Fatalf("duplicate feedback: %v", err)
	}

	var count int
	var inputJSON, proposedJSON, finalJSON []byte
	if err := conn.QueryRow(context.Background(), `SELECT COUNT(*), min(input::text), min(proposed::text), min(final::text) FROM route_feedback WHERE session_id = $1`, record.SessionID).
		Scan(&count, &inputJSON, &proposedJSON, &finalJSON); err != nil {
		t.Fatalf("query feedback: %v", err)
	}
	if count != 1 {
		t.Fatalf("feedback rows = %d, want 1", count)
	}
	for label, payload := range map[string][]byte{"input": inputJSON, "proposed": proposedJSON, "final": finalJSON} {
		if !json.Valid(payload) {
			t.Fatalf("%s JSON invalid: %s", label, payload)
		}
	}
	var final []routefeedback.Route
	if err := json.Unmarshal(finalJSON, &final); err != nil {
		t.Fatalf("decode final JSON: %v", err)
	}
	if final[0].TotalDistanceMeters != 1000 {
		t.Fatalf("duplicate replaced stored final distance: %v", final[0].TotalDistanceMeters)
	}

	if err := store.Events().Delete(context.Background(), event.ID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	if err := conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM route_feedback WHERE event_id = $1`, event.ID).Scan(&count); err != nil {
		t.Fatalf("count feedback after event delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("feedback rows after event delete = %d, want 0", count)
	}
}
