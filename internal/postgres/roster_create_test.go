package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type manualCreateSpec[T any] struct {
	rosterBatchSpec[T]
	table   string
	create  func(context.Context, *postgres.Store, *T, []int64) error
	labels  func(context.Context, *postgres.Store, int64) ([]models.Label, error)
	delete  func(context.Context, *postgres.Store, int64) error
	restore func(context.Context, *postgres.Store, int64) error
}

func participantManualSpec() manualCreateSpec[models.Participant] {
	return manualCreateSpec[models.Participant]{
		rosterBatchSpec: participantBatchSpec(), table: "participants",
		create: func(ctx context.Context, s *postgres.Store, p *models.Participant, labels []int64) error {
			_, err := s.Participants().CreateManualWithLabels(ctx, p, labels)
			return err
		},
		labels: func(ctx context.Context, s *postgres.Store, id int64) ([]models.Label, error) {
			return s.Labels().ListLabelsForParticipant(ctx, id)
		},
		delete:  func(ctx context.Context, s *postgres.Store, id int64) error { return s.Participants().Delete(ctx, id) },
		restore: func(ctx context.Context, s *postgres.Store, id int64) error { return s.Participants().Restore(ctx, id) },
	}
}

func driverManualSpec() manualCreateSpec[models.Driver] {
	return manualCreateSpec[models.Driver]{
		rosterBatchSpec: driverBatchSpec(), table: "drivers",
		create: func(ctx context.Context, s *postgres.Store, p *models.Driver, labels []int64) error {
			_, err := s.Drivers().CreateManualWithLabels(ctx, p, labels)
			return err
		},
		labels: func(ctx context.Context, s *postgres.Store, id int64) ([]models.Label, error) {
			return s.Labels().ListLabelsForDriver(ctx, id)
		},
		delete:  func(ctx context.Context, s *postgres.Store, id int64) error { return s.Drivers().Delete(ctx, id) },
		restore: func(ctx context.Context, s *postgres.Store, id int64) error { return s.Drivers().Restore(ctx, id) },
	}
}

func TestManualRosterCreationContention(t *testing.T) {
	t.Run("participants", func(t *testing.T) { testManualContention(t, participantManualSpec()) })
	t.Run("drivers", func(t *testing.T) { testManualContention(t, driverManualSpec()) })
}

func testManualContention[T any](t *testing.T, spec manualCreateSpec[T]) {
	for _, order := range []string{"manual-manual", "manual-import", "import-manual"} {
		t.Run(order, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			databaseURL := postgrestest.DatabaseURL(t)
			control, err := pgx.Connect(ctx, databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = control.Close(context.Background()) }()
			const barrier = int64(872340017)
			if _, err := control.Exec(ctx, `SELECT pg_advisory_lock($1)`, barrier); err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = control.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, barrier) }()
			execSQL(t, databaseURL, fmt.Sprintf(`CREATE FUNCTION hold_roster_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$; CREATE TRIGGER hold_insert BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION hold_roster_insert();`, barrier, spec.table))
			namedURL := func(name string) string {
				u, err := url.Parse(databaseURL)
				if err != nil {
					t.Fatal(err)
				}
				q := u.Query()
				q.Set("application_name", name)
				u.RawQuery = q.Encode()
				return u.String()
			}
			winnerName, loserName := "winner-"+spec.noun+order, "loser-"+spec.noun+order
			winnerStore := openStore(t, namedURL(winnerName))
			loserStore := openStore(t, namedURL(loserName))
			label, err := winnerStore.Labels().Create(ctx, &models.Label{Name: "Manual label"})
			if err != nil {
				t.Fatal(err)
			}
			winner := spec.newEntity("Anne-Marie O'Brien", "4 Main St., Apt 2")
			loser := spec.newEntity(" anne marie O’Brien ", "4 MAIN st Apt 2")
			type outcome struct {
				batch database.BatchUpsertResult
				err   error
			}
			run := func(store *postgres.Store, entity *T, importing bool, done chan<- outcome) {
				if importing {
					result, err := spec.createBatch(ctx, store, []*T{entity})
					done <- outcome{result, err}
				} else {
					done <- outcome{err: spec.create(ctx, store, entity, []int64{label.ID})}
				}
			}
			first, second := make(chan outcome, 1), make(chan outcome, 1)
			go run(winnerStore, winner, order == "import-manual", first)
			waitForAdvisoryWait(t, ctx, control, winnerName)
			go run(loserStore, loser, order == "manual-import", second)
			waitForAdvisoryWait(t, ctx, control, loserName)
			if _, err := control.Exec(ctx, `SELECT pg_advisory_unlock($1)`, barrier); err != nil {
				t.Fatal(err)
			}
			var a, b outcome
			select {
			case a = <-first:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case b = <-second:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if a.err != nil {
				t.Fatalf("winner: %v", a.err)
			}
			if order == "manual-import" {
				if b.err != nil || b.batch != (database.BatchUpsertResult{Updated: 1}) || spec.id(loser) != spec.id(winner) {
					t.Fatalf("import loser=%+v IDs=%d/%d", b, spec.id(winner), spec.id(loser))
				}
			} else if !errors.Is(b.err, database.ErrDuplicate) {
				t.Fatalf("manual loser error=%v, want ErrDuplicate", b.err)
			}
			if count, err := spec.count(ctx, winnerStore); err != nil || count != 1 {
				t.Fatalf("live count=%d err=%v, want 1", count, err)
			}
			labels, err := spec.labels(ctx, winnerStore, spec.id(winner))
			wantLabels := 1
			if order == "import-manual" {
				wantLabels = 0
				if a.batch != (database.BatchUpsertResult{Created: 1}) {
					t.Fatalf("import winner=%+v", a)
				}
			}
			if err != nil || len(labels) != wantLabels {
				t.Fatalf("labels=%+v err=%v, want %d", labels, err, wantLabels)
			}
			t.Logf("%s: both independent stores blocked, winner committed, loser followed path policy, one live row and %d labels", order, wantLabels)
		})
	}
}

