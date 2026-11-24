package importer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/geocoding"
	"github.com/aryan-binazir/ride-home-router/internal/postgres/postgrestest"
)

type boundedImportReads struct {
	database.ImportJobRepository
	rejectFullRows atomic.Bool
}

func (r *boundedImportReads) Rows(ctx context.Context, id string) ([]database.ImportRow, error) {
	if r.rejectFullRows.Load() {
		return nil, errors.New("unexpected full import row read")
	}
	return r.ImportJobRepository.Rows(ctx, id)
}

func TestPersistentProgressDoesNotLoadRows(t *testing.T) {
	db := postgrestest.Open(t)
	jobs := &boundedImportReads{ImportJobRepository: db.ImportJobs()}
	g := &fakeGeocoder{result: func(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	s := NewPersistentStore(t.Context(), g, db, db.Workflows(), jobs)
	defer s.Close()
	grid := testGrid(t, "name,address\nFirst,1 Shared St\n,Invalid row\nThird,1 Shared St\n")
	created, err := s.CreateContext(t.Context(), KindParticipant, "shared.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers))
	if err != nil {
		t.Fatal(err)
	}
	jobs.rejectFullRows.Store(true)
	progress, ok, err := s.LoadProgress(t.Context(), created.ID)
	if err != nil || !ok {
		t.Fatalf("progress exists=%v err=%v", ok, err)
	}
	if progress.Status != snapshot.Status || progress.GeocodeProgress != snapshot.GeocodeProgress || progress.RowCount != 3 || progress.SelectedCount != 2 {
		t.Fatalf("progress=%+v snapshot=%+v", progress, snapshot)
	}
	if _, ok, err := s.LoadProgress(t.Context(), "missing"); err != nil || ok {
		t.Fatalf("missing progress=%v err=%v", ok, err)
	}
	if _, err := s.CancelContext(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.LoadProgress(t.Context(), created.ID); err != nil || ok {
		t.Fatalf("canceled progress=%v err=%v", ok, err)
	}
}
