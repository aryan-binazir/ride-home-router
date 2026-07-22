package routesession_test

import (
	"context"
	"errors"
	"math"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"sync"
	"testing"
)

type calculator struct{}

func (calculator) GetDistance(_ context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	d := math.Hypot(dest.Lat-origin.Lat, dest.Lng-origin.Lng) * 1000
	return &distance.DistanceResult{DistanceMeters: d, DurationSecs: d}, nil
}

func (c calculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	result := make([][]distance.DistanceResult, len(points))
	for i := range points {
		result[i] = make([]distance.DistanceResult, len(points))
		for j := range points {
			d, _ := c.GetDistance(ctx, points[i], points[j])
			result[i][j] = *d
		}
	}
	return result, nil
}

func (c calculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	result := make([]distance.DistanceResult, len(destinations))
	for i := range destinations {
		d, _ := c.GetDistance(ctx, origin, destinations[i])
		result[i] = *d
	}
	return result, nil
}
func (calculator) PrewarmCache(context.Context, []models.Coordinates) error { return nil }

type failingCalculator struct{ err error }

func (c failingCalculator) GetDistance(context.Context, models.Coordinates, models.Coordinates) (*distance.DistanceResult, error) {
	return nil, c.err
}

func (c failingCalculator) GetDistanceMatrix(context.Context, []models.Coordinates) ([][]distance.DistanceResult, error) {
	return nil, c.err
}

func (c failingCalculator) GetDistancesFromPoint(context.Context, models.Coordinates, []models.Coordinates) ([]distance.DistanceResult, error) {
	return nil, c.err
}

func (c failingCalculator) PrewarmCache(context.Context, []models.Coordinates) error {
	return c.err
}

func TestCreateReturnsIndependentFreshSnapshot(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	routes := []models.CalculatedRoute{{
		Driver: &models.Driver{ID: 1, Name: "Driver", VehicleCapacity: 2},
		Stops:  []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Rider"}}},
	}}
	location := &models.ActivityLocation{ID: 1, Name: "HQ"}

	created := store.Create(routesession.CreateInput{Routes: routes, SelectedDrivers: []models.Driver{*routes[0].Driver}, ActivityLocation: location, UseMiles: true, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	if created.ID == "" || created.IsEditing {
		t.Fatalf("created snapshot = %#v, want ID and IsEditing=false", created)
	}
	created.Routes[0].Driver.Name = "mutated"
	created.ActivityLocation.Name = "mutated"

	got, ok := store.Snapshot(created.ID)
	if !ok {
		t.Fatal("Snapshot() did not find created session")
	}
	if got.Routes[0].Driver.Name != "Driver" || got.ActivityLocation.Name != "HQ" {
		t.Fatalf("snapshot aliases caller state: %#v", got)
	}
}

func TestApplyMovesRequiresClaimedSourceOnlyWhenRequested(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	created := store.Create(testInput())
	move := routesession.Move{ParticipantID: 10, FromRouteIndex: 1, ToRouteIndex: 1, InsertAtPosition: -1}

	if _, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{move}, routesession.ApplyMovesOptions{RequireClaimedSource: true}); !errors.Is(err, routesession.ErrParticipantNotInSource) {
		t.Fatalf("legacy ApplyMoves error = %v, want ErrParticipantNotInSource", err)
	}
	got, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{move}, routesession.ApplyMovesOptions{})
	if err != nil {
		t.Fatalf("batch ApplyMoves: %v", err)
	}
	if len(got.Routes[0].Stops) != 0 || len(got.Routes[1].Stops) != 1 || !got.IsEditing {
		t.Fatalf("moved snapshot = %#v", got)
	}
}

func TestApplyMovesRollsBackWholeBatchOnValidationFailure(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	created := store.Create(testInput())
	_, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{
		{ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1},
		{ParticipantID: 999, ToRouteIndex: 0, InsertAtPosition: -1},
	}, routesession.ApplyMovesOptions{})
	if !errors.Is(err, routesession.ErrParticipantNotFound) {
		t.Fatalf("ApplyMoves error = %v", err)
	}
	got, _ := store.Snapshot(created.ID)
	if len(got.Routes[0].Stops) != 1 || len(got.Routes[1].Stops) != 0 || got.IsEditing {
		t.Fatalf("batch was not rolled back: %#v", got)
	}
}

