package database

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"ride-home-router/internal/models"
)

// DistanceCacheRepository handles distance cache persistence
type DistanceCacheRepository interface {
	Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error)
	GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error)
	Set(ctx context.Context, entry *models.DistanceCacheEntry) error
	SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error
	Clear(ctx context.Context) error
}

type distanceCacheRepository struct {
	db *sql.DB
}

func roundCoord(coord float64) float64 {
	return math.Round(coord*100000) / 100000
}

func makeCacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
}

func (r *distanceCacheRepository) Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	query := `
		SELECT origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs
		FROM distance_cache
		WHERE ROUND(origin_lat, 5) = ROUND(?, 5)
		  AND ROUND(origin_lng, 5) = ROUND(?, 5)
		  AND ROUND(dest_lat, 5) = ROUND(?, 5)
		  AND ROUND(dest_lng, 5) = ROUND(?, 5)
	`

	var entry models.DistanceCacheEntry
	err := r.db.QueryRowContext(ctx, query, origin.Lat, origin.Lng, dest.Lat, dest.Lng).Scan(
		&entry.Origin.Lat, &entry.Origin.Lng, &entry.Destination.Lat, &entry.Destination.Lng,
		&entry.DistanceMeters, &entry.DurationSecs,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get cached distance: %w", err)
	}

	return &entry, nil
}

func (r *distanceCacheRepository) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	if len(pairs) == 0 {
		return make(map[string]*models.DistanceCacheEntry), nil
	}

	result := make(map[string]*models.DistanceCacheEntry)

	for _, pair := range pairs {
		entry, err := r.Get(ctx, pair.Origin, pair.Dest)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			key := makeCacheKey(pair.Origin, pair.Dest)
			result[key] = entry
		}
	}

	return result, nil
}

func (r *distanceCacheRepository) Set(ctx context.Context, entry *models.DistanceCacheEntry) error {
	query := `
		INSERT INTO distance_cache (origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(ROUND(origin_lat, 5), ROUND(origin_lng, 5), ROUND(dest_lat, 5), ROUND(dest_lng, 5))
		DO UPDATE SET distance_meters = excluded.distance_meters, duration_secs = excluded.duration_secs, cached_at = CURRENT_TIMESTAMP
	`

	_, err := r.db.ExecContext(ctx, query,
		entry.Origin.Lat, entry.Origin.Lng, entry.Destination.Lat, entry.Destination.Lng,
		entry.DistanceMeters, entry.DurationSecs,
	)
	if err != nil {
		return fmt.Errorf("failed to set cached distance: %w", err)
	}

	return nil
}

func (r *distanceCacheRepository) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO distance_cache (origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(ROUND(origin_lat, 5), ROUND(origin_lng, 5), ROUND(dest_lat, 5), ROUND(dest_lng, 5))
		DO UPDATE SET distance_meters = excluded.distance_meters, duration_secs = excluded.duration_secs, cached_at = CURRENT_TIMESTAMP
	`

	for _, entry := range entries {
		_, err := tx.ExecContext(ctx, query,
			entry.Origin.Lat, entry.Origin.Lng, entry.Destination.Lat, entry.Destination.Lng,
			entry.DistanceMeters, entry.DurationSecs,
		)
		if err != nil {
			return fmt.Errorf("failed to set cached distance: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (r *distanceCacheRepository) Clear(ctx context.Context) error {
	query := `DELETE FROM distance_cache`

	_, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to clear distance cache: %w", err)
	}

	return nil
}
