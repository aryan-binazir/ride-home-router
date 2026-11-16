package orderedroute

import (
	"context"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestReservationDatabaseFailureReachableOnMain(t *testing.T) {
	for _, fault := range []struct{ name, code, deferral string }{
		{"serialization at commit", "40001", "DEFERRABLE INITIALLY DEFERRED"},
		{"deadlock at commit", "40P01", "DEFERRABLE INITIALLY DEFERRED"},
		{"serialization before commit", "40001", ""},
		{"statement error before commit", "P0001", ""},
	} {
		t.Run(fault.name, func(t *testing.T) {
			for _, batch := range []bool{false, true} {
				for _, firstCost := range []int{2, 3} {
					t.Run(fmt.Sprintf("batch=%v/first=%d", batch, firstCost), func(t *testing.T) {
						databaseURL := postgrestest.DatabaseURL(t)
						store := postgrestest.OpenURL(t, databaseURL)
						ledger := store.GoogleUsage()
						if err := ledger.SetCeiling(t.Context(), database.UsageSKURoutes, 100); err != nil {
							t.Fatal(err)
						}
						conn, err := pgx.Connect(t.Context(), databaseURL)
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = conn.Close(context.Background()) }()
						_, err = conn.Exec(t.Context(), fmt.Sprintf(`
                    CREATE SEQUENCE reservation_commit_number;
                    CREATE FUNCTION reject_one_reservation_commit() RETURNS trigger LANGUAGE plpgsql AS $$
                    BEGIN
                        IF nextval('reservation_commit_number') = 1 THEN
                            RAISE EXCEPTION 'injected transient reservation failure' USING ERRCODE = '%s';
                        END IF;
                        RETURN NEW;
                    END $$;
                    CREATE CONSTRAINT TRIGGER fail_first_reservation_commit
                    AFTER UPDATE ON google_usage %s
                    FOR EACH ROW EXECUTE FUNCTION reject_one_reservation_commit();`, fault.code, fault.deferral))
						if err != nil {
							t.Fatal(err)
						}
						firstDone := make(chan struct{})
						server, seen, mu := fakeRoutes(t, 500, 60)
						var attempts atomic.Int32
						client := newClient(staticKey("synthetic"), func(ctx context.Context, units int) error {
							if units != 5 && units != firstCost && !batch {
								<-firstDone
							}
							attempts.Add(1)
							err := ledger.Reserve(ctx, database.UsageSKURoutes, units)
							if units == firstCost {
								close(firstDone)
							}
							return err
						}, server.Client(), server.URL)
						measure := client.measureBeforeBatching
						if batch {
							measure = client.Measure
						}
						results := measure(t.Context(), reservationRequests(0, 2, 3))
						successful, failed := 0, 0
						for i, result := range results {
							wantFailedCar := 0
							if !batch && firstCost == 3 {
								wantFailedCar = 1
							}
							if (result.Err != nil) != (i/2 == wantFailedCar) {
								t.Fatalf("unexpected result for car %d: %v", i/2, result.Err)
							}
							if result.Err == nil {
								successful++
								continue
							}
							failed++
							if result.Err.Error() != fmt.Sprintf("orderedroute: ERROR: injected transient reservation failure (SQLSTATE %s)", fault.code) {
								t.Fatalf("error text=%v", result.Err)
							}
							var pgError *pgconn.PgError
							if !errors.As(result.Err, &pgError) || pgError.Code != fault.code {
								t.Fatalf("unexpected error: %v", result.Err)
							}
							t.Logf("%s error=%s", result.ID, result.Err)
						}
						used, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
						if err != nil {
							t.Fatal(err)
						}
						mu.Lock()
						calls := len(*seen)
						mu.Unlock()
						t.Logf("batch=%v firstCost=%d successful_results=%d failed_results=%d reserved=%d reserve_transactions=%d provider_requests=%d", batch, firstCost, successful, failed, used, attempts.Load(), calls)
						if batch {
							if successful != 2 || failed != 2 || used != 3 || attempts.Load() != 2 || calls != 3 {
								t.Fatal("unexpected candidate outcome")
							}
						} else if successful != 2 || failed != 2 || used != 5-firstCost || attempts.Load() != 2 || calls != 5-firstCost {
							t.Fatal("unexpected main outcome")
						}
					})
				}
			}
		})
	}
}
