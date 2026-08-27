package postgres_test

import (
	"context"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"testing"

	"github.com/jackc/pgx/v5"
)

func createTestParticipant(t *testing.T, store *postgres.Store, name string) *models.Participant {
	t.Helper()
	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name: name, Address: "1 Rider Way", Lat: 40.1, Lng: -73.9,
	})
	if err != nil {
		t.Fatalf("create participant %q: %v", name, err)
	}
	return participant
}

func createTestDriver(t *testing.T, store *postgres.Store, name string) *models.Driver {
	t.Helper()
	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name: name, Address: "1 Driver Way", Lat: 40.2, Lng: -73.8, VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver %q: %v", name, err)
	}
	return driver
}

// execSQL runs raw SQL against the test schema for fault injection.
func execSQL(t *testing.T, databaseURL, query string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect for raw SQL: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, query); err != nil {
		t.Fatalf("exec raw SQL: %v", err)
	}
}

func openStore(t *testing.T, databaseURL string) *postgres.Store {
	t.Helper()
	store, err := postgres.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
