package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"ride-home-router/internal/models"
)

// DriverRepository handles driver persistence
type DriverRepository interface {
	List(ctx context.Context, search string) ([]models.Driver, error)
	GetByID(ctx context.Context, id int64) (*models.Driver, error)
	GetByIDs(ctx context.Context, ids []int64) ([]models.Driver, error)
	GetInstituteVehicle(ctx context.Context) (*models.Driver, error)
	Create(ctx context.Context, d *models.Driver) (*models.Driver, error)
	Update(ctx context.Context, d *models.Driver) (*models.Driver, error)
	Delete(ctx context.Context, id int64) error
}

type driverRepository struct {
	db *sql.DB
}

func (r *driverRepository) List(ctx context.Context, search string) ([]models.Driver, error) {
	query := `SELECT id, name, address, lat, lng, vehicle_capacity, is_institute_vehicle, created_at, updated_at FROM drivers`
	args := []interface{}{}

	if search != "" {
		query += ` WHERE name LIKE ?`
		args = append(args, "%"+search+"%")
	}

	query += ` ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query drivers: %w", err)
	}
	defer rows.Close()

	var drivers []models.Driver
	for rows.Next() {
		var d models.Driver
		if err := rows.Scan(&d.ID, &d.Name, &d.Address, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.IsInstituteVehicle, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan driver: %w", err)
		}
		drivers = append(drivers, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return drivers, nil
}

func (r *driverRepository) GetByID(ctx context.Context, id int64) (*models.Driver, error) {
	query := `SELECT id, name, address, lat, lng, vehicle_capacity, is_institute_vehicle, created_at, updated_at FROM drivers WHERE id = ?`

	var d models.Driver
	err := r.db.QueryRowContext(ctx, query, id).Scan(&d.ID, &d.Name, &d.Address, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.IsInstituteVehicle, &d.CreatedAt, &d.UpdatedAt)
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

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT id, name, address, lat, lng, vehicle_capacity, is_institute_vehicle, created_at, updated_at FROM drivers WHERE id IN (%s) ORDER BY name ASC`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query drivers by IDs: %w", err)
	}
	defer rows.Close()

	var drivers []models.Driver
	for rows.Next() {
		var d models.Driver
		if err := rows.Scan(&d.ID, &d.Name, &d.Address, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.IsInstituteVehicle, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan driver: %w", err)
		}
		drivers = append(drivers, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return drivers, nil
}

func (r *driverRepository) GetInstituteVehicle(ctx context.Context) (*models.Driver, error) {
	query := `SELECT id, name, address, lat, lng, vehicle_capacity, is_institute_vehicle, created_at, updated_at FROM drivers WHERE is_institute_vehicle = TRUE`

	var d models.Driver
	err := r.db.QueryRowContext(ctx, query).Scan(&d.ID, &d.Name, &d.Address, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.IsInstituteVehicle, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get institute vehicle: %w", err)
	}

	return &d, nil
}

func (r *driverRepository) Create(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	query := `INSERT INTO drivers (name, address, lat, lng, vehicle_capacity, is_institute_vehicle) VALUES (?, ?, ?, ?, ?, ?) RETURNING id, created_at, updated_at`

	err := r.db.QueryRowContext(ctx, query, d.Name, d.Address, d.Lat, d.Lng, d.VehicleCapacity, d.IsInstituteVehicle).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		log.Printf("[DB] Failed to create driver: name=%s address=%s err=%v", d.Name, d.Address, err)
		return nil, fmt.Errorf("failed to create driver: %w", err)
	}

	log.Printf("[DB] Created driver: id=%d name=%s capacity=%d institute=%v", d.ID, d.Name, d.VehicleCapacity, d.IsInstituteVehicle)
	return d, nil
}

func (r *driverRepository) Update(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	query := `UPDATE drivers SET name = ?, address = ?, lat = ?, lng = ?, vehicle_capacity = ?, is_institute_vehicle = ? WHERE id = ? RETURNING updated_at`

	err := r.db.QueryRowContext(ctx, query, d.Name, d.Address, d.Lat, d.Lng, d.VehicleCapacity, d.IsInstituteVehicle, d.ID).Scan(&d.UpdatedAt)
	if err == sql.ErrNoRows {
		log.Printf("[DB] Driver not found for update: id=%d", d.ID)
		return nil, nil
	}
	if err != nil {
		log.Printf("[DB] Failed to update driver: id=%d err=%v", d.ID, err)
		return nil, fmt.Errorf("failed to update driver: %w", err)
	}

	log.Printf("[DB] Updated driver: id=%d name=%s capacity=%d", d.ID, d.Name, d.VehicleCapacity)
	return d, nil
}

func (r *driverRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM drivers WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		log.Printf("[DB] Failed to delete driver: id=%d err=%v", id, err)
		return fmt.Errorf("failed to delete driver: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		log.Printf("[DB] Failed to get rows affected for delete: id=%d err=%v", id, err)
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		log.Printf("[DB] Driver not found for delete: id=%d", id)
		return sql.ErrNoRows
	}

	log.Printf("[DB] Deleted driver: id=%d", id)
	return nil
}
