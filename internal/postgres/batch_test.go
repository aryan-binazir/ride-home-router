package postgres_test

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"sync"
	"testing"
	"time"
)

type rosterBatchSpec[T any] struct {
	noun        string
	newEntity   func(name, address string) *T
	createOne   func(context.Context, *postgres.Store, *T) error
	createBatch func(context.Context, *postgres.Store, []*T, []bool) (database.BatchCreateResult, error)
	id          func(*T) int64
	timestamps  func(*T) (time.Time, time.Time)
	count       func(context.Context, *postgres.Store) (int, error)
}

func participantBatchSpec() rosterBatchSpec[models.Participant] {
	return rosterBatchSpec[models.Participant]{
		noun: "participant",
		newEntity: func(name, address string) *models.Participant {
			return &models.Participant{Name: name, Address: address, Lat: 40, Lng: -73}
		},
		createOne: func(ctx context.Context, store *postgres.Store, participant *models.Participant) error {
			_, err := store.Participants().Create(ctx, participant)
			return err
		},
		createBatch: func(ctx context.Context, store *postgres.Store, participants []*models.Participant, allow []bool) (database.BatchCreateResult, error) {
			return store.Participants().CreateBatch(ctx, participants, allow)
		},
		id: func(participant *models.Participant) int64 { return participant.ID },
		timestamps: func(participant *models.Participant) (time.Time, time.Time) {
			return participant.CreatedAt, participant.UpdatedAt
		},
		count: func(ctx context.Context, store *postgres.Store) (int, error) {
			participants, err := store.Participants().List(ctx, "")
			return len(participants), err
		},
	}
}

func driverBatchSpec() rosterBatchSpec[models.Driver] {
	return rosterBatchSpec[models.Driver]{
		noun: "driver",
		newEntity: func(name, address string) *models.Driver {
			return &models.Driver{Name: name, Address: address, Lat: 40, Lng: -73, VehicleCapacity: 4}
		},
		createOne: func(ctx context.Context, store *postgres.Store, driver *models.Driver) error {
			_, err := store.Drivers().Create(ctx, driver)
			return err
		},
		createBatch: func(ctx context.Context, store *postgres.Store, drivers []*models.Driver, allow []bool) (database.BatchCreateResult, error) {
			return store.Drivers().CreateBatch(ctx, drivers, allow)
		},
		id: func(driver *models.Driver) int64 { return driver.ID },
		timestamps: func(driver *models.Driver) (time.Time, time.Time) {
			return driver.CreatedAt, driver.UpdatedAt
		},
		count: func(ctx context.Context, store *postgres.Store) (int, error) {
			drivers, err := store.Drivers().List(ctx, "")
			return len(drivers), err
		},
	}
}

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
	for i, driver := range drivers {
		if driver.ID != 0 {
			t.Errorf("drivers[%d].ID = %d after rollback, want 0", i, driver.ID)
		}
	}
}

