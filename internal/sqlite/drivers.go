package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"ride-home-router/internal/models"
)

type driverRepository struct {
	store *Store
}

func (r *driverRepository) List(ctx context.Context, search string) ([]models.Driver, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var rows *sql.Rows
	var err error

	if search != "" {
		query := `SELECT id, name, address, lat, lng, vehicle_capacity, created_at, updated_at
		          FROM drivers
		          WHERE name LIKE ?
		          ORDER BY name`
		rows, err = r.store.db.QueryContext(ctx, query, "%"+search+"%")
	} else {
		query := `SELECT id, name, address, lat, lng, vehicle_capacity, created_at, updated_at
		          FROM drivers
		          ORDER BY name`
		rows, err = r.store.db.QueryContext(ctx, query)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query drivers: %w", err)
	}
	defer rows.Close()

	var drivers []models.Driver
	for rows.Next() {
		var d models.Driver
		if err := rows.Scan(&d.ID, &d.Name, &d.Address, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan driver: %w", err)
		}
		drivers = append(drivers, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating drivers: %w", err)
	}

	return drivers, nil
}

func (r *driverRepository) GetByID(ctx context.Context, id int64) (*models.Driver, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT id, name, address, lat, lng, vehicle_capacity, created_at, updated_at
	          FROM drivers WHERE id = ?`

	var d models.Driver
	err := r.store.db.QueryRowContext(ctx, query, id).Scan(
		&d.ID, &d.Name, &d.Address, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.CreatedAt, &d.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get driver: %w", err)
	}

	return &d, nil
}

func (r *driverRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.Driver, error) {
	if len(ids) == 0 {
		return []models.Driver{}, nil
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
		`SELECT id, name, address, lat, lng, vehicle_capacity, created_at, updated_at
		 FROM drivers WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query drivers by IDs: %w", err)
	}
	defer rows.Close()

	var drivers []models.Driver
	for rows.Next() {
		var d models.Driver
		if err := rows.Scan(&d.ID, &d.Name, &d.Address, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan driver: %w", err)
		}
		drivers = append(drivers, d)
	}

	return drivers, rows.Err()
}

func (r *driverRepository) Create(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now

	query := `INSERT INTO drivers (name, address, lat, lng, vehicle_capacity, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`

	result, err := r.store.db.ExecContext(ctx, query,
		d.Name, d.Address, d.Lat, d.Lng, d.VehicleCapacity, d.CreatedAt, d.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create driver: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}
	d.ID = id

	return d, nil
}

func (r *driverRepository) Update(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	d.UpdatedAt = time.Now()

	query := `UPDATE drivers
	          SET name = ?, address = ?, lat = ?, lng = ?, vehicle_capacity = ?, updated_at = ?
	          WHERE id = ?`

	result, err := r.store.db.ExecContext(ctx, query,
		d.Name, d.Address, d.Lat, d.Lng, d.VehicleCapacity, d.UpdatedAt, d.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update driver: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("driver not found")
	}

	return d, nil
}

func (r *driverRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	query := `DELETE FROM drivers WHERE id = ?`
	result, err := r.store.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete driver: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("driver not found")
	}

	return nil
}
