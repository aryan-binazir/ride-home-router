package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/models"
)

func TestEventCreate(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	event := &models.Event{
		EventDate: time.Now(),
		Notes:     "Test event notes",
	}

	assignments := []models.EventAssignment{
		{
			DriverID:           1,
			DriverName:         "Driver A",
			DriverAddress:      "123 Main St",
			RouteOrder:         0,
			ParticipantID:      1,
			ParticipantName:    "Participant A",
			ParticipantAddress: "456 Oak St",
			DistanceFromPrev:   1500.0,
		},
		{
			DriverID:           1,
			DriverName:         "Driver A",
			DriverAddress:      "123 Main St",
			RouteOrder:         1,
			ParticipantID:      2,
			ParticipantName:    "Participant B",
			ParticipantAddress: "789 Elm St",
			DistanceFromPrev:   2000.0,
		},
	}

	summary := &models.EventSummary{
		TotalParticipants: 2,
		TotalDrivers:      1,
		TotalDistanceMeters: 3500.0,
		UsedInstituteVehicle: false,
	}

	created, err := db.EventRepository.Create(ctx, event, assignments, summary)
	require.NoError(t, err)
	assert.NotNil(t, created)
	assert.NotZero(t, created.ID)
	assert.Equal(t, "Test event notes", created.Notes)
	assert.NotZero(t, created.CreatedAt)
}

func TestEventCreateWithInstituteVehicle(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	event := &models.Event{
		EventDate: time.Now(),
		Notes:     "Event with institute vehicle",
	}

	assignments := []models.EventAssignment{
		{
			DriverID:               100,
			DriverName:             "Institute Van",
			DriverAddress:          "School Parking",
			RouteOrder:             0,
			ParticipantID:          1,
			ParticipantName:        "Student A",
			ParticipantAddress:     "123 Home St",
			DistanceFromPrev:       1000.0,
			UsedInstituteVehicle:   true,
		},
	}

	summary := &models.EventSummary{
		TotalParticipants:          1,
		TotalDrivers:               1,
		TotalDistanceMeters:        1000.0,
		UsedInstituteVehicle:       true,
		InstituteVehicleDriverName: "John Smith",
	}

	created, err := db.EventRepository.Create(ctx, event, assignments, summary)
	require.NoError(t, err)
	assert.NotNil(t, created)
	assert.NotZero(t, created.ID)
}

func TestEventGetByID(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create event
	event := &models.Event{
		EventDate: time.Now(),
		Notes:     "Get by ID test",
	}

	assignments := []models.EventAssignment{
		{
			DriverID: 1, DriverName: "Driver A", DriverAddress: "Addr A",
			RouteOrder: 0, ParticipantID: 1, ParticipantName: "Participant A",
			ParticipantAddress: "P Addr A", DistanceFromPrev: 1000.0,
		},
	}

	summary := &models.EventSummary{
		TotalParticipants:   1,
		TotalDrivers:        1,
		TotalDistanceMeters: 1000.0,
	}

	created, err := db.EventRepository.Create(ctx, event, assignments, summary)
	require.NoError(t, err)

	// Get by ID
	foundEvent, foundAssignments, foundSummary, err := db.EventRepository.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.NotNil(t, foundEvent)
	assert.NotNil(t, foundAssignments)
	assert.NotNil(t, foundSummary)

	assert.Equal(t, created.ID, foundEvent.ID)
	assert.Equal(t, "Get by ID test", foundEvent.Notes)

	assert.Len(t, foundAssignments, 1)
	assert.Equal(t, int64(1), foundAssignments[0].DriverID)
	assert.Equal(t, "Participant A", foundAssignments[0].ParticipantName)

	assert.Equal(t, created.ID, foundSummary.EventID)
	assert.Equal(t, 1, foundSummary.TotalParticipants)
	assert.Equal(t, 1000.0, foundSummary.TotalDistanceMeters)
}

func TestEventGetByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	event, assignments, summary, err := db.EventRepository.GetByID(ctx, 99999)
	require.NoError(t, err)
	assert.Nil(t, event)
	assert.Nil(t, assignments)
	assert.Nil(t, summary)
}

func TestEventList(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create multiple events
	now := time.Now()
	for i := 0; i < 5; i++ {
		event := &models.Event{
			EventDate: now.Add(time.Duration(-i) * 24 * time.Hour),
			Notes:     "Event " + string(rune('A'+i)),
		}
		summary := &models.EventSummary{
			TotalParticipants: i + 1,
			TotalDrivers:      1,
			TotalDistanceMeters: float64(i * 1000),
		}
		db.EventRepository.Create(ctx, event, []models.EventAssignment{}, summary)
	}

	// List all events
	events, total, err := db.EventRepository.List(ctx, 10, 0)
	require.NoError(t, err)
	assert.Len(t, events, 5)
	assert.Equal(t, 5, total)

	// Events should be ordered by date descending (most recent first)
	for i := 1; i < len(events); i++ {
		assert.True(t, events[i-1].EventDate.After(events[i].EventDate) || events[i-1].EventDate.Equal(events[i].EventDate))
	}
}

