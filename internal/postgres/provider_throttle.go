package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ProviderGate shares Nominatim's budget across imports and interactive searches.
// It never holds a database connection while waiting, and canceled requests do
// not reserve a queue of future slots (an acquired slot is spent).
type ProviderGate struct{ db *sql.DB }

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
	_, err := g.db.ExecContext(ctx, `UPDATE provider_throttles SET next_at=GREATEST(next_at,clock_timestamp()+$1*interval '1 second') WHERE name='nominatim'`, delay.Seconds())
	return err
}
