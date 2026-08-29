package routesession_test

import (
	"context"
	"errors"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"testing"
)

func TestCommitSnapshotIsDeepCopiedAcrossFailedPersistence(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	vehicle := &models.OrganizationVehicle{ID: 50, Name: "Van", Capacity: 8}
	created := store.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{Driver: &models.Driver{ID: 1, Name: "Driver One", VehicleCapacity: 8}, EffectiveCapacity: 8, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Rider"}}}},
			{Driver: &models.Driver{ID: 2, Name: "Driver Two", VehicleCapacity: 4}, EffectiveCapacity: 4},
		},
		SelectedDrivers:   []models.Driver{{ID: 1, Name: "Driver One", VehicleCapacity: 8}, {ID: 2, Name: "Driver Two", VehicleCapacity: 4}},
		DriverOrgVehicles: map[int64]*models.OrganizationVehicle{1: vehicle},
		ActivityLocation:  &models.ActivityLocation{ID: 100, Name: "HQ", Lat: 35, Lng: -78},
		Mode:              models.RouteModeDropoff,
	})
	if _, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{{ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{}); err != nil {
		t.Fatalf("move participant: %v", err)
	}

	wantErr := errors.New("persistence failed")
	err := store.Commit(context.Background(), created.ID, func(_ context.Context, snapshot routesession.CommitSnapshot) error {
		if snapshot.SessionID != created.ID || len(snapshot.Original[0].Stops) != 1 || len(snapshot.Final[1].Stops) != 1 {
			t.Fatalf("commit snapshot = %#v", snapshot)
		}
		result := snapshot.RoutingResult()
		result.Routes[1].Driver.Name = "mutated result"
		if snapshot.Final[1].Driver.Name != "Driver Two" {
			t.Fatal("RoutingResult aliases CommitSnapshot.Final")
		}
		persistedResult := snapshot.RoutingResult()

		snapshot.Original[0].Driver.Name = "mutated original"
		snapshot.Original[0].Stops[0].Participant.Name = "mutated rider"
		snapshot.Final[1].Driver.Name = "mutated final"
		snapshot.Final[1].Stops[0].Participant.Name = "mutated final rider"
		snapshot.SelectedDrivers[0].Name = "mutated selected"
		snapshot.DriverOrgVehicles[1].Name = "mutated vehicle"
		snapshot.ActivityLocation.Name = "mutated location"
		if persistedResult.Routes[1].Driver.Name != "Driver Two" || persistedResult.Routes[1].Stops[0].Participant.Name != "Rider" {
			t.Fatal("CommitSnapshot mutation changed the derived event payload")
		}
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("Commit error = %v, want %v", err, wantErr)
	}

	err = store.Commit(context.Background(), created.ID, func(_ context.Context, snapshot routesession.CommitSnapshot) error {
		if snapshot.Original[0].Driver.Name != "Driver One" || snapshot.Original[0].Stops[0].Participant.Name != "Rider" {
			t.Fatalf("original snapshot retained mutation: %#v", snapshot.Original)
		}
		if snapshot.Final[1].Driver.Name != "Driver Two" || snapshot.Final[1].Stops[0].Participant.Name != "Rider" {
			t.Fatalf("final snapshot retained mutation: %#v", snapshot.Final)
		}
		if snapshot.SelectedDrivers[0].Name != "Driver One" || snapshot.DriverOrgVehicles[1].Name != "Van" || snapshot.ActivityLocation.Name != "HQ" {
			t.Fatalf("commit metadata retained mutation: %#v", snapshot)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry Commit: %v", err)
	}
}