func waitForAdvisoryWait(t *testing.T, ctx context.Context, conn *pgx.Conn, name string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event='advisory')`, name).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s did not wait: %v", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestManualRosterCreationPolicies(t *testing.T) {
	t.Run("participants", func(t *testing.T) { testManualPolicies(t, participantManualSpec()) })
	t.Run("drivers", func(t *testing.T) { testManualPolicies(t, driverManualSpec()) })
}

func testManualPolicies[T any](t *testing.T, spec manualCreateSpec[T]) {
	ctx := t.Context()
	store := postgrestest.Open(t)
	label, err := store.Labels().Create(ctx, &models.Label{Name: "Retained label"})
	if err != nil {
		t.Fatal(err)
	}
	original := spec.newEntity("José Anne-Marie O'Brien", "4 Main St., Apt 2")
	if err := spec.create(ctx, store, original, []int64{label.ID}); err != nil {
		t.Fatal(err)
	}
	normalized := spec.newEntity(" jose\u0301 anne marie O’Brien ", "4 MAIN st Apt 2")
	if err := spec.create(ctx, store, normalized, nil); !errors.Is(err, database.ErrDuplicate) {
		t.Fatalf("normalized error=%v", err)
	}
	if spec.id(normalized) != 0 {
		t.Fatalf("rejected entity ID=%d", spec.id(normalized))
	}
	if err := spec.delete(ctx, store, spec.id(original)); err != nil {
		t.Fatal(err)
	}
	replacement := spec.newEntity("jose\u0301 anne marie O’Brien", "4 MAIN st Apt 2")
	if err := spec.create(ctx, store, replacement, nil); err != nil {
		t.Fatalf("archived match: %v", err)
	}
	if err := spec.restore(ctx, store, spec.id(original)); err != nil {
		t.Fatalf("restore duplicate: %v", err)
	}
	raw := spec.newEntity("José Anne-Marie O'Brien", "4 Main St., Apt 2")
	if err := spec.createOne(ctx, store, raw); err != nil {
		t.Fatalf("raw duplicate create: %v", err)
	}
	imported := spec.newEntity("José Anne-Marie O'Brien", "4 Main St., Apt 2")
	result, err := spec.createBatch(ctx, store, []*T{imported})
	if err != nil || result != (database.BatchUpsertResult{Updated: 1}) || spec.id(imported) != spec.id(original) {
		t.Fatalf("oldest live import=%+v err=%v ID=%d want %d", result, err, spec.id(imported), spec.id(original))
	}
	if count, err := spec.count(ctx, store); err != nil || count != 3 {
		t.Fatalf("live count=%d err=%v, want restored, manual, raw", count, err)
	}
	labels, err := spec.labels(ctx, store, spec.id(original))
	if err != nil || len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("original labels=%+v err=%v", labels, err)
	}
	if err := spec.create(ctx, store, spec.newEntity("José Anne-Marie O'Brien", "4 Main St., Apt 2"), nil); !errors.Is(err, database.ErrDuplicate) {
		t.Fatalf("live restored duplicates error=%v", err)
	}
}

func TestManualRosterCreationLabelRollback(t *testing.T) {
	t.Run("participants", func(t *testing.T) { testManualLabelRollback(t, participantManualSpec()) })
	t.Run("drivers", func(t *testing.T) { testManualLabelRollback(t, driverManualSpec()) })
}

func testManualLabelRollback[T any](t *testing.T, spec manualCreateSpec[T]) {
	ctx := t.Context()
	store := postgrestest.Open(t)
	label, err := store.Labels().Create(ctx, &models.Label{Name: "Valid label"})
	if err != nil {
		t.Fatal(err)
	}
	entity := spec.newEntity("Label Failure", "1 Main St")
	if err := spec.create(ctx, store, entity, []int64{label.ID, 987654321}); err == nil {
		t.Fatal("invalid second label did not fail")
	}
	if count, err := spec.count(ctx, store); err != nil || count != 0 {
		t.Fatalf("failed creation persisted rows=%d err=%v", count, err)
	}
	if labels, err := spec.labels(ctx, store, spec.id(entity)); err != nil || len(labels) != 0 {
		t.Fatalf("failed creation persisted labels=%+v err=%v", labels, err)
	}
	retry := spec.newEntity("Label Failure", "1 Main St")
	if err := spec.create(ctx, store, retry, []int64{label.ID}); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if labels, err := spec.labels(ctx, store, spec.id(retry)); err != nil || len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("retry labels=%+v err=%v", labels, err)
	}
}
