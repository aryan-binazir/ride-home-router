package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ride-home-router/internal/models"
)

func TestCache_CoordinateMatching(t *testing.T) {
	// Create temp directory for cache
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "distance_cache.json")

	// Create cache manually (bypass GetDistanceCachePath)
	cache := &FileDistanceCache{
		filePath: cachePath,
		data:     &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}},
	}

	ctx := context.Background()

	// Set an entry with specific coordinates
	entry := &models.DistanceCacheEntry{
		Origin:         models.Coordinates{Lat: 1.234567, Lng: 2.345678},
		Destination:    models.Coordinates{Lat: 3.456789, Lng: 4.567890},
		DistanceMeters: 1000,
		DurationSecs:   60,
	}
	if err := cache.Set(ctx, entry); err != nil {
		t.Fatalf("failed to set cache entry: %v", err)
	}

	// Get with same coordinates (should match)
	result, err := cache.Get(ctx, entry.Origin, entry.Destination)
	if err != nil {
		t.Fatalf("failed to get cache entry: %v", err)
	}
	if result == nil {
		t.Fatal("expected to find cache entry with exact coordinates")
	}
	if result.DistanceMeters != 1000 {
		t.Errorf("expected distance 1000, got %f", result.DistanceMeters)
	}

	// Get with coordinates that round to same value (within 5 decimal places)
	slightlyDifferent := models.Coordinates{
		Lat: 1.2345674, // Rounds to 1.23457
		Lng: 2.3456784, // Rounds to 2.34568
	}
	result2, err := cache.Get(ctx, slightlyDifferent, entry.Destination)
	if err != nil {
		t.Fatalf("failed to get cache entry: %v", err)
	}
	if result2 == nil {
		t.Fatal("expected to find cache entry with coordinates that round to same value")
	}

	// Get with different coordinates (should not match)
	different := models.Coordinates{Lat: 9.0, Lng: 9.0}
	result3, err := cache.Get(ctx, different, entry.Destination)
	if err != nil {
		t.Fatalf("failed to get cache entry: %v", err)
	}
	if result3 != nil {
		t.Error("should not find cache entry with different coordinates")
	}
}

func TestCache_BatchSet(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "distance_cache.json")

	cache := &FileDistanceCache{
		filePath: cachePath,
		data:     &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}},
	}

	ctx := context.Background()

	entries := []models.DistanceCacheEntry{
		{
			Origin:         models.Coordinates{Lat: 0, Lng: 0},
			Destination:    models.Coordinates{Lat: 1, Lng: 1},
			DistanceMeters: 100,
			DurationSecs:   10,
		},
		{
			Origin:         models.Coordinates{Lat: 1, Lng: 1},
			Destination:    models.Coordinates{Lat: 2, Lng: 2},
			DistanceMeters: 200,
			DurationSecs:   20,
		},
		{
			Origin:         models.Coordinates{Lat: 2, Lng: 2},
			Destination:    models.Coordinates{Lat: 3, Lng: 3},
			DistanceMeters: 300,
			DurationSecs:   30,
		},
	}

	if err := cache.SetBatch(ctx, entries); err != nil {
		t.Fatalf("failed to batch set: %v", err)
	}

	// Verify all entries are stored
	for _, entry := range entries {
		result, err := cache.Get(ctx, entry.Origin, entry.Destination)
		if err != nil {
			t.Fatalf("failed to get entry: %v", err)
		}
		if result == nil {
			t.Errorf("entry not found: origin=%v dest=%v", entry.Origin, entry.Destination)
		}
		if result.DistanceMeters != entry.DistanceMeters {
			t.Errorf("distance mismatch: expected %f, got %f", entry.DistanceMeters, result.DistanceMeters)
		}
	}

	// Verify file was written
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("failed to read cache file: %v", err)
	}
	if len(data) == 0 {
		t.Error("cache file should not be empty")
	}
}

