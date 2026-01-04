package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"ride-home-router/internal/models"
)

type distanceCacheRepository struct {
	store *Store
}

func makeCacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat),
		models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat),
		models.RoundCoordinate(dest.Lng),
	)
}

func (r *distanceCacheRepository) Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs
	          FROM distance_cache
	          WHERE origin_lat = ? AND origin_lng = ? AND dest_lat = ? AND dest_lng = ?`

	originLat := models.RoundCoordinate(origin.Lat)
	originLng := models.RoundCoordinate(origin.Lng)
	destLat := models.RoundCoordinate(dest.Lat)
	destLng := models.RoundCoordinate(dest.Lng)

	var entry models.DistanceCacheEntry
	err := r.store.db.QueryRowContext(ctx, query, originLat, originLng, destLat, destLng).Scan(
		&entry.Origin.Lat, &entry.Origin.Lng,
		&entry.Destination.Lat, &entry.Destination.Lng,
		&entry.DistanceMeters, &entry.DurationSecs,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get distance cache entry: %w", err)
	}

	return &entry, nil
}

func (r *distanceCacheRepository) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	if len(pairs) == 0 {
		return make(map[string]*models.DistanceCacheEntry), nil
	}

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	result := make(map[string]*models.DistanceCacheEntry)

	// SQLite doesn't support tuple comparisons well, so we query each pair
	// For large batches, this could be optimized with a temporary table
	query := `SELECT origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs
	          FROM distance_cache
	          WHERE origin_lat = ? AND origin_lng = ? AND dest_lat = ? AND dest_lng = ?`

	stmt, err := r.store.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare batch query: %w", err)
	}
	defer stmt.Close()

	for _, pair := range pairs {
		originLat := models.RoundCoordinate(pair.Origin.Lat)
		originLng := models.RoundCoordinate(pair.Origin.Lng)
		destLat := models.RoundCoordinate(pair.Dest.Lat)
		destLng := models.RoundCoordinate(pair.Dest.Lng)

		var entry models.DistanceCacheEntry
		err := stmt.QueryRowContext(ctx, originLat, originLng, destLat, destLng).Scan(
			&entry.Origin.Lat, &entry.Origin.Lng,
			&entry.Destination.Lat, &entry.Destination.Lng,
			&entry.DistanceMeters, &entry.DurationSecs,
		)

		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to query batch entry: %w", err)
		}

		key := makeCacheKey(pair.Origin, pair.Dest)
		result[key] = &entry
	}

	return result, nil
}

func (r *distanceCacheRepository) Set(ctx context.Context, entry *models.DistanceCacheEntry) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	query := `INSERT OR REPLACE INTO distance_cache
	          (origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs)
	          VALUES (?, ?, ?, ?, ?, ?)`

	originLat := models.RoundCoordinate(entry.Origin.Lat)
	originLng := models.RoundCoordinate(entry.Origin.Lng)
	destLat := models.RoundCoordinate(entry.Destination.Lat)
	destLng := models.RoundCoordinate(entry.Destination.Lng)

	_, err := r.store.db.ExecContext(ctx, query,
		originLat, originLng, destLat, destLng,
		entry.DistanceMeters, entry.DurationSecs,
	)
	if err != nil {
		return fmt.Errorf("failed to set distance cache entry: %w", err)
	}

	return nil
}

func (r *distanceCacheRepository) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT OR REPLACE INTO distance_cache
	          (origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs)
	          VALUES (?, ?, ?, ?, ?, ?)`

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, entry := range entries {
		originLat := models.RoundCoordinate(entry.Origin.Lat)
		originLng := models.RoundCoordinate(entry.Origin.Lng)
		destLat := models.RoundCoordinate(entry.Destination.Lat)
		destLng := models.RoundCoordinate(entry.Destination.Lng)

		_, err := stmt.ExecContext(ctx, originLat, originLng, destLat, destLng,
			entry.DistanceMeters, entry.DurationSecs)
		if err != nil {
			return fmt.Errorf("failed to insert batch entry: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (r *distanceCacheRepository) Clear(ctx context.Context) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	_, err := r.store.db.ExecContext(ctx, "DELETE FROM distance_cache")
	if err != nil {
		return fmt.Errorf("failed to clear distance cache: %w", err)
	}

	return nil
}
