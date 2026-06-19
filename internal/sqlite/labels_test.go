package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"

	_ "modernc.org/sqlite"
)

func TestLabelRepository_CRUDCountsAndUniqueness(t *testing.T) {
	store := newTestLabelStore(t)
	ctx := context.Background()

	label, err := store.Labels().Create(ctx, &models.Label{Name: "  Youth Conference  "})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if label.Name != "Youth Conference" {
		t.Fatalf("label.Name = %q, want trimmed name", label.Name)
	}

	if _, err := store.Labels().Create(ctx, &models.Label{Name: "Youth Conference"}); err == nil {
		t.Fatal("Create() duplicate error = nil, want unique constraint error")
	}

	participant, err := store.Participants().Create(ctx, &models.Participant{
		Name:    "Rider One",
		Address: "1 Rider Way",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	driver, err := store.Drivers().Create(ctx, &models.Driver{
		Name:            "Driver One",
		Address:         "1 Driver Way",
		Lat:             40.2,
		Lng:             -73.8,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if err := store.Labels().SetLabelsForParticipant(ctx, participant.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() error = %v", err)
	}
	if err := store.Labels().SetLabelsForDriver(ctx, driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	labels, err := store.Labels().List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(labels) != 1 {
		t.Fatalf("List() len = %d, want 1", len(labels))
	}
	if labels[0].ParticipantCount != 1 || labels[0].DriverCount != 1 {
		t.Fatalf("counts = participants:%d drivers:%d, want 1/1", labels[0].ParticipantCount, labels[0].DriverCount)
	}

	label.Name = "Updated Label"
	updated, err := store.Labels().Update(ctx, label)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "Updated Label" {
		t.Fatalf("updated.Name = %q, want Updated Label", updated.Name)
	}

	if err := store.Labels().Delete(ctx, label.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := store.Labels().Delete(ctx, label.ID); err != database.ErrNotFound {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
	assertRowCount(t, store.db, "participant_labels", 0)
	assertRowCount(t, store.db, "driver_labels", 0)
}

func TestLabelRepository_GetByIDsReturnsExistingLabels(t *testing.T) {
	store := newTestLabelStore(t)
	ctx := context.Background()

	first, err := store.Labels().Create(ctx, &models.Label{Name: "First"})
	if err != nil {
		t.Fatalf("create first label: %v", err)
	}
	second, err := store.Labels().Create(ctx, &models.Label{Name: "Second"})
	if err != nil {
		t.Fatalf("create second label: %v", err)
	}

	labels, err := store.Labels().GetByIDs(ctx, []int64{second.ID, first.ID, second.ID, 9999})
	if err != nil {
		t.Fatalf("GetByIDs() error = %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("labels = %#v, want two existing unique labels", labels)
	}
	found := map[int64]bool{}
	for _, label := range labels {
		found[label.ID] = true
	}
	if !found[first.ID] || !found[second.ID] {
		t.Fatalf("labels = %#v, want first and second labels", labels)
	}
}

func TestLabelRepository_SetAndBulkMembershipsAreIdempotent(t *testing.T) {
	store := newTestLabelStore(t)
	ctx := context.Background()

	firstLabel, err := store.Labels().Create(ctx, &models.Label{Name: "First"})
	if err != nil {
		t.Fatalf("create first label: %v", err)
	}
	secondLabel, err := store.Labels().Create(ctx, &models.Label{Name: "Second"})
	if err != nil {
		t.Fatalf("create second label: %v", err)
	}
	participantOne := createTestParticipant(t, store, "Rider One")
	participantTwo := createTestParticipant(t, store, "Rider Two")
	driver := createTestDriver(t, store, "Driver One")

	if err := store.Labels().SetLabelsForParticipant(ctx, participantOne.ID, []int64{firstLabel.ID, firstLabel.ID, 0, secondLabel.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() error = %v", err)
	}
	participantLabels, err := store.Labels().ListLabelsForParticipant(ctx, participantOne.ID)
	if err != nil {
		t.Fatalf("ListLabelsForParticipant() error = %v", err)
	}
	if len(participantLabels) != 2 {
		t.Fatalf("participant label count = %d, want 2", len(participantLabels))
	}

	if err := store.Labels().SetLabelsForParticipant(ctx, participantOne.ID, []int64{secondLabel.ID}); err != nil {
		t.Fatalf("replace SetLabelsForParticipant() error = %v", err)
	}
	labelIDs, err := store.Labels().ListLabelIDsForParticipants(ctx)
	if err != nil {
		t.Fatalf("ListLabelIDsForParticipants() error = %v", err)
	}
	if got := labelIDs[participantOne.ID]; len(got) != 1 || got[0] != secondLabel.ID {
		t.Fatalf("participant label IDs = %#v, want [%d]", got, secondLabel.ID)
	}

	if err := store.Labels().AddLabelToParticipants(ctx, firstLabel.ID, []int64{participantOne.ID, participantTwo.ID, participantTwo.ID}); err != nil {
		t.Fatalf("AddLabelToParticipants() error = %v", err)
	}
	if err := store.Labels().AddLabelToParticipants(ctx, firstLabel.ID, []int64{participantOne.ID}); err != nil {
		t.Fatalf("second AddLabelToParticipants() error = %v", err)
	}
	labelIDs, err = store.Labels().ListLabelIDsForParticipants(ctx)
	if err != nil {
		t.Fatalf("ListLabelIDsForParticipants() after add error = %v", err)
	}
	if got := labelIDs[participantTwo.ID]; len(got) != 1 || got[0] != firstLabel.ID {
		t.Fatalf("participant two label IDs = %#v, want [%d]", got, firstLabel.ID)
	}

	if err := store.Labels().RemoveLabelFromParticipants(ctx, firstLabel.ID, []int64{participantOne.ID, participantTwo.ID}); err != nil {
		t.Fatalf("RemoveLabelFromParticipants() error = %v", err)
	}
	if err := store.Labels().RemoveLabelFromParticipants(ctx, firstLabel.ID, []int64{participantOne.ID}); err != nil {
		t.Fatalf("second RemoveLabelFromParticipants() error = %v", err)
	}
	labelIDs, err = store.Labels().ListLabelIDsForParticipants(ctx)
	if err != nil {
		t.Fatalf("ListLabelIDsForParticipants() after remove error = %v", err)
	}
	if got := labelIDs[participantTwo.ID]; len(got) != 0 {
		t.Fatalf("participant two label IDs after remove = %#v, want empty", got)
	}

	if err := store.Labels().SetLabelsForDriver(ctx, driver.ID, []int64{firstLabel.ID, secondLabel.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}
	if err := store.Labels().RemoveLabelFromDrivers(ctx, firstLabel.ID, []int64{driver.ID}); err != nil {
		t.Fatalf("RemoveLabelFromDrivers() error = %v", err)
	}
	driverLabels, err := store.Labels().ListLabelsForDriver(ctx, driver.ID)
	if err != nil {
		t.Fatalf("ListLabelsForDriver() error = %v", err)
	}
	if len(driverLabels) != 1 || driverLabels[0].ID != secondLabel.ID {
		t.Fatalf("driver labels = %#v, want second label only", driverLabels)
	}
}

func TestParticipantRepository_CreateWithLabelsRollsBackOnInvalidLabel(t *testing.T) {
	store := newTestLabelStore(t)
	ctx := context.Background()

	_, err := store.Participants().CreateWithLabels(ctx, &models.Participant{
		Name:    "Rider With Bad Label",
		Address: "1 Rider Way",
		Lat:     40.1,
		Lng:     -73.9,
	}, []int64{9999})
	if err == nil {
		t.Fatal("CreateWithLabels() error = nil, want invalid label error")
	}

	assertRowCount(t, store.db, "participants", 0)
	assertRowCount(t, store.db, "participant_labels", 0)
}

func TestParticipantRepository_UpdateWithLabelsRollsBackOnInvalidLabel(t *testing.T) {
	store := newTestLabelStore(t)
	ctx := context.Background()
	participant := createTestParticipant(t, store, "Original Rider")

	participant.Name = "Changed Rider"
	_, err := store.Participants().UpdateWithLabels(ctx, participant, []int64{9999})
	if err == nil {
		t.Fatal("UpdateWithLabels() error = nil, want invalid label error")
	}

	unchanged, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if unchanged.Name != "Original Rider" {
		t.Fatalf("participant name = %q, want rollback to Original Rider", unchanged.Name)
	}
	assertRowCount(t, store.db, "participant_labels", 0)
}

func TestDriverRepository_CreateWithLabelsRollsBackOnInvalidLabel(t *testing.T) {
	store := newTestLabelStore(t)
	ctx := context.Background()

	_, err := store.Drivers().CreateWithLabels(ctx, &models.Driver{
		Name:            "Driver With Bad Label",
		Address:         "1 Driver Way",
		Lat:             40.1,
		Lng:             -73.9,
		VehicleCapacity: 4,
	}, []int64{9999})
	if err == nil {
		t.Fatal("CreateWithLabels() error = nil, want invalid label error")
	}

	assertRowCount(t, store.db, "drivers", 0)
	assertRowCount(t, store.db, "driver_labels", 0)
}

func TestDriverRepository_UpdateWithLabelsRollsBackOnInvalidLabel(t *testing.T) {
	store := newTestLabelStore(t)
	ctx := context.Background()
	driver := createTestDriver(t, store, "Original Driver")

	driver.Name = "Changed Driver"
	_, err := store.Drivers().UpdateWithLabels(ctx, driver, []int64{9999})
	if err == nil {
		t.Fatal("UpdateWithLabels() error = nil, want invalid label error")
	}

	unchanged, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if unchanged.Name != "Original Driver" {
		t.Fatalf("driver name = %q, want rollback to Original Driver", unchanged.Name)
	}
	assertRowCount(t, store.db, "driver_labels", 0)
}

func TestStoreMigratesV3DatabaseToV4LabelsSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v3-label-migration.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY);
		INSERT INTO schema_version (version) VALUES (3);
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
		t.Fatalf("seed v3 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	assertSchemaVersion(t, store.db, 4)
	for _, tableName := range []string{"labels", "participant_labels", "driver_labels"} {
		assertTableExists(t, store.db, tableName)
	}
	assertRowCount(t, store.db, "participants", 1)
	assertRowCount(t, store.db, "drivers", 1)
}

func newTestLabelStore(t *testing.T) *Store {
	t.Helper()

	store, err := New(filepath.Join(t.TempDir(), "labels-test.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return store
}

func createTestParticipant(t *testing.T, store *Store, name string) *models.Participant {
	t.Helper()

	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    name,
		Address: "1 Rider Way",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create participant %q: %v", name, err)
	}
	return participant
}

func createTestDriver(t *testing.T, store *Store, name string) *models.Driver {
	t.Helper()

	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            name,
		Address:         "1 Driver Way",
		Lat:             40.2,
		Lng:             -73.8,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver %q: %v", name, err)
	}
	return driver
}
