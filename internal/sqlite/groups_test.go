package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"ride-home-router/internal/models"
)

func TestStoreFreshSchemaCreatesV4GroupTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "groups-schema.db")

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
	for _, tableName := range []string{"groups", "participant_groups", "driver_groups"} {
		assertTableExists(t, store.db, tableName)
	}
}

func TestGroupRepositoryTracksMembershipCountsWithoutJoinMultiplication(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "groups-repo.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx := context.Background()
	group, err := store.Groups().Create(ctx, &models.Group{Name: "Youth Conference"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	participants := []models.Participant{
		{Name: "P1", Address: "1 Rider Way", Lat: 40.1, Lng: -73.9},
		{Name: "P2", Address: "2 Rider Way", Lat: 40.2, Lng: -73.8},
	}
	participantIDs := make([]int64, 0, len(participants))
	for _, participant := range participants {
		created, err := store.Participants().Create(ctx, &participant)
		if err != nil {
			t.Fatalf("create participant: %v", err)
		}
		participantIDs = append(participantIDs, created.ID)
		if err := store.Groups().SetGroupsForParticipant(ctx, created.ID, []int64{group.ID}); err != nil {
			t.Fatalf("SetGroupsForParticipant() error = %v", err)
		}
	}

	drivers := []models.Driver{
		{Name: "D1", Address: "1 Driver Way", Lat: 40.3, Lng: -73.7, VehicleCapacity: 4},
		{Name: "D2", Address: "2 Driver Way", Lat: 40.4, Lng: -73.6, VehicleCapacity: 4},
		{Name: "D3", Address: "3 Driver Way", Lat: 40.5, Lng: -73.5, VehicleCapacity: 4},
	}
	driverIDs := make([]int64, 0, len(drivers))
	for _, driver := range drivers {
		created, err := store.Drivers().Create(ctx, &driver)
		if err != nil {
			t.Fatalf("create driver: %v", err)
		}
		driverIDs = append(driverIDs, created.ID)
		if err := store.Groups().SetGroupsForDriver(ctx, created.ID, []int64{group.ID}); err != nil {
			t.Fatalf("SetGroupsForDriver() error = %v", err)
		}
	}

	groups, err := store.Groups().List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("List() len = %d, want 1", len(groups))
	}
	if groups[0].ParticipantCount != 2 {
		t.Fatalf("ParticipantCount = %d, want 2", groups[0].ParticipantCount)
	}
	if groups[0].DriverCount != 3 {
		t.Fatalf("DriverCount = %d, want 3", groups[0].DriverCount)
	}

	participantMap, err := store.Groups().ListGroupIDsForParticipants(ctx)
	if err != nil {
		t.Fatalf("ListGroupIDsForParticipants() error = %v", err)
	}
	for _, participantID := range participantIDs {
		if len(participantMap[participantID]) != 1 || participantMap[participantID][0] != group.ID {
			t.Fatalf("participant group map[%d] = %#v, want [%d]", participantID, participantMap[participantID], group.ID)
		}
	}

	driverMap, err := store.Groups().ListGroupIDsForDrivers(ctx)
	if err != nil {
		t.Fatalf("ListGroupIDsForDrivers() error = %v", err)
	}
	for _, driverID := range driverIDs {
		if len(driverMap[driverID]) != 1 || driverMap[driverID][0] != group.ID {
			t.Fatalf("driver group map[%d] = %#v, want [%d]", driverID, driverMap[driverID], group.ID)
		}
	}
}

func TestGroupRepositoryBulkAddAndRemoveMembershipsAreIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "groups-membership-idempotent.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx := context.Background()
	group, err := store.Groups().Create(ctx, &models.Group{Name: "Regional Meetup"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	participant, err := store.Participants().Create(ctx, &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Way",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	driver, err := store.Drivers().Create(ctx, &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Way",
		Lat:             40.2,
		Lng:             -73.8,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}

	if err := store.Groups().AddGroupToParticipants(ctx, group.ID, []int64{participant.ID, participant.ID}); err != nil {
		t.Fatalf("AddGroupToParticipants() error = %v", err)
	}
	if err := store.Groups().AddGroupToParticipants(ctx, group.ID, []int64{participant.ID}); err != nil {
		t.Fatalf("AddGroupToParticipants() second call error = %v", err)
	}

	participantGroups, err := store.Groups().ListGroupsForParticipant(ctx, participant.ID)
	if err != nil {
		t.Fatalf("ListGroupsForParticipant() error = %v", err)
	}
	if len(participantGroups) != 1 || participantGroups[0].ID != group.ID {
		t.Fatalf("participant groups = %#v, want one group %d", participantGroups, group.ID)
	}

	if err := store.Groups().RemoveGroupFromParticipants(ctx, group.ID, []int64{participant.ID}); err != nil {
		t.Fatalf("RemoveGroupFromParticipants() error = %v", err)
	}
	if err := store.Groups().RemoveGroupFromParticipants(ctx, group.ID, []int64{participant.ID}); err != nil {
		t.Fatalf("RemoveGroupFromParticipants() second call error = %v", err)
	}

	participantGroups, err = store.Groups().ListGroupsForParticipant(ctx, participant.ID)
	if err != nil {
		t.Fatalf("ListGroupsForParticipant() after remove error = %v", err)
	}
	if len(participantGroups) != 0 {
		t.Fatalf("participant groups after remove = %#v, want none", participantGroups)
	}

	if err := store.Groups().AddGroupToDrivers(ctx, group.ID, []int64{driver.ID, driver.ID}); err != nil {
		t.Fatalf("AddGroupToDrivers() error = %v", err)
	}
	if err := store.Groups().RemoveGroupFromDrivers(ctx, group.ID, []int64{driver.ID}); err != nil {
		t.Fatalf("RemoveGroupFromDrivers() error = %v", err)
	}
	if err := store.Groups().RemoveGroupFromDrivers(ctx, group.ID, []int64{driver.ID}); err != nil {
		t.Fatalf("RemoveGroupFromDrivers() second call error = %v", err)
	}
	driverGroups, err := store.Groups().ListGroupsForDriver(ctx, driver.ID)
	if err != nil {
		t.Fatalf("ListGroupsForDriver() error = %v", err)
	}
	if len(driverGroups) != 0 {
		t.Fatalf("driver groups after remove = %#v, want none", driverGroups)
	}
}
