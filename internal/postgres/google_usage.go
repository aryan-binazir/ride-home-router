package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
)

type googleUsageRepository struct{ db *sql.DB }

func (s *Store) GoogleUsage() database.GoogleUsageLedger { return &googleUsageRepository{db: s.db} }

const googleBillingMonth = `(date_trunc('month', transaction_timestamp() AT TIME ZONE 'America/Los_Angeles'))::date`

func (r *googleUsageRepository) ensureRow(ctx context.Context, tx *sql.Tx, sku database.UsageSKU) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO google_usage(month_start, sku, reserved, ceiling) VALUES (`+googleBillingMonth+`, $1, 0, $2) ON CONFLICT (month_start, sku) DO NOTHING`, string(sku), database.UsageDefaultCeiling)
	return err
}

func (r *googleUsageRepository) Reserve(ctx context.Context, sku database.UsageSKU, attempts int) error {
	if attempts <= 0 {
		return fmt.Errorf("google usage: attempts must be positive")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.ensureRow(ctx, tx, sku); err != nil {
		return err
	}
	var reserved int
	err = tx.QueryRowContext(ctx, `UPDATE google_usage SET reserved = reserved + $2 WHERE month_start = `+googleBillingMonth+` AND sku = $1 AND reserved + $2 <= ceiling RETURNING reserved`, string(sku), attempts).Scan(&reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return database.ErrUsageExhausted
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *googleUsageRepository) Reserved(ctx context.Context, sku database.UsageSKU) (int, error) {
	var reserved int
	err := r.db.QueryRowContext(ctx, `SELECT reserved FROM google_usage WHERE month_start = `+googleBillingMonth+` AND sku = $1`, string(sku)).Scan(&reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return reserved, err
}

func (r *googleUsageRepository) SetCeiling(ctx context.Context, sku database.UsageSKU, ceiling int) error {
	if ceiling < 0 {
		return fmt.Errorf("google usage: ceiling must not be negative")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO google_usage(month_start, sku, reserved, ceiling) VALUES (`+googleBillingMonth+`, $1, 0, $2) ON CONFLICT (month_start, sku) DO UPDATE SET ceiling = GREATEST(EXCLUDED.ceiling, google_usage.reserved)`, string(sku), ceiling)
	return err
}

func (r *googleUsageRepository) Seed(ctx context.Context, sku database.UsageSKU, alreadyUsed int) error {
	if alreadyUsed < 0 {
		return fmt.Errorf("google usage: seed must not be negative")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO google_usage(month_start, sku, reserved, ceiling) VALUES (`+googleBillingMonth+`, $1, LEAST($2::int, $3::int), $3::int) ON CONFLICT (month_start, sku) DO UPDATE SET reserved = GREATEST(google_usage.reserved, LEAST($2::int, google_usage.ceiling))`, string(sku), alreadyUsed, database.UsageDefaultCeiling)
	return err
}
