package postgres_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func TestLabelRepository_CRUDCountsAndUniqueness(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	label, err := store.Labels().Create(ctx, &models.Label{Name: "  Youth Conference  "})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if label.Name != "Youth Conference" {
		t.Fatalf("label.Name = %q, want trimmed name", label.Name)
	}
	if _, err := store.Labels().Create(ctx, &models.Label{Name: "Youth Conference"}); !errors.Is(err, database.ErrDuplicate) {
		t.Fatalf("Create() duplicate error = %v, want ErrDuplicate", err)
	}
	other, err := store.Labels().Create(ctx, &models.Label{Name: "Other"})
	if err != nil {
		t.Fatalf("Create() other label error = %v", err)
	}
	other.Name = "youth conference "
	if _, err := store.Labels().Update(ctx, other); err != nil {
		t.Fatalf("Update() to a differently-cased name error = %v (names are case-sensitive)", err)
	}
	other.Name = "Youth Conference"
	if _, err := store.Labels().Update(ctx, other); !errors.Is(err, database.ErrDuplicate) {
		t.Fatalf("Update() duplicate error = %v, want ErrDuplicate", err)
	}
	if err := store.Labels().Delete(ctx, other.ID); err != nil {
		t.Fatalf("Delete() other label error = %v", err)
	}

	participant := createTestParticipant(t, store, "Rider One")
	driver := createTestDriver(t, store, "Driver One")
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
	if len(labels) != 1 || labels[0].ParticipantCount != 1 || labels[0].DriverCount != 1 {
		t.Fatalf("List() = %#v, want one label with 1/1 counts", labels)
	}
	got, err := store.Labels().GetByID(ctx, label.ID)
	if err != nil || got.ParticipantCount != 1 || got.DriverCount != 1 {
		t.Fatalf("GetByID() = %#v, %v; want 1/1 counts", got, err)
	}

	label.Name = "Updated Label"
	if updated, err := store.Labels().Update(ctx, label); err != nil || updated.Name != "Updated Label" {
		t.Fatalf("Update() = %#v, %v", updated, err)
	}

	if err := store.Labels().Delete(ctx, label.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := store.Labels().Delete(ctx, label.ID); err != database.ErrNotFound {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
	if ids, err := store.Labels().ListLabelIDsForParticipants(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("participant memberships after delete = %#v, %v; want cascade-deleted", ids, err)
	}
	if ids, err := store.Labels().ListLabelIDsForDrivers(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("driver memberships after delete = %#v, %v; want cascade-deleted", ids, err)
	}
}

func TestLabelRepository_GetByIDsReturnsExistingLabels(t *testing.T) {
	store := postgrestest.Open(t)
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
	if len(labels) != 2 || labels[0].ID != first.ID || labels[1].ID != second.ID {
		t.Fatalf("labels = %#v, want first and second ordered by name", labels)
	}
}

func TestLabelRepository_SetAndBulkMembershipsAreIdempotent(t *testing.T) {
	store := postgrestest.Open(t)
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

	if err := store.Labels().SetLabelsForParticipant(ctx, participantOne.ID, []int64{firstLabel.ID, firstLabel.ID, secondLabel.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() error = %v", err)
	}
	participantLabels, err := store.Labels().ListLabelsForParticipant(ctx, participantOne.ID)
	if err != nil || len(participantLabels) != 2 {
		t.Fatalf("ListLabelsForParticipant() = %#v, %v; want 2", participantLabels, err)
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
	if err := store.Labels().AddLabelToDrivers(ctx, firstLabel.ID, []int64{driver.ID}); err != nil {
		t.Fatalf("AddLabelToDrivers() error = %v", err)
	}
	if err := store.Labels().RemoveLabelFromDrivers(ctx, firstLabel.ID, []int64{driver.ID}); err != nil {
		t.Fatalf("RemoveLabelFromDrivers() error = %v", err)
	}
	driverLabels, err := store.Labels().ListLabelsForDriver(ctx, driver.ID)
	if err != nil || len(driverLabels) != 1 || driverLabels[0].ID != secondLabel.ID {
		t.Fatalf("driver labels = %#v, %v; want second label only", driverLabels, err)
	}
}

func TestLabelRepository_SetLabelsRejectsNonPositiveLabelIDWithoutMutation(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	firstLabel, err := store.Labels().Create(ctx, &models.Label{Name: "First"})
	if err != nil {
		t.Fatalf("create first label: %v", err)
	}
	secondLabel, err := store.Labels().Create(ctx, &models.Label{Name: "Second"})
	if err != nil {
		t.Fatalf("create second label: %v", err)
	}
	participant := createTestParticipant(t, store, "Rider One")
	if err := store.Labels().SetLabelsForParticipant(ctx, participant.ID, []int64{firstLabel.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() initial error = %v", err)
	}

	if err := store.Labels().SetLabelsForParticipant(ctx, participant.ID, []int64{secondLabel.ID, 0}); err == nil {
		t.Fatal("SetLabelsForParticipant() error = nil, want invalid label error")
	}
	labels, err := store.Labels().ListLabelsForParticipant(ctx, participant.ID)
	if err != nil || len(labels) != 1 || labels[0].ID != firstLabel.ID {
		t.Fatalf("participant labels = %#v, %v; want original label preserved", labels, err)
	}
}

func TestRepositories_LabelWritesRollBackOnInvalidLabel(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	for _, labelIDs := range [][]int64{{9999}, {0}} {
		if _, err := store.Participants().CreateWithLabels(ctx, &models.Participant{
			Name: "Rider With Bad Label", Address: "1 Rider Way", Lat: 40.1, Lng: -73.9,
		}, labelIDs); err == nil {
			t.Fatalf("participant CreateWithLabels(%v) error = nil, want invalid label error", labelIDs)
		}
	}
	if _, err := store.Drivers().CreateWithLabels(ctx, &models.Driver{
		Name: "Driver With Bad Label", Address: "1 Driver Way", Lat: 40.1, Lng: -73.9, VehicleCapacity: 4,
	}, []int64{9999}); err == nil {
		t.Fatal("driver CreateWithLabels() error = nil, want invalid label error")
	}
	if participants, err := store.Participants().List(ctx, ""); err != nil || len(participants) != 0 {
		t.Fatalf("participants after failed creates = %#v, %v; want none", participants, err)
	}
	if drivers, err := store.Drivers().List(ctx, ""); err != nil || len(drivers) != 0 {
		t.Fatalf("drivers after failed creates = %#v, %v; want none", drivers, err)
	}

	participant := createTestParticipant(t, store, "Original Rider")
	participant.Name = "Changed Rider"
	if _, err := store.Participants().UpdateWithLabels(ctx, participant, []int64{9999}); err == nil {
		t.Fatal("participant UpdateWithLabels() error = nil, want invalid label error")
	}
	if unchanged, err := store.Participants().GetByID(ctx, participant.ID); err != nil || unchanged.Name != "Original Rider" {
		t.Fatalf("participant after failed update = %#v, %v; want rollback", unchanged, err)
	}

	driver := createTestDriver(t, store, "Original Driver")
	driver.Name = "Changed Driver"
	if _, err := store.Drivers().UpdateWithLabels(ctx, driver, []int64{9999}); err == nil {
		t.Fatal("driver UpdateWithLabels() error = nil, want invalid label error")
	}
	if unchanged, err := store.Drivers().GetByID(ctx, driver.ID); err != nil || unchanged.Name != "Original Driver" {
		t.Fatalf("driver after failed update = %#v, %v; want rollback", unchanged, err)
	}
	if ids, err := store.Labels().ListLabelIDsForParticipants(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("participant memberships = %#v, %v; want none", ids, err)
	}
	if ids, err := store.Labels().ListLabelIDsForDrivers(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("driver memberships = %#v, %v; want none", ids, err)
	}
}

func TestRepositories_UpdatePreservesOrClearsLabelsAsRequested(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	label, err := store.Labels().Create(ctx, &models.Label{Name: "Assigned"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	participant, err := store.Participants().CreateWithLabels(ctx, &models.Participant{
		Name: "Rider", Address: "1 Rider Way", Lat: 40.1, Lng: -73.9,
	}, []int64{label.ID})
	if err != nil {
		t.Fatalf("participant CreateWithLabels() error = %v", err)
	}
	participant.Name = "Updated Rider"
	if _, err := store.Participants().Update(ctx, participant); err != nil {
		t.Fatalf("participant Update() error = %v", err)
	}
	if labels, err := store.Labels().ListLabelsForParticipant(ctx, participant.ID); err != nil || len(labels) != 1 {
		t.Fatalf("participant labels after Update() = %#v, %v; want preserved", labels, err)
	}
	if _, err := store.Participants().UpdateWithLabels(ctx, participant, nil); err != nil {
		t.Fatalf("participant UpdateWithLabels(nil) error = %v", err)
	}
	if labels, err := store.Labels().ListLabelsForParticipant(ctx, participant.ID); err != nil || len(labels) != 0 {
		t.Fatalf("participant labels after UpdateWithLabels(nil) = %#v, %v; want none", labels, err)
	}

	driver, err := store.Drivers().CreateWithLabels(ctx, &models.Driver{
		Name: "Driver", Address: "1 Driver Way", Lat: 40.1, Lng: -73.9, VehicleCapacity: 4,
	}, []int64{label.ID})
	if err != nil {
		t.Fatalf("driver CreateWithLabels() error = %v", err)
	}
	driver.Name = "Updated Driver"
	if _, err := store.Drivers().Update(ctx, driver); err != nil {
		t.Fatalf("driver Update() error = %v", err)
	}
	if labels, err := store.Labels().ListLabelsForDriver(ctx, driver.ID); err != nil || len(labels) != 1 {
		t.Fatalf("driver labels after Update() = %#v, %v; want preserved", labels, err)
	}
	if _, err := store.Drivers().UpdateWithLabels(ctx, driver, nil); err != nil {
		t.Fatalf("driver UpdateWithLabels(nil) error = %v", err)
	}
	if labels, err := store.Labels().ListLabelsForDriver(ctx, driver.ID); err != nil || len(labels) != 0 {
		t.Fatalf("driver labels after UpdateWithLabels(nil) = %#v, %v; want none", labels, err)
	}
}

func TestLabelRepositorySoftDeletedOwnersKeepMembershipsAndRejectBulkChanges(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	assigned, err := store.Labels().Create(ctx, &models.Label{Name: "Assigned"})
	if err != nil {
		t.Fatalf("create assigned label: %v", err)
	}
	other, err := store.Labels().Create(ctx, &models.Label{Name: "Other"})
	if err != nil {
		t.Fatalf("create other label: %v", err)
	}
	liveParticipant := createTestParticipant(t, store, "Live Rider")
	archivedParticipant := createTestParticipant(t, store, "Archived Rider")
	liveDriver := createTestDriver(t, store, "Live Driver")
	archivedDriver := createTestDriver(t, store, "Archived Driver")

	for _, participantID := range []int64{liveParticipant.ID, archivedParticipant.ID} {
		if err := store.Labels().SetLabelsForParticipant(ctx, participantID, []int64{assigned.ID}); err != nil {
			t.Fatalf("SetLabelsForParticipant(%d) error = %v", participantID, err)
		}
	}
	for _, driverID := range []int64{liveDriver.ID, archivedDriver.ID} {
		if err := store.Labels().SetLabelsForDriver(ctx, driverID, []int64{assigned.ID}); err != nil {
			t.Fatalf("SetLabelsForDriver(%d) error = %v", driverID, err)
		}
	}
	if err := store.Participants().Delete(ctx, archivedParticipant.ID); err != nil {
		t.Fatalf("delete participant: %v", err)
	}
	if err := store.Drivers().Delete(ctx, archivedDriver.ID); err != nil {
		t.Fatalf("delete driver: %v", err)
	}

	got, err := store.Labels().GetByID(ctx, assigned.ID)
	if err != nil || got.ParticipantCount != 1 || got.DriverCount != 1 {
		t.Fatalf("label counts after delete = %#v, %v; want 1/1", got, err)
	}
	participantMemberships, err := store.Labels().ListLabelIDsForParticipants(ctx)
	if err != nil || len(participantMemberships[archivedParticipant.ID]) != 1 {
		t.Fatalf("archived participant memberships = %#v, %v; want retained", participantMemberships, err)
	}
	driverMemberships, err := store.Labels().ListLabelIDsForDrivers(ctx)
	if err != nil || len(driverMemberships[archivedDriver.ID]) != 1 {
		t.Fatalf("archived driver memberships = %#v, %v; want retained", driverMemberships, err)
	}

	if err := store.Labels().AddLabelToParticipants(ctx, other.ID, []int64{liveParticipant.ID, archivedParticipant.ID}); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("AddLabelToParticipants() error = %v, want ErrNotFound", err)
	}
	if err := store.Labels().RemoveLabelFromParticipants(ctx, assigned.ID, []int64{liveParticipant.ID, archivedParticipant.ID}); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("RemoveLabelFromParticipants() error = %v, want ErrNotFound", err)
	}
	participantMemberships, err = store.Labels().ListLabelIDsForParticipants(ctx)
	if err != nil || len(participantMemberships[liveParticipant.ID]) != 1 || participantMemberships[liveParticipant.ID][0] != assigned.ID || len(participantMemberships[archivedParticipant.ID]) != 1 || participantMemberships[archivedParticipant.ID][0] != assigned.ID {
		t.Fatalf("participant memberships after rejected remove = %#v, %v; want unchanged", participantMemberships, err)
	}
	if err := store.Labels().SetLabelsForParticipant(ctx, archivedParticipant.ID, []int64{other.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() archived owner error = %v", err)
	}
	participantMemberships, err = store.Labels().ListLabelIDsForParticipants(ctx)
	if err != nil || len(participantMemberships[liveParticipant.ID]) != 1 || participantMemberships[liveParticipant.ID][0] != assigned.ID || len(participantMemberships[archivedParticipant.ID]) != 1 || participantMemberships[archivedParticipant.ID][0] != other.ID {
		t.Fatalf("participant memberships after remove and replace = %#v, %v", participantMemberships, err)
	}

	if err := store.Labels().AddLabelToDrivers(ctx, other.ID, []int64{liveDriver.ID, archivedDriver.ID}); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("AddLabelToDrivers() error = %v, want ErrNotFound", err)
	}
	if err := store.Labels().RemoveLabelFromDrivers(ctx, assigned.ID, []int64{liveDriver.ID, archivedDriver.ID}); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("RemoveLabelFromDrivers() error = %v, want ErrNotFound", err)
	}
	driverMemberships, err = store.Labels().ListLabelIDsForDrivers(ctx)
	if err != nil || len(driverMemberships[liveDriver.ID]) != 1 || driverMemberships[liveDriver.ID][0] != assigned.ID || len(driverMemberships[archivedDriver.ID]) != 1 || driverMemberships[archivedDriver.ID][0] != assigned.ID {
		t.Fatalf("driver memberships after rejected remove = %#v, %v; want unchanged", driverMemberships, err)
	}
	if err := store.Labels().SetLabelsForDriver(ctx, archivedDriver.ID, []int64{other.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() archived owner error = %v", err)
	}
	driverMemberships, err = store.Labels().ListLabelIDsForDrivers(ctx)
	if err != nil || len(driverMemberships[liveDriver.ID]) != 1 || driverMemberships[liveDriver.ID][0] != assigned.ID || len(driverMemberships[archivedDriver.ID]) != 1 || driverMemberships[archivedDriver.ID][0] != other.ID {
		t.Fatalf("driver memberships after remove and replace = %#v, %v", driverMemberships, err)
	}

	if err := store.Participants().Restore(ctx, archivedParticipant.ID); err != nil {
		t.Fatalf("restore participant: %v", err)
	}
	if err := store.Drivers().Restore(ctx, archivedDriver.ID); err != nil {
		t.Fatalf("restore driver: %v", err)
	}
	got, err = store.Labels().GetByID(ctx, assigned.ID)
	if err != nil || got.ParticipantCount != 1 || got.DriverCount != 1 {
		t.Fatalf("assigned label counts after restore = %#v, %v; want 1/1", got, err)
	}
	got, err = store.Labels().GetByID(ctx, other.ID)
	if err != nil || got.ParticipantCount != 1 || got.DriverCount != 1 {
		t.Fatalf("replacement label counts after restore = %#v, %v; want 1/1", got, err)
	}
}
