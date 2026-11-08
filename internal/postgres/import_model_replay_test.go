package postgres_test

import (
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func stagedGeocodeJob(t *testing.T) (*postgres.Store, string, database.ImportJob) {
	t.Helper()
	url := postgrestest.DatabaseURL(t)
	db := postgrestest.OpenURL(t, url)
	if err := db.Workflows().Create(t.Context(), "import", "model-replay", []byte(`{}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	err := db.Workflows().Transact(t.Context(), "import", "model-replay", time.Hour, func(_ *database.WorkflowRecord, w database.WorkflowWrites) error {
		return w.StageImport(t.Context(), "model-replay", []database.ImportRow{
			{Index: 0, Data: []byte(`{"NeedsGeocoding":true}`), Selected: true},
			{Index: 1, Data: []byte(`{"NeedsGeocoding":true}`), Selected: true},
		}, []database.ImportJob{{Index: 0, Address: "1 Example Street", Rows: []int{0, 1}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	job, ok, err := db.ImportJobs().Claim(t.Context(), "worker-a-run-1", time.Minute)
	if err != nil || !ok || job.Attempts != 1 {
		t.Fatalf("initial claim = %+v, %v, %v", job, ok, err)
	}
	return db, url, job
}

func TestImportGeocodeModelClaimFencing(t *testing.T) {
	for _, staleAction := range []string{"release", "finish"} {
		t.Run(staleAction, func(t *testing.T) {
			db, url, stale := stagedGeocodeJob(t)
			execSQL(t, url, `UPDATE import_jobs SET claimed_until=clock_timestamp()-interval '1 second'`)
			replica := postgrestest.OpenURL(t, url)
			current, ok, err := replica.ImportJobs().Claim(t.Context(), "worker-b-run-1", time.Minute)
			if err != nil || !ok || current.Attempts != 2 {
				t.Fatalf("reclaimed job = %+v, %v, %v", current, ok, err)
			}
			if staleAction == "release" {
				if err := db.ImportJobs().Release(t.Context(), stale); err != nil {
					t.Fatal(err)
				}
			} else if err := db.ImportJobs().Finish(t.Context(), stale, []database.ImportRow{{Index: 0, Data: []byte(`{"result":"stale"}`)}}, true); !errors.Is(err, database.ErrWorkflowConflict) {
				t.Fatalf("stale Finish = %v, want conflict", err)
			}
			rows := []database.ImportRow{{Index: 0, Data: []byte(`{"result":"current"}`)}, {Index: 1, Data: []byte(`{"result":"current"}`)}}
			if err := replica.ImportJobs().Finish(t.Context(), current, rows, false); err != nil {
				t.Fatalf("stale %s stole current claim: %v", staleAction, err)
			}
			got, err := db.ImportJobs().Rows(t.Context(), stale.SessionID)
			if err != nil || len(got) != 2 || string(got[0].Data) != `{"result":"current"}` || !got[0].Selected {
				t.Fatalf("current result = %+v, %v", got, err)
			}
		})
	}
}

func TestImportGeocodeModelFinishKeepsLiveSelection(t *testing.T) {
	db, _, job := stagedGeocodeJob(t)
	rows, err := db.ImportJobs().RowsByIndices(t.Context(), job.SessionID, job.Rows)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Workflows().Transact(t.Context(), "import", job.SessionID, time.Hour, func(_ *database.WorkflowRecord, w database.WorkflowWrites) error {
		return w.PatchImportSelections(t.Context(), job.SessionID, map[int]bool{0: false})
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		rows[i].Data = []byte(`{"NeedsGeocoding":false}`)
	}
	if err := db.ImportJobs().Finish(t.Context(), job, rows, false); err != nil {
		t.Fatal(err)
	}
	got, err := db.ImportJobs().Rows(t.Context(), job.SessionID)
	if err != nil || len(got) != 2 || got[0].Selected || !got[1].Selected {
		t.Fatalf("selection after geocoding = %+v, %v", got, err)
	}
}

func TestImportGeocodeModelFinishRollsBackRowsAndClaim(t *testing.T) {
	db, _, job := stagedGeocodeJob(t)
	rows := []database.ImportRow{{Index: 0, Data: []byte(`{"NeedsGeocoding":false}`)}, {Index: 1, Data: []byte(`invalid-json`)}}
	if err := db.ImportJobs().Finish(t.Context(), job, rows, true); err == nil {
		t.Fatal("invalid second row should abort the whole Finish")
	}
	got, err := db.ImportJobs().Rows(t.Context(), job.SessionID)
	if err != nil || len(got) != 2 || string(got[0].Data) != `{"NeedsGeocoding":true}` || !got[0].Selected {
		t.Fatalf("failed Finish left partial rows = %+v, %v", got, err)
	}
	rows[1].Data = []byte(`{"NeedsGeocoding":false}`)
	if err := db.ImportJobs().Finish(t.Context(), job, rows, false); err != nil {
		t.Fatalf("failed Finish consumed the claim: %v", err)
	}
}

func TestImportGeocodeModelClosedJobCannotReopen(t *testing.T) {
	db, url, job := stagedGeocodeJob(t)
	rows := []database.ImportRow{{Index: 0, Data: []byte(`{"NeedsGeocoding":false}`)}, {Index: 1, Data: []byte(`{"NeedsGeocoding":false}`)}}
	if err := db.ImportJobs().Finish(t.Context(), job, rows, false); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportJobs().Release(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportJobs().Finish(t.Context(), job, rows, true); !errors.Is(err, database.ErrWorkflowConflict) {
		t.Fatalf("second Finish = %v, want conflict", err)
	}
	execSQL(t, url, `UPDATE import_jobs SET claimed_until=clock_timestamp()-interval '1 second'`)
	if _, ok, err := db.ImportJobs().Claim(t.Context(), "worker-a-run-2", time.Minute); err != nil || ok {
		t.Fatalf("completed job reclaimed: %v, %v", ok, err)
	}
}

func TestImportGeocodeModelExpiredLeaseCannotFinish(t *testing.T) {
	db, url, job := stagedGeocodeJob(t)
	execSQL(t, url, `UPDATE import_jobs SET claimed_until=clock_timestamp()-interval '1 second'`)
	if err := db.ImportJobs().Finish(t.Context(), job, []database.ImportRow{{Index: 0, Data: []byte(`{}`)}}, true); !errors.Is(err, database.ErrWorkflowConflict) {
		t.Fatalf("expired Finish = %v, want conflict", err)
	}
}

func TestImportGeocodeModelExpiredWorkflowCannotFinish(t *testing.T) {
	db, url, job := stagedGeocodeJob(t)
	execSQL(t, url, `UPDATE workflow_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE kind='import'`)
	if err := db.ImportJobs().Finish(t.Context(), job, []database.ImportRow{{Index: 0, Data: []byte(`{"NeedsGeocoding":false}`)}}, false); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("expired workflow accepted Finish: %v", err)
	}
	rows, err := db.ImportJobs().Rows(t.Context(), job.SessionID)
	if err != nil || len(rows) != 2 || string(rows[0].Data) != `{"NeedsGeocoding":true}` {
		t.Fatalf("expired workflow rows changed: %+v %v", rows, err)
	}
}
