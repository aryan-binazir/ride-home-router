package postgres_test

import (
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

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

func TestImportSummaryKeepsPendingRowsRunningWithoutJobs(t *testing.T) {
	db := postgrestest.Open(t)
	ctx := t.Context()
	if err := db.Workflows().Create(ctx, "import", "pending", []byte(`{}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	err := db.Workflows().Transact(ctx, "import", "pending", time.Hour, func(_ *database.WorkflowRecord, w database.WorkflowWrites) error {
		return w.StageImport(ctx, "pending", []database.ImportRow{{Index: 0, Data: []byte(`{"Errors":null,"NeedsGeocoding":true}`), Selected: true}}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := db.ImportJobs().Summary(ctx, "pending")
	if err != nil || !summary.Running || summary.Done != 0 || summary.Total != 0 || summary.SelectedCount != 1 {
		t.Fatalf("pending summary=%+v err=%v", summary, err)
	}
}
