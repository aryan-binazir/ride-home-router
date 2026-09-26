package routing

import (
	"context"
	"fmt"
	"math/rand/v2"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"slices"
	"testing"
)

func TestStopOrderingWithRelocationsUsuallyFindsBetterOrders(t *testing.T) {
	rng := rand.New(rand.NewPCG(2026, 913)) //nolint:gosec // Seeded test data.
	institute := models.Coordinates{Lat: 0, Lng: 0}
	better, worse := 0, 0
	for range 300 {
		driver := &models.Driver{ID: 1, Name: "D", Lat: rng.Float64()*0.3 - 0.15, Lng: rng.Float64()*0.3 - 0.15, VehicleCapacity: 7}
		n := 4 + rng.IntN(3)
		riders := make([]*models.Participant, 0, n)
		for i := range n {
			riders = append(riders, &models.Participant{ID: int64(10 + i), Name: fmt.Sprintf("R%d", i), Address: fmt.Sprintf("%d Random Rd", i), Lat: rng.Float64()*0.3 - 0.15, Lng: rng.Float64()*0.3 - 0.15})
		}
		req := &RoutingRequest{InstituteCoords: institute, Drivers: []models.Driver{*driver}, Mode: "pickup"}
		for _, r := range riders {
			req.Participants = append(req.Participants, *r)
		}
		lookup, err := prepareSolveDistances(context.Background(), distance.NewEstimator(), req)
		if err != nil {
			t.Fatal(err)
		}
		rc := newRouteContext(lookup, institute, RouteModePickup)
		rc.prepareParticipants(riders)
		ids := []int64{driver.ID}
		fresh := func() map[int64]*balancedRoute {
			return map[int64]*balancedRoute{driver.ID: {driver: driver, stops: slices.Clone(riders)}}
		}
		reversalsOnly, err := rc.optimizeRouteOrdersWith(context.Background(), fresh(), ids, false)
		if err != nil {
			t.Fatal(err)
		}
		withRelocations, err := rc.optimizeRouteOrdersWith(context.Background(), fresh(), ids, true)
		if err != nil {
			t.Fatal(err)
		}
		if withRelocations.betterThan(reversalsOnly) {
			better++
		} else if reversalsOnly.betterThan(withRelocations) {
			worse++
		}
	}
	if better < 10 || better <= worse {
		t.Fatalf("relocations found a better order in %d and a worse one in %d of 300 random pickup routes", better, worse)
	}
}
