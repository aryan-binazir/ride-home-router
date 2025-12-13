package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"

	"ride-home-router/internal/models"
)

// SettingsRepository handles settings persistence
type SettingsRepository interface {
	Get(ctx context.Context) (*models.Settings, error)
	Update(ctx context.Context, s *models.Settings) error
}

type settingsRepository struct {
	db *sql.DB
}

func (r *settingsRepository) Get(ctx context.Context) (*models.Settings, error) {
	query := `SELECT key, value FROM settings WHERE key IN ('institute_address', 'institute_lat', 'institute_lng', 'selected_activity_location_id', 'use_miles')`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query settings: %w", err)
	}
	defer rows.Close()

	settings := &models.Settings{}
	settingsMap := make(map[string]string)

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan setting: %w", err)
		}
		settingsMap[key] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	settings.InstituteAddress = settingsMap["institute_address"]
	if lat, err := strconv.ParseFloat(settingsMap["institute_lat"], 64); err == nil {
		settings.InstituteLat = lat
	}
	if lng, err := strconv.ParseFloat(settingsMap["institute_lng"], 64); err == nil {
		settings.InstituteLng = lng
	}
	if id, err := strconv.ParseInt(settingsMap["selected_activity_location_id"], 10, 64); err == nil {
		settings.SelectedActivityLocationID = id
	}
	settings.UseMiles = settingsMap["use_miles"] == "true"

	return settings, nil
}

func (r *settingsRepository) Update(ctx context.Context, s *models.Settings) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[DB] Failed to begin settings update transaction: err=%v", err)
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`

	if _, err := tx.ExecContext(ctx, query, "institute_address", s.InstituteAddress); err != nil {
		log.Printf("[DB] Failed to update institute_address: err=%v", err)
		return fmt.Errorf("failed to update institute_address: %w", err)
	}

	if _, err := tx.ExecContext(ctx, query, "institute_lat", fmt.Sprintf("%f", s.InstituteLat)); err != nil {
		log.Printf("[DB] Failed to update institute_lat: err=%v", err)
		return fmt.Errorf("failed to update institute_lat: %w", err)
	}

	if _, err := tx.ExecContext(ctx, query, "institute_lng", fmt.Sprintf("%f", s.InstituteLng)); err != nil {
		log.Printf("[DB] Failed to update institute_lng: err=%v", err)
		return fmt.Errorf("failed to update institute_lng: %w", err)
	}

	if _, err := tx.ExecContext(ctx, query, "selected_activity_location_id", fmt.Sprintf("%d", s.SelectedActivityLocationID)); err != nil {
		log.Printf("[DB] Failed to update selected_activity_location_id: err=%v", err)
		return fmt.Errorf("failed to update selected_activity_location_id: %w", err)
	}

	useMilesStr := "false"
	if s.UseMiles {
		useMilesStr = "true"
	}
	if _, err := tx.ExecContext(ctx, query, "use_miles", useMilesStr); err != nil {
		log.Printf("[DB] Failed to update use_miles: err=%v", err)
		return fmt.Errorf("failed to update use_miles: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[DB] Failed to commit settings transaction: err=%v", err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("[DB] Updated settings: address=%s lat=%.6f lng=%.6f selected_location_id=%d use_miles=%v", s.InstituteAddress, s.InstituteLat, s.InstituteLng, s.SelectedActivityLocationID, s.UseMiles)
	return nil
}
