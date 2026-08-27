package postgres_test

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"sync"
	"testing"
)

const failOnNameTrigger = `
	CREATE FUNCTION fail_on_name() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.name = 'FAIL' THEN RAISE EXCEPTION 'injected batch failure'; END IF;
		RETURN NEW;
	END $$;
	CREATE TRIGGER fail_participant_batch BEFORE INSERT ON participants FOR EACH ROW EXECUTE FUNCTION fail_on_name();
	CREATE TRIGGER fail_driver_batch BEFORE INSERT ON drivers FOR EACH ROW EXECUTE FUNCTION fail_on_name();`

func TestCreateBatchRollsBackOnMidBatchFailure(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	execSQL(t, databaseURL, failOnNameTrigger)
	store := openStore(t, databaseURL)
	ctx := context.Background()

	participants := []*models.Participant{
		{Name: "First", Address: "1 Main St", Lat: 40.1, Lng: -73.1},
		{Name: "FAIL", Address: "2 Main St", Lat: 40.2, Lng: -73.2},
		{Name: "Third", Address: "3 Main St", Lat: 40.3, Lng: -73.3},
	}
	if _, err := store.Participants().CreateBatch(ctx, participants, nil); err == nil {
		t.Fatal("participant CreateBatch() error = nil, want injected failure")
	}
	if list, err := store.Participants().List(ctx, ""); err != nil || len(list) != 0 {
		t.Fatalf("participants after failed batch = %#v, %v; want none", list, err)
	}
	for i, participant := range participants {
		if participant.ID != 0 {
			t.Errorf("participants[%d].ID = %d after rollback, want 0", i, participant.ID)
		}
	}

	drivers := []*models.Driver{
		{Name: "First", Address: "1 Main St", Lat: 40.1, Lng: -73.1, VehicleCapacity: 4},
		{Name: "FAIL", Address: "2 Main St", Lat: 40.2, Lng: -73.2, VehicleCapacity: 4},
	}
	if _, err := store.Drivers().CreateBatch(ctx, drivers, nil); err == nil {
		t.Fatal("driver CreateBatch() error = nil, want injected failure")
	}
	if list, err := store.Drivers().List(ctx, ""); err != nil || len(list) != 0 {
		t.Fatalf("drivers after failed batch = %#v, %v; want none", list, err)
	}
}

func TestCreateBatchRechecksNormalizedDuplicatesInsideTransaction(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	if _, err := store.Participants().Create(ctx, &models.Participant{Name: "Jane Doe", Address: "1 Main St", Lat: 40, Lng: -73}); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	if _, err := store.Drivers().Create(ctx, &models.Driver{Name: "John Doe", Address: "2 Main St", Lat: 41, Lng: -74, VehicleCapacity: 4}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}

	participants := []*models.Participant{
		{Name: " jane   DOE ", Address: " 1 MAIN st ", Lat: 40, Lng: -73},
		{Name: "New Rider", Address: "3 Main St", Lat: 42, Lng: -75},
		{Name: "Another Rider", Address: "5 Main St", Lat: 44, Lng: -77},
	}
	participantResult, err := store.Participants().CreateBatch(ctx, participants, nil)
	if err != nil {
		t.Fatalf("participant CreateBatch() error = %v", err)
	}
	if participantResult != (database.BatchCreateResult{Created: 2, SkippedDuplicate: 1}) {
		t.Fatalf("participant CreateBatch() result = %#v", participantResult)
	}
	if participants[0].ID != 0 || participants[1].ID == 0 || participants[2].ID == 0 {
		t.Fatalf("participant IDs = [%d %d %d], want [0 created created]", participants[0].ID, participants[1].ID, participants[2].ID)
	}

	drivers := []*models.Driver{
		{Name: " JOHN doe ", Address: "2   MAIN ST", Lat: 41, Lng: -74, VehicleCapacity: 4},
		{Name: "New Driver", Address: "4 Main St", Lat: 43, Lng: -76, VehicleCapacity: 5},
	}
	driverResult, err := store.Drivers().CreateBatch(ctx, drivers, nil)
	if err != nil {
		t.Fatalf("driver CreateBatch() error = %v", err)
	}
	if driverResult != (database.BatchCreateResult{Created: 1, SkippedDuplicate: 1}) {
		t.Fatalf("driver CreateBatch() result = %#v", driverResult)
	}
	if drivers[0].ID != 0 || drivers[1].ID == 0 {
		t.Fatalf("driver IDs = [%d %d], want [0 created]", drivers[0].ID, drivers[1].ID)
	}

	if list, err := store.Participants().List(ctx, ""); err != nil || len(list) != 3 {
		t.Fatalf("participants after batch = %d, err=%v, want 3", len(list), err)
	}
	if list, err := store.Drivers().List(ctx, ""); err != nil || len(list) != 2 {
		t.Fatalf("drivers after batch = %d, err=%v, want 2", len(list), err)
	}
}

func TestCreateBatchAllowsOnlyPreviewKnownDuplicateOverrides(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	for _, participant := range []*models.Participant{
		{Name: "Known At Preview", Address: "1 Main St", Lat: 40, Lng: -73},
		{Name: "Appeared Later", Address: "2 Main St", Lat: 41, Lng: -74},
	} {
		if _, err := store.Participants().Create(ctx, participant); err != nil {
			t.Fatalf("seed participant: %v", err)
		}
	}
	batch := []*models.Participant{
		{Name: " known at preview ", Address: "1 MAIN ST", Lat: 40, Lng: -73},
		{Name: "appeared later", Address: " 2 MAIN ST ", Lat: 41, Lng: -74},
	}
	result, err := store.Participants().CreateBatch(ctx, batch, []bool{true, false})
	if err != nil {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	if result != (database.BatchCreateResult{Created: 1, SkippedDuplicate: 1}) {
		t.Fatalf("CreateBatch() result = %#v", result)
	}
	if batch[0].ID == 0 || batch[1].ID != 0 {
		t.Fatalf("batch IDs = [%d %d], want [created 0]", batch[0].ID, batch[1].ID)
	}
}

func TestCreateBatchSerializesConcurrentDuplicateRechecks(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	const workers = 8

	var wg sync.WaitGroup
	results := make([]database.BatchCreateResult, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Go(func() {
			results[i], errs[i] = store.Participants().CreateBatch(ctx, []*models.Participant{
				{Name: "Same Rider", Address: "1 Main St", Lat: 40, Lng: -73},
			}, nil)
		})
	}
	wg.Wait()

	created, skipped := 0, 0
	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("CreateBatch() worker %d error = %v", i, errs[i])
		}
		created += results[i].Created
		skipped += results[i].SkippedDuplicate
	}
	if created != 1 || skipped != workers-1 {
		t.Fatalf("created = %d skipped = %d, want exactly one insert across %d concurrent batches", created, skipped, workers)
	}
	if list, err := store.Participants().List(ctx, ""); err != nil || len(list) != 1 {
		t.Fatalf("participants = %d, err=%v, want 1", len(list), err)
	}
}
