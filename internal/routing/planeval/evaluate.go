package planeval

import (
	"context"
	"fmt"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"sort"
	"strings"
	"time"
)

// Result is one planner run: a scenario, a seed and a mode.
type Result struct {
	Key      string  `json:"key"` // scenario/mode/seed
	Scenario string  `json:"scenario"`
	Mode     string  `json:"mode"`
	Seed     uint64  `json:"seed"`
	Riders   int     `json:"riders"`
	Drivers  int     `json:"drivers"`
	Metrics  Metrics `json:"metrics"`
}

// Run plans every scenario for each seed in both modes with a fresh router.
func Run(ctx context.Context, newRouter func() routing.Router, scenarios []Scenario, seeds uint64) ([]Result, error) {
	results := make([]Result, 0, len(scenarios)*2)
	for _, scenario := range scenarios {
		for seed := uint64(1); seed <= seeds; seed++ {
			roster := Generate(scenario, seed)
			for _, mode := range []models.RouteMode{models.RouteModeDropoff, models.RouteModePickup} {
				req := &routing.RoutingRequest{InstituteCoords: roster.Venue, Participants: roster.Participants, Drivers: roster.Drivers, Mode: routing.RouteMode(mode)}
				start := time.Now()
				plan, err := newRouter().CalculateRoutes(ctx, req)
				if err != nil {
					return nil, fmt.Errorf("%s/%s/%d: %w", scenario.Name, mode, seed, err)
				}
				results = append(results, Result{
					Key: fmt.Sprintf("%s/%s/%d", scenario.Name, mode, seed), Scenario: scenario.Name, Mode: string(mode), Seed: seed,
					Riders: len(roster.Participants), Drivers: len(roster.Drivers),
					Metrics: Measure(plan, roster.Venue, time.Since(start).Milliseconds()),
				})
			}
		}
	}
	return results, nil
}

// Tolerances: a change must not make any run worse than this on any metric.
// Solve time is reported, not gated, because it depends on the machine.
const (
	distanceTolerance   = 0.01 // relative
	detourToleranceMin  = 2.0
	riderToleranceMin   = 2.0
	countTolerance      = 1 // far drivers, backtracking cars
	householdsTolerance = 0
)

// Compare lists every run where the current plan is materially worse than the
// baseline. An empty list means the change is acceptable everywhere.
func Compare(baseline, current []Result) []string {
	base := make(map[string]Metrics, len(baseline))
	for _, r := range baseline {
		base[r.Key] = r.Metrics
	}
	var regressions []string
	for _, r := range current {
		b, ok := base[r.Key]
		if !ok {
			regressions = append(regressions, fmt.Sprintf("%s: no baseline (run with UPDATE_PLANNER_BASELINE=1 after reviewing the table)", r.Key))
			continue
		}
		c := r.Metrics
		add := func(format string, args ...any) {
			regressions = append(regressions, r.Key+": "+fmt.Sprintf(format, args...))
		}
		if c.TotalDistanceKm > b.TotalDistanceKm*(1+distanceTolerance)+0.05 {
			add("total distance %.1f -> %.1f km", b.TotalDistanceKm, c.TotalDistanceKm)
		}
		if c.MaxDetourMin > b.MaxDetourMin+detourToleranceMin {
			add("max detour %.1f -> %.1f min", b.MaxDetourMin, c.MaxDetourMin)
		}
		if c.LongestRiderMin > b.LongestRiderMin+riderToleranceMin {
			add("longest rider %.1f -> %.1f min", b.LongestRiderMin, c.LongestRiderMin)
		}
		if c.FarDrivers > b.FarDrivers+countTolerance {
			add("far drivers %d -> %d", b.FarDrivers, c.FarDrivers)
		}
		if c.BacktrackingCars > b.BacktrackingCars+countTolerance {
			add("backtracking cars %d -> %d", b.BacktrackingCars, c.BacktrackingCars)
		}
		if c.SplitHouseholds > householdsTolerance {
			add("split households %d", c.SplitHouseholds)
		}
		if c.CarsUsed != b.CarsUsed {
			add("cars used %d -> %d", b.CarsUsed, c.CarsUsed)
		}
	}
	return regressions
}

// Table renders the results for a human: one line per run.
func Table(results []Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-32s %-7s %4s %5s %5s %9s %8s %8s %8s %4s %4s %4s %7s\n", "scenario", "mode", "seed", "rider", "drvr", "km", "maxdet", "avgdet", "rider", "far", "back", "hh", "ms")
	for _, r := range results {
		m := r.Metrics
		fmt.Fprintf(&b, "%-32s %-7s %4d %5d %5d %9.1f %8.1f %8.1f %8.1f %4d %4d %4d %7d\n", r.Scenario, r.Mode, r.Seed, r.Riders, r.Drivers, m.TotalDistanceKm, m.MaxDetourMin, m.AverageDetourMin, m.LongestRiderMin, m.FarDrivers, m.BacktrackingCars, m.SplitHouseholds, m.SolveMs)
	}
	return b.String()
}

// Summary compares current results with the baseline per scenario, so an
// improvement is visible as numbers rather than an absence of failures.
func Summary(baseline, current []Result) string {
	base := make(map[string]Metrics, len(baseline))
	for _, r := range baseline {
		base[r.Key] = r.Metrics
	}
	type agg struct {
		baseKm, curKm           float64
		baseFar, curFar         int
		baseBack, curBack       int
		runs                    int
		maxSolve                int64
		baseLongest, curLongest float64
		baseMaxDet, curMaxDet   float64
	}
	byScenario := map[string]*agg{}
	var order []string
	for _, r := range current {
		a := byScenario[r.Scenario]
		if a == nil {
			a = &agg{}
			byScenario[r.Scenario] = a
			order = append(order, r.Scenario)
		}
		b, ok := base[r.Key]
		if !ok {
			continue
		}
		a.runs++
		a.baseKm += b.TotalDistanceKm
		a.curKm += r.Metrics.TotalDistanceKm
		a.baseFar += b.FarDrivers
		a.curFar += r.Metrics.FarDrivers
		a.baseBack += b.BacktrackingCars
		a.curBack += r.Metrics.BacktrackingCars
		a.baseLongest = maxf(a.baseLongest, b.LongestRiderMin)
		a.curLongest = maxf(a.curLongest, r.Metrics.LongestRiderMin)
		a.baseMaxDet = maxf(a.baseMaxDet, b.MaxDetourMin)
		a.curMaxDet = maxf(a.curMaxDet, r.Metrics.MaxDetourMin)
		if r.Metrics.SolveMs > a.maxSolve {
			a.maxSolve = r.Metrics.SolveMs
		}
	}
	sort.Strings(order)
	var out strings.Builder
	fmt.Fprintf(&out, "%-32s %6s %10s %8s %8s %9s %9s %8s\n", "scenario", "runs", "km change", "far", "back", "maxdet", "rider", "max ms")
	for _, name := range order {
		a := byScenario[name]
		change := 0.0
		if a.baseKm > 0 {
			change = (a.curKm - a.baseKm) / a.baseKm * 100
		}
		fmt.Fprintf(&out, "%-32s %6d %+9.2f%% %3d->%-3d %3d->%-3d %4.0f->%-4.0f %4.0f->%-4.0f %8d\n", name, a.runs, change, a.baseFar, a.curFar, a.baseBack, a.curBack, a.baseMaxDet, a.curMaxDet, a.baseLongest, a.curLongest, a.maxSolve)
	}
	return out.String()
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
