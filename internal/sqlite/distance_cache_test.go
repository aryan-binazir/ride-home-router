package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"ride-home-router/internal/models"
)

func TestDistanceCacheGetBatch_HitsMissesAndDuplicates(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "distance-cache.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	origin := models.Coordinates{Lat: 40.12345, Lng: -74.12345}
	destHit := models.Coordinates{Lat: 40.23456, Lng: -74.23456}
	destMiss := models.Coordinates{Lat: 40.34567, Lng: -74.34567}

	if err := store.DistanceCache().Set(ctx, &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    destHit,
		DistanceMeters: 1500,
		DurationSecs:   180,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	pairs := []struct{ Origin, Dest models.Coordinates }{
		{Origin: origin, Dest: destHit},
		{Origin: origin, Dest: destHit},
		{Origin: origin, Dest: destMiss},
	}

	result, err := store.DistanceCache().GetBatch(ctx, pairs)
	if err != nil {
		t.Fatalf("GetBatch() error = %v", err)
	}

	hitKey := makeCacheKey(origin, destHit)
	missKey := makeCacheKey(origin, destMiss)
	if result[hitKey] == nil {
		t.Fatalf("expected cache hit for key %s", hitKey)
	}
	if result[missKey] != nil {
		t.Fatalf("did not expect cache entry for miss key %s", missKey)
	}
	if result[hitKey].DistanceMeters != 1500 {
		t.Fatalf("hit distance = %.0f, want 1500", result[hitKey].DistanceMeters)
	}
}

func TestDistanceCacheGetBatch_NoHits(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "distance-cache-miss.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	origin := models.Coordinates{Lat: 40.11111, Lng: -74.11111}
	dest := models.Coordinates{Lat: 40.22222, Lng: -74.22222}
	pairs := []struct{ Origin, Dest models.Coordinates }{
		{Origin: origin, Dest: dest},
	}

	result, err := store.DistanceCache().GetBatch(context.Background(), pairs)
	if err != nil {
		t.Fatalf("GetBatch() error = %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty result map, got %d entries", len(result))
	}
}

func TestDistanceCacheGetBatch_EmptyInput(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "distance-cache-empty.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	result, err := store.DistanceCache().GetBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetBatch() error = %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty result map, got %d entries", len(result))
	}
}
