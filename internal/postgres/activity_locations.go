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

const activityLocationColumns = `id, name, address, lat, lng, deleted_at`

func scanActivityLocation(scanner interface{ Scan(dest ...any) error }) (models.ActivityLocation, error) {
	var loc models.ActivityLocation
	err := scanner.Scan(&loc.ID, &loc.Name, &loc.Address, &loc.Lat, &loc.Lng, &loc.DeletedAt)
	return loc, err
}

func (r *activityLocationRepository) List(ctx context.Context) ([]models.ActivityLocation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+activityLocationColumns+` FROM activity_locations WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("failed to query activity locations: %w", err)
	}
	return collectActivityLocations(rows)
}

func (r *activityLocationRepository) ListDeleted(ctx context.Context) ([]models.ActivityLocation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+activityLocationColumns+` FROM activity_locations WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query deleted activity locations: %w", err)
	}
	return collectActivityLocations(rows)
}

func collectActivityLocations(rows *sql.Rows) ([]models.ActivityLocation, error) {
	defer func() { _ = rows.Close() }()
	var locations []models.ActivityLocation
	for rows.Next() {
		loc, err := scanActivityLocation(rows)
		if err != nil {
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
	loc, err := scanActivityLocation(r.db.QueryRowContext(ctx, `SELECT `+activityLocationColumns+` FROM activity_locations WHERE id = $1 AND deleted_at IS NULL`, id))
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
		UPDATE activity_locations SET name = $1, address = $2, lat = $3, lng = $4 WHERE id = $5 AND deleted_at IS NULL`,
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin activity location delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `UPDATE activity_locations SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("failed to delete activity location: %w", err)
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET selected_activity_location_id = NULL WHERE selected_activity_location_id = $1`, id); err != nil {
		return fmt.Errorf("failed to clear deleted activity location from settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit activity location delete: %w", err)
	}
	return nil
}

func (r *activityLocationRepository) Restore(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE activity_locations SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL`, id)
	if err != nil {
		return fmt.Errorf("failed to restore activity location: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}
