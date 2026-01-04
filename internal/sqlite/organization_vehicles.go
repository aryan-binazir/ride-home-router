package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"ride-home-router/internal/models"
)

type organizationVehicleRepository struct {
	store *Store
}

func (r *organizationVehicleRepository) List(ctx context.Context) ([]models.OrganizationVehicle, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT id, name, capacity, created_at, updated_at
	          FROM organization_vehicles
	          ORDER BY name`

	rows, err := r.store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query organization vehicles: %w", err)
	}
	defer rows.Close()

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

func (r *organizationVehicleRepository) GetByID(ctx context.Context, id int64) (*models.OrganizationVehicle, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT id, name, capacity, created_at, updated_at
	          FROM organization_vehicles WHERE id = ?`

	var v models.OrganizationVehicle
	err := r.store.db.QueryRowContext(ctx, query, id).Scan(
		&v.ID, &v.Name, &v.Capacity, &v.CreatedAt, &v.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
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

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT id, name, capacity, created_at, updated_at
		 FROM organization_vehicles WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query organization vehicles by IDs: %w", err)
	}
	defer rows.Close()

	var vehicles []models.OrganizationVehicle
	for rows.Next() {
		var v models.OrganizationVehicle
		if err := rows.Scan(&v.ID, &v.Name, &v.Capacity, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan organization vehicle: %w", err)
		}
		vehicles = append(vehicles, v)
	}

	return vehicles, rows.Err()
}

func (r *organizationVehicleRepository) Create(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	now := time.Now()
	v.CreatedAt = now
	v.UpdatedAt = now

	query := `INSERT INTO organization_vehicles (name, capacity, created_at, updated_at)
	          VALUES (?, ?, ?, ?)`

	result, err := r.store.db.ExecContext(ctx, query, v.Name, v.Capacity, v.CreatedAt, v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create organization vehicle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}
	v.ID = id

	return v, nil
}

func (r *organizationVehicleRepository) Update(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	v.UpdatedAt = time.Now()

	query := `UPDATE organization_vehicles
	          SET name = ?, capacity = ?, updated_at = ?
	          WHERE id = ?`

	result, err := r.store.db.ExecContext(ctx, query, v.Name, v.Capacity, v.UpdatedAt, v.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update organization vehicle: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("organization vehicle not found")
	}

	return v, nil
}

func (r *organizationVehicleRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	query := `DELETE FROM organization_vehicles WHERE id = ?`
	result, err := r.store.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete organization vehicle: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("organization vehicle not found")
	}

	return nil
}
