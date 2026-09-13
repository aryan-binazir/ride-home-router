package planeval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	// About 10% of riders share a home, averaged over seeds to smooth chance.
	shared := 0
	for seed := uint64(1); seed <= 10; seed++ {
		households := map[string]int{}
		for _, p := range Generate(scenario, seed).Participants {
			households[p.Address]++
		}
		for _, n := range households {
			if n > 1 {
				shared += n
			}
		}
	}
	if shared < 150 || shared > 250 {
		t.Fatalf("riders in shared households over 10 seeds = %d, want about 200", shared)
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

func TestBurdenAndHomePassMetrics(t *testing.T) {
	venue := models.Coordinates{Lat: 0, Lng: 0}
	rider := func(id int64, lat, lng float64) *models.Participant {
		return &models.Participant{ID: id, Address: fmt.Sprintf("%d Any St", id), Lat: lat, Lng: lng}
	}
	// Car 1: driver lives at 0.05N; the route goes venue -> 0.05N (within 2 km
	// of home) -> 0.20N (17 km beyond home): a home-pass excursion. Detour
	// 20 min for 2 riders = 10 min/rider.
	// Car 2: driver at 0.30E, riders at 0.10E, 0.20E and 0.21E on the way: no pass.
	// Detour 6 min for 3 riders = 2 min/rider.
	result := &models.RoutingResult{Mode: models.RouteModeDropoff, Routes: []models.CalculatedRoute{
		{Driver: &models.Driver{ID: 1, Lat: 0.05, Lng: 0}, DetourSecs: 1200, Stops: []models.RouteStop{{Participant: rider(1, 0.05, 0.001)}, {Participant: rider(2, 0.20, 0)}}},
		{Driver: &models.Driver{ID: 2, Lat: 0, Lng: 0.30}, DetourSecs: 360, Stops: []models.RouteStop{{Participant: rider(3, 0, 0.10)}, {Participant: rider(4, 0, 0.20)}, {Participant: rider(5, 0, 0.21)}}},
	}}
	m := Measure(result, venue, 0)
	if m.HomePassCars != 1 || m.BurdenMaxMin != 10 || m.BurdenP95Min != 10 {
		t.Fatalf("metrics = %+v, want 1 home-pass car and 10 min/rider burden", m)
	}
	// Pickup order is reversed into venue-to-riders order before the test, and
	// this fixture only passes when it is. Home 0.10E; pickup order: near rider
	// at home, then a far rider at 0.30E offset 0.06N.
	//   Canonical (reversed) venue -> far -> near: the venue->far leg runs about
	//   6.7 km north of home (miss); the far->near leg ends at home with nothing
	//   far still ahead: no pass.
	//   Raw pickup order venue -> near -> far: the first leg reaches home with
	//   the far rider (22 km away) ahead: a pass.
	pickup := &models.RoutingResult{Mode: models.RouteModePickup, Routes: []models.CalculatedRoute{
		{Driver: &models.Driver{ID: 1, Lat: 0, Lng: 0.10}, DetourSecs: 600, Stops: []models.RouteStop{{Participant: rider(1, 0.001, 0.10)}, {Participant: rider(2, 0.06, 0.30)}}},
	}}
	if got := Measure(pickup, venue, 0).HomePassCars; got != 0 {
		t.Fatalf("pickup home-pass cars = %d, want 0: pickup order must be reversed before the test", got)
	}
	asDropoff := &models.RoutingResult{Mode: models.RouteModeDropoff, Routes: pickup.Routes}
	if got := Measure(asDropoff, venue, 0).HomePassCars; got != 1 {
		t.Fatalf("same stops read as dropoff = %d home-pass cars, want 1", got)
	}
	base := []Result{{Key: "drivers-in-durham-100/dropoff/1", Scenario: "drivers-in-durham-100", Riders: 100, Drivers: 28, Metrics: Metrics{CarsUsed: 28, BurdenP95Min: 20, BurdenMaxMin: 30, HomePassCars: 5}}}
	worse := []Result{{Key: "drivers-in-durham-100/dropoff/1", Scenario: "drivers-in-durham-100", Riders: 100, Drivers: 28, Metrics: Metrics{CarsUsed: 28, BurdenP95Min: 21.5, BurdenMaxMin: 32.5, HomePassCars: 6}}}
	joined := strings.Join(Compare(base, worse), "\n")
	for _, want := range []string{"burden p95 20.00 -> 21.50", "burden max 30.00 -> 32.50", "home-pass cars 5 -> 6"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	other := []Result{{Key: "vans-300/dropoff/1", Scenario: "vans-300", Riders: 300, Drivers: 60, Metrics: Metrics{CarsUsed: 60, HomePassCars: 5}}}
	okOther := []Result{{Key: "vans-300/dropoff/1", Scenario: "vans-300", Riders: 300, Drivers: 60, Metrics: Metrics{CarsUsed: 60, HomePassCars: 6}}}
	if got := Compare(other, okOther); len(got) != 0 {
		t.Fatalf("one more home-pass car outside the Durham shapes should pass: %v", got)
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

func TestReportListsWorstCarsInPlainLanguage(t *testing.T) {
	scenario := Scenario{Name: "tiny", Riders: 6, SeatFactor: 1.5, RiderMix: Mix{Carrboro: 1}, DriverMix: Mix{Durham: 1}}
	results, err := Run(context.Background(), func() routing.Router { return routing.NewBalancedRouter(distance.NewEstimator()) }, []Scenario{scenario}, 1)
	if err != nil {
		t.Fatal(err)
	}
	report := Report(results, results)
	for _, want := range []string{"## Compared with the baseline", "tiny/dropoff/1", "lives in Durham", "Carrboro"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestCompareCoversCountsRostersAndTimeouts(t *testing.T) {
	base := []Result{{Key: "b/pickup/1", Scenario: "b", Riders: 100, Drivers: 27, Metrics: Metrics{TotalDistanceKm: 500, FarDrivers: 20, BacktrackingCars: 3, CarsUsed: 27}}}
	ok := []Result{{Key: "b/pickup/1", Scenario: "b", Riders: 100, Drivers: 27, Metrics: Metrics{TotalDistanceKm: 500, FarDrivers: 22, BacktrackingCars: 4, CarsUsed: 27}}}
	if got := Compare(base, ok); len(got) != 0 {
		t.Fatalf("10%% more far drivers and one more backtracking car should pass: %v", got)
	}
	bad := []Result{{Key: "b/pickup/1", Scenario: "b", Riders: 100, Drivers: 27, Metrics: Metrics{TotalDistanceKm: 500, FarDrivers: 23, BacktrackingCars: 5, CarsUsed: 26}}}
	joined := strings.Join(Compare(base, bad), "\n")
	for _, want := range []string{"far drivers 20 -> 23", "backtracking cars 3 -> 5", "cars used 27 -> 26"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	roster := []Result{{Key: "b/pickup/1", Riders: 101, Drivers: 27, Metrics: Metrics{TotalDistanceKm: 500, CarsUsed: 27}}}
	if got := Compare(base, roster); len(got) != 1 || !strings.Contains(got[0], "roster changed") {
		t.Fatalf("generator change = %v", got)
	}
	timedOut := []Result{{Key: "b/pickup/1", Riders: 100, Drivers: 27, Metrics: Metrics{TimedOut: true}}}
	if got := Compare(base, timedOut); len(got) != 1 || !strings.Contains(got[0], "timed out") {
		t.Fatalf("timeout = %v", got)
	}
	// Planning a run the baseline could not is never a regression.
	slowBase := []Result{{Key: "b/pickup/1", Riders: 100, Drivers: 27, Metrics: Metrics{TimedOut: true}}}
	if got := Compare(slowBase, bad); len(got) != 0 {
		t.Fatalf("improvement over a timeout flagged: %v", got)
	}
	if !strings.Contains(Table(timedOut), "TIMEOUT") {
		t.Fatal("table does not mark the timed-out run")
	}
	summary := Summary(base, ok)
	if !strings.Contains(summary, "b ") || !strings.Contains(summary, "20->22") || !strings.Contains(summary, "3->4") {
		t.Fatalf("summary missing the scenario row with far 20->22 and back 3->4:\n%s", summary)
	}
	// A timeout on either side leaves the km totals to the planned runs only.
	mixed := Summary(slowBase, bad)
	if !strings.Contains(mixed, "+0.00%") || !strings.Contains(mixed, "1->0") {
		t.Fatalf("summary with a baseline timeout should show no km change and 1->0 timeouts:\n%s", mixed)
	}
}

func TestCompareRefusesDroppedOrDuplicateRuns(t *testing.T) {
	base := []Result{{Key: "a/dropoff/1"}, {Key: "a/pickup/1"}}
	if got := Compare(base, nil); len(got) != 1 || !strings.Contains(got[0], "no runs") {
		t.Fatalf("empty run = %v", got)
	}
	dropped := Compare(base, []Result{{Key: "a/dropoff/1"}})
	if len(dropped) != 1 || !strings.Contains(dropped[0], "missing") {
		t.Fatalf("dropped seed = %v", dropped)
	}
	duplicate := Compare(base, []Result{{Key: "a/dropoff/1"}, {Key: "a/dropoff/1"}, {Key: "a/pickup/1"}})
	if len(duplicate) != 1 || !strings.Contains(duplicate[0], "duplicate") {
		t.Fatalf("duplicate run = %v", duplicate)
	}
	corrupt := Compare([]Result{{Key: "a/dropoff/1"}, {Key: "a/dropoff/1"}}, []Result{{Key: "a/dropoff/1"}})
	if len(corrupt) != 1 || !strings.Contains(corrupt[0], "duplicate baseline") {
		t.Fatalf("duplicate baseline = %v", corrupt)
	}
}

func TestRunRejectsPlansThatLoseOrOverloadRiders(t *testing.T) {
	scenario := Scenario{Name: "tiny", Riders: 4, SeatFactor: 1.5, RiderMix: Mix{Durham: 1}}
	lossy := func() routing.Router {
		return routerFunc(func(req *routing.RoutingRequest) *models.RoutingResult {
			// Drops the last rider and reports a cheap plan.
			stops := []models.RouteStop{}
			for i := range req.Participants[:len(req.Participants)-1] {
				stops = append(stops, models.RouteStop{Participant: &req.Participants[i]})
			}
			return &models.RoutingResult{Routes: []models.CalculatedRoute{{Driver: &req.Drivers[0], EffectiveCapacity: 9, Stops: stops}}}
		})
	}
	if _, err := Run(context.Background(), lossy, []Scenario{scenario}, 1); err == nil || !strings.Contains(err.Error(), "assigned 0 times") {
		t.Fatalf("lost rider not rejected: %v", err)
	}
	overloaded := func() routing.Router {
		return routerFunc(func(req *routing.RoutingRequest) *models.RoutingResult {
			stops := []models.RouteStop{}
			for i := range req.Participants {
				stops = append(stops, models.RouteStop{Participant: &req.Participants[i]})
			}
			return &models.RoutingResult{Routes: []models.CalculatedRoute{{Driver: &req.Drivers[0], EffectiveCapacity: 1, Stops: stops}}}
		})
	}
	if _, err := Run(context.Background(), overloaded, []Scenario{scenario}, 1); err == nil || !strings.Contains(err.Error(), "seats") {
		t.Fatalf("overloaded car not rejected: %v", err)
	}
}

type routerFunc func(*routing.RoutingRequest) *models.RoutingResult

func (f routerFunc) CalculateRoutes(_ context.Context, req *routing.RoutingRequest) (*models.RoutingResult, error) {
	return f(req), nil
}

// TestPlannerEvaluation is the gate: every scenario, seed and mode against the
// committed baseline. Each solve is capped at the production timeout, so the
// whole suite takes a few minutes; it runs only when asked (make eval).
//
// UPDATE_PLANNER_BASELINE=1 compares and reports against the OLD baseline
// first, so the before/after numbers are on record, then writes the new one.
func TestPlannerEvaluation(t *testing.T) {
	if os.Getenv("PLANNER_EVAL") != "1" {
		t.Skip("set PLANNER_EVAL=1 (make eval) to run the planner evaluation suite")
	}
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })
	results, err := Run(context.Background(), func() routing.Router { return routing.NewBalancedRouter(distance.NewEstimator()) }, Scenarios(), 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + Table(results))
	path := filepath.Join("testdata", "baseline.json")
	var baseline []Result
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // Fixed testdata path.
		if err := json.Unmarshal(data, &baseline); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Log("\n" + Summary(baseline, results))
	if out := os.Getenv("PLANNER_EVAL_REPORT"); out != "" {
		if err := os.WriteFile(out, []byte(Report(baseline, results)), 0o600); err != nil { //nolint:gosec // Operator-chosen report path from the environment.
			t.Fatal(err)
		}
		t.Logf("report written to %s", out)
	}
	regressions := Compare(baseline, results)
	if os.Getenv("UPDATE_PLANNER_BASELINE") == "1" {
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if len(regressions) > 0 {
			t.Logf("runs worse than the old baseline, now accepted as the new baseline:\n%s", strings.Join(regressions, "\n"))
		}
		t.Logf("baseline rewritten (%d finding(s) against the old baseline; improvements are visible in the summary above)", len(regressions))
		return
	}
	if len(regressions) > 0 {
		t.Fatalf("planner regressed against the baseline:\n%s", strings.Join(regressions, "\n"))
	}
}
