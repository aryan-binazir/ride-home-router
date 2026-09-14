package postgres

import (
	"context"
	"database/sql"
	"errors"
)

func (r *settingsRepository) GoogleMapsKey(ctx context.Context) (string, error) {
	var key string
	err := r.db.QueryRowContext(ctx, `SELECT encrypted_api_key FROM google_maps_credentials WHERE id = 1`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("failed to read Google Maps credential")
	}
	return r.cipher.Open(key, "google_maps_api_key")
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
	if len(key) == 0 || len(key) > 4096 {
		return errors.New("google maps credential must contain between 1 and 4096 bytes")
	}
	encrypted, err := r.cipher.Seal(key, "google_maps_api_key")
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO google_maps_credentials (id, encrypted_api_key) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET encrypted_api_key = EXCLUDED.encrypted_api_key`, encrypted)
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
