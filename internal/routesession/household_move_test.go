package routesession_test

import (
	"context"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"testing"
)

func TestApplyMovesMovesTheWholeHousehold(t *testing.T) {
	store := routesession.NewStore(calculator{})
	t.Cleanup(store.Close)
	sibling1 := &models.Participant{ID: 1, Name: "Amelia Bennett", Address: "12 Oak St", Lat: 1, Lng: 1}
	sibling2 := &models.Participant{ID: 2, Name: "Noah Bennett", Address: "12 Oak St", Lat: 1, Lng: 1}
	other := &models.Participant{ID: 3, Name: "Owen Carter", Address: "40 Elm St", Lat: 2, Lng: 2}
	created := mustCreate(t, store, routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{
				Driver: &models.Driver{ID: 1, Name: "From", Lat: 10, Lng: 0, VehicleCapacity: 4}, EffectiveCapacity: 4,
				Stops: []models.RouteStop{{Participant: sibling1}, {Participant: other}, {Participant: sibling2}},
			},
			{
				Driver: &models.Driver{ID: 2, Name: "To", Lat: 10, Lng: 0, VehicleCapacity: 4}, EffectiveCapacity: 4,
				Stops: []models.RouteStop{{Participant: &models.Participant{ID: 4, Name: "Rider Four", Address: "9 Pine St", Lat: 9, Lng: 0}}},
			},
		},
		ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 0, Lng: 0},
		RouteTime:        "18:30",
		Mode:             models.RouteModeDropoff,
	})

	updated, err := store.ApplyMoves(context.Background(), created.ID, []routesession.Move{{ParticipantID: 1, FromRouteIndex: 0, ToRouteIndex: 1, InsertAtPosition: 0}}, routesession.ApplyMovesOptions{})
	if err != nil {
		t.Fatalf("ApplyMoves() error = %v", err)
	}
	if got := stopNames(updated.Routes[0]); len(got) != 1 || got[0] != "Owen Carter" {
		t.Fatalf("source stops = %v, want only Owen Carter", got)
	}
	if got := stopNames(updated.Routes[1]); len(got) != 3 || got[0] != "Amelia Bennett" || got[1] != "Noah Bennett" || got[2] != "Rider Four" {
		t.Fatalf("destination stops = %v, want the household together at the front", got)
	}

	full := mustCreate(t, store, routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{Driver: &models.Driver{ID: 1, Name: "From", VehicleCapacity: 4}, EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: sibling1}, {Participant: sibling2}}},
			{Driver: &models.Driver{ID: 2, Name: "Tiny", VehicleCapacity: 1}, EffectiveCapacity: 1},
		},
		ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ"}, RouteTime: "18:30", Mode: models.RouteModeDropoff,
	})
	after, err := store.ApplyMoves(context.Background(), full.ID, []routesession.Move{{ParticipantID: 1, ToRouteIndex: 1, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{})
	if err != nil {
		t.Fatalf("ApplyMoves() into a smaller car error = %v", err)
	}
	if len(after.Routes[1].Stops) != 2 || !after.IsOutOfBalance {
		t.Fatalf("household move into a one-seat car: stops=%d outOfBalance=%v, want both riders moved and the plan flagged", len(after.Routes[1].Stops), after.IsOutOfBalance)
	}
}

func stopNames(route models.CalculatedRoute) []string {
	out := make([]string, len(route.Stops))
	for i, stop := range route.Stops {
		out[i] = stop.Participant.Name
	}
	return out
}
