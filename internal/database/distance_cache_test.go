package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/models"
)

func TestDistanceCacheSet(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	entry := &models.DistanceCacheEntry{
		Origin:         models.Coordinates{Lat: 40.7128, Lng: -74.0060},
		Destination:    models.Coordinates{Lat: 42.3601, Lng: -71.0589},
		DistanceMeters: 35000.0,
		DurationSecs:   3600.0,
	}

	err := db.DistanceCacheRepository.Set(ctx, entry)
	require.NoError(t, err)
}

func TestDistanceCacheGet(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	origin := models.Coordinates{Lat: 40.7128, Lng: -74.0060}
	dest := models.Coordinates{Lat: 42.3601, Lng: -71.0589}

	entry := &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 35000.0,
		DurationSecs:   3600.0,
	}

	err := db.DistanceCacheRepository.Set(ctx, entry)
	require.NoError(t, err)

	// Get cached entry
	cached, err := db.DistanceCacheRepository.Get(ctx, origin, dest)
	require.NoError(t, err)
	assert.NotNil(t, cached)
	assert.Equal(t, 35000.0, cached.DistanceMeters)
	assert.Equal(t, 3600.0, cached.DurationSecs)
}

func TestDistanceCacheGetNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	origin := models.Coordinates{Lat: 40.0, Lng: -75.0}
	dest := models.Coordinates{Lat: 41.0, Lng: -76.0}

	cached, err := db.DistanceCacheRepository.Get(ctx, origin, dest)
	require.NoError(t, err)
	assert.Nil(t, cached)
}

func TestDistanceCacheRounding(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Set entry with high-precision coordinates
	origin := models.Coordinates{Lat: 40.712345678, Lng: -74.006012345}
	dest := models.Coordinates{Lat: 42.360123456, Lng: -71.058987654}

	entry := &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 35000.0,
		DurationSecs:   3600.0,
	}

	err := db.DistanceCacheRepository.Set(ctx, entry)
	require.NoError(t, err)

	// Get with slightly different coordinates (within rounding tolerance)
	originSlightlyDifferent := models.Coordinates{Lat: 40.712346, Lng: -74.006013}
	destSlightlyDifferent := models.Coordinates{Lat: 42.360124, Lng: -71.058988}

	cached, err := db.DistanceCacheRepository.Get(ctx, originSlightlyDifferent, destSlightlyDifferent)
	require.NoError(t, err)
	assert.NotNil(t, cached)
	assert.Equal(t, 35000.0, cached.DistanceMeters)
}

func TestDistanceCacheUpdateExisting(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	origin := models.Coordinates{Lat: 40.0, Lng: -75.0}
	dest := models.Coordinates{Lat: 41.0, Lng: -76.0}

	// Set initial entry
	entry1 := &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 10000.0,
		DurationSecs:   1000.0,
	}
	err := db.DistanceCacheRepository.Set(ctx, entry1)
	require.NoError(t, err)

	// Update with new values
	entry2 := &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 12000.0,
		DurationSecs:   1200.0,
	}
	err = db.DistanceCacheRepository.Set(ctx, entry2)
	require.NoError(t, err)

	// Get and verify updated values
	cached, err := db.DistanceCacheRepository.Get(ctx, origin, dest)
	require.NoError(t, err)
	assert.NotNil(t, cached)
	assert.Equal(t, 12000.0, cached.DistanceMeters)
	assert.Equal(t, 1200.0, cached.DurationSecs)
}

func TestDistanceCacheSetBatch(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	entries := []models.DistanceCacheEntry{
		{
			Origin:         models.Coordinates{Lat: 40.0, Lng: -75.0},
			Destination:    models.Coordinates{Lat: 41.0, Lng: -76.0},
			DistanceMeters: 10000.0,
			DurationSecs:   1000.0,
		},
		{
			Origin:         models.Coordinates{Lat: 41.0, Lng: -76.0},
			Destination:    models.Coordinates{Lat: 42.0, Lng: -77.0},
			DistanceMeters: 15000.0,
			DurationSecs:   1500.0,
		},
		{
			Origin:         models.Coordinates{Lat: 42.0, Lng: -77.0},
			Destination:    models.Coordinates{Lat: 40.0, Lng: -75.0},
			DistanceMeters: 25000.0,
			DurationSecs:   2500.0,
		},
	}

	err := db.DistanceCacheRepository.SetBatch(ctx, entries)
	require.NoError(t, err)

	// Verify all entries were cached
	for _, entry := range entries {
		cached, err := db.DistanceCacheRepository.Get(ctx, entry.Origin, entry.Destination)
		require.NoError(t, err)
		assert.NotNil(t, cached)
		assert.Equal(t, entry.DistanceMeters, cached.DistanceMeters)
		assert.Equal(t, entry.DurationSecs, cached.DurationSecs)
	}
}

