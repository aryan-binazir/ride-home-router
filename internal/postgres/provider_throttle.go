package postgres

import (
	"context"
	"database/sql"
	"errors"
	"ride-home-router/internal/geocoding"
	"time"
)

// ProviderGate shares Nominatim's budget across imports and interactive searches.
// It never holds a database connection while waiting, and canceled requests do
// not reserve a queue of future slots (an acquired slot is spent).
type ProviderGate struct{ db *sql.DB }

// ProviderCooldownError reports an invalid persisted cooldown without waiting indefinitely.
type ProviderCooldownError = geocoding.CooldownError

func (s *Store) NominatimGate() *ProviderGate { return &ProviderGate{db: s.db} }
func (g *ProviderGate) Wait(ctx context.Context) error {
	for {
		var unused time.Time
		err := g.db.QueryRowContext(ctx, `UPDATE provider_throttles SET next_at=clock_timestamp()+interval '1 second' WHERE name='nominatim' AND next_at<=clock_timestamp() RETURNING next_at`).Scan(&unused)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var seconds float64
		if err = g.db.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM next_at-clock_timestamp())::float8 FROM provider_throttles WHERE name='nominatim'`).Scan(&seconds); err != nil {
			return err
		}
		if seconds > (15 * time.Minute).Seconds() {
			if _, err := g.db.ExecContext(ctx, `UPDATE provider_throttles SET next_at=LEAST(next_at,clock_timestamp()+interval '15 minutes') WHERE name='nominatim'`); err != nil {
				return err
			}
			return &ProviderCooldownError{}
		}
		delay := time.Duration(min(max(seconds, 0.01), 1) * float64(time.Second))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (g *ProviderGate) Defer(ctx context.Context, delay time.Duration) error {
	_, err := g.db.ExecContext(ctx, `UPDATE provider_throttles SET next_at=GREATEST(LEAST(next_at,clock_timestamp()+interval '15 minutes'),clock_timestamp()+$1*interval '1 second') WHERE name='nominatim'`, min(max(delay, 0), 15*time.Minute).Seconds())
	return err
}
