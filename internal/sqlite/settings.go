package sqlite

import (
	"context"
	"fmt"

	"ride-home-router/internal/models"
)

type settingsRepository struct {
	store *Store
}

func (r *settingsRepository) Get(ctx context.Context) (*models.Settings, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT selected_activity_location_id, use_miles FROM settings WHERE id = 1`

	var s models.Settings
	var selectedLocationID *int64
	var useMiles int

	err := r.store.db.QueryRowContext(ctx, query).Scan(&selectedLocationID, &useMiles)
	if err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}

	if selectedLocationID != nil {
		s.SelectedActivityLocationID = *selectedLocationID
	}
	s.UseMiles = useMiles == 1

	return &s, nil
}

func (r *settingsRepository) Update(ctx context.Context, s *models.Settings) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	useMiles := 0
	if s.UseMiles {
		useMiles = 1
	}

	var selectedLocationID *int64
	if s.SelectedActivityLocationID != 0 {
		selectedLocationID = &s.SelectedActivityLocationID
	}

	query := `UPDATE settings SET selected_activity_location_id = ?, use_miles = ? WHERE id = 1`
	_, err := r.store.db.ExecContext(ctx, query, selectedLocationID, useMiles)
	if err != nil {
		return fmt.Errorf("failed to update settings: %w", err)
	}

	return nil
}
