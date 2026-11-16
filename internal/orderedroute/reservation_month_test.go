package orderedroute

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type monthUsage struct {
	Month             string
	Reserved, Ceiling int
}

func runReservationRollover(t *testing.T, batch, refused bool) []monthUsage {
	t.Helper()
	databaseURL := postgrestest.DatabaseURL(t)
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(t.Context(), `
 CREATE TABLE reservation_clock(boundary timestamptz NOT NULL);
 INSERT INTO reservation_clock VALUES ('infinity');
 CREATE FUNCTION transaction_timestamp() RETURNS timestamptz LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN pg_catalog.transaction_timestamp() < boundary
 THEN '2026-12-01 07:59:59+00'::timestamptz ELSE '2026-12-01 08:00:00+00'::timestamptz END FROM reservation_clock
 $$;
 INSERT INTO google_usage(month_start,sku,reserved,ceiling) VALUES ('2026-11-01','routes',0,100),('2026-12-01','routes',0,100);
 `)
	if err != nil {
		t.Fatal(err)
	}
	if refused {
		_, err = conn.Exec(t.Context(), `
 CREATE FUNCTION hold_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_advisory_xact_lock(hashtext(current_schema()),42); RETURN NEW; END $$;
 CREATE TRIGGER hold_reservation BEFORE INSERT ON google_usage FOR EACH ROW EXECUTE FUNCTION hold_reservation();
 UPDATE google_usage SET ceiling=4 WHERE month_start='2026-11-01'`)
		if err != nil {
			t.Fatal(err)
		}
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", query.Get("search_path")+",pg_catalog")
	parsed.RawQuery = query.Encode()
	ledger := postgrestest.OpenURL(t, parsed.String()).GoogleUsage()
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if refused {
		_, err = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtext(current_schema()),42)`)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = tx.Exec(t.Context(), `SELECT 1 FROM google_usage WHERE month_start='2026-11-01' FOR UPDATE`)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close(context.Background()) }()
	server, _, _ := fakeRoutes(t, 500, 60)
	client := newClient(staticKey("synthetic"), func(ctx context.Context, n int) error { return ledger.Reserve(ctx, database.UsageSKURoutes, n) }, server.Client(), server.URL)
	if !batch && refused {
		_, err = observer.Exec(t.Context(), `UPDATE reservation_clock SET boundary=clock_timestamp()`)
		if err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []Result, 1)
	go func() {
		measure := client.measureBeforeBatching
		if batch {
			measure = client.Measure
		}
		done <- measure(t.Context(), reservationRequests(0, 2, 3))
	}()
	if batch || !refused {
		count := 1
		if !batch {
			count = 2
		}
		waitForReservationLock(t, observer, conn.PgConn().PID(), count)
		_, err = observer.Exec(t.Context(), `UPDATE reservation_clock SET boundary=clock_timestamp()`)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case results := <-done:
		for _, r := range results {
			if r.Err != nil {
				t.Fatal(r.Err)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rollover deadlocked")
	}
	rows, err := observer.Query(t.Context(), `SELECT month_start::text,reserved,ceiling FROM google_usage WHERE sku='routes' ORDER BY month_start`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var usage []monthUsage
	for rows.Next() {
		var u monthUsage
		if err := rows.Scan(&u.Month, &u.Reserved, &u.Ceiling); err != nil {
			t.Fatal(err)
		}
		if u.Reserved > u.Ceiling {
			t.Fatalf("ceiling exceeded: %v", u)
		}
		usage = append(usage, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return usage
}

func TestReservationMonthRolloverReachableOnMain(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(fmt.Sprint(refused), func(t *testing.T) {
			main := runReservationRollover(t, false, refused)
			batch := runReservationRollover(t, true, refused)
			if !reflect.DeepEqual(main, batch) {
				t.Fatalf("main=%v batch=%v", main, batch)
			}
			want := []monthUsage{{"2026-11-01", 5, 100}, {"2026-12-01", 0, 100}}
			if refused {
				want = []monthUsage{{"2026-11-01", 0, 4}, {"2026-12-01", 5, 100}}
			}
			if !reflect.DeepEqual(batch, want) {
				t.Fatalf("usage=%v want=%v", batch, want)
			}
			t.Logf("main=batch=%v released=0", batch)
		})
	}
}
