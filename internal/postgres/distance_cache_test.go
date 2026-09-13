package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func cacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat), models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat), models.RoundCoordinate(dest.Lng))
}

func TestDistanceCacheGetBatch_HitsMissesAndDuplicates(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	origin := models.Coordinates{Lat: 40.12345, Lng: -74.12345}
	destHit := models.Coordinates{Lat: 40.23456, Lng: -74.23456}
	destMiss := models.Coordinates{Lat: 40.34567, Lng: -74.34567}

	if err := store.DistanceCache().Set(ctx, &models.DistanceCacheEntry{
		Origin: origin, Destination: destHit, DistanceMeters: 1500, DurationSecs: 180,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	result, err := store.DistanceCache().GetBatch(ctx, []struct{ Origin, Dest models.Coordinates }{
		{Origin: origin, Dest: destHit},
		{Origin: origin, Dest: destHit},
		{Origin: origin, Dest: destMiss},
	})
	if err != nil {
		t.Fatalf("GetBatch() error = %v", err)
	}
	if hit := result[cacheKey(origin, destHit)]; hit == nil || hit.DistanceMeters != 1500 {
		t.Fatalf("hit = %#v, want distance 1500", hit)
	}
	if miss := result[cacheKey(origin, destMiss)]; miss != nil {
		t.Fatalf("miss = %#v, want nil", miss)
	}
	if len(result) != 1 {
		t.Fatalf("result len = %d, want 1", len(result))
	}
}

func TestDistanceCacheSetOverwritesAndClears(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	origin := models.Coordinates{Lat: 40.111111, Lng: -74.111111}
	dest := models.Coordinates{Lat: 40.222222, Lng: -74.222222}

	if err := store.DistanceCache().SetBatch(ctx, []models.DistanceCacheEntry{
		{Origin: origin, Destination: dest, DistanceMeters: 100, DurationSecs: 10},
		{Origin: origin, Destination: dest, DistanceMeters: 200, DurationSecs: 20},
	}); err != nil {
		t.Fatalf("SetBatch() error = %v", err)
	}
	entry, err := store.DistanceCache().Get(ctx, origin, dest)
	if err != nil || entry.DistanceMeters != 200 {
		t.Fatalf("Get() = %#v, %v; want last write with rounded coordinates", entry, err)
	}
	if entry.Origin.Lat != models.RoundCoordinate(origin.Lat) {
		t.Fatalf("stored origin lat = %v, want rounded %v", entry.Origin.Lat, models.RoundCoordinate(origin.Lat))
	}

	if err := store.DistanceCache().Clear(ctx); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if _, err := store.DistanceCache().Get(ctx, origin, dest); !errors.Is(err, database.ErrCacheMiss) {
		t.Fatalf("Get() after clear error = %v, want ErrCacheMiss", err)
	}
	result, err := store.DistanceCache().GetBatch(ctx, nil)
	if err != nil || len(result) != 0 {
		t.Fatalf("GetBatch(nil) = %#v, %v; want empty", result, err)
	}
}
