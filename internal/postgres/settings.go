package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/credentials"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type settingsRepository struct {
	db     *sql.DB
	cipher *credentials.Cipher
}

func (r *settingsRepository) Get(ctx context.Context) (*models.Settings, error) {
	var s models.Settings
	var selectedLocationID sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `
		SELECT location.id, settings.use_miles, settings.sme_email, settings.collect_reviewer_notes
		FROM settings
		LEFT JOIN activity_locations location
		  ON location.id = settings.selected_activity_location_id
		 AND location.deleted_at IS NULL
		WHERE settings.id = 1`).
		Scan(&selectedLocationID, &s.UseMiles, &s.SMEEmail, &s.CollectReviewerNotes); err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}
	if selectedLocationID.Valid {
		s.SelectedActivityLocationID = selectedLocationID.Int64
	}
	return &s, nil
}

func (r *settingsRepository) Update(ctx context.Context, s *models.Settings) error {
	_, err := r.Patch(ctx, database.SettingsPatch{SelectedActivityLocationID: &s.SelectedActivityLocationID, UseMiles: s.UseMiles, SMEEmail: &s.SMEEmail, CollectReviewerNotes: &s.CollectReviewerNotes})
	return err
}

func (r *settingsRepository) Patch(ctx context.Context, patch database.SettingsPatch) (*models.Settings, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin settings update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var selectedLocationID *int64
	if patch.SelectedActivityLocationID != nil && *patch.SelectedActivityLocationID != 0 {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM activity_locations WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, *patch.SelectedActivityLocationID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("validate selected activity location: %w", err)
		}
		selectedLocationID = &id
	}
	var s models.Settings
	var location sql.NullInt64
	err = tx.QueryRowContext(ctx, `UPDATE settings SET
 selected_activity_location_id = CASE WHEN $5 THEN $1 ELSE selected_activity_location_id END,
 use_miles = $2, sme_email = COALESCE($3, sme_email), collect_reviewer_notes = COALESCE($4, collect_reviewer_notes)
 WHERE id = 1 RETURNING selected_activity_location_id, use_miles, sme_email, collect_reviewer_notes`,
		selectedLocationID, patch.UseMiles, patch.SMEEmail, patch.CollectReviewerNotes, patch.SelectedActivityLocationID != nil).Scan(&location, &s.UseMiles, &s.SMEEmail, &s.CollectReviewerNotes)
	if err != nil {
		return nil, fmt.Errorf("update settings: %w", err)
	}
	if location.Valid {
		s.SelectedActivityLocationID = location.Int64
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit settings update: %w", err)
	}
	return &s, nil
}
