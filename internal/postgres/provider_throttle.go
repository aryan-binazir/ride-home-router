package postgres

import (
	"context"
	"database/sql"
	"errors"
	"ride-home-router/internal/geocoding"
	"time"
)

// geocodingThrottle is the provider_throttles row shared by Google Geocoding and
// Places Autocomplete. Google enforces quotas per project, so the gate only
// propagates cooldowns (429/503 or quota responses) across instances; it never
// paces requests itself.
const geocodingThrottle = "google_geocoding"

// ProviderGate shares a provider cooldown across imports and interactive searches.
// It never holds a database connection while waiting.
type ProviderGate struct{ db *sql.DB }

// ProviderCooldownError reports an invalid persisted cooldown without waiting indefinitely.
type ProviderCooldownError = geocoding.CooldownError

func (s *Store) GeocodingGate() *ProviderGate { return &ProviderGate{db: s.db} }

func (g *ProviderGate) Wait(ctx context.Context) error {
	for {
		var seconds float64
		err := g.db.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM next_at-clock_timestamp())::float8 FROM provider_throttles WHERE name=$1`, geocodingThrottle).Scan(&seconds)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("provider throttle row is missing; run migrations")
		}
		if err != nil {
			return err
		}
		if seconds <= 0 {
			return nil
		}
		if seconds > (15 * time.Minute).Seconds() {
			if _, err := g.db.ExecContext(ctx, `UPDATE provider_throttles SET next_at=LEAST(next_at,clock_timestamp()+interval '15 minutes') WHERE name=$1`, geocodingThrottle); err != nil {
				return err
			}
			return &ProviderCooldownError{}
		}
		timer := time.NewTimer(time.Duration(min(max(seconds, 0.01), 1) * float64(time.Second)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (g *ProviderGate) Defer(ctx context.Context, delay time.Duration) error {
	_, err := g.db.ExecContext(ctx, `UPDATE provider_throttles SET next_at=GREATEST(LEAST(next_at,clock_timestamp()+interval '15 minutes'),clock_timestamp()+$2*interval '1 second') WHERE name=$1`, geocodingThrottle, min(max(delay, 0), 15*time.Minute).Seconds())
	return err
}
