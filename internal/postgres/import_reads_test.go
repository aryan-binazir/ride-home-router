package postgres_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func TestImportRowsByIndicesReturnsOnlyRequestedRows(t *testing.T) {
	db := postgrestest.Open(t)
	ctx := t.Context()
	if err := db.Workflows().Create(ctx, "import", "subset", []byte(`{}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	err := db.Workflows().Transact(ctx, "import", "subset", time.Hour, func(_ *database.WorkflowRecord, w database.WorkflowWrites) error {
		return w.StageImport(ctx, "subset", []database.ImportRow{
			{Index: 0, Data: []byte(`{"Name":"First"}`)},
			{Index: 1, Data: []byte(`{"Name":"Unrelated"}`)},
			{Index: 2, Data: []byte(`{"Name":"Third"}`)},
		}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.ImportJobs().RowsByIndices(ctx, "subset", []int{2, 0})
	if err != nil || len(rows) != 2 {
		t.Fatalf("subset=%+v err=%v", rows, err)
	}
	if rows[0].Index != 0 || rows[1].Index != 2 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if _, err := db.ImportJobs().RowsByIndices(ctx, "subset", []int{0, 8}); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("missing row: %v", err)
	}
	rows, err = db.ImportJobs().RowsByIndices(ctx, "subset", nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty selection=%+v err=%v", rows, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.ImportJobs().RowsByIndices(canceled, "subset", []int{0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestImportSummaryCountsJobsAndSelectedValidRows(t *testing.T) {
	db := postgrestest.Open(t)
	ctx := t.Context()
	if err := db.Workflows().Create(ctx, "import", "summary", []byte(`{}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	err := db.Workflows().Transact(ctx, "import", "summary", time.Hour, func(_ *database.WorkflowRecord, w database.WorkflowWrites) error {
		return w.StageImport(ctx, "summary", []database.ImportRow{
			{Index: 0, Data: []byte(`{}`), Selected: true},
			{Index: 1, Data: []byte(`{"Errors":null}`), Selected: true},
			{Index: 2, Data: []byte(`{"Errors":[]}`), Selected: false},
			{Index: 3, Data: []byte(`{"Errors":["invalid"]}`), Selected: true},
			{Index: 4, Data: []byte(`{"Errors":[]}`), Selected: true},
		}, []database.ImportJob{{Index: 0, Address: "Shared", Rows: []int{0, 1}}, {Index: 1, Address: "Other", Rows: []int{2, 4}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := db.ImportJobs().Summary(ctx, "summary")
	if err != nil || summary.Done != 0 || summary.Total != 2 || summary.RowCount != 5 || summary.SelectedCount != 3 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	job, ok, err := db.ImportJobs().Claim(ctx, "summary-claim", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := db.ImportJobs().Finish(ctx, job, []database.ImportRow{{Index: 0, Data: []byte(`{"Errors":["failed"]}`)}, {Index: 1, Data: []byte(`{"Errors":["failed"]}`)}}, true); err != nil {
		t.Fatal(err)
	}
	summary, err = db.ImportJobs().Summary(ctx, "summary")
	if err != nil || summary.Done != 1 || summary.Total != 2 || summary.RowCount != 5 || summary.SelectedCount != 1 {
		t.Fatalf("finished summary=%+v err=%v", summary, err)
	}
	summary, err = db.ImportJobs().Summary(ctx, "missing")
	if err != nil || summary.RowCount != 0 || summary.Total != 0 {
		t.Fatalf("empty summary=%+v err=%v", summary, err)
	}
}
