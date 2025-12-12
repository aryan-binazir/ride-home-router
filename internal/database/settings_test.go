package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/models"
)

func TestSettingsGetDefault(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	settings, err := db.SettingsRepository.Get(ctx)
	require.NoError(t, err)
	assert.NotNil(t, settings)

	// Default values from schema
	assert.Equal(t, "", settings.InstituteAddress)
	assert.Equal(t, 0.0, settings.InstituteLat)
	assert.Equal(t, 0.0, settings.InstituteLng)
}

func TestSettingsUpdate(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	newSettings := &models.Settings{
		InstituteAddress: "100 School St, Boston, MA",
		InstituteLat:     42.3601,
		InstituteLng:     -71.0589,
	}

	err := db.SettingsRepository.Update(ctx, newSettings)
	require.NoError(t, err)

	// Retrieve and verify
	retrieved, err := db.SettingsRepository.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "100 School St, Boston, MA", retrieved.InstituteAddress)
	assert.Equal(t, 42.3601, retrieved.InstituteLat)
	assert.Equal(t, -71.0589, retrieved.InstituteLng)
}

func TestSettingsUpdateMultipleTimes(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// First update
	settings1 := &models.Settings{
		InstituteAddress: "Address 1",
		InstituteLat:     40.0,
		InstituteLng:     -75.0,
	}
	err := db.SettingsRepository.Update(ctx, settings1)
	require.NoError(t, err)

	// Second update
	settings2 := &models.Settings{
		InstituteAddress: "Address 2",
		InstituteLat:     41.0,
		InstituteLng:     -76.0,
	}
	err = db.SettingsRepository.Update(ctx, settings2)
	require.NoError(t, err)

	// Verify latest values
	retrieved, err := db.SettingsRepository.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Address 2", retrieved.InstituteAddress)
	assert.Equal(t, 41.0, retrieved.InstituteLat)
	assert.Equal(t, -76.0, retrieved.InstituteLng)
}

func TestSettingsUpdatePartial(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Set initial values
	initial := &models.Settings{
		InstituteAddress: "Initial Address",
		InstituteLat:     40.0,
		InstituteLng:     -75.0,
	}
	err := db.SettingsRepository.Update(ctx, initial)
	require.NoError(t, err)

	// Update all fields (settings always updates all fields)
	updated := &models.Settings{
		InstituteAddress: "Updated Address",
		InstituteLat:     41.0,
		InstituteLng:     -76.0,
	}
	err = db.SettingsRepository.Update(ctx, updated)
	require.NoError(t, err)

	// Verify
	retrieved, err := db.SettingsRepository.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Updated Address", retrieved.InstituteAddress)
	assert.Equal(t, 41.0, retrieved.InstituteLat)
	assert.Equal(t, -76.0, retrieved.InstituteLng)
}

func TestSettingsGetCoords(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	settings := &models.Settings{
		InstituteAddress: "Test School",
		InstituteLat:     48.8566,
		InstituteLng:     2.3522,
	}
	err := db.SettingsRepository.Update(ctx, settings)
	require.NoError(t, err)

	retrieved, err := db.SettingsRepository.Get(ctx)
	require.NoError(t, err)

	coords := retrieved.GetCoords()
	assert.Equal(t, 48.8566, coords.Lat)
	assert.Equal(t, 2.3522, coords.Lng)
}
