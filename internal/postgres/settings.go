package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type settingsRepository struct {
	db *sql.DB
}

func (r *settingsRepository) Get(ctx context.Context) (*models.Settings, error) {
	var s models.Settings
	var selectedLocationID sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `
		SELECT location.id, settings.use_miles, settings.sme_email
		FROM settings
		LEFT JOIN activity_locations location
		  ON location.id = settings.selected_activity_location_id
		 AND location.deleted_at IS NULL
		WHERE settings.id = 1`).
		Scan(&selectedLocationID, &s.UseMiles, &s.SMEEmail); err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}
	if selectedLocationID.Valid {
		s.SelectedActivityLocationID = selectedLocationID.Int64
	}
	return &s, nil
}

func (r *settingsRepository) Update(ctx context.Context, s *models.Settings) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin settings transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var selectedLocationID *int64
	if s.SelectedActivityLocationID != 0 {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM activity_locations WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, s.SelectedActivityLocationID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return database.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to validate selected activity location: %w", err)
		}
		selectedLocationID = &s.SelectedActivityLocationID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET selected_activity_location_id = $1, use_miles = $2, sme_email = $3 WHERE id = 1`,
		selectedLocationID, s.UseMiles, s.SMEEmail); err != nil {
		return fmt.Errorf("failed to update settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit settings update: %w", err)
	}
	return nil
}
