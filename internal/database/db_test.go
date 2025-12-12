package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *DB {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewDatabase(t *testing.T) {
	db := setupTestDB(t)
	assert.NotNil(t, db)
	assert.NotNil(t, db.ParticipantRepository)
	assert.NotNil(t, db.DriverRepository)
	assert.NotNil(t, db.SettingsRepository)
	assert.NotNil(t, db.EventRepository)
	assert.NotNil(t, db.DistanceCacheRepository)
}

func TestHealthCheck(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	err := db.HealthCheck(ctx)
	assert.NoError(t, err)
}

func TestHealthCheckAfterClose(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)

	err = db.Close()
	require.NoError(t, err)

	ctx := context.Background()
	err = db.HealthCheck(ctx)
	assert.Error(t, err)
}

func TestDatabaseMigrations(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Verify that all required tables exist by attempting simple queries

	// Check settings table
	settings, err := db.SettingsRepository.Get(ctx)
	require.NoError(t, err)
	assert.NotNil(t, settings)

	// Check participants table (should return empty list, not nil)
	participants, err := db.ParticipantRepository.List(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, participants)

	// Check drivers table (should return empty list, not nil)
	drivers, err := db.DriverRepository.List(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, drivers)

	// Check events table (should return empty list, not nil)
	events, total, err := db.EventRepository.List(ctx, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Equal(t, 0, total)
}

func TestNewDatabaseInvalidPath(t *testing.T) {
	// Try to create a database in a non-existent directory
	_, err := New("/non/existent/path/db.sqlite")
	assert.Error(t, err)
}
