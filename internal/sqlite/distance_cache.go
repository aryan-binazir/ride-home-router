package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"ride-home-router/internal/database"
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
		return nil, database.ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get distance cache entry: %w", err)
	}

	return &entry, nil
}

const distanceCacheBatchSize = 200

func (r *distanceCacheRepository) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	if len(pairs) == 0 {
		return make(map[string]*models.DistanceCacheEntry), nil
	}

	uniquePairs := make([]struct{ Origin, Dest models.Coordinates }, 0, len(pairs))
	seen := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		key := makeCacheKey(pair.Origin, pair.Dest)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		uniquePairs = append(uniquePairs, struct{ Origin, Dest models.Coordinates }{
			Origin: pair.Origin,
			Dest:   pair.Dest,
		})
	}

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	result := make(map[string]*models.DistanceCacheEntry, len(uniquePairs))
	for start := 0; start < len(uniquePairs); start += distanceCacheBatchSize {
		end := min(start+distanceCacheBatchSize, len(uniquePairs))
		chunk := uniquePairs[start:end]

		query, args := buildDistanceCacheBatchQuery(chunk)
		rows, err := r.store.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to query batch entries: %w", err)
		}

		for rows.Next() {
			var entry models.DistanceCacheEntry
			if err := rows.Scan(
				&entry.Origin.Lat, &entry.Origin.Lng,
				&entry.Destination.Lat, &entry.Destination.Lng,
				&entry.DistanceMeters, &entry.DurationSecs,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan batch entry: %w", err)
			}
			key := makeCacheKey(entry.Origin, entry.Destination)
			result[key] = &entry
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("failed to close batch rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("failed to iterate batch entries: %w", err)
		}
	}

	return result, nil
}

func buildDistanceCacheBatchQuery(pairs []struct{ Origin, Dest models.Coordinates }) (string, []any) {
	valuePlaceholders := make([]string, len(pairs))
	args := make([]any, 0, len(pairs)*4)
	for i, pair := range pairs {
		valuePlaceholders[i] = "(?, ?, ?, ?)"
		args = append(args,
			models.RoundCoordinate(pair.Origin.Lat),
			models.RoundCoordinate(pair.Origin.Lng),
			models.RoundCoordinate(pair.Dest.Lat),
			models.RoundCoordinate(pair.Dest.Lng),
		)
	}

	query := fmt.Sprintf(`WITH requested(origin_lat, origin_lng, dest_lat, dest_lng) AS (
		VALUES %s
	)
	SELECT dc.origin_lat, dc.origin_lng, dc.dest_lat, dc.dest_lng,
	       dc.distance_meters, dc.duration_secs
	FROM requested r
	JOIN distance_cache dc
	  ON dc.origin_lat = r.origin_lat
	 AND dc.origin_lng = r.origin_lng
	 AND dc.dest_lat = r.dest_lat
	 AND dc.dest_lng = r.dest_lng`, strings.Join(valuePlaceholders, ", "))

	return query, args
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
	defer func() { _ = tx.Rollback() }()

	query := `INSERT OR REPLACE INTO distance_cache
	          (origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs)
	          VALUES (?, ?, ?, ?, ?, ?)`

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

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
