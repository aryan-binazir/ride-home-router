package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type activityLocationRepository struct {
	db *sql.DB
}

func (r *activityLocationRepository) List(ctx context.Context) ([]models.ActivityLocation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, address, lat, lng FROM activity_locations ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("failed to query activity locations: %w", err)
	}
	defer func() { _ = rows.Close() }()

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
	var loc models.ActivityLocation
	err := r.db.QueryRowContext(ctx, `SELECT id, name, address, lat, lng FROM activity_locations WHERE id = $1`, id).
		Scan(&loc.ID, &loc.Name, &loc.Address, &loc.Lat, &loc.Lng)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get activity location: %w", err)
	}
	return &loc, nil
}

func (r *activityLocationRepository) Create(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error) {
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO activity_locations (name, address, lat, lng) VALUES ($1, $2, $3, $4) RETURNING id`,
		loc.Name, loc.Address, loc.Lat, loc.Lng).Scan(&loc.ID); err != nil {
		return nil, fmt.Errorf("failed to create activity location: %w", err)
	}
	return loc, nil
}

func (r *activityLocationRepository) Update(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE activity_locations SET name = $1, address = $2, lat = $3, lng = $4 WHERE id = $5`,
		loc.Name, loc.Address, loc.Lat, loc.Lng, loc.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update activity location: %w", err)
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return nil, err
	}
	return loc, nil
}

func (r *activityLocationRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM activity_locations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete activity location: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}
