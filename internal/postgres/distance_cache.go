package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type distanceCacheRepository struct {
	db *sql.DB
}

func cacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat), models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat), models.RoundCoordinate(dest.Lng))
}

const cacheColumns = `origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs`

func scanCacheEntry(scanner interface{ Scan(dest ...any) error }) (models.DistanceCacheEntry, error) {
	var entry models.DistanceCacheEntry
	err := scanner.Scan(
		&entry.Origin.Lat, &entry.Origin.Lng,
		&entry.Destination.Lat, &entry.Destination.Lng,
		&entry.DistanceMeters, &entry.DurationSecs,
	)
	return entry, err
}

func (r *distanceCacheRepository) Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	entry, err := scanCacheEntry(r.db.QueryRowContext(ctx, `
		SELECT `+cacheColumns+` FROM distance_cache
		WHERE origin_lat = $1 AND origin_lng = $2 AND dest_lat = $3 AND dest_lng = $4`,
		models.RoundCoordinate(origin.Lat), models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat), models.RoundCoordinate(dest.Lng)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get distance cache entry: %w", err)
	}
	return &entry, nil
}

func (r *distanceCacheRepository) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	result := make(map[string]*models.DistanceCacheEntry, len(pairs))
	if len(pairs) == 0 {
		return result, nil
	}

	seen := make(map[string]struct{}, len(pairs))
	var originLats, originLngs, destLats, destLngs []float64
	for _, pair := range pairs {
		key := cacheKey(pair.Origin, pair.Dest)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		originLats = append(originLats, models.RoundCoordinate(pair.Origin.Lat))
		originLngs = append(originLngs, models.RoundCoordinate(pair.Origin.Lng))
		destLats = append(destLats, models.RoundCoordinate(pair.Dest.Lat))
		destLngs = append(destLngs, models.RoundCoordinate(pair.Dest.Lng))
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT dc.origin_lat, dc.origin_lng, dc.dest_lat, dc.dest_lng, dc.distance_meters, dc.duration_secs
		FROM unnest($1::float8[], $2::float8[], $3::float8[], $4::float8[]) AS requested(origin_lat, origin_lng, dest_lat, dest_lng)
		JOIN distance_cache dc
		  ON dc.origin_lat = requested.origin_lat
		 AND dc.origin_lng = requested.origin_lng
		 AND dc.dest_lat = requested.dest_lat
		 AND dc.dest_lng = requested.dest_lng`,
		originLats, originLngs, destLats, destLngs)
	if err != nil {
		return nil, fmt.Errorf("failed to query batch entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		entry, err := scanCacheEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan batch entry: %w", err)
		}
		result[cacheKey(entry.Origin, entry.Destination)] = &entry
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate batch entries: %w", err)
	}
	return result, nil
}

const upsertCacheEntry = `
	INSERT INTO distance_cache (` + cacheColumns + `)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (origin_lat, origin_lng, dest_lat, dest_lng)
	DO UPDATE SET distance_meters = EXCLUDED.distance_meters, duration_secs = EXCLUDED.duration_secs`

func (r *distanceCacheRepository) Set(ctx context.Context, entry *models.DistanceCacheEntry) error {
	if _, err := r.db.ExecContext(ctx, upsertCacheEntry,
		models.RoundCoordinate(entry.Origin.Lat), models.RoundCoordinate(entry.Origin.Lng),
		models.RoundCoordinate(entry.Destination.Lat), models.RoundCoordinate(entry.Destination.Lng),
		entry.DistanceMeters, entry.DurationSecs); err != nil {
		return fmt.Errorf("failed to set distance cache entry: %w", err)
	}
	return nil
}

func (r *distanceCacheRepository) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}
	n := len(entries)
	originLats := make([]float64, n)
	originLngs := make([]float64, n)
	destLats := make([]float64, n)
	destLngs := make([]float64, n)
	distances := make([]float64, n)
	durations := make([]float64, n)
	for i, entry := range entries {
		originLats[i] = models.RoundCoordinate(entry.Origin.Lat)
		originLngs[i] = models.RoundCoordinate(entry.Origin.Lng)
		destLats[i] = models.RoundCoordinate(entry.Destination.Lat)
		destLngs[i] = models.RoundCoordinate(entry.Destination.Lng)
		distances[i] = entry.DistanceMeters
		durations[i] = entry.DurationSecs
	}
	// One round trip for the whole matrix. Duplicate keys within the batch
	// are collapsed to the last occurrence before the upsert, because ON
	// CONFLICT cannot touch the same row twice in one statement.
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO distance_cache (`+cacheColumns+`)
		SELECT DISTINCT ON (origin_lat, origin_lng, dest_lat, dest_lng)
		       origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs
		FROM unnest($1::float8[], $2::float8[], $3::float8[], $4::float8[], $5::float8[], $6::float8[])
		     WITH ORDINALITY AS e(origin_lat, origin_lng, dest_lat, dest_lng, distance_meters, duration_secs, ord)
		ORDER BY origin_lat, origin_lng, dest_lat, dest_lng, ord DESC
		ON CONFLICT (origin_lat, origin_lng, dest_lat, dest_lng)
		DO UPDATE SET distance_meters = EXCLUDED.distance_meters, duration_secs = EXCLUDED.duration_secs`,
		originLats, originLngs, destLats, destLngs, distances, durations); err != nil {
		return fmt.Errorf("failed to insert batch entries: %w", err)
	}
	return nil
}

func (r *distanceCacheRepository) Clear(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM distance_cache`); err != nil {
		return fmt.Errorf("failed to clear distance cache: %w", err)
	}
	return nil
}
