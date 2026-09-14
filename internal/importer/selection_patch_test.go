package importer

import (
	"context"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func TestPersistentPageSelectionAndCommitPreserveOffPageChoices(t *testing.T) {
	db, err := postgres.New(t.Context(), postgrestest.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewPersistentStore(t.Context(), successfulTestGeocoder(), db, db.Workflows(), db.ImportJobs())
	t.Cleanup(s.Close)
	grid := testGrid(t, "name,address\nFirst,1 Main St\nSecond,2 Main St\nThird,3 Main St\n")
	created, err := s.CreateContext(t.Context(), KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, ok, err := s.Load(t.Context(), created.ID)
		if err != nil || !ok {
			t.Fatalf("load: found=%t err=%v", ok, err)
		}
		if !snapshot.GeocodeProgress.Running {
			break
		}
		select {
		case <-deadline:
			t.Fatal("geocoding timed out")
		case <-ticker.C:
		}
	}
	if _, err = s.SelectRowsPatch(t.Context(), created.ID, map[int]bool{0: false}); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitRowsPatch(t.Context(), created.ID, map[int]bool{2: false})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.NotSelected != 2 {
		t.Fatalf("off-page choices lost: %+v", result)
	}
}

func TestPageSelectionAndCommitPreserveOffPageChoices(t *testing.T) {
	db := newFakeDataStore()
	s := newStore(successfulTestGeocoder(), db, time.Hour, time.Hour, time.Now)
	t.Cleanup(s.Close)
	grid := testGrid(t, "name,address\nFirst,1 Main St\nSecond,2 Main St\nThird,3 Main St\n")
	created, err := s.Create(KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyMapping(context.Background(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatal(err)
	}
	waitForGeocoding(t, s, created.ID)
	if _, err = s.SelectRowsPatch(t.Context(), created.ID, map[int]bool{0: false}); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitRowsPatch(t.Context(), created.ID, map[int]bool{2: false})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.NotSelected != 2 {
		t.Fatalf("off-page choices lost: %+v", result)
	}
}
