package routesession_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"sync"
	"testing"
	"time"
)

func TestPersistentRoutesShareStateAndCommitOnce(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	first, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	a := routesession.NewPersistentStore(calculator{}, first.Workflows())
	b := routesession.NewPersistentStore(calculator{}, second.Workflows())
	created, err := a.CreateContext(t.Context(), routesession.CreateInput{Mode: models.RouteModeDropoff, RouteTime: "18:30"})
	if err != nil {
		t.Fatal(err)
	}
	read, ok, err := b.Load(t.Context(), created.ID)
	if err != nil || !ok || read.RouteTime != "18:30" {
		t.Fatalf("read on second instance: %+v %v %v", read, ok, err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, store := range []*routesession.Store{a, b} {
		wg.Go(func() {
			outcomes <- store.CommitEvent(t.Context(), created.ID, func(ctx context.Context, _ routesession.CommitSnapshot, w database.WorkflowWrites) error {
				_, err := w.CreateEvent(ctx, &models.Event{EventDate: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), Mode: models.RouteModeDropoff}, nil, nil)
				return err
			})
		})
	}
	wg.Wait()
	close(outcomes)
	success, duplicate := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else if errors.Is(err, routesession.ErrAlreadyCommitted) {
			duplicate++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || duplicate != 1 {
		t.Fatalf("success=%d duplicate=%d", success, duplicate)
	}
	_, total, err := first.Events().List(t.Context(), 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("saved %d events: %v", total, err)
	}
}

func TestPersistentEventFailureRollsBackConsumptionAndInsert(t *testing.T) {
	db := postgrestest.Open(t)
	store := routesession.NewPersistentStore(calculator{}, db.Workflows())
	created, err := store.CreateContext(t.Context(), routesession.CreateInput{Mode: models.RouteModeDropoff})
	if err != nil {
		t.Fatal(err)
	}
	failed := errors.New("synthetic failure after insert")
	err = store.CommitEvent(t.Context(), created.ID, func(ctx context.Context, _ routesession.CommitSnapshot, w database.WorkflowWrites) error {
		if _, err := w.CreateEvent(ctx, &models.Event{EventDate: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), Mode: models.RouteModeDropoff}, nil, nil); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	_, total, err := db.Events().List(t.Context(), 10, 0)
	if err != nil || total != 0 {
		t.Fatalf("rollback left %d events: %v", total, err)
	}
	if _, ok, err := store.Load(t.Context(), created.ID); err != nil || !ok {
		t.Fatalf("failed save consumed session: %v %v", ok, err)
	}
}

func TestPersistentRouteConflictDoesNotOverwriteOtherInstance(t *testing.T) {
	db := postgrestest.Open(t)
	blocked := newBlockingCalculator()
	defer blocked.unblock()
	a := routesession.NewPersistentStore(blocked, db.Workflows())
	b := routesession.NewPersistentStore(calculator{}, db.Workflows())
	created, err := a.CreateContext(t.Context(), routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{Driver: &models.Driver{ID: 1, VehicleCapacity: 2}, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10, Lat: 35, Lng: -79}}}},
			{Driver: &models.Driver{ID: 2, VehicleCapacity: 2}},
		}, ActivityLocation: &models.ActivityLocation{ID: 1, Lat: 35.1, Lng: -79.1}, Mode: models.RouteModeDropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.ApplyMoves(t.Context(), created.ID, []routesession.Move{{ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{})
		done <- err
	}()
	select {
	case <-blocked.started:
	case <-time.After(3 * time.Second):
		t.Fatal("calculation did not start")
	}
	if _, err = b.ResetContext(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	// A separate plan stays usable while the first calculation is blocked.
	other, err := b.CreateContext(t.Context(), routesession.CreateInput{RouteTime: "07:00", Mode: models.RouteModePickup})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.Load(t.Context(), other.ID); !ok || err != nil {
		t.Fatalf("other plan blocked: %v", err)
	}
	blocked.unblock()
	if err = <-done; !errors.Is(err, database.ErrWorkflowConflict) {
		t.Fatalf("stale edit = %v", err)
	}
	got, ok, err := b.Load(t.Context(), created.ID)
	if err != nil || !ok || len(got.Routes[0].Stops) != 1 || len(got.Routes[1].Stops) != 0 {
		t.Fatalf("stale edit overwrote reset: %+v %v", got, err)
	}
}
