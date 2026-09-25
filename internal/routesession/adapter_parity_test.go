package routesession_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"slices"
	"testing"
)

func TestRouteSessionAdaptersPreserveEditAndCommitBehavior(t *testing.T) {
	for _, adapter := range []string{"memory", "durable"} {
		t.Run(adapter, func(t *testing.T) {
			var store *routesession.Store
			if adapter == "memory" {
				store = routesession.NewStore(calculator{})
				t.Cleanup(store.Close)
			} else {
				store = routesession.NewPersistentStore(calculator{}, postgrestest.Open(t).Workflows())
			}
			ctx := t.Context()
			input := routesession.CreateInput{
				Routes: []models.CalculatedRoute{
					{Driver: &models.Driver{ID: 1, Name: "First", VehicleCapacity: 3}, EffectiveCapacity: 3, Stops: []models.RouteStop{
						{Participant: &models.Participant{ID: 10, Name: "Sister", Address: "12 Oak St", Lat: 1}},
						{Participant: &models.Participant{ID: 11, Name: "Brother", Address: "12 Oak St", Lat: 1}},
					}},
					{Driver: &models.Driver{ID: 2, Name: "Second", VehicleCapacity: 3}, EffectiveCapacity: 3, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 12, Name: "Other", Address: "9 Pine St", Lat: 2}}}},
				},
				SelectedDrivers:  []models.Driver{{ID: 1, Name: "First", VehicleCapacity: 3}, {ID: 2, Name: "Second", VehicleCapacity: 3}, {ID: 3, Name: "Third", VehicleCapacity: 3}},
				ActivityLocation: &models.ActivityLocation{ID: 1, Lat: 0, Lng: 0}, Mode: models.RouteModeDropoff,
			}
			created, err := store.CreateContext(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(created.ChangedRouteIndexes, []int{0, 1}) {
				t.Fatalf("created changed routes = %v", created.ChangedRouteIndexes)
			}
			input.Routes[0].Stops[0].Participant.Name = "changed input"
			created.Routes[0].Stops[0].Participant.Name = "changed output"
			loaded, ok, err := store.Load(ctx, created.ID)
			if err != nil || !ok || loaded.Routes[0].Stops[0].Participant.Name != "Sister" || len(loaded.ChangedRouteIndexes) != 0 {
				t.Fatalf("load did not isolate input and output: %+v %v %v", loaded, ok, err)
			}
			moved, err := store.ApplyMoves(ctx, created.ID, []routesession.Move{{ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: 0}}, routesession.ApplyMovesOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(moved.ChangedRouteIndexes, []int{0, 1}) || len(moved.Routes[0].Stops) != 0 || len(moved.Routes[1].Stops) != 3 || moved.Routes[1].Stops[0].Participant.ID != 10 || moved.Routes[1].Stops[1].Participant.ID != 11 {
				t.Fatalf("household move = %+v", moved)
			}
			if _, err := store.ApplyMoves(ctx, created.ID, []routesession.Move{{ParticipantID: 999, ToRouteIndex: 0}}, routesession.ApplyMovesOptions{}); !errors.Is(err, routesession.ErrParticipantNotFound) {
				t.Fatalf("invalid move = %v", err)
			}
			afterFailure, ok, err := store.Load(ctx, created.ID)
			if err != nil || !ok || len(afterFailure.Routes[1].Stops) != 3 {
				t.Fatalf("invalid move changed route: %+v %v %v", afterFailure, ok, err)
			}
			noted, err := store.SetReviewerNote(ctx, created.ID, "  checked  ")
			if err != nil || noted.ReviewerNote != "checked" || len(noted.ChangedRouteIndexes) != 0 {
				t.Fatalf("note = %+v %v", noted, err)
			}
			added, err := store.AddDriver(ctx, created.ID, 3)
			if err != nil || !slices.Equal(added.ChangedRouteIndexes, []int{2}) {
				t.Fatalf("add driver = %+v %v", added, err)
			}
			swapped, err := store.SwapDrivers(ctx, created.ID, 0, 2)
			if err != nil || !slices.Equal(swapped.ChangedRouteIndexes, []int{0, 2}) || swapped.Routes[0].Driver.ID != 3 || swapped.Routes[2].Driver.ID != 1 {
				t.Fatalf("swap drivers = %+v %v", swapped, err)
			}
			failed := errors.New("save failed")
			err = store.CommitEvent(ctx, created.ID, func(_ context.Context, snapshot routesession.CommitSnapshot, _ database.WorkflowWrites) error {
				if snapshot.ReviewerNote != "checked" || len(snapshot.Final[1].Stops) != 3 || snapshot.Final[1].Stops[0].Participant.Name != "Sister" {
					t.Fatalf("commit snapshot = %+v", snapshot)
				}
				snapshot.Final[1].Stops[0].Participant.Name = "changed commit payload"
				return failed
			})
			if !errors.Is(err, failed) {
				t.Fatalf("failed save = %v", err)
			}
			if err := store.CommitEvent(ctx, created.ID, func(_ context.Context, snapshot routesession.CommitSnapshot, _ database.WorkflowWrites) error {
				if snapshot.Final[1].Stops[0].Participant.Name != "Sister" {
					t.Fatalf("failed save mutated session: %+v", snapshot.Final)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.CommitEvent(ctx, created.ID, func(context.Context, routesession.CommitSnapshot, database.WorkflowWrites) error { return nil }); !errors.Is(err, routesession.ErrAlreadyCommitted) {
				t.Fatalf("second save = %v", err)
			}
		})
	}
}

func TestRouteSessionAdaptersRollBackBatchAndRefreshDirtyRoutes(t *testing.T) {
	for _, adapter := range []string{"memory", "durable"} {
		t.Run(adapter, func(t *testing.T) {
			var store *routesession.Store
			if adapter == "memory" {
				store = routesession.NewStore(calculator{})
				t.Cleanup(store.Close)
			} else {
				store = routesession.NewPersistentStore(calculator{}, postgrestest.Open(t).Workflows())
			}
			ctx := t.Context()
			created, err := store.CreateContext(ctx, routesession.CreateInput{
				Routes: []models.CalculatedRoute{
					{Driver: &models.Driver{ID: 1, VehicleCapacity: 1}, EffectiveCapacity: 1, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 1, Address: "First", Lat: 1}}}},
					{Driver: &models.Driver{ID: 2, VehicleCapacity: 3}, EffectiveCapacity: 3, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 2, Address: "Second", Lat: 2}}, {Participant: &models.Participant{ID: 3, Address: "Third", Lat: 3}}}},
				},
				ActivityLocation: &models.ActivityLocation{ID: 1}, Mode: models.RouteModeDropoff,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.ApplyMoves(ctx, created.ID, []routesession.Move{{ParticipantID: 3, ToRouteIndex: 0}, {ParticipantID: 999, ToRouteIndex: 1}}, routesession.ApplyMovesOptions{})
			if !errors.Is(err, routesession.ErrParticipantNotFound) {
				t.Fatalf("batch error = %v", err)
			}
			stillOriginal, ok, err := store.Load(ctx, created.ID)
			if err != nil || !ok || len(stillOriginal.Routes[0].Stops) != 1 || len(stillOriginal.Routes[1].Stops) != 2 {
				t.Fatalf("batch rollback = %+v %v %v", stillOriginal, ok, err)
			}
			over, err := store.ApplyMoves(ctx, created.ID, []routesession.Move{{ParticipantID: 3, ToRouteIndex: 0}}, routesession.ApplyMovesOptions{})
			if err != nil || !over.IsOutOfBalance || len(over.Routes[0].Stops) != 2 {
				t.Fatalf("over capacity move = %+v %v", over, err)
			}
			if err := store.CommitEvent(ctx, created.ID, func(context.Context, routesession.CommitSnapshot, database.WorkflowWrites) error { return nil }); !errors.Is(err, routesession.ErrUnbalanced) {
				t.Fatalf("unbalanced commit = %v", err)
			}
			balanced, err := store.ApplyMoves(ctx, created.ID, []routesession.Move{{ParticipantID: 1, ToRouteIndex: 1}}, routesession.ApplyMovesOptions{})
			if err != nil || balanced.IsOutOfBalance || len(balanced.Routes[0].Stops) != 1 || len(balanced.Routes[1].Stops) != 2 {
				t.Fatalf("balance restoring move = %+v %v", balanced, err)
			}
			if balanced.Routes[0].TotalDistanceMeters <= 0 || balanced.Routes[1].TotalDistanceMeters <= 0 {
				t.Fatalf("dirty routes were not recalculated: %+v", balanced.Routes)
			}
			reset, err := store.ResetContext(ctx, created.ID)
			if err != nil || reset.IsOutOfBalance || !slices.Equal(reset.ChangedRouteIndexes, []int{0, 1}) || len(reset.Routes[0].Stops) != 1 || len(reset.Routes[1].Stops) != 2 {
				t.Fatalf("reset = %+v %v", reset, err)
			}
		})
	}
}
