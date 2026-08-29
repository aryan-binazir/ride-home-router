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
	createBatch func(context.Context, *postgres.Store, []*T) (database.BatchUpsertResult, error)
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
		createBatch: func(ctx context.Context, store *postgres.Store, participants []*models.Participant) (database.BatchUpsertResult, error) {
			return store.Participants().UpsertBatch(ctx, participants)
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
		createBatch: func(ctx context.Context, store *postgres.Store, drivers []*models.Driver) (database.BatchUpsertResult, error) {
			return store.Drivers().UpsertBatch(ctx, drivers)
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
	if _, err := store.Participants().UpsertBatch(ctx, participants); err == nil {
		t.Fatal("participant UpsertBatch() error = nil, want injected failure")
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
	if _, err := store.Drivers().UpsertBatch(ctx, drivers); err == nil {
		t.Fatal("driver UpsertBatch() error = nil, want injected failure")
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
	jane, err := store.Participants().Create(ctx, &models.Participant{Name: "Jane Doe", Address: "1 Main St", Lat: 40, Lng: -73})
	if err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	anne, err := store.Participants().Create(ctx, &models.Participant{Name: "Anne-Marie O'Brien", Address: "4 Main St.", Lat: 43, Lng: -76})
	if err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	john, err := store.Drivers().Create(ctx, &models.Driver{Name: "John Doe", Address: "2 Main St", Lat: 41, Lng: -74, VehicleCapacity: 4})
	if err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	jr, err := store.Drivers().Create(ctx, &models.Driver{Name: "J.R. Smith-Jones", Address: "6 Main St, Apt 2", Lat: 45, Lng: -78, VehicleCapacity: 4})
	if err != nil {
		t.Fatalf("seed driver: %v", err)
	}

	participants := []*models.Participant{
		{Name: " jane   DOE ", Address: " 1 MAIN st ", Lat: 40, Lng: -73},
		{Name: "Anne Marie O’Brien", Address: "4 Main St", Lat: 43, Lng: -76},
		{Name: "New Rider", Address: "3 Main St", Lat: 42, Lng: -75},
		{Name: "Another Rider", Address: "5 Main St", Lat: 44, Lng: -77},
	}
	participantResult, err := store.Participants().UpsertBatch(ctx, participants)
	if err != nil {
		t.Fatalf("participant UpsertBatch() error = %v", err)
	}
	if participantResult != (database.BatchUpsertResult{Created: 2, Updated: 2}) {
		t.Fatalf("participant UpsertBatch() result = %#v", participantResult)
	}
	if participants[0].ID != jane.ID || participants[1].ID != anne.ID || participants[2].ID == 0 || participants[3].ID == 0 {
		t.Fatalf("participant IDs = [%d %d %d %d], want [%d %d created created]", participants[0].ID, participants[1].ID, participants[2].ID, participants[3].ID, jane.ID, anne.ID)
	}

	drivers := []*models.Driver{
		{Name: " JOHN doe ", Address: "2   MAIN ST", Lat: 41, Lng: -74, VehicleCapacity: 4},
		{Name: "JR Smith Jones", Address: "6 Main St Apt 2", Lat: 45, Lng: -78, VehicleCapacity: 4},
		{Name: "New Driver", Address: "4 Main St", Lat: 43, Lng: -76, VehicleCapacity: 5},
	}
	driverResult, err := store.Drivers().UpsertBatch(ctx, drivers)
	if err != nil {
		t.Fatalf("driver UpsertBatch() error = %v", err)
	}
	if driverResult != (database.BatchUpsertResult{Created: 1, Updated: 2}) {
		t.Fatalf("driver UpsertBatch() result = %#v", driverResult)
	}
	if drivers[0].ID != john.ID || drivers[1].ID != jr.ID || drivers[2].ID == 0 {
		t.Fatalf("driver IDs = [%d %d %d], want [%d %d created]", drivers[0].ID, drivers[1].ID, drivers[2].ID, john.ID, jr.ID)
	}

	if list, err := store.Participants().List(ctx, ""); err != nil || len(list) != 4 {
		t.Fatalf("participants after batch = %d, err=%v, want 4", len(list), err)
	}
	if list, err := store.Drivers().List(ctx, ""); err != nil || len(list) != 3 {
		t.Fatalf("drivers after batch = %d, err=%v, want 3", len(list), err)
	}
}

func TestUpsertBatchIgnoresArchivedDuplicates(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	participant := &models.Participant{Name: "Archived Rider", Address: "1 Main St", Lat: 40, Lng: -73}
	if _, err := store.Participants().Create(ctx, participant); err != nil {
		t.Fatalf("create participant: %v", err)
	}
	if err := store.Participants().Delete(ctx, participant.ID); err != nil {
		t.Fatalf("delete participant: %v", err)
	}
	participantImport := &models.Participant{Name: participant.Name, Address: participant.Address, Lat: 41, Lng: -74}
	participantResult, err := store.Participants().UpsertBatch(ctx, []*models.Participant{participantImport})
	if err != nil || participantResult != (database.BatchUpsertResult{Created: 1}) || participantImport.ID == 0 || participantImport.ID == participant.ID {
		t.Fatalf("participant UpsertBatch() = %#v, id=%d, err=%v; want new row", participantResult, participantImport.ID, err)
	}
	if archived, err := store.Participants().ListDeleted(ctx); err != nil || len(archived) != 1 || archived[0].ID != participant.ID {
		t.Fatalf("archived participants = %#v, err=%v; want original row archived", archived, err)
	}
	if live, err := store.Participants().GetByID(ctx, participantImport.ID); err != nil || live.ID != participantImport.ID {
		t.Fatalf("live imported participant = %#v, err=%v", live, err)
	}

	driver := &models.Driver{Name: "Archived Driver", Address: "2 Main St", Lat: 40, Lng: -73, VehicleCapacity: 4}
	if _, err := store.Drivers().Create(ctx, driver); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if err := store.Drivers().Delete(ctx, driver.ID); err != nil {
		t.Fatalf("delete driver: %v", err)
	}
	driverImport := &models.Driver{Name: driver.Name, Address: driver.Address, Lat: 41, Lng: -74, VehicleCapacity: 5}
	driverResult, err := store.Drivers().UpsertBatch(ctx, []*models.Driver{driverImport})
	if err != nil || driverResult != (database.BatchUpsertResult{Created: 1}) || driverImport.ID == 0 || driverImport.ID == driver.ID {
		t.Fatalf("driver UpsertBatch() = %#v, id=%d, err=%v; want new row", driverResult, driverImport.ID, err)
	}
	if archived, err := store.Drivers().ListDeleted(ctx); err != nil || len(archived) != 1 || archived[0].ID != driver.ID {
		t.Fatalf("archived drivers = %#v, err=%v; want original row archived", archived, err)
	}
	if live, err := store.Drivers().GetByID(ctx, driverImport.ID); err != nil || live.ID != driverImport.ID {
		t.Fatalf("live imported driver = %#v, err=%v", live, err)
	}
}

func TestUpsertBatchUpdatesMutableFieldsAndKeepsIdentity(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	participant, err := store.Participants().Create(ctx, &models.Participant{Name: "Jane Doe", Address: "1 Main St", AddressName: "Home", Lat: 40, Lng: -73})
	if err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	driver, err := store.Drivers().Create(ctx, &models.Driver{Name: "John Doe", Address: "2 Main St", Lat: 41, Lng: -74, VehicleCapacity: 4})
	if err != nil {
		t.Fatalf("seed driver: %v", err)
	}

	if _, err := store.Participants().UpsertBatch(ctx, []*models.Participant{{Name: " JANE doe ", Address: "1 main st.", Lat: 99, Lng: 99}}); err != nil {
		t.Fatalf("participant UpsertBatch() error = %v", err)
	}
	got, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Name != "Jane Doe" || got.Address != "1 Main St" || got.AddressName != "Home" || got.Lat != 40 || got.Lng != -73 {
		t.Fatalf("participant after upsert = %#v, want name, address, address name, and coordinates preserved", got)
	}
	if !got.UpdatedAt.After(participant.UpdatedAt) {
		t.Fatalf("participant updated_at = %v, want later than %v", got.UpdatedAt, participant.UpdatedAt)
	}

	if _, err := store.Drivers().UpsertBatch(ctx, []*models.Driver{{Name: "John Doe", Address: "2 Main St", AddressName: "Work", Lat: 99, Lng: 99, VehicleCapacity: 7}}); err != nil {
		t.Fatalf("driver UpsertBatch() error = %v", err)
	}
	gotDriver, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if gotDriver.VehicleCapacity != 7 || gotDriver.AddressName != "Work" || gotDriver.Lat != 41 || gotDriver.Lng != -74 {
		t.Fatalf("driver after upsert = %#v, want capacity 7, address name Work, coordinates preserved", gotDriver)
	}

	// Capacity 0 means the import had no capacity column: keep the existing value, default new drivers.
	batch := []*models.Driver{
		{Name: "John Doe", Address: "2 Main St", Lat: 41, Lng: -74},
		{Name: "New Driver", Address: "3 Main St", Lat: 42, Lng: -75},
	}
	if _, err := store.Drivers().UpsertBatch(ctx, batch); err != nil {
		t.Fatalf("driver UpsertBatch() error = %v", err)
	}
	gotDriver, err = store.Drivers().GetByID(ctx, driver.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if gotDriver.VehicleCapacity != 7 {
		t.Fatalf("driver capacity after capacity-less upsert = %d, want 7 preserved", gotDriver.VehicleCapacity)
	}
	created, err := store.Drivers().GetByID(ctx, batch[1].ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if created.VehicleCapacity != models.DefaultVehicleCapacity || batch[1].VehicleCapacity != models.DefaultVehicleCapacity {
		t.Fatalf("new driver capacity = %d (entity %d), want default %d", created.VehicleCapacity, batch[1].VehicleCapacity, models.DefaultVehicleCapacity)
	}
}

func TestUpsertBatchIsIdempotent(t *testing.T) {
	t.Run("participants", func(t *testing.T) {
		testUpsertBatchIsIdempotent(t, participantBatchSpec())
	})
	t.Run("drivers", func(t *testing.T) {
		testUpsertBatchIsIdempotent(t, driverBatchSpec())
	})
}

func testUpsertBatchIsIdempotent[T any](t *testing.T, spec rosterBatchSpec[T]) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	batch := func() []*T {
		return []*T{spec.newEntity("First", "1 Main St"), spec.newEntity("Second", "2 Main St")}
	}
	first, err := spec.createBatch(ctx, store, batch())
	if err != nil {
		t.Fatalf("first UpsertBatch() error = %v", err)
	}
	second, err := spec.createBatch(ctx, store, batch())
	if err != nil {
		t.Fatalf("second UpsertBatch() error = %v", err)
	}
	if first != (database.BatchUpsertResult{Created: 2}) || second != (database.BatchUpsertResult{Updated: 2}) {
		t.Fatalf("results = %#v then %#v, want 2 created then 2 updated", first, second)
	}
	if count, err := spec.count(ctx, store); err != nil || count != 2 {
		t.Fatalf("%ss after re-import = %d, err=%v, want 2", spec.noun, count, err)
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
	results := make([]database.BatchUpsertResult, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Go(func() {
			results[i], errs[i] = spec.createBatch(ctx, store, []*T{
				spec.newEntity("Same Entry", "1 Main St"),
			})
		})
	}
	wg.Wait()

	created, updated := 0, 0
	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("UpsertBatch() worker %d error = %v", i, errs[i])
		}
		created += results[i].Created
		updated += results[i].Updated
	}
	if created != 1 || updated != workers-1 {
		t.Fatalf("created = %d updated = %d, want exactly one insert across %d concurrent batches", created, updated, workers)
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
	result, err := spec.createBatch(ctx, store, batch)
	if err != nil {
		t.Fatalf("UpsertBatch() error = %v", err)
	}
	if result != (database.BatchUpsertResult{Created: 2}) {
		t.Fatalf("UpsertBatch() result = %#v, want 2 created", result)
	}
	for i, entity := range batch {
		createdAt, updatedAt := spec.timestamps(entity)
		if spec.id(entity) == 0 || createdAt.IsZero() || !createdAt.Equal(updatedAt) {
			t.Errorf("batch[%d] id=%d created=%v updated=%v, want committed ID and equal non-zero timestamps", i, spec.id(entity), createdAt, updatedAt)
		}
	}
}

func TestUpsertBatchMergesWithinBatchDuplicates(t *testing.T) {
	t.Run("participants", func(t *testing.T) {
		testUpsertBatchMergesWithinBatchDuplicates(t, participantBatchSpec())
	})
	t.Run("drivers", func(t *testing.T) {
		testUpsertBatchMergesWithinBatchDuplicates(t, driverBatchSpec())
	})
}

func testUpsertBatchMergesWithinBatchDuplicates[T any](t *testing.T, spec rosterBatchSpec[T]) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	batch := []*T{
		spec.newEntity("Same Entry", "1 Main St"),
		spec.newEntity(" same entry ", " 1 MAIN ST "),
	}
	result, err := spec.createBatch(ctx, store, batch)
	if err != nil {
		t.Fatalf("UpsertBatch() error = %v", err)
	}
	if result != (database.BatchUpsertResult{Created: 1, Updated: 1}) {
		t.Fatalf("UpsertBatch() result = %#v, want the second duplicate merged into the first", result)
	}
	if spec.id(batch[0]) == 0 || spec.id(batch[1]) != spec.id(batch[0]) {
		t.Fatalf("batch IDs = [%d %d], want both the same created row", spec.id(batch[0]), spec.id(batch[1]))
	}
	if count, err := spec.count(ctx, store); err != nil || count != 1 {
		t.Fatalf("%ss = %d, err=%v, want 1", spec.noun, count, err)
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
	result, err := spec.createBatch(ctx, store, []*T{first, nil})
	if err == nil || err.Error() != spec.noun+" batch contains a nil "+spec.noun {
		t.Fatalf("UpsertBatch() error = %v, want nil %s error", err, spec.noun)
	}
	if result != (database.BatchUpsertResult{}) {
		t.Fatalf("UpsertBatch() result = %#v, want zero value", result)
	}
	createdAt, updatedAt := spec.timestamps(first)
	if spec.id(first) != 0 || !createdAt.IsZero() || !updatedAt.IsZero() {
		t.Fatalf("first entity mutated after rollback: id=%d created=%v updated=%v", spec.id(first), createdAt, updatedAt)
	}
	if count, countErr := spec.count(ctx, store); countErr != nil || count != 0 {
		t.Fatalf("%ss after rollback = %d, err=%v, want 0", spec.noun, count, countErr)
	}
}