func TestDistanceCacheSetBatchEmpty(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	err := db.DistanceCacheRepository.SetBatch(ctx, []models.DistanceCacheEntry{})
	require.NoError(t, err)
}

func TestDistanceCacheGetBatch(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Set multiple cache entries
	entries := []models.DistanceCacheEntry{
		{
			Origin:         models.Coordinates{Lat: 40.0, Lng: -75.0},
			Destination:    models.Coordinates{Lat: 41.0, Lng: -76.0},
			DistanceMeters: 10000.0,
			DurationSecs:   1000.0,
		},
		{
			Origin:         models.Coordinates{Lat: 41.0, Lng: -76.0},
			Destination:    models.Coordinates{Lat: 42.0, Lng: -77.0},
			DistanceMeters: 15000.0,
			DurationSecs:   1500.0,
		},
	}
	err := db.DistanceCacheRepository.SetBatch(ctx, entries)
	require.NoError(t, err)

	// Get batch
	pairs := []struct{ Origin, Dest models.Coordinates }{
		{Origin: models.Coordinates{Lat: 40.0, Lng: -75.0}, Dest: models.Coordinates{Lat: 41.0, Lng: -76.0}},
		{Origin: models.Coordinates{Lat: 41.0, Lng: -76.0}, Dest: models.Coordinates{Lat: 42.0, Lng: -77.0}},
		{Origin: models.Coordinates{Lat: 99.0, Lng: -99.0}, Dest: models.Coordinates{Lat: 88.0, Lng: -88.0}}, // Not cached
	}

	results, err := db.DistanceCacheRepository.GetBatch(ctx, pairs)
	require.NoError(t, err)
	assert.NotNil(t, results)

	// Should have 2 results (third pair not cached)
	assert.Len(t, results, 2)

	// Verify results exist for cached pairs
	key1 := "40.00000,-75.00000->41.00000,-76.00000"
	assert.Contains(t, results, key1)
	assert.Equal(t, 10000.0, results[key1].DistanceMeters)

	key2 := "41.00000,-76.00000->42.00000,-77.00000"
	assert.Contains(t, results, key2)
	assert.Equal(t, 15000.0, results[key2].DistanceMeters)
}

func TestDistanceCacheGetBatchEmpty(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	pairs := []struct{ Origin, Dest models.Coordinates }{}
	results, err := db.DistanceCacheRepository.GetBatch(ctx, pairs)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestDistanceCacheClear(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Set multiple entries
	entries := []models.DistanceCacheEntry{
		{
			Origin:         models.Coordinates{Lat: 40.0, Lng: -75.0},
			Destination:    models.Coordinates{Lat: 41.0, Lng: -76.0},
			DistanceMeters: 10000.0,
			DurationSecs:   1000.0,
		},
		{
			Origin:         models.Coordinates{Lat: 41.0, Lng: -76.0},
			Destination:    models.Coordinates{Lat: 42.0, Lng: -77.0},
			DistanceMeters: 15000.0,
			DurationSecs:   1500.0,
		},
	}
	err := db.DistanceCacheRepository.SetBatch(ctx, entries)
	require.NoError(t, err)

	// Verify entries exist
	cached, err := db.DistanceCacheRepository.Get(ctx, entries[0].Origin, entries[0].Destination)
	require.NoError(t, err)
	assert.NotNil(t, cached)

	// Clear cache
	err = db.DistanceCacheRepository.Clear(ctx)
	require.NoError(t, err)

	// Verify cache is empty
	cached, err = db.DistanceCacheRepository.Get(ctx, entries[0].Origin, entries[0].Destination)
	require.NoError(t, err)
	assert.Nil(t, cached)

	cached, err = db.DistanceCacheRepository.Get(ctx, entries[1].Origin, entries[1].Destination)
	require.NoError(t, err)
	assert.Nil(t, cached)
}
