package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"ride-home-router/internal/models"
)

// ActivityLocationRepository handles activity location persistence
type ActivityLocationRepository interface {
	List(ctx context.Context) ([]models.ActivityLocation, error)
	GetByID(ctx context.Context, id int64) (*models.ActivityLocation, error)
	Create(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error)
	Delete(ctx context.Context, id int64) error
}

type activityLocationRepository struct {
	db *sql.DB
}

func (r *activityLocationRepository) List(ctx context.Context) ([]models.ActivityLocation, error) {
	query := `SELECT id, name, address, lat, lng FROM activity_locations ORDER BY name`

	rows, err := r.db.QueryContext(ctx, query)
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
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return locations, nil
}

func (r *activityLocationRepository) GetByID(ctx context.Context, id int64) (*models.ActivityLocation, error) {
	query := `SELECT id, name, address, lat, lng FROM activity_locations WHERE id = ?`

	var loc models.ActivityLocation
	err := r.db.QueryRowContext(ctx, query, id).Scan(&loc.ID, &loc.Name, &loc.Address, &loc.Lat, &loc.Lng)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get activity location: %w", err)
	}

	return &loc, nil
}

func (r *activityLocationRepository) Create(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error) {
	query := `INSERT INTO activity_locations (name, address, lat, lng) VALUES (?, ?, ?, ?)`

	result, err := r.db.ExecContext(ctx, query, loc.Name, loc.Address, loc.Lat, loc.Lng)
	if err != nil {
		log.Printf("[DB] Failed to create activity location: err=%v", err)
		return nil, fmt.Errorf("failed to create activity location: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}

	loc.ID = id
	log.Printf("[DB] Created activity location: id=%d name=%s", loc.ID, loc.Name)
	return loc, nil
}

func (r *activityLocationRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM activity_locations WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		log.Printf("[DB] Failed to delete activity location: err=%v", err)
		return fmt.Errorf("failed to delete activity location: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("activity location not found")
	}

	log.Printf("[DB] Deleted activity location: id=%d", id)
	return nil
}