func TestApplyMovesRollsBackWholeBatchOnDistanceFailure(t *testing.T) {
	distanceFailure := errors.New("distance failed")
	store := routesession.NewStore(failingCalculator{err: distanceFailure})
	t.Cleanup(store.Close)
	created := store.Create(testInput())

	_, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{{
		ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1,
	}}, routesession.ApplyMovesOptions{})
	if !errors.Is(err, distanceFailure) {
		t.Fatalf("ApplyMoves error = %v, want distance failure", err)
	}

	got, ok := store.Snapshot(created.ID)
	if !ok {
		t.Fatal("session disappeared after rollback")
	}
	if len(got.Routes[0].Stops) != 1 || len(got.Routes[1].Stops) != 0 || got.IsEditing {
		t.Fatalf("distance failure was not rolled back: %#v", got)
	}
}

func TestApplyMovesBatchMatchesSequentialBalancedIntermediateRecalculation(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	routes := []models.CalculatedRoute{
		{Driver: &models.Driver{ID: 1, VehicleCapacity: 2}, EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10, Lat: 1}}, {Participant: &models.Participant{ID: 11, Lat: 2}}}},
		{Driver: &models.Driver{ID: 2, VehicleCapacity: 1}, EffectiveCapacity: 1, Stops: []models.RouteStop{}},
	}
	input := routesession.CreateInput{Routes: routes, ActivityLocation: &models.ActivityLocation{}, RouteTime: "18:30", Mode: models.RouteModeDropoff}
	sequential := store.Create(input)
	batch := store.Create(input)
	moves := []routesession.Move{{ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1}, {ParticipantID: 11, ToRouteIndex: 1, InsertAtPosition: -1}}
	for _, move := range moves {
		if _, err := store.ApplyMoves(context.Background(), sequential.ID, []routesession.Move{move}, routesession.ApplyMovesOptions{}); err != nil {
			t.Fatalf("sequential ApplyMoves: %v", err)
		}
	}
	batched, err := store.ApplyMoves(context.Background(), batch.ID, moves, routesession.ApplyMovesOptions{})
	if err != nil {
		t.Fatalf("batch ApplyMoves: %v", err)
	}
	sequentialResult, _ := store.Snapshot(sequential.ID)
	if !batched.IsOutOfBalance || len(batched.Routes[1].Stops) != len(sequentialResult.Routes[1].Stops) || batched.Routes[1].TotalDistanceMeters != sequentialResult.Routes[1].TotalDistanceMeters {
		t.Fatalf("batch=%#v sequential=%#v", batched, sequentialResult)
	}
}

func TestApplyMovesRecalculatesEveryDirtyRouteWhenBatchReturnsToBalance(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	routes := []models.CalculatedRoute{
		{
			Driver:              &models.Driver{ID: 1, Lat: 10, VehicleCapacity: 2},
			EffectiveCapacity:   2,
			Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 10, Lat: 9}}, {Participant: &models.Participant{ID: 11, Lat: 8}}},
			TotalDistanceMeters: 111,
		},
		{
			Driver:              &models.Driver{ID: 2, Lat: 7, VehicleCapacity: 1},
			EffectiveCapacity:   1,
			Stops:               []models.RouteStop{{Participant: &models.Participant{ID: 20, Lat: 6}}},
			TotalDistanceMeters: 222,
		},
	}
	created := store.Create(routesession.CreateInput{Routes: routes, ActivityLocation: &models.ActivityLocation{}, RouteTime: "18:30", Mode: models.RouteModePickup})

	got, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{
		{ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1},
		{ParticipantID: 10, ToRouteIndex: 0, InsertAtPosition: -1},
	}, routesession.ApplyMovesOptions{})
	if err != nil {
		t.Fatalf("ApplyMoves: %v", err)
	}
	if got.IsOutOfBalance {
		t.Fatal("routes remained out of balance")
	}
	if got.Routes[0].TotalDistanceMeters == 111 || got.Routes[1].TotalDistanceMeters == 222 {
		t.Fatalf("dirty route metrics were not refreshed: %#v", got.Routes)
	}
}

func TestApplyMovesResolvesDuplicateParticipantMovesSequentially(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	created := store.Create(testInput())

	got, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{
		{ParticipantID: 10, FromRouteIndex: 0, ToRouteIndex: 1, InsertAtPosition: -1},
		{ParticipantID: 10, FromRouteIndex: 0, ToRouteIndex: 0, InsertAtPosition: -1},
	}, routesession.ApplyMovesOptions{})
	if err != nil {
		t.Fatalf("ApplyMoves: %v", err)
	}
	if len(got.Routes[0].Stops) != 1 || len(got.Routes[1].Stops) != 0 || got.IsEditing {
		t.Fatalf("duplicate participant moves were not sequential: %#v", got)
	}
}

