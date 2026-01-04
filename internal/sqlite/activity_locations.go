package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"ride-home-router/internal/models"
)

type activityLocationRepository struct {
	store *Store
}

func (r *activityLocationRepository) List(ctx context.Context) ([]models.ActivityLocation, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT id, name, address, lat, lng FROM activity_locations ORDER BY name`

	rows, err := r.store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query activity locations: %w", err)
	}
	defer rows.Close()

	var locations []models.ActivityLocation
	for rows.Next() {
		var loc models.ActivityLocation
		if err := rows.Scan(&loc.ID, &loc.Name, &loc.Address, &loc.Lat, &loc.Lng); err != nil {
			return nil, fmt.Errorf("failed to scan activity location: %w", err)
		}
		locations = append(locations, loc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating activity locations: %w", err)
	}

	return locations, nil
}

func (r *activityLocationRepository) GetByID(ctx context.Context, id int64) (*models.ActivityLocation, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT id, name, address, lat, lng FROM activity_locations WHERE id = ?`

	var loc models.ActivityLocation
	err := r.store.db.QueryRowContext(ctx, query, id).Scan(
		&loc.ID, &loc.Name, &loc.Address, &loc.Lat, &loc.Lng,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get activity location: %w", err)
	}

	return &loc, nil
}

func (r *activityLocationRepository) Create(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	query := `INSERT INTO activity_locations (name, address, lat, lng) VALUES (?, ?, ?, ?)`

	result, err := r.store.db.ExecContext(ctx, query, loc.Name, loc.Address, loc.Lat, loc.Lng)
	if err != nil {
		return nil, fmt.Errorf("failed to create activity location: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}
	loc.ID = id

	return loc, nil
}

func (r *activityLocationRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	query := `DELETE FROM activity_locations WHERE id = ?`
	result, err := r.store.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete activity location: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("activity location not found")
	}

	return nil
}
