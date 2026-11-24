package importer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aryan-binazir/ride-home-router/internal/postgres/postgrestest"

	"github.com/jackc/pgx/v5"
)

func TestReviewPagesPreserveCountsOrderAndOffPageChoices(t *testing.T) {
	db := postgrestest.Open(t)
	s := durableTestStore(t, db, successfulTestGeocoder())
	var csv strings.Builder
	csv.WriteString("name,address\n")
	for i := range 61 {
		name := fmt.Sprintf("Rider %03d", i)
		if i == 60 {
			name = ""
		}
		fmt.Fprintf(&csv, "%s,1 Main St\n", name)
	}
	preview := stageDurableImport(t, s, KindParticipant, csv.String())
	waitDurableImport(t, s, preview.ID)
	page, err := s.LoadReviewPage(t.Context(), preview.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 50 || page.RowCount != 61 || page.SelectedCount != 60 || page.Next != 50 || page.Rows[0].Index != 0 {
		t.Fatalf("first page: %+v", page)
	}
	counts, err := s.SelectRowsPatch(t.Context(), preview.ID, map[int]bool{0: false, 60: true})
	if err != nil {
		t.Fatal(err)
	}
	if counts.RowCount != 61 || counts.SelectedCount != 59 {
		t.Fatalf("selected counts: %+v", counts)
	}
	page, err = s.LoadReviewPage(t.Context(), preview.ID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if page.Offset != 50 || page.Previous != 0 || page.Next != 0 || len(page.Rows) != 11 || page.Rows[0].Index != 50 || !page.Rows[10].Selected || len(page.Rows[10].Errors) == 0 || page.SelectedCount != 59 {
		t.Fatalf("clamped page: %+v", page)
	}
	page, err = s.LoadReviewPage(t.Context(), preview.ID, -10)
	if err != nil || page.Offset != 0 || page.Rows[0].Selected {
		t.Fatalf("off-page choice: %+v %v", page, err)
	}
}

func TestReviewDeltaRejectsAllInvalidIndicesWithoutChangingChoices(t *testing.T) {
	db := postgrestest.Open(t)
	s := durableTestStore(t, db, successfulTestGeocoder())
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nFirst,1 Main St\nSecond,1 Main St\n")
	waitDurableImport(t, s, preview.ID)
	for _, bad := range []int{-1, 2, 2000, int(^uint(0) >> 1), -int(^uint(0) >> 1)} {
		if _, err := s.SelectRowsPatch(t.Context(), preview.ID, map[int]bool{0: false, bad: false}); !errors.Is(err, ErrInvalidSelection) {
			t.Fatalf("index %d: %v", bad, err)
		}
		page, err := s.LoadReviewPage(t.Context(), preview.ID, 0)
		if err != nil || page.SelectedCount != 2 || !page.Rows[0].Selected || !page.Rows[1].Selected {
			t.Fatalf("partial delta: %+v %v", page, err)
		}
	}
}

func TestReviewRejectsMappingConsumedCanceledAndExpiredSessions(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	db := postgrestest.OpenURL(t, databaseURL)
	s := durableTestStore(t, db, successfulTestGeocoder())
	grid := testGrid(t, "name,address\nRider,1 Main St\n")
	mapping, err := s.CreateContext(t.Context(), KindParticipant, "mapping.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	assertReviewErrors(t, s, mapping.ID, ErrInvalidSessionState)
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nOther,1 Main St\n")
	waitDurableImport(t, s, preview.ID)
	if _, err := s.LoadReviewPage(t.Context(), preview.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelContext(t.Context(), preview.ID); err != nil {
		t.Fatal(err)
	}
	assertReviewErrors(t, s, preview.ID, ErrSessionNotFound)
	expired := stageDurableImport(t, s, KindParticipant, "name,address\nExpired,1 Main St\n")
	waitDurableImport(t, s, expired.ID)
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(t.Context(), `UPDATE workflow_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE kind='import' AND id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	assertReviewErrors(t, s, expired.ID, ErrSessionNotFound)
	committed := stageDurableImport(t, s, KindParticipant, "name,address\nCommitted,1 Main St\n")
	waitDurableImport(t, s, committed.ID)
	if _, err := s.CommitRowsPatch(t.Context(), committed.ID, nil); err != nil {
		t.Fatal(err)
	}
	assertReviewErrors(t, s, committed.ID, ErrInvalidSessionState)
}

func assertReviewErrors(t *testing.T, s *Store, id string, want error) {
	t.Helper()
	if _, err := s.LoadReviewPage(t.Context(), id, 0); !errors.Is(err, want) {
		t.Fatalf("review %s: %v, want %v", id, err, want)
	}
	if _, err := s.SelectRowsPatch(t.Context(), id, map[int]bool{0: false}); !errors.Is(err, want) {
		t.Fatalf("selection %s: %v, want %v", id, err, want)
	}
}

func TestCommitPageDeltaRollsBackRosterChoicesAndToken(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	db := postgrestest.OpenURL(t, databaseURL)
	s := durableTestStore(t, db, successfulTestGeocoder())
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nFirst,1 Main St\nSecond,1 Main St\nThird,1 Main St\n")
	waitDurableImport(t, s, preview.ID)
	if _, err := s.SelectRowsPatch(t.Context(), preview.ID, map[int]bool{0: false}); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(t.Context(), `CREATE FUNCTION reject_import_clear() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic clear failure'; END $$;
 CREATE TRIGGER reject_import_clear BEFORE DELETE ON import_rows FOR EACH ROW EXECUTE FUNCTION reject_import_clear();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitRowsPatch(t.Context(), preview.ID, map[int]bool{1: false}); err == nil {
		t.Fatal("commit succeeded despite failing after roster writes")
	}
	roster, err := db.Participants().List(t.Context(), "")
	if err != nil || len(roster) != 0 {
		t.Fatalf("roster rollback: %+v %v", roster, err)
	}
	page, err := s.LoadReviewPage(t.Context(), preview.ID, 0)
	if err != nil || page.Status != StatusPreviewing || page.SelectedCount != 2 || page.Rows[0].Selected || !page.Rows[1].Selected || !page.Rows[2].Selected {
		t.Fatalf("choices rollback: %+v %v", page, err)
	}
	if _, err := conn.Exec(t.Context(), `DROP TRIGGER reject_import_clear ON import_rows`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitRowsPatch(t.Context(), preview.ID, map[int]bool{1: false, 3: false}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("invalid commit delta: %v", err)
	}
	committed, err := s.CommitRowsPatch(t.Context(), preview.ID, nil)
	if err != nil || committed.Result.Created != 2 || committed.Result.NotSelected != 1 {
		t.Fatalf("retry token/choices: %+v %v", committed, err)
	}
}