func TestSwapDriversRejectsCapacityAndRollsBackDistanceFailure(t *testing.T) {
	capacityStore := routesession.NewStore(calculator{})
	t.Cleanup(capacityStore.Close)
	capacityRoutes := testRoutes()
	capacityRoutes[0].Stops = append(capacityRoutes[0].Stops, models.RouteStop{Participant: &models.Participant{ID: 11}})
	capacityRoutes[1].EffectiveCapacity = 1
	created := capacityStore.Create(routesession.CreateInput{Routes: capacityRoutes, ActivityLocation: &models.ActivityLocation{}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	if _, err := capacityStore.SwapDrivers(context.Background(), created.ID, 0, 1); !errors.Is(err, routesession.ErrSwapCapacity) {
		t.Fatalf("SwapDrivers error = %v, want ErrSwapCapacity", err)
	}

	distanceFailure := errors.New("distance failed")
	failureStore := routesession.NewStore(failingCalculator{err: distanceFailure})
	t.Cleanup(failureStore.Close)
	created = failureStore.Create(testInput())
	if _, err := failureStore.SwapDrivers(context.Background(), created.ID, 0, 1); !errors.Is(err, distanceFailure) {
		t.Fatalf("SwapDrivers error = %v, want distance failure", err)
	}
	got, _ := failureStore.Snapshot(created.ID)
	if got.Routes[0].Driver.ID != 1 || got.Routes[1].Driver.ID != 2 || got.IsEditing {
		t.Fatalf("failed swap was not rolled back: %#v", got)
	}
}

func TestSwapResetAndAddDriverOperateThroughSnapshots(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	drivers := []models.Driver{{ID: 1, VehicleCapacity: 2}, {ID: 2, VehicleCapacity: 2}, {ID: 3, VehicleCapacity: 1}}
	created := store.Create(routesession.CreateInput{
		Routes: testRoutes(), SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{},
		RouteTime: "18:30", Mode: models.RouteModeDropoff,
		DriverOrgVehicles: map[int64]*models.OrganizationVehicle{3: {ID: 30, Name: "Van", Capacity: 5}},
	})

	swapped, err := store.SwapDrivers(context.Background(), created.ID, 0, 1)
	if err != nil || swapped.Routes[0].Driver.ID != 2 || !swapped.IsEditing {
		t.Fatalf("SwapDrivers = %#v, %v", swapped, err)
	}
	added, err := store.AddDriver(context.Background(), created.ID, 3)
	if err != nil {
		t.Fatalf("AddDriver: %v", err)
	}
	last := added.Routes[len(added.Routes)-1]
	if last.OrgVehicleID != 30 || last.EffectiveCapacity != 5 {
		t.Fatalf("added route = %#v", last)
	}
	if len(added.UnusedDrivers) != 0 {
		t.Fatalf("driver rendered on an empty route is still unused: %#v", added.UnusedDrivers)
	}
	reset, err := store.Reset(created.ID)
	if err != nil || reset.IsEditing || len(reset.Routes) != 2 {
		t.Fatalf("Reset = %#v, %v", reset, err)
	}
}

func TestSaveSnapshotRejectsUnbalancedAndReturnsIndependentPayload(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	routes := testRoutes()
	routes[0].EffectiveCapacity = 0
	routes[0].Driver.VehicleCapacity = 0
	created := store.Create(routesession.CreateInput{Routes: routes, ActivityLocation: &models.ActivityLocation{}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	if _, err := store.SaveSnapshot(created.ID); !errors.Is(err, routesession.ErrUnbalanced) {
		t.Fatalf("SaveSnapshot error = %v, want ErrUnbalanced", err)
	}

	balanced := store.Create(testInput())
	payload, err := store.SaveSnapshot(balanced.ID)
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	payload.Routes[0].Driver.ID = 999
	got, _ := store.Snapshot(balanced.ID)
	if got.Routes[0].Driver.ID != 1 {
		t.Fatal("saved payload aliases stored routes")
	}
}

func TestStoreSupportsConcurrentSnapshotsAndResets(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	created := store.Create(testInput())
	var wait sync.WaitGroup
	for range 50 {
		wait.Go(func() { _, _ = store.Snapshot(created.ID) })
		wait.Go(func() { _, _ = store.Reset(created.ID) })
	}
	wait.Wait()
	if _, ok := store.Snapshot(created.ID); !ok {
		t.Fatal("session disappeared during concurrent access")
	}
}

func testRoutes() []models.CalculatedRoute {
	return []models.CalculatedRoute{
		{Driver: &models.Driver{ID: 1, VehicleCapacity: 2}, EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10, Lat: 1}}}},
		{Driver: &models.Driver{ID: 2, VehicleCapacity: 2}, EffectiveCapacity: 2, Stops: []models.RouteStop{}},
	}
}

func testInput() routesession.CreateInput {
	return routesession.CreateInput{
		Routes: testRoutes(), ActivityLocation: &models.ActivityLocation{},
		RouteTime: "18:30", Mode: models.RouteModeDropoff,
	}
}
