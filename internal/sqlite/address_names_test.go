package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"ride-home-router/internal/models"
	"testing"
)

func TestParticipantRepositoryAddressNameRoundTrip(t *testing.T) {
	store := newAddressNameTestStore(t)
	ctx := context.Background()

	participant, err := store.Participants().CreateWithLabels(ctx, &models.Participant{
		Name:        "Rider One",
		Address:     "1000 Collins Crossing Dr",
		AddressName: "Collins Crossing",
		Lat:         40.1,
		Lng:         -73.9,
	}, nil)
	if err != nil {
		t.Fatalf("CreateWithLabels() error = %v", err)
	}
	if participant.AddressName != "Collins Crossing" {
		t.Fatalf("created AddressName = %q, want Collins Crossing", participant.AddressName)
	}

	got, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.AddressName != "Collins Crossing" {
		t.Fatalf("GetByID() AddressName = %q, want Collins Crossing", got.AddressName)
	}

	participants, err := store.Participants().List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(participants) != 1 || participants[0].AddressName != "Collins Crossing" {
		t.Fatalf("List() participants = %#v, want address name", participants)
	}

	got.AddressName = "Community Center"
	if _, err := store.Participants().Update(ctx, got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil {
		t.Fatalf("GetByID() after update error = %v", err)
	}
	if updated.AddressName != "Community Center" {
		t.Fatalf("updated AddressName = %q, want Community Center", updated.AddressName)
	}
}

func TestDriverRepositoryAddressNameRoundTrip(t *testing.T) {
	store := newAddressNameTestStore(t)
	ctx := context.Background()

	driver, err := store.Drivers().CreateWithLabels(ctx, &models.Driver{
		Name:            "Driver One",
		Address:         "200 Driver Lane",
		AddressName:     "North Campus",
		Lat:             40.2,
		Lng:             -73.8,
		VehicleCapacity: 4,
	}, nil)
	if err != nil {
		t.Fatalf("CreateWithLabels() error = %v", err)
	}
	if driver.AddressName != "North Campus" {
		t.Fatalf("created AddressName = %q, want North Campus", driver.AddressName)
	}

	got, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.AddressName != "North Campus" {
		t.Fatalf("GetByID() AddressName = %q, want North Campus", got.AddressName)
	}

	drivers, err := store.Drivers().List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(drivers) != 1 || drivers[0].AddressName != "North Campus" {
		t.Fatalf("List() drivers = %#v, want address name", drivers)
	}

	got.AddressName = "South Campus"
	if _, err := store.Drivers().Update(ctx, got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil {
		t.Fatalf("GetByID() after update error = %v", err)
	}
	if updated.AddressName != "South Campus" {
		t.Fatalf("updated AddressName = %q, want South Campus", updated.AddressName)
	}
}

func TestStoreMigratesV4DatabaseToV5AddressNames(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v4-address-name-migration.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY);
		INSERT INTO schema_version (version) VALUES (4);
		CREATE TABLE participants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			address TEXT NOT NULL,
			lat REAL NOT NULL,
			lng REAL NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE drivers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			address TEXT NOT NULL,
			lat REAL NOT NULL,
			lng REAL NOT NULL,
			vehicle_capacity INTEGER NOT NULL DEFAULT 4,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO participants (name, address, lat, lng) VALUES ('Rider', '1 Rider Way', 40.1, -73.9);
		INSERT INTO drivers (name, address, lat, lng, vehicle_capacity) VALUES ('Driver', '1 Driver Way', 40.2, -73.8, 4);
	`)
	if err != nil {
		t.Fatalf("seed v4 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assertSchemaVersion(t, store.db, 6)
	for _, tableName := range []string{"participants", "drivers"} {
		exists, err := columnExists(store.db, tableName, "address_name")
		if err != nil {
			t.Fatalf("columnExists(%s.address_name) error = %v", tableName, err)
		}
		if !exists {
			t.Fatalf("expected %s.address_name after migration", tableName)
		}
	}

	participant, err := store.Participants().GetByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("get migrated participant: %v", err)
	}
	if participant.AddressName != "" {
		t.Fatalf("migrated participant AddressName = %q, want empty", participant.AddressName)
	}
	driver, err := store.Drivers().GetByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("get migrated driver: %v", err)
	}
	if driver.AddressName != "" {
		t.Fatalf("migrated driver AddressName = %q, want empty", driver.AddressName)
	}
}

func newAddressNameTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "address-names.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