func TestEventListPagination(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create 10 events
	for i := 0; i < 10; i++ {
		event := &models.Event{
			EventDate: time.Now().Add(time.Duration(-i) * time.Hour),
			Notes:     "Event",
		}
		summary := &models.EventSummary{
			TotalParticipants: 1,
			TotalDrivers:      1,
			TotalDistanceMeters: 1000.0,
		}
		db.EventRepository.Create(ctx, event, []models.EventAssignment{}, summary)
	}

	// Get first page
	page1, total, err := db.EventRepository.List(ctx, 5, 0)
	require.NoError(t, err)
	assert.Len(t, page1, 5)
	assert.Equal(t, 10, total)

	// Get second page
	page2, total, err := db.EventRepository.List(ctx, 5, 5)
	require.NoError(t, err)
	assert.Len(t, page2, 5)
	assert.Equal(t, 10, total)

	// Pages should have different events
	assert.NotEqual(t, page1[0].ID, page2[0].ID)
}

func TestEventDelete(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create event with assignments and summary
	event := &models.Event{
		EventDate: time.Now(),
		Notes:     "To be deleted",
	}

	assignments := []models.EventAssignment{
		{
			DriverID: 1, DriverName: "Driver", DriverAddress: "Addr",
			RouteOrder: 0, ParticipantID: 1, ParticipantName: "Participant",
			ParticipantAddress: "P Addr", DistanceFromPrev: 1000.0,
		},
	}

	summary := &models.EventSummary{
		TotalParticipants:   1,
		TotalDrivers:        1,
		TotalDistanceMeters: 1000.0,
	}

	created, err := db.EventRepository.Create(ctx, event, assignments, summary)
	require.NoError(t, err)

	// Delete event
	err = db.EventRepository.Delete(ctx, created.ID)
	require.NoError(t, err)

	// Verify deletion (should cascade to assignments and summary)
	foundEvent, foundAssignments, foundSummary, err := db.EventRepository.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Nil(t, foundEvent)
	assert.Nil(t, foundAssignments)
	assert.Nil(t, foundSummary)
}

func TestEventDeleteNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	err := db.EventRepository.Delete(ctx, 99999)
	assert.Error(t, err)
	assert.Equal(t, sql.ErrNoRows, err)
}

func TestEventMultipleDriverRoutes(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	event := &models.Event{
		EventDate: time.Now(),
		Notes:     "Multiple drivers",
	}

	// Create assignments for multiple drivers
	assignments := []models.EventAssignment{
		{
			DriverID: 1, DriverName: "Driver A", DriverAddress: "Addr A",
			RouteOrder: 0, ParticipantID: 1, ParticipantName: "P1",
			ParticipantAddress: "P Addr 1", DistanceFromPrev: 1000.0,
		},
		{
			DriverID: 1, DriverName: "Driver A", DriverAddress: "Addr A",
			RouteOrder: 1, ParticipantID: 2, ParticipantName: "P2",
			ParticipantAddress: "P Addr 2", DistanceFromPrev: 1500.0,
		},
		{
			DriverID: 2, DriverName: "Driver B", DriverAddress: "Addr B",
			RouteOrder: 0, ParticipantID: 3, ParticipantName: "P3",
			ParticipantAddress: "P Addr 3", DistanceFromPrev: 2000.0,
		},
	}

	summary := &models.EventSummary{
		TotalParticipants:   3,
		TotalDrivers:        2,
		TotalDistanceMeters: 4500.0,
	}

	created, err := db.EventRepository.Create(ctx, event, assignments, summary)
	require.NoError(t, err)

	// Retrieve and verify
	_, foundAssignments, foundSummary, err := db.EventRepository.GetByID(ctx, created.ID)
	require.NoError(t, err)

	assert.Len(t, foundAssignments, 3)
	assert.Equal(t, 2, foundSummary.TotalDrivers)

	// Verify ordering (by driver_id, then route_order)
	assert.Equal(t, int64(1), foundAssignments[0].DriverID)
	assert.Equal(t, 0, foundAssignments[0].RouteOrder)
	assert.Equal(t, int64(1), foundAssignments[1].DriverID)
	assert.Equal(t, 1, foundAssignments[1].RouteOrder)
	assert.Equal(t, int64(2), foundAssignments[2].DriverID)
	assert.Equal(t, 0, foundAssignments[2].RouteOrder)
}
