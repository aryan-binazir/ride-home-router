package importer

import (
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func TestPersistentPageSelectionAndCommitPreserveOffPageChoices(t *testing.T) {
	db := postgrestest.Open(t)
	s := durableTestStore(t, db, successfulTestGeocoder())
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nFirst,1 Main St\nSecond,2 Main St\nThird,3 Main St\n")
	waitDurableImport(t, s, preview.ID)
	if _, err := s.SelectRowsPatch(t.Context(), preview.ID, map[int]bool{0: false}); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitRowsPatch(t.Context(), preview.ID, map[int]bool{2: false})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.NotSelected != 2 {
		t.Fatalf("off-page choices lost: %+v", result)
	}
}
