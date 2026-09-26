package importer

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"sync/atomic"
	"testing"
	"time"
)

const workerBackoffBeforeWake = 2 * time.Second

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

func TestPersistentGeocodingReadsOnlyItsAddressRows(t *testing.T) {
	db := postgrestest.Open(t)
	jobs := &boundedImportReads{ImportJobRepository: db.ImportJobs()}
	release := make(chan struct{})
	g := &fakeGeocoder{result: func(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
		select {
		case <-release:
			return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 35, Lng: -79}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	s := NewPersistentStore(t.Context(), g, db, db.Workflows(), jobs)
	defer s.Close()
	time.Sleep(workerBackoffBeforeWake)
	grid := testGrid(t, "name,address\nFirst,1 Shared St\n,Invalid row\nThird,1 Shared St\n")
	created, err := s.CreateContext(t.Context(), KindParticipant, "shared.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatal(err)
	}
	jobs.rejectFullRows.Store(true)
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		done, total, err := db.ImportJobs().Progress(t.Context(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if done == 1 && total == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("targeted geocoding did not finish: %d/%d", done, total)
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := s.Commit(t.Context(), created.ID, nil)
	if err != nil || result.Created != 2 {
		t.Fatalf("commit=%+v err=%v", result, err)
	}
	rows, err := db.Participants().List(t.Context(), "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("participants=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.Lat != 35 || row.Lng != -79 {
			t.Fatalf("row was not geocoded: %+v", row)
		}
	}
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
