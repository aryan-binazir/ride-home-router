package postgres_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRosterRetentionPreservesEventSnapshots(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	store, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	participant := createTestParticipant(t, store, "Saved rider")
	driver := createTestDriver(t, store, "Saved driver")
	event, err := store.Events().Create(t.Context(), &models.Event{EventDate: time.Now(), Mode: "dropoff"}, []models.EventRoute{{DriverID: driver.ID, DriverName: driver.Name, DriverAddress: driver.Address, EffectiveCapacity: 4, Mode: "dropoff", Stops: []models.EventRouteStop{{ParticipantID: participant.ID, ParticipantName: participant.Name, ParticipantAddress: participant.Address}}}}, &models.EventSummary{Mode: "dropoff", TotalParticipants: 1, TotalDrivers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Participants().Delete(t.Context(), participant.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Drivers().Delete(t.Context(), driver.ID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"UPDATE participants SET deleted_at=clock_timestamp()-interval '31 days'", "UPDATE drivers SET deleted_at=clock_timestamp()-interval '31 days'"} {
		if _, err = conn.Exec(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.CleanupWorkflows(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = store.Participants().Restore(t.Context(), participant.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("expired rider restored: %v", err)
	}
	if err = store.Drivers().Restore(t.Context(), driver.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("expired driver restored: %v", err)
	}
	_, routes, _, err := store.Events().GetByID(t.Context(), event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].DriverName != "Saved driver" || len(routes[0].Stops) != 1 || routes[0].Stops[0].ParticipantName != "Saved rider" {
		t.Fatalf("snapshot lost: %+v", routes)
	}
}

func TestProviderGateFailsFastForInvalidStoredCooldown(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	store, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err = conn.Exec(t.Context(), "UPDATE provider_throttles SET next_at=clock_timestamp()+interval '10 years'"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var cooldown *postgres.ProviderCooldownError
	if err = store.NominatimGate().Wait(ctx); !errors.As(err, &cooldown) {
		t.Fatalf("cooldown=%v", err)
	}
}
