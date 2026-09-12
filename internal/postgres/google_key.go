package postgres

import (
	"context"
	"database/sql"
	"errors"
)

func (r *settingsRepository) GoogleMapsKey(ctx context.Context) (string, error) {
	var key string
	err := r.db.QueryRowContext(ctx, `SELECT api_key FROM google_maps_credentials WHERE id = 1`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("failed to read Google Maps credential")
	}
	return key, nil
}

func (r *settingsRepository) GoogleMapsKeyConfigured(ctx context.Context) (bool, error) {
	var configured bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM google_maps_credentials WHERE id = 1)`).Scan(&configured)
	if err != nil {
		return false, errors.New("failed to read Google Maps credential status")
	}
	return configured, nil
}

func (r *settingsRepository) SetGoogleMapsKey(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO google_maps_credentials (id, api_key) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET api_key = EXCLUDED.api_key`, key)
	if err != nil {
		return errors.New("failed to update Google Maps credential")
	}
	return nil
}

func (r *settingsRepository) DeleteGoogleMapsKey(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM google_maps_credentials WHERE id = 1`)
	if err != nil {
		return errors.New("failed to delete Google Maps credential")
	}
	return nil
}
