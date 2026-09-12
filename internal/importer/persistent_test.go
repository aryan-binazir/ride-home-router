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
	deadline := time.Now().Add(5 * time.Second)
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
