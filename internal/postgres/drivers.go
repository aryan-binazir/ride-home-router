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

type driverRepository struct {
	db *sql.DB
}

const driverColumns = `id, name, address, COALESCE(address_name, ''), lat, lng, vehicle_capacity, created_at, updated_at`

func scanDriver(scanner interface{ Scan(dest ...any) error }) (models.Driver, error) {
	var d models.Driver
	err := scanner.Scan(&d.ID, &d.Name, &d.Address, &d.AddressName, &d.Lat, &d.Lng, &d.VehicleCapacity, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func (r *driverRepository) List(ctx context.Context, search string) ([]models.Driver, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+driverColumns+`
		FROM drivers
		WHERE $1 = '' OR name ILIKE '%' || $1 || '%'
		ORDER BY name`, search)
	if err != nil {
		return nil, fmt.Errorf("failed to query drivers: %w", err)
	}
	return collectDrivers(rows)
}

func (r *driverRepository) GetByID(ctx context.Context, id int64) (*models.Driver, error) {
	d, err := scanDriver(r.db.QueryRowContext(ctx, `SELECT `+driverColumns+` FROM drivers WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
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
	rows, err := r.db.QueryContext(ctx, `SELECT `+driverColumns+` FROM drivers WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to query drivers by IDs: %w", err)
	}
	return collectDrivers(rows)
}

func collectDrivers(rows *sql.Rows) ([]models.Driver, error) {
	defer func() { _ = rows.Close() }()
	var drivers []models.Driver
	for rows.Next() {
		d, err := scanDriver(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan driver: %w", err)
		}
		drivers = append(drivers, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating drivers: %w", err)
	}
	return drivers, nil
}

const insertDriver = `
	INSERT INTO drivers (name, address, address_name, lat, lng, vehicle_capacity, created_at, updated_at)
	VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8)
	RETURNING id`

func (r *driverRepository) writes() rosterWriteCore[models.Driver] {
	return rosterWriteCore[models.Driver]{
		db:     r.db,
		noun:   "driver",
		table:  "drivers",
		labels: driverLabels,
		key: func(d *models.Driver) string {
			return models.RosterKey(d.Name, d.Address)
		},
		insert: func(ctx context.Context, tx *sql.Tx, d *models.Driver, now time.Time) (int64, error) {
			var id int64
			err := tx.QueryRowContext(ctx, insertDriver,
				d.Name, d.Address, d.AddressName, d.Lat, d.Lng, d.VehicleCapacity, now, now,
			).Scan(&id)
			return id, err
		},
		updateRow: func(ctx context.Context, tx *sql.Tx, d *models.Driver, now time.Time) (sql.Result, error) {
			return tx.ExecContext(ctx, `
				UPDATE drivers
				SET name = $1, address = $2, address_name = NULLIF($3, ''), lat = $4, lng = $5, vehicle_capacity = $6, updated_at = $7
				WHERE id = $8`,
				d.Name, d.Address, d.AddressName, d.Lat, d.Lng, d.VehicleCapacity, now, d.ID)
		},
		importUpdate: func(ctx context.Context, tx *sql.Tx, id int64, d *models.Driver, now time.Time) error {
			_, err := tx.ExecContext(ctx, `
				UPDATE drivers
				SET address_name = COALESCE(NULLIF($1, ''), address_name), vehicle_capacity = $2, updated_at = $3
				WHERE id = $4`,
				d.AddressName, d.VehicleCapacity, now, id)
			return err
		},
		fields: func(d *models.Driver) rosterFields {
			return rosterFields{id: &d.ID, createdAt: &d.CreatedAt, updatedAt: &d.UpdatedAt}
		},
	}
}

func (r *driverRepository) Create(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	return r.CreateWithLabels(ctx, d, nil)
}

func (r *driverRepository) UpsertBatch(ctx context.Context, drivers []*models.Driver) (database.BatchUpsertResult, error) {
	return r.writes().upsertBatch(ctx, drivers)
}

func (r *driverRepository) CreateWithLabels(ctx context.Context, d *models.Driver, labelIDs []int64) (*models.Driver, error) {
	return r.writes().createWithLabels(ctx, d, labelIDs)
}

func (r *driverRepository) Update(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	return r.writes().updateWithLabels(ctx, d, nil, false)
}

func (r *driverRepository) UpdateWithLabels(ctx context.Context, d *models.Driver, labelIDs []int64) (*models.Driver, error) {
	return r.writes().updateWithLabels(ctx, d, labelIDs, true)
}

func (r *driverRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM drivers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete driver: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}
