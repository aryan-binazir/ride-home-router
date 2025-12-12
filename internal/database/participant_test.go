package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/models"
)

func TestParticipantCreate(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	p := &models.Participant{
		Name:    "Alice Johnson",
		Address: "123 Main St, New York, NY",
		Lat:     40.7128,
		Lng:     -74.0060,
	}

	created, err := db.ParticipantRepository.Create(ctx, p)
	require.NoError(t, err)
	assert.NotNil(t, created)
	assert.NotZero(t, created.ID)
	assert.Equal(t, "Alice Johnson", created.Name)
	assert.Equal(t, "123 Main St, New York, NY", created.Address)
	assert.Equal(t, 40.7128, created.Lat)
	assert.Equal(t, -74.0060, created.Lng)
	assert.NotZero(t, created.CreatedAt)
	assert.NotZero(t, created.UpdatedAt)
}

func TestParticipantGetByID(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create a participant
	p := &models.Participant{
		Name:    "Bob Smith",
		Address: "456 Oak Ave, Boston, MA",
		Lat:     42.3601,
		Lng:     -71.0589,
	}
	created, err := db.ParticipantRepository.Create(ctx, p)
	require.NoError(t, err)

	// Get by ID
	found, err := db.ParticipantRepository.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "Bob Smith", found.Name)
	assert.Equal(t, "456 Oak Ave, Boston, MA", found.Address)
}

func TestParticipantGetByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	found, err := db.ParticipantRepository.GetByID(ctx, 99999)
	require.NoError(t, err)
	assert.Nil(t, found)
}

func TestParticipantGetByIDs(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create multiple participants
	p1, _ := db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Charlie Brown", Address: "111 Pine St", Lat: 40.0, Lng: -75.0,
	})
	_, _ = db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Diana Prince", Address: "222 Elm St", Lat: 41.0, Lng: -76.0,
	})
	p3, _ := db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Eve Adams", Address: "333 Maple St", Lat: 42.0, Lng: -77.0,
	})

	// Get by multiple IDs
	found, err := db.ParticipantRepository.GetByIDs(ctx, []int64{p1.ID, p3.ID})
	require.NoError(t, err)
	assert.Len(t, found, 2)

	// Results should be ordered by name
	names := []string{found[0].Name, found[1].Name}
	assert.Contains(t, names, "Charlie Brown")
	assert.Contains(t, names, "Eve Adams")
}

func TestParticipantGetByIDsEmpty(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	found, err := db.ParticipantRepository.GetByIDs(ctx, []int64{})
	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestParticipantList(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create participants
	db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Alice", Address: "Addr1", Lat: 40.0, Lng: -75.0,
	})
	db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Bob", Address: "Addr2", Lat: 41.0, Lng: -76.0,
	})
	db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Charlie", Address: "Addr3", Lat: 42.0, Lng: -77.0,
	})

	// List all
	participants, err := db.ParticipantRepository.List(ctx, "")
	require.NoError(t, err)
	assert.Len(t, participants, 3)

	// Results should be ordered by name
	assert.Equal(t, "Alice", participants[0].Name)
	assert.Equal(t, "Bob", participants[1].Name)
	assert.Equal(t, "Charlie", participants[2].Name)
}

func TestParticipantListWithSearch(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create participants
	db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Alice Anderson", Address: "Addr1", Lat: 40.0, Lng: -75.0,
	})
	db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Bob Brown", Address: "Addr2", Lat: 41.0, Lng: -76.0,
	})
	db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Alice Baker", Address: "Addr3", Lat: 42.0, Lng: -77.0,
	})

	// Search for "Alice"
	participants, err := db.ParticipantRepository.List(ctx, "Alice")
	require.NoError(t, err)
	assert.Len(t, participants, 2)
	assert.Contains(t, participants[0].Name, "Alice")
	assert.Contains(t, participants[1].Name, "Alice")
}

func TestParticipantUpdate(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create participant
	p, err := db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "Original Name", Address: "Original Address", Lat: 40.0, Lng: -75.0,
	})
	require.NoError(t, err)
	originalUpdatedAt := p.UpdatedAt

	// Update participant
	p.Name = "Updated Name"
	p.Address = "Updated Address"
	p.Lat = 41.0
	p.Lng = -76.0

	updated, err := db.ParticipantRepository.Update(ctx, p)
	require.NoError(t, err)
	assert.NotNil(t, updated)
	assert.Equal(t, p.ID, updated.ID)
	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, "Updated Address", updated.Address)
	assert.Equal(t, 41.0, updated.Lat)
	assert.Equal(t, -76.0, updated.Lng)
	assert.True(t, updated.UpdatedAt.After(originalUpdatedAt) || updated.UpdatedAt.Equal(originalUpdatedAt))

	// Verify update persisted
	found, err := db.ParticipantRepository.GetByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", found.Name)
}

func TestParticipantUpdateNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	p := &models.Participant{
		ID:      99999,
		Name:    "Non-existent",
		Address: "Nowhere",
		Lat:     0.0,
		Lng:     0.0,
	}

	updated, err := db.ParticipantRepository.Update(ctx, p)
	require.NoError(t, err)
	assert.Nil(t, updated)
}

func TestParticipantDelete(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create participant
	p, err := db.ParticipantRepository.Create(ctx, &models.Participant{
		Name: "To Delete", Address: "Delete Me", Lat: 40.0, Lng: -75.0,
	})
	require.NoError(t, err)

	// Delete participant
	err = db.ParticipantRepository.Delete(ctx, p.ID)
	require.NoError(t, err)

	// Verify deletion
	found, err := db.ParticipantRepository.GetByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Nil(t, found)
}

func TestParticipantDeleteNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	err := db.ParticipantRepository.Delete(ctx, 99999)
	assert.Error(t, err)
	assert.Equal(t, sql.ErrNoRows, err)
}
