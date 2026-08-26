package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"ride-home-router/internal/models"
)

type settingsRepository struct {
	db *sql.DB
}

func (r *settingsRepository) Get(ctx context.Context) (*models.Settings, error) {
	var s models.Settings
	var selectedLocationID sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `SELECT selected_activity_location_id, use_miles FROM settings WHERE id = 1`).
		Scan(&selectedLocationID, &s.UseMiles); err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}
	if selectedLocationID.Valid {
		s.SelectedActivityLocationID = selectedLocationID.Int64
	}
	return &s, nil
}

func (r *settingsRepository) Update(ctx context.Context, s *models.Settings) error {
	var selectedLocationID *int64
	if s.SelectedActivityLocationID != 0 {
		selectedLocationID = &s.SelectedActivityLocationID
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE settings SET selected_activity_location_id = $1, use_miles = $2 WHERE id = 1`,
		selectedLocationID, s.UseMiles); err != nil {
		return fmt.Errorf("failed to update settings: %w", err)
	}
	return nil
}
