package routesession

import (
	"context"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"slices"
	"testing"
)

func changedFixture(t *testing.T) (*Store, Snapshot) {
	t.Helper()
	store := NewStore(distance.NewEstimator())
	t.Cleanup(store.Close)
	riders := []*models.Participant{
		{ID: 1, Name: "A", Address: "1 A St", Lat: 42.01, Lng: -71.01},
		{ID: 2, Name: "B", Address: "2 B St", Lat: 42.02, Lng: -71.02},
		{ID: 3, Name: "C", Address: "3 C St", Lat: 42.03, Lng: -71.03},
	}
	drivers := []models.Driver{
		{ID: 10, Name: "D1", Lat: 42.1, Lng: -71.1, VehicleCapacity: 4},
		{ID: 11, Name: "D2", Lat: 42.2, Lng: -71.2, VehicleCapacity: 4},
		{ID: 12, Name: "D3", Lat: 42.3, Lng: -71.3, VehicleCapacity: 4},
	}
	snapshot := store.Create(CreateInput{
		Routes: []models.CalculatedRoute{
			{Driver: &drivers[0], EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: riders[0]}, {Participant: riders[1]}}},
			{Driver: &drivers[1], EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: riders[2]}}},
		},
		SelectedDrivers:  drivers,
		ActivityLocation: &models.ActivityLocation{Lat: 42, Lng: -71},
		Mode:             models.RouteModeDropoff,
	})
	return store, snapshot
}

func TestSnapshotReportsWhichRoutesChanged(t *testing.T) {
	store, created := changedFixture(t)
	if !slices.Equal(created.ChangedRouteIndexes, []int{0, 1}) {
		t.Fatalf("a fresh calculation changes every route: %v", created.ChangedRouteIndexes)
	}
	moved, err := store.ApplyMoves(context.Background(), created.ID, []Move{{ParticipantID: 1, FromRouteIndex: 0, ToRouteIndex: 1, InsertAtPosition: 0}}, ApplyMovesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(moved.ChangedRouteIndexes, []int{0, 1}) {
		t.Fatalf("move changes source and destination: %v", moved.ChangedRouteIndexes)
	}
	added, err := store.AddDriver(context.Background(), created.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	// The appended car is reported so callers can render it; measuring an empty car costs nothing.
	if !slices.Equal(added.ChangedRouteIndexes, []int{2}) {
		t.Fatalf("adding a car reports the new index: %v", added.ChangedRouteIndexes)
	}
	swapped, err := store.SwapDrivers(context.Background(), created.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Route 0's driver became D3 and route 2's became D1; route 2 stays empty.
	if !slices.Equal(swapped.ChangedRouteIndexes, []int{0, 2}) {
		t.Fatalf("swap changes both cars: %v", swapped.ChangedRouteIndexes)
	}
	reset, err := store.Reset(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reset.ChangedRouteIndexes, []int{0, 1}) {
		t.Fatalf("reset changes every route that differs from the current state: %v", reset.ChangedRouteIndexes)
	}
	loaded, ok := store.Snapshot(created.ID)
	if !ok || len(loaded.ChangedRouteIndexes) != 0 {
		t.Fatalf("a plain read changes nothing: %v", loaded.ChangedRouteIndexes)
	}
}
