package routing_test

import (
	"context"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"ride-home-router/internal/routing/planeval"
	"testing"
	"time"
)

// With seats barely covering riders and many household pairs, the bearing
// sweep used to give up and fall back to a slow, poor round-robin (about 85 s
// and three times the normal driving). The seed must now complete quickly
// with a plan of ordinary quality.
func TestTightSeatsSeedCompletesQuicklyWithAnOrdinaryPlan(t *testing.T) {
	discardRoutingLogs(t)
	var scenario planeval.Scenario
	for _, s := range planeval.Scenarios() {
		if s.Name == "tight-seats-500" {
			scenario = s
		}
	}
	if scenario.Name == "" {
		t.Fatal("tight-seats-500 scenario missing from the evaluation suite")
	}
	for _, seed := range []uint64{1, 3} { // the seeds that used to time out
		for _, mode := range []models.RouteMode{models.RouteModeDropoff, models.RouteModePickup} {
			roster := planeval.Generate(scenario, seed)
			// Under a second normally; the race detector slows the solve about
			// tenfold, so the limit is generous while still far below the old 85 s.
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			start := time.Now()
			plan, err := routing.NewBalancedRouter(distance.NewEstimator()).CalculateRoutes(ctx, &routing.RoutingRequest{
				InstituteCoords: roster.Venue, Participants: roster.Participants, Drivers: roster.Drivers, Mode: routing.RouteMode(mode),
			})
			cancel()
			if err != nil {
				t.Fatalf("seed %d %s: %v after %v", seed, mode, err, time.Since(start).Round(time.Millisecond))
			}
			m := planeval.Measure(plan, roster.Venue, time.Since(start).Milliseconds())
			if m.TotalDistanceKm > 5000 || m.MaxDetourMin > 90 || m.LongestRiderMin > 100 || m.FarDrivers > 20 || m.SplitHouseholds != 0 || m.CarsUsed != len(roster.Drivers) {
				t.Fatalf("seed %d %s: plan is not of ordinary quality: %+v (drivers %d)", seed, mode, m, len(roster.Drivers))
			}
		}
	}
}
