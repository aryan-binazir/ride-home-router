package planeval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"strings"
	"testing"
)

func TestGenerateHonoursMixSeatsAndHouseholds(t *testing.T) {
	scenario := Scenario{
		Name: "carrboro-durham", Riders: 200, Households: 0.1, SeatFactor: 1.08,
		RiderMix: Mix{Carrboro: 45, Durham: 55},
	}
	roster := Generate(scenario, 1)
	if len(roster.Participants) != 200 {
		t.Fatalf("riders = %d", len(roster.Participants))
	}
	counts := map[Region]int{}
	for _, p := range roster.Participants {
		counts[RegionOf(p.GetCoords())]++
	}
	if counts[Carrboro] < 70 || counts[Carrboro] > 110 || counts[Durham] < 90 || counts[Durham] > 130 || counts[Raleigh] != 0 {
		t.Fatalf("region counts = %v, want roughly 45%% Carrboro and 55%% Durham", counts)
	}
	seats := 0
	for _, d := range roster.Drivers {
		seats += d.VehicleCapacity
	}
	if seats < 216 || seats > 216+9 {
		t.Fatalf("seats = %d for 200 riders at factor 1.08, want just over 216", seats)
	}
	households := map[string][]int64{}
	for _, p := range roster.Participants {
		households[p.Address] = append(households[p.Address], p.ID)
	}
	shared := 0
	for _, ids := range households {
		if len(ids) > 1 {
			shared += len(ids)
		}
	}
	if shared < 14 || shared > 26 {
		t.Fatalf("riders in shared households = %d, want about 20", shared)
	}
	again := Generate(scenario, 1)
	if fmt.Sprint(again.Participants[7]) != fmt.Sprint(roster.Participants[7]) {
		t.Fatal("the same seed must generate the same roster")
	}
	other := Generate(scenario, 2)
	if fmt.Sprint(other.Participants[7]) == fmt.Sprint(roster.Participants[7]) {
		t.Fatal("a different seed must generate a different roster")
	}
}

func TestGenerateDriverMixCanDifferFromRiders(t *testing.T) {
	roster := Generate(Scenario{Name: "mismatch", Riders: 100, SeatFactor: 1.1, RiderMix: Mix{Carrboro: 50, ChapelHill: 50}, DriverMix: Mix{Durham: 100}}, 3)
	for _, d := range roster.Drivers {
		if RegionOf(d.GetCoords()) != Durham {
			t.Fatalf("driver %s lives in %v, want Durham", d.Name, RegionOf(d.GetCoords()))
		}
	}
}

func TestMeasureReadsPlanQuality(t *testing.T) {
	venue := models.Coordinates{Lat: 0, Lng: 0}
	near := &models.Participant{ID: 1, Address: "1 A St", Lat: 0.02, Lng: 0}
	far := &models.Participant{ID: 2, Address: "2 B St", Lat: 0.10, Lng: 0}
	sibling := &models.Participant{ID: 3, Address: "1 A St", Lat: 0.02, Lng: 0}
	result := &models.RoutingResult{Mode: models.RouteModeDropoff, Routes: []models.CalculatedRoute{
		{ // Drives to the far stop first, then back to the near one: backtracking.
			Driver: &models.Driver{ID: 1, Lat: 0.12, Lng: 0}, TotalDistanceMeters: 30000, DetourSecs: 600, RouteDurationSecs: 2400,
			Stops: []models.RouteStop{{Participant: far, CumulativeDurationSecs: 1000}, {Participant: near, CumulativeDurationSecs: 1800}},
		},
		{ // Splits the household at 1 A St and lives far from its rider.
			Driver: &models.Driver{ID: 2, Lat: 0.5, Lng: 0.5}, TotalDistanceMeters: 80000, DetourSecs: 1200, RouteDurationSecs: 3000,
			Stops: []models.RouteStop{{Participant: sibling, CumulativeDurationSecs: 200}},
		},
		{Driver: &models.Driver{ID: 3, Lat: 0.01, Lng: 0.01}},
	}}
	m := Measure(result, venue, 1500)
	if m.TotalDistanceKm != 110 || m.CarsUsed != 2 || m.MaxDetourMin != 20 || m.LongestRiderMin != 30 || m.BacktrackingCars != 1 || m.FarDrivers != 1 || m.SplitHouseholds != 1 || m.SolveMs != 1500 {
		t.Fatalf("metrics = %+v", m)
	}
	pickup := &models.RoutingResult{Mode: models.RouteModePickup, Routes: []models.CalculatedRoute{{
		Driver: &models.Driver{ID: 1, Lat: 0.3, Lng: 0}, RouteDurationSecs: 2400,
		// Picked up at 600 s, so the rider sits in the car for 1800 s.
		Stops: []models.RouteStop{{Participant: far, CumulativeDurationSecs: 600}, {Participant: near, CumulativeDurationSecs: 1800}},
	}}}
	if got := Measure(pickup, venue, 0).LongestRiderMin; got != 30 {
		t.Fatalf("pickup longest rider = %v min, want 30", got)
	}
}

func TestCompareFlagsOnlyMaterialRegressions(t *testing.T) {
	base := []Result{{Key: "a/dropoff/1", Metrics: Metrics{TotalDistanceKm: 100, MaxDetourMin: 20, LongestRiderMin: 40, CarsUsed: 10}}}
	same := []Result{{Key: "a/dropoff/1", Metrics: Metrics{TotalDistanceKm: 100.9, MaxDetourMin: 21.5, LongestRiderMin: 41, CarsUsed: 10}}}
	if regressions := Compare(base, same); len(regressions) != 0 {
		t.Fatalf("within tolerance flagged: %v", regressions)
	}
	worse := []Result{{Key: "a/dropoff/1", Metrics: Metrics{TotalDistanceKm: 102, MaxDetourMin: 23, LongestRiderMin: 40, CarsUsed: 10, SplitHouseholds: 1}}}
	regressions := Compare(base, worse)
	joined := strings.Join(regressions, "\n")
	for _, want := range []string{"total distance", "max detour", "split households"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, regressions)
		}
	}
	if strings.Contains(joined, "longest rider") {
		t.Fatalf("unchanged metric flagged: %v", regressions)
	}
}

// TestPlannerEvaluation is the gate: every scenario, seed and mode against the
// committed baseline. It takes a couple of minutes, so it runs only when asked.
func TestPlannerEvaluation(t *testing.T) {
	if os.Getenv("PLANNER_EVAL") != "1" {
		t.Skip("set PLANNER_EVAL=1 (make eval) to run the planner evaluation suite")
	}
	results, err := Run(context.Background(), func() routing.Router { return routing.NewBalancedRouter(distance.NewEstimator()) }, Scenarios(), 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + Table(results))
	path := filepath.Join("testdata", "baseline.json")
	if os.Getenv("UPDATE_PLANNER_BASELINE") == "1" {
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path) //nolint:gosec // Fixed testdata path.
	if err != nil {
		t.Fatal(err)
	}
	var baseline []Result
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatal(err)
	}
	if regressions := Compare(baseline, results); len(regressions) > 0 {
		t.Fatalf("planner regressed against the baseline:\n%s", strings.Join(regressions, "\n"))
	}
	t.Log("\n" + Summary(baseline, results))
}