func TestCache_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "distance_cache.json")

	// Create first cache instance and write data
	cache1 := &FileDistanceCache{
		filePath: cachePath,
		data:     &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}},
	}

	ctx := context.Background()
	entry := &models.DistanceCacheEntry{
		Origin:         models.Coordinates{Lat: 10, Lng: 20},
		Destination:    models.Coordinates{Lat: 30, Lng: 40},
		DistanceMeters: 5000,
		DurationSecs:   300,
	}

	if err := cache1.Set(ctx, entry); err != nil {
		t.Fatalf("failed to set entry: %v", err)
	}

	// Create second cache instance and load data
	cache2 := &FileDistanceCache{
		filePath: cachePath,
		data:     &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}},
	}

	if err := cache2.load(); err != nil {
		t.Fatalf("failed to load cache: %v", err)
	}

	// Verify data was persisted
	result, err := cache2.Get(ctx, entry.Origin, entry.Destination)
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}
	if result == nil {
		t.Fatal("entry should be persisted and loadable")
	}
	if result.DistanceMeters != 5000 {
		t.Errorf("expected distance 5000, got %f", result.DistanceMeters)
	}
}

func TestCache_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "distance_cache.json")

	cache := &FileDistanceCache{
		filePath: cachePath,
		data:     &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}},
	}

	ctx := context.Background()

	// Add some entries
	entries := []models.DistanceCacheEntry{
		{Origin: models.Coordinates{Lat: 0, Lng: 0}, Destination: models.Coordinates{Lat: 1, Lng: 1}, DistanceMeters: 100},
		{Origin: models.Coordinates{Lat: 1, Lng: 1}, Destination: models.Coordinates{Lat: 2, Lng: 2}, DistanceMeters: 200},
	}
	cache.SetBatch(ctx, entries)

	// Verify entries exist
	result, _ := cache.Get(ctx, entries[0].Origin, entries[0].Destination)
	if result == nil {
		t.Fatal("entry should exist before clear")
	}

	// Clear the cache
	if err := cache.Clear(ctx); err != nil {
		t.Fatalf("failed to clear cache: %v", err)
	}

	// Verify entries are gone
	result, _ = cache.Get(ctx, entries[0].Origin, entries[0].Destination)
	if result != nil {
		t.Error("entry should not exist after clear")
	}
}

func TestCache_UpdateExisting(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "distance_cache.json")

	cache := &FileDistanceCache{
		filePath: cachePath,
		data:     &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}},
	}

	ctx := context.Background()
	origin := models.Coordinates{Lat: 1, Lng: 2}
	dest := models.Coordinates{Lat: 3, Lng: 4}

	// Set initial entry
	entry1 := &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 1000,
		DurationSecs:   60,
	}
	cache.Set(ctx, entry1)

	// Update with new values
	entry2 := &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 2000,
		DurationSecs:   120,
	}
	cache.Set(ctx, entry2)

	// Verify updated value
	result, _ := cache.Get(ctx, origin, dest)
	if result == nil {
		t.Fatal("entry should exist")
	}
	if result.DistanceMeters != 2000 {
		t.Errorf("expected updated distance 2000, got %f", result.DistanceMeters)
	}
	if result.DurationSecs != 120 {
		t.Errorf("expected updated duration 120, got %f", result.DurationSecs)
	}

	// Verify no duplicate entries
	if len(cache.data.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d (duplicate created)", len(cache.data.Entries))
	}
}

func TestCoordsMatch(t *testing.T) {
	tests := []struct {
		name     string
		a, b     models.Coordinates
		expected bool
	}{
		{
			name:     "exact match",
			a:        models.Coordinates{Lat: 1.23456, Lng: 2.34567},
			b:        models.Coordinates{Lat: 1.23456, Lng: 2.34567},
			expected: true,
		},
		{
			name:     "match after rounding",
			a:        models.Coordinates{Lat: 1.234561, Lng: 2.345671},
			b:        models.Coordinates{Lat: 1.234564, Lng: 2.345674},
			expected: true, // Both round to 1.23456, 2.34567
		},
		{
			name:     "no match different lat",
			a:        models.Coordinates{Lat: 1.0, Lng: 2.0},
			b:        models.Coordinates{Lat: 1.1, Lng: 2.0},
			expected: false,
		},
		{
			name:     "no match different lng",
			a:        models.Coordinates{Lat: 1.0, Lng: 2.0},
			b:        models.Coordinates{Lat: 1.0, Lng: 2.1},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := coordsMatch(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("coordsMatch(%v, %v) = %v, expected %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}
