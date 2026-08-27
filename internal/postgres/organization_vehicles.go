package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"time"
)

type organizationVehicleRepository struct {
	db *sql.DB
}

const vehicleColumns = `id, name, capacity, created_at, updated_at`

func (r *organizationVehicleRepository) List(ctx context.Context) ([]models.OrganizationVehicle, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+vehicleColumns+` FROM organization_vehicles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("failed to query organization vehicles: %w", err)
	}
	return collectVehicles(rows)
}

func (r *organizationVehicleRepository) GetByID(ctx context.Context, id int64) (*models.OrganizationVehicle, error) {
	var v models.OrganizationVehicle
	err := r.db.QueryRowContext(ctx, `SELECT `+vehicleColumns+` FROM organization_vehicles WHERE id = $1`, id).
		Scan(&v.ID, &v.Name, &v.Capacity, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get organization vehicle: %w", err)
	}
	return &v, nil
}

func (r *organizationVehicleRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.OrganizationVehicle, error) {
	if len(ids) == 0 {
		return []models.OrganizationVehicle{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+vehicleColumns+` FROM organization_vehicles WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to query organization vehicles by IDs: %w", err)
	}
	return collectVehicles(rows)
}

func collectVehicles(rows *sql.Rows) ([]models.OrganizationVehicle, error) {
	defer func() { _ = rows.Close() }()
	var vehicles []models.OrganizationVehicle
	for rows.Next() {
		var v models.OrganizationVehicle
		if err := rows.Scan(&v.ID, &v.Name, &v.Capacity, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan organization vehicle: %w", err)
		}
		vehicles = append(vehicles, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating organization vehicles: %w", err)
	}
	return vehicles, nil
}

func (r *organizationVehicleRepository) Create(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error) {
	now := time.Now()
	v.CreatedAt = now
	v.UpdatedAt = now
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO organization_vehicles (name, capacity, created_at, updated_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		v.Name, v.Capacity, v.CreatedAt, v.UpdatedAt).Scan(&v.ID); err != nil {
		return nil, fmt.Errorf("failed to create organization vehicle: %w", err)
	}
	return v, nil
}

func (r *organizationVehicleRepository) Update(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error) {
	v.UpdatedAt = time.Now()
	result, err := r.db.ExecContext(ctx, `
		UPDATE organization_vehicles SET name = $1, capacity = $2, updated_at = $3 WHERE id = $4`,
		v.Name, v.Capacity, v.UpdatedAt, v.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update organization vehicle: %w", err)
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return nil, err
	}
	return v, nil
}

func (r *organizationVehicleRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM organization_vehicles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete organization vehicle: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}
