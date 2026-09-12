package importer

import (
	"context"
	"errors"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func TestPersistentImportResumesAfterWorkerShutdownAndCommitsOnce(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	a, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	started := make(chan struct{}, 1)
	slow := &fakeGeocoder{result: func(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	first := NewPersistentStore(t.Context(), slow, a, a.Workflows(), a.ImportJobs())
	defer first.Close()
	grid := testGrid(t, "name,address\nSynthetic Rider,1 Example St\n")
	created, err := first.CreateContext(t.Context(), KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not claim address")
	}
	first.Close()
	fast := &fakeGeocoder{result: func(context.Context, string, int) (*geocoding.GeocodingResult, error) {
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 35, Lng: -79}}, nil
	}}
	second := NewPersistentStore(t.Context(), fast, b, b.Workflows(), b.ImportJobs())
	defer second.Close()
	deadline := time.Now().Add(35 * time.Second)
	for {
		snapshot, ok, err := second.Load(t.Context(), created.ID)
		if err != nil || !ok {
			t.Fatalf("load: %v %v", ok, err)
		}
		if !snapshot.GeocodeProgress.Running {
			if len(snapshot.Rows) != 1 || !snapshot.Rows[0].HasCoordinates {
				t.Fatalf("rows: %+v", snapshot.Rows)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("geocoding did not resume")
		}
		time.Sleep(20 * time.Millisecond)
	}
	result, err := second.Commit(t.Context(), created.ID, nil)
	if err != nil || result.Created != 1 {
		t.Fatalf("commit: %+v %v", result, err)
	}
	if _, err = second.Commit(t.Context(), created.ID, nil); !errors.Is(err, ErrCommitConsumed) {
		t.Fatalf("repeat commit: %v", err)
	}
	rows, err := a.Participants().List(t.Context(), "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("roster: %d %v", len(rows), err)
	}
}

func TestPersistentImportPreservesDiagnosticsAndSkipsInvalidRows(t *testing.T) {
	db := postgrestest.Open(t)
	g := &fakeGeocoder{result: func(context.Context, string, int) (*geocoding.GeocodingResult, error) {
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 35, Lng: -79}}, nil
	}}
	s := NewPersistentStore(t.Context(), g, db, db.Workflows(), db.ImportJobs())
	defer s.Close()
	grid := testGrid(t, "name,address\nGood,1 Example St\n,2 Example St\nBad\x00Name,3 Example St\n")
	grid.Warnings = []string{"synthetic file warning"}
	created, err := s.CreateContext(t.Context(), KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, ok, err := s.Load(t.Context(), created.ID)
		if err != nil || !ok {
			t.Fatalf("load: %v %v", ok, err)
		}
		if len(snap.Grid.Headers) != 2 || len(snap.Grid.Warnings) != 1 {
			t.Fatalf("lost diagnostics: %+v", snap.Grid)
		}
		if !snap.GeocodeProgress.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("geocode timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = s.SelectRowsContext(t.Context(), created.ID, []bool{true}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("invalid selection: %v", err)
	}
	result, err := s.Commit(t.Context(), created.ID, nil)
	if err != nil || result.Created != 1 {
		t.Fatalf("commit valid row: %+v %v", result, err)
	}
}

func TestPersistentImportProviderDeadlineRemainsRetryable(t *testing.T) {
	t.Parallel()
	db := postgrestest.Open(t)
	g := &fakeGeocoder{result: func(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	s := NewPersistentStore(t.Context(), g, db, db.Workflows(), db.ImportJobs())
	defer s.Close()
	grid := testGrid(t, "name,address\nRider,1 Example St\n")
	created, err := s.CreateContext(t.Context(), KindParticipant, "riders.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(32 * time.Second)
	for {
		snap, ok, err := s.Load(t.Context(), created.ID)
		if err != nil || !ok {
			t.Fatalf("load: %v %v", ok, err)
		}
		if !snap.GeocodeProgress.Running || len(snap.Rows[0].Errors) > 0 {
			t.Fatalf("temporary deadline failed address: %+v", snap)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
