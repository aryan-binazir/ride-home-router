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

func (r *driverRepository) Create(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	return r.CreateWithLabels(ctx, d, nil)
}

func (r *driverRepository) CreateBatch(ctx context.Context, drivers []*models.Driver, allowExistingDuplicate []bool) (database.BatchCreateResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to begin driver batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, "drivers"); err != nil {
		return database.BatchCreateResult{}, err
	}
	existing, err := rosterKeys(ctx, tx, "drivers")
	if err != nil {
		return database.BatchCreateResult{}, err
	}
	batchResult := database.BatchCreateResult{}

	type created struct {
		driver    *models.Driver
		id        int64
		createdAt time.Time
	}
	var createdRows []created
	for i, driver := range drivers {
		if driver == nil {
			return database.BatchCreateResult{}, errors.New("driver batch contains a nil driver")
		}
		key := models.RosterKey(driver.Name, driver.Address)
		_, duplicate := existing[key]
		allowDuplicate := i < len(allowExistingDuplicate) && allowExistingDuplicate[i]
		if key != "" && duplicate && !allowDuplicate {
			batchResult.SkippedDuplicate++
			continue
		}
		now := time.Now()
		var id int64
		if err := tx.QueryRowContext(ctx, insertDriver,
			driver.Name, driver.Address, driver.AddressName, driver.Lat, driver.Lng, driver.VehicleCapacity, now, now,
		).Scan(&id); err != nil {
			return database.BatchCreateResult{}, fmt.Errorf("failed to create driver in batch: %w", err)
		}
		createdRows = append(createdRows, created{driver: driver, id: id, createdAt: now})
		batchResult.Created++
	}

	if err := tx.Commit(); err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to commit driver batch transaction: %w", err)
	}
	for _, item := range createdRows {
		item.driver.ID = item.id
		item.driver.CreatedAt = item.createdAt
		item.driver.UpdatedAt = item.createdAt
	}
	return batchResult, nil
}

func (r *driverRepository) CreateWithLabels(ctx context.Context, d *models.Driver, labelIDs []int64) (*models.Driver, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin driver transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, "drivers"); err != nil {
		return nil, err
	}
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now
	if err := tx.QueryRowContext(ctx, insertDriver,
		d.Name, d.Address, d.AddressName, d.Lat, d.Lng, d.VehicleCapacity, d.CreatedAt, d.UpdatedAt,
	).Scan(&d.ID); err != nil {
		return nil, fmt.Errorf("failed to create driver: %w", err)
	}
	if err := insertLabelMemberships(ctx, tx, driverLabels, d.ID, labelIDs); err != nil {
		return nil, fmt.Errorf("failed to insert driver label memberships: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit driver transaction: %w", err)
	}
	return d, nil
}

func (r *driverRepository) Update(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	return r.update(ctx, d, nil, false)
}

func (r *driverRepository) UpdateWithLabels(ctx context.Context, d *models.Driver, labelIDs []int64) (*models.Driver, error) {
	return r.update(ctx, d, labelIDs, true)
}

func (r *driverRepository) update(ctx context.Context, d *models.Driver, labelIDs []int64, replaceLabels bool) (*models.Driver, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin driver transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	d.UpdatedAt = time.Now()
	result, err := tx.ExecContext(ctx, `
		UPDATE drivers
		SET name = $1, address = $2, address_name = NULLIF($3, ''), lat = $4, lng = $5, vehicle_capacity = $6, updated_at = $7
		WHERE id = $8`,
		d.Name, d.Address, d.AddressName, d.Lat, d.Lng, d.VehicleCapacity, d.UpdatedAt, d.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update driver: %w", err)
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return nil, err
	}
	if replaceLabels {
		if err := replaceLabelMemberships(ctx, tx, driverLabels, d.ID, labelIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit driver transaction: %w", err)
	}
	return d, nil
}

func (r *driverRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM drivers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete driver: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}
