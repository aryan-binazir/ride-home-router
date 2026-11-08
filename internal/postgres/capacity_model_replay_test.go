package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestWorkflowCapacityModelConcurrentReplicas(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	a, b := postgrestest.OpenURL(t, url), postgrestest.OpenURL(t, url)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			<-start
			repo := a.Workflows()
			if i%2 == 1 {
				repo = b.Workflows()
			}
			results <- repo.Create(ctx, "import", fmt.Sprint(i), []byte(`{}`), time.Hour)
		})
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, database.ErrWorkflowCapacity) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 4 || rejected != 8 {
		t.Fatalf("accepted/rejected = %d/%d, want 4/8", accepted, rejected)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM workflow_sessions WHERE kind='import' AND NOT consumed AND expires_at>clock_timestamp()`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("committed imports=%d, want 4", count)
	}
}

func TestWorkflowCapacityModelEvictionRollbackAndIsolation(t *testing.T) {
	for _, kind := range []string{"route", "draft"} {
		t.Run(kind, func(t *testing.T) {
			db := postgrestest.Open(t)
			repo := db.Workflows()
			ctx := t.Context()
			for i := range 256 {
				if err := repo.Create(ctx, kind, fmt.Sprint(i), []byte(`{}`), time.Hour); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.Create(ctx, "import", "sentinel", []byte(`{"sentinel":true}`), time.Hour); err != nil {
				t.Fatal(err)
			}
			// Invalid JSON reaches INSERT after the oldest row's pending deletion.
			if err := repo.Create(ctx, kind, "bad", []byte(`invalid-json`), time.Hour); err == nil {
				t.Fatal("invalid INSERT succeeded")
			}
			for i := range 256 {
				if _, err := repo.Load(ctx, kind, fmt.Sprint(i), time.Hour); err != nil {
					t.Fatalf("rollback lost row %d: %v", i, err)
				}
			}
			if err := repo.Create(ctx, kind, "replacement", []byte(`{}`), time.Hour); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.Load(ctx, kind, "0", time.Hour); !errors.Is(err, database.ErrNotFound) {
				t.Fatalf("oldest row after successful eviction: %v", err)
			}
			if got, err := repo.Load(ctx, "import", "sentinel", time.Hour); err != nil || string(got.Data) != `{"sentinel":true}` {
				t.Fatalf("other kind changed: %+v %v", got, err)
			}
			if _, err := repo.Load(ctx, kind, "bad", time.Hour); !errors.Is(err, database.ErrNotFound) {
				t.Fatalf("failed insertion survived: %v", err)
			}
		})
	}
}

func TestWorkflowCapacityModelConsumedSlotReuse(t *testing.T) {
	db := postgrestest.Open(t)
	r := db.Workflows()
	ctx := t.Context()
	for i := range 4 {
		if err := r.Create(ctx, "import", fmt.Sprint(i), []byte(`{}`), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Create(ctx, "import", "full", []byte(`{}`), time.Hour); !errors.Is(err, database.ErrWorkflowCapacity) {
		t.Fatalf("full import: %v", err)
	}
	if err := r.Update(ctx, "import", "0", time.Hour, func(record *database.WorkflowRecord) error { record.Consumed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, "import", "reuse", []byte(`{}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Load(ctx, "import", "0", time.Hour); err != nil || !got.Consumed {
		t.Fatalf("consumed tombstone lost: %+v %v", got, err)
	}
}