func TestCreateBatchRechecksNormalizedDuplicatesInsideTransaction(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	if _, err := store.Participants().Create(ctx, &models.Participant{Name: "Jane Doe", Address: "1 Main St", Lat: 40, Lng: -73}); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	if _, err := store.Participants().Create(ctx, &models.Participant{Name: "Anne-Marie O'Brien", Address: "4 Main St.", Lat: 43, Lng: -76}); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	if _, err := store.Drivers().Create(ctx, &models.Driver{Name: "John Doe", Address: "2 Main St", Lat: 41, Lng: -74, VehicleCapacity: 4}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	if _, err := store.Drivers().Create(ctx, &models.Driver{Name: "J.R. Smith-Jones", Address: "6 Main St, Apt 2", Lat: 45, Lng: -78, VehicleCapacity: 4}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}

	participants := []*models.Participant{
		{Name: " jane   DOE ", Address: " 1 MAIN st ", Lat: 40, Lng: -73},
		{Name: "Anne Marie O’Brien", Address: "4 Main St", Lat: 43, Lng: -76},
		{Name: "New Rider", Address: "3 Main St", Lat: 42, Lng: -75},
		{Name: "Another Rider", Address: "5 Main St", Lat: 44, Lng: -77},
	}
	participantResult, err := store.Participants().CreateBatch(ctx, participants, nil)
	if err != nil {
		t.Fatalf("participant CreateBatch() error = %v", err)
	}
	if participantResult != (database.BatchCreateResult{Created: 2, SkippedDuplicate: 2}) {
		t.Fatalf("participant CreateBatch() result = %#v", participantResult)
	}
	if participants[0].ID != 0 || participants[1].ID != 0 || participants[2].ID == 0 || participants[3].ID == 0 {
		t.Fatalf("participant IDs = [%d %d %d %d], want [0 0 created created]", participants[0].ID, participants[1].ID, participants[2].ID, participants[3].ID)
	}

	drivers := []*models.Driver{
		{Name: " JOHN doe ", Address: "2   MAIN ST", Lat: 41, Lng: -74, VehicleCapacity: 4},
		{Name: "JR Smith Jones", Address: "6 Main St Apt 2", Lat: 45, Lng: -78, VehicleCapacity: 4},
		{Name: "New Driver", Address: "4 Main St", Lat: 43, Lng: -76, VehicleCapacity: 5},
	}
	driverResult, err := store.Drivers().CreateBatch(ctx, drivers, nil)
	if err != nil {
		t.Fatalf("driver CreateBatch() error = %v", err)
	}
	if driverResult != (database.BatchCreateResult{Created: 1, SkippedDuplicate: 2}) {
		t.Fatalf("driver CreateBatch() result = %#v", driverResult)
	}
	if drivers[0].ID != 0 || drivers[1].ID != 0 || drivers[2].ID == 0 {
		t.Fatalf("driver IDs = [%d %d %d], want [0 0 created]", drivers[0].ID, drivers[1].ID, drivers[2].ID)
	}

	if list, err := store.Participants().List(ctx, ""); err != nil || len(list) != 4 {
		t.Fatalf("participants after batch = %d, err=%v, want 4", len(list), err)
	}
	if list, err := store.Drivers().List(ctx, ""); err != nil || len(list) != 3 {
		t.Fatalf("drivers after batch = %d, err=%v, want 3", len(list), err)
	}
}

func TestCreateBatchAllowsOnlyPreviewKnownDuplicateOverrides(t *testing.T) {
	t.Run("participants", func(t *testing.T) {
		testCreateBatchAllowsOnlyPreviewKnownDuplicateOverrides(t, participantBatchSpec())
	})
	t.Run("drivers", func(t *testing.T) {
		testCreateBatchAllowsOnlyPreviewKnownDuplicateOverrides(t, driverBatchSpec())
	})
}

func testCreateBatchAllowsOnlyPreviewKnownDuplicateOverrides[T any](t *testing.T, spec rosterBatchSpec[T]) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	for _, entity := range []*T{
		spec.newEntity("Skip Existing", "1 Main St"),
		spec.newEntity("Known At Preview", "2 Main St"),
	} {
		if err := spec.createOne(ctx, store, entity); err != nil {
			t.Fatalf("seed %s: %v", spec.noun, err)
		}
	}
	batch := []*T{
		spec.newEntity(" skip existing ", " 1 MAIN ST "),
		spec.newEntity(" known at preview ", " 2 MAIN ST "),
		spec.newEntity("New Entry", "3 Main St"),
	}
	result, err := spec.createBatch(ctx, store, batch, []bool{false, true, false})
	if err != nil {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	if result != (database.BatchCreateResult{Created: 2, SkippedDuplicate: 1}) {
		t.Fatalf("CreateBatch() result = %#v", result)
	}
	if spec.id(batch[0]) != 0 || spec.id(batch[1]) == 0 || spec.id(batch[2]) == 0 {
		t.Fatalf("batch IDs = [%d %d %d], want [0 created created]", spec.id(batch[0]), spec.id(batch[1]), spec.id(batch[2]))
	}
}

func TestCreateBatchSerializesConcurrentDuplicateRechecks(t *testing.T) {
	t.Run("participants", func(t *testing.T) {
		testCreateBatchSerializesConcurrentDuplicateRechecks(t, participantBatchSpec())
	})
	t.Run("drivers", func(t *testing.T) {
		testCreateBatchSerializesConcurrentDuplicateRechecks(t, driverBatchSpec())
	})
}

func testCreateBatchSerializesConcurrentDuplicateRechecks[T any](t *testing.T, spec rosterBatchSpec[T]) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	const workers = 8

	var wg sync.WaitGroup
	results := make([]database.BatchCreateResult, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Go(func() {
			results[i], errs[i] = spec.createBatch(ctx, store, []*T{
				spec.newEntity("Same Entry", "1 Main St"),
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
	if count, err := spec.count(ctx, store); err != nil || count != 1 {
		t.Fatalf("%ss = %d, err=%v, want 1", spec.noun, count, err)
	}
}

func TestCreateBatchBackfillsOnlyCommittedRows(t *testing.T) {
	t.Run("participants", func(t *testing.T) {
		testCreateBatchBackfillsOnlyCommittedRows(t, participantBatchSpec())
	})
	t.Run("drivers", func(t *testing.T) {
		testCreateBatchBackfillsOnlyCommittedRows(t, driverBatchSpec())
	})
}

func testCreateBatchBackfillsOnlyCommittedRows[T any](t *testing.T, spec rosterBatchSpec[T]) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	batch := []*T{
		spec.newEntity("First", "1 Main St"),
		spec.newEntity("Second", "2 Main St"),
	}
	result, err := spec.createBatch(ctx, store, batch, nil)
	if err != nil {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	if result != (database.BatchCreateResult{Created: 2}) {
		t.Fatalf("CreateBatch() result = %#v, want 2 created", result)
	}
	for i, entity := range batch {
		createdAt, updatedAt := spec.timestamps(entity)
		if spec.id(entity) == 0 || createdAt.IsZero() || !createdAt.Equal(updatedAt) {
			t.Errorf("batch[%d] id=%d created=%v updated=%v, want committed ID and equal non-zero timestamps", i, spec.id(entity), createdAt, updatedAt)
		}
	}
}

func TestCreateBatchPreservesWithinBatchDuplicates(t *testing.T) {
	t.Run("participants", func(t *testing.T) {
		testCreateBatchPreservesWithinBatchDuplicates(t, participantBatchSpec())
	})
	t.Run("drivers", func(t *testing.T) {
		testCreateBatchPreservesWithinBatchDuplicates(t, driverBatchSpec())
	})
}

func testCreateBatchPreservesWithinBatchDuplicates[T any](t *testing.T, spec rosterBatchSpec[T]) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	batch := []*T{
		spec.newEntity("Same Entry", "1 Main St"),
		spec.newEntity(" same entry ", " 1 MAIN ST "),
	}
	result, err := spec.createBatch(ctx, store, batch, nil)
	if err != nil {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	if result != (database.BatchCreateResult{Created: 2}) {
		t.Fatalf("CreateBatch() result = %#v, want both incoming duplicates created", result)
	}
	if spec.id(batch[0]) == 0 || spec.id(batch[1]) == 0 {
		t.Fatalf("batch IDs = [%d %d], want both created", spec.id(batch[0]), spec.id(batch[1]))
	}
}

func TestCreateBatchNilEntityRollsBackWithoutBackfill(t *testing.T) {
	t.Run("participants", func(t *testing.T) {
		testCreateBatchNilEntityRollsBackWithoutBackfill(t, participantBatchSpec())
	})
	t.Run("drivers", func(t *testing.T) {
		testCreateBatchNilEntityRollsBackWithoutBackfill(t, driverBatchSpec())
	})
}

func testCreateBatchNilEntityRollsBackWithoutBackfill[T any](t *testing.T, spec rosterBatchSpec[T]) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	first := spec.newEntity("First", "1 Main St")
	result, err := spec.createBatch(ctx, store, []*T{first, nil}, nil)
	if err == nil || err.Error() != spec.noun+" batch contains a nil "+spec.noun {
		t.Fatalf("CreateBatch() error = %v, want nil %s error", err, spec.noun)
	}
	if result != (database.BatchCreateResult{}) {
		t.Fatalf("CreateBatch() result = %#v, want zero value", result)
	}
	createdAt, updatedAt := spec.timestamps(first)
	if spec.id(first) != 0 || !createdAt.IsZero() || !updatedAt.IsZero() {
		t.Fatalf("first entity mutated after rollback: id=%d created=%v updated=%v", spec.id(first), createdAt, updatedAt)
	}
	if count, countErr := spec.count(ctx, store); countErr != nil || count != 0 {
		t.Fatalf("%ss after rollback = %d, err=%v, want 0", spec.noun, count, countErr)
	}
}
