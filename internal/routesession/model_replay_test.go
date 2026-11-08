package routesession_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"testing"
	"time"
)

type replayLoadBarrier struct {
	database.WorkflowRepository
	loaded  chan struct{}
	release chan struct{}
}

func (r replayLoadBarrier) Load(ctx context.Context, kind, id string, ttl time.Duration) (database.WorkflowRecord, error) {
	rec, err := r.WorkflowRepository.Load(ctx, kind, id, ttl)
	close(r.loaded)
	select {
	case <-r.release:
		return rec, err
	case <-ctx.Done():
		return rec, ctx.Err()
	}
}

func TestModelReplayStaleResetAndNote(t *testing.T) {
	for _, staleReset := range []bool{true, false} {
		t.Run(map[bool]string{true: "reset", false: "note"}[staleReset], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			db := postgrestest.Open(t)
			live := routesession.NewPersistentStore(distance.NewEstimator(), db.Workflows())
			a, err := live.CreateContext(ctx, routesession.CreateInput{Mode: models.RouteModeDropoff, ActivityLocation: &models.ActivityLocation{}, Routes: []models.CalculatedRoute{
				{Driver: &models.Driver{ID: 1, VehicleCapacity: 2}, EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10}}}},
				{Driver: &models.Driver{ID: 2, VehicleCapacity: 2}, EffectiveCapacity: 2},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = live.ApplyMoves(ctx, a.ID, []routesession.Move{{ParticipantID: 10, ToRouteIndex: 1}}, routesession.ApplyMovesOptions{}); err != nil {
				t.Fatal(err)
			}
			barrier := replayLoadBarrier{db.Workflows(), make(chan struct{}), make(chan struct{})}
			stale := routesession.NewPersistentStore(distance.NewEstimator(), barrier)
			done := make(chan error, 1)
			go func() {
				var err error
				if staleReset {
					_, err = stale.ResetContext(ctx, a.ID)
				} else {
					_, err = stale.SetReviewerNote(ctx, a.ID, "stale")
				}
				done <- err
			}()
			select {
			case <-barrier.loaded:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if staleReset {
				_, err = live.SetReviewerNote(ctx, a.ID, "latest")
			} else {
				_, err = live.ResetContext(ctx, a.ID)
			}
			close(barrier.release)
			if err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, database.ErrWorkflowConflict) {
				t.Fatalf("stale write: %v", err)
			}
			if staleReset {
				_, err = live.ResetContext(ctx, a.ID)
			} else {
				_, err = live.SetReviewerNote(ctx, a.ID, "latest")
			}
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := live.Load(ctx, a.ID)
			if err != nil || !ok || got.ReviewerNote != "latest" || len(got.Routes[0].Stops) != 1 || got.Routes[0].Stops[0].Participant.ID != 10 || len(got.Routes[1].Stops) != 0 {
				t.Fatalf("reset/note oracle: %+v %v", got, err)
			}
			rec, err := db.Workflows().Load(ctx, "route", a.ID, time.Hour)
			if err != nil || rec.Revision != 4 {
				t.Fatalf("revision: %d %v", rec.Revision, err)
			}
		})
	}
}

func TestModelReplayCallbackSaveDeleteRetry(t *testing.T) {
	db := postgrestest.Open(t)
	s := routesession.NewPersistentStore(distance.NewEstimator(), db.Workflows())
	a, err := s.CreateContext(t.Context(), routesession.CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var eventID int64
	persist := func(ctx context.Context, snap routesession.CommitSnapshot, w database.WorkflowWrites) error {
		calls++
		e, err := w.CreateEvent(ctx, &models.Event{RouteSessionID: snap.SessionID, EventDate: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), Mode: models.RouteModeDropoff}, nil, nil)
		if err == nil {
			eventID = e.ID
		}
		return err
	}
	if err = s.CommitEvent(t.Context(), a.ID, persist); err != nil {
		t.Fatal(err)
	}
	if err = db.Events().Delete(t.Context(), eventID); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitEvent(t.Context(), a.ID, persist); !errors.Is(err, routesession.ErrAlreadyCommitted) {
		t.Fatalf("retry: %v", err)
	}
	if calls != 1 {
		t.Fatalf("callback calls: %d", calls)
	}
	if _, n, err := db.Events().List(t.Context(), 10, 0); err != nil || n != 0 {
		t.Fatalf("resurrection: %d %v", n, err)
	}
}
