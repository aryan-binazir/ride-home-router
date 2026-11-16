package orderedroute

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestReservationEverySequentialQuotaLevel(t *testing.T) {
	for remaining := 0; remaining <= 10; remaining++ {
		t.Run(fmt.Sprint(remaining), func(t *testing.T) {
			input := [][]Request{reservationRequests(0, 2, 3, 4)}
			turns := []reservationTurn{{units: 2}, {units: 3}, {units: 4}}
			before, usedBefore, callsBefore, _ := runReservationScenario(t, false, remaining, false, input, turns)
			after, usedAfter, callsAfter, _ := runReservationScenario(t, true, remaining, false, input, turns)
			if fmt.Sprint(before) != fmt.Sprint(after) || usedBefore != usedAfter || fmt.Sprint(callsBefore) != fmt.Sprint(callsAfter) {
				t.Fatalf("quota=%d before=%v/%d/%v after=%v/%d/%v", remaining, before, usedBefore, callsBefore, after, usedAfter, callsAfter)
			}
		})
	}
}

func waitForReservationLock(t *testing.T, conn *pgx.Conn, holderPID uint32, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		var waiting int
		err := conn.QueryRow(ctx, `WITH RECURSIVE blocked(pid) AS (SELECT pid FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)) UNION SELECT activity.pid FROM pg_stat_activity activity JOIN blocked ON blocked.pid=ANY(pg_blocking_pids(activity.pid))) SELECT count(*) FROM pg_stat_activity JOIN blocked USING(pid) WHERE query LIKE '%google_usage%' AND wait_event_type='Lock'`, holderPID).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= count {
			return
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestReservationCancellationBeforeCommit(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			databaseURL := postgrestest.DatabaseURL(t)
			ledger := postgrestest.OpenURL(t, databaseURL).GoogleUsage()
			if err := ledger.SetCeiling(t.Context(), database.UsageSKURoutes, 100); err != nil {
				t.Fatal(err)
			}
			holder, err := pgx.Connect(t.Context(), databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = holder.Close(context.Background()) }()
			tx, err := holder.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			_, err = tx.Exec(t.Context(), `SELECT 1 FROM google_usage WHERE sku='routes' FOR UPDATE`)
			if err != nil {
				t.Fatal(err)
			}
			observer, err := pgx.Connect(t.Context(), databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = observer.Close(context.Background()) }()
			server, seen, mu := fakeRoutes(t, 500, 60)
			client := newClient(staticKey("synthetic"), func(ctx context.Context, n int) error { return ledger.Reserve(ctx, database.UsageSKURoutes, n) }, server.Client(), server.URL)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan []Result, 1)
			go func() {
				measure := client.measureBeforeBatching
				if batch {
					measure = client.Measure
				}
				done <- measure(ctx, reservationRequests(0, 2, 3))
			}()
			waitForReservationLock(t, observer, holder.PgConn().PID(), 1)
			cancel()
			select {
			case results := <-done:
				for _, r := range results {
					if r.Err == nil {
						t.Fatalf("unexpected success: %v", r)
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("canceled reservation did not finish")
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			used, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
			if err != nil || used != 0 {
				t.Fatalf("reserved=%d err=%v", used, err)
			}
			mu.Lock()
			calls := len(*seen)
			mu.Unlock()
			if calls != 0 {
				t.Fatalf("provider calls=%d", calls)
			}
			if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, 1); err != nil {
				t.Fatalf("leaked lock: %v", err)
			}
		})
	}
}

func TestReservationCancellationDuringProvider(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			ledger := postgrestest.Open(t).GoogleUsage()
			ready := make(chan struct{})
			var once sync.Once
			var calls atomic.Int32
			client := newClient(staticKey("synthetic"), func(ctx context.Context, n int) error { return ledger.Reserve(ctx, database.UsageSKURoutes, n) }, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				once.Do(func() { close(ready) })
				<-r.Context().Done()
				return nil, r.Context().Err()
			})}, "http://synthetic")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan []Result, 1)
			go func() {
				measure := client.measureBeforeBatching
				if batch {
					measure = client.Measure
				}
				done <- measure(ctx, reservationRequests(0, 2, 3))
			}()
			<-ready
			deadline := time.Now().Add(10 * time.Second)
			for {
				n, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
				if err != nil {
					t.Fatal(err)
				}
				if n == 5 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("reservations did not finish")
				}
			}
			lockCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			if err := ledger.SetCeiling(lockCtx, database.UsageSKURoutes, 100); err != nil {
				stop()
				t.Fatalf("provider holds ledger lock: %v", err)
			}
			stop()
			cancel()
			for _, r := range <-done {
				if !errors.Is(r.Err, context.Canceled) {
					t.Fatalf("error=%v", r.Err)
				}
			}
			used, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
			if err != nil || used != 5 {
				t.Fatalf("reserved=%d err=%v", used, err)
			}
			t.Logf("reserved=%d released=0 provider calls started=%d", used, calls.Load())
		})
	}
}
