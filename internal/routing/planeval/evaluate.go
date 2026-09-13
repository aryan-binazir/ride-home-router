package planeval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"sort"
	"strings"
	"time"
)

// SolveTimeout mirrors the server's calculation timeout (routeSolveTimeout in
// internal/handlers): a plan that takes longer never reaches a user.
const SolveTimeout = 30 * time.Second

// Result is one planner run: a scenario, a seed and a mode.
type Result struct {
	Key      string  `json:"key"` // scenario/mode/seed
	Scenario string  `json:"scenario"`
	Mode     string  `json:"mode"`
	Seed     uint64  `json:"seed"`
	Riders   int     `json:"riders"`
	Drivers  int     `json:"drivers"`
	Metrics  Metrics `json:"metrics"`
	// WorstCars helps a reviewer judge a plan; it is not part of the baseline.
	WorstCars []CarNote `json:"-"`
}

// CarNote is one car described the way a coordinator would read it.
type CarNote struct {
	Driver       string
	DriverRegion Region
	RiderRegions string
	Riders       int
	DistanceKm   float64
	DetourMin    float64
}

// Run plans every scenario for each seed in both modes with a fresh router,
// each solve under the production timeout. A solve that times out is recorded
// as such rather than failing the run, so the baseline can say so honestly.
func Run(ctx context.Context, newRouter func() routing.Router, scenarios []Scenario, seeds uint64) ([]Result, error) {
	results := make([]Result, 0, len(scenarios)*int(seeds)*2) //nolint:gosec // Small counts.
	for _, scenario := range scenarios {
		for seed := uint64(1); seed <= seeds; seed++ {
			roster := Generate(scenario, seed)
			for _, mode := range []models.RouteMode{models.RouteModeDropoff, models.RouteModePickup} {
				result := Result{
					Key: fmt.Sprintf("%s/%s/%d", scenario.Name, mode, seed), Scenario: scenario.Name, Mode: string(mode), Seed: seed,
					Riders: len(roster.Participants), Drivers: len(roster.Drivers),
				}
				req := &routing.RoutingRequest{InstituteCoords: roster.Venue, Participants: roster.Participants, Drivers: roster.Drivers, Mode: routing.RouteMode(mode)}
				solveCtx, cancel := context.WithTimeout(ctx, SolveTimeout)
				start := time.Now()
				plan, err := newRouter().CalculateRoutes(solveCtx, req)
				cancel()
				switch {
				case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
					result.Metrics = Metrics{TimedOut: true, SolveMs: time.Since(start).Milliseconds()}
				case err != nil:
					return nil, fmt.Errorf("%s: %w", result.Key, err)
				default:
					if err := validPlan(plan, roster); err != nil {
						return nil, fmt.Errorf("%s: %w", result.Key, err)
					}
					result.Metrics = Measure(plan, roster.Venue, time.Since(start).Milliseconds())
					result.WorstCars = worstCars(plan, 3)
				}
				results = append(results, result)
			}
		}
	}
	return results, nil
}

// worstCars lists the n cars with the largest detour, described plainly.
func worstCars(plan *models.RoutingResult, n int) []CarNote {
	notes := make([]CarNote, 0, len(plan.Routes))
	for _, route := range plan.Routes {
		if len(route.Stops) == 0 || route.Driver == nil {
			continue
		}
		regions := map[Region]int{}
		for _, stop := range route.Stops {
			if stop.Participant != nil {
				regions[RegionOf(stop.Participant.GetCoords())]++
			}
		}
		var parts []string
		for _, r := range []Region{Durham, ChapelHill, Carrboro, Raleigh} {
			if regions[r] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", r, regions[r]))
			}
		}
		notes = append(notes, CarNote{Driver: route.Driver.Name, DriverRegion: RegionOf(route.Driver.GetCoords()), RiderRegions: strings.Join(parts, ", "), Riders: len(route.Stops), DistanceKm: math.Round(route.TotalDistanceMeters/100) / 10, DetourMin: math.Round(route.DetourSecs/6) / 10})
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].DetourMin > notes[j].DetourMin })
	if len(notes) > n {
		notes = notes[:n]
	}
	return notes
}

// Report is the markdown a reviewer reads after a run: the per-scenario
// comparison, then every run's numbers and its worst cars.
func Report(baseline, current []Result) string {
	var b strings.Builder
	b.WriteString("# Planner evaluation\n\n## Compared with the baseline\n\n```\n")
	b.WriteString(Summary(baseline, current))
	b.WriteString("```\n\n## Every run\n\n```\n")
	b.WriteString(Table(current))
	b.WriteString("```\n\n## Worst cars per run (largest detour)\n\n")
	for _, r := range current {
		fmt.Fprintf(&b, "### %s\n\n", r.Key)
		for _, c := range r.WorstCars {
			fmt.Fprintf(&b, "- %s (lives in %s): %d riders from %s; %.1f km, detour %.1f min\n", c.Driver, c.DriverRegion, c.Riders, c.RiderRegions, c.DistanceKm, c.DetourMin)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// validPlan rejects a plan that looks cheap because it is wrong: every rider
// exactly once, and no car over its capacity.
func validPlan(plan *models.RoutingResult, roster Roster) error {
	seen := make(map[int64]int, len(roster.Participants))
	for _, route := range plan.Routes {
		if len(route.Stops) > route.EffectiveCapacity && route.EffectiveCapacity > 0 {
			return fmt.Errorf("car %d carries %d riders with %d seats", route.Driver.ID, len(route.Stops), route.EffectiveCapacity)
		}
		for _, stop := range route.Stops {
			if stop.Participant != nil {
				seen[stop.Participant.ID]++
			}
		}
	}
	for _, p := range roster.Participants {
		if seen[p.ID] != 1 {
			return fmt.Errorf("rider %d assigned %d times", p.ID, seen[p.ID])
		}
	}
	if len(seen) != len(roster.Participants) {
		return fmt.Errorf("plan carries %d riders, roster has %d", len(seen), len(roster.Participants))
	}
	return nil
}

// Tolerances: a change must not make any run worse than this on any metric.
// Solve time is reported, not gated, because it depends on the machine; a
// timeout is gated because production would show the user an error.
const (
	distanceTolerance  = 0.01 // relative
	detourToleranceMin = 2.0
	riderToleranceMin  = 2.0
	countTolerance     = 0.10 // relative, at least one: far drivers, backtracking cars
)

func countAllowance(base int) int {
	return max(1, int(math.Ceil(float64(base)*countTolerance)))
}

func indexByKey(results []Result) map[string]Result {
	byKey := make(map[string]Result, len(results))
	for _, r := range results {
		byKey[r.Key] = r
	}
	return byKey
}

// Compare lists every run where the current plan is materially worse than the
// baseline. An empty list means the change is acceptable everywhere.
func Compare(baseline, current []Result) []string {
	if len(current) == 0 {
		return []string{"no runs to compare"}
	}
	base := indexByKey(baseline)
	var regressions []string
	seen := make(map[string]bool, len(current))
	for _, r := range current {
		if seen[r.Key] {
			regressions = append(regressions, r.Key+": duplicate run")
		}
		seen[r.Key] = true
	}
	for _, r := range baseline {
		if !seen[r.Key] {
			regressions = append(regressions, r.Key+": missing from this run (a scenario or seed was dropped)")
		}
	}
	for _, r := range current {
		baseResult, ok := base[r.Key]
		if !ok {
			regressions = append(regressions, fmt.Sprintf("%s: no baseline (run with UPDATE_PLANNER_BASELINE=1 after reviewing the table)", r.Key))
			continue
		}
		add := func(format string, args ...any) {
			regressions = append(regressions, r.Key+": "+fmt.Sprintf(format, args...))
		}
		if r.Riders != baseResult.Riders || r.Drivers != baseResult.Drivers {
			add("roster changed (%d/%d riders/drivers, baseline %d/%d): the generator changed, regenerate the baseline deliberately", r.Riders, r.Drivers, baseResult.Riders, baseResult.Drivers)
			continue
		}
		b, c := baseResult.Metrics, r.Metrics
		if c.TimedOut && !b.TimedOut {
			add("timed out after %s; the baseline planned it", SolveTimeout)
			continue
		}
		if c.TimedOut {
			continue
		}
		if b.TimedOut {
			continue // planned where the baseline could not: an improvement
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
		if c.FarDrivers > b.FarDrivers+countAllowance(b.FarDrivers) {
			add("far drivers %d -> %d", b.FarDrivers, c.FarDrivers)
		}
		if c.BacktrackingCars > b.BacktrackingCars+countAllowance(b.BacktrackingCars) {
			add("backtracking cars %d -> %d", b.BacktrackingCars, c.BacktrackingCars)
		}
		if c.SplitHouseholds > 0 {
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
		if m.TimedOut {
			fmt.Fprintf(&b, "%-32s %-7s %4d %5d %5d %9s %8s %8s %8s %4s %4s %4s %7d\n", r.Scenario, r.Mode, r.Seed, r.Riders, r.Drivers, "TIMEOUT", "", "", "", "", "", "", m.SolveMs)
			continue
		}
		fmt.Fprintf(&b, "%-32s %-7s %4d %5d %5d %9.1f %8.1f %8.1f %8.1f %4d %4d %4d %7d\n", r.Scenario, r.Mode, r.Seed, r.Riders, r.Drivers, m.TotalDistanceKm, m.MaxDetourMin, m.AverageDetourMin, m.LongestRiderMin, m.FarDrivers, m.BacktrackingCars, m.SplitHouseholds, m.SolveMs)
	}
	return b.String()
}

// Summary compares current results with the baseline per scenario, so an
// improvement is visible as numbers rather than an absence of failures.
func Summary(baseline, current []Result) string {
	base := indexByKey(baseline)
	type agg struct {
		baseKm, curKm             float64
		baseFar, curFar           int
		baseBack, curBack         int
		baseTimeouts, curTimeouts int
		runs                      int
		maxSolve                  int64
		baseLongest, curLongest   float64
		baseMaxDet, curMaxDet     float64
	}
	byScenario := map[string]*agg{}
	for _, r := range current {
		a := byScenario[r.Scenario]
		if a == nil {
			a = &agg{}
			byScenario[r.Scenario] = a
		}
		bl, ok := base[r.Key]
		if !ok {
			continue
		}
		b, c := bl.Metrics, r.Metrics
		a.runs++
		if b.TimedOut {
			a.baseTimeouts++
		}
		if c.TimedOut {
			a.curTimeouts++
		}
		a.baseKm += b.TotalDistanceKm
		a.curKm += c.TotalDistanceKm
		a.baseFar += b.FarDrivers
		a.curFar += c.FarDrivers
		a.baseBack += b.BacktrackingCars
		a.curBack += c.BacktrackingCars
		a.baseLongest = max(a.baseLongest, b.LongestRiderMin)
		a.curLongest = max(a.curLongest, c.LongestRiderMin)
		a.baseMaxDet = max(a.baseMaxDet, b.MaxDetourMin)
		a.curMaxDet = max(a.curMaxDet, c.MaxDetourMin)
		a.maxSolve = max(a.maxSolve, c.SolveMs)
	}
	order := make([]string, 0, len(byScenario))
	for name := range byScenario {
		order = append(order, name)
	}
	sort.Strings(order)
	var out strings.Builder
	fmt.Fprintf(&out, "%-32s %5s %10s %8s %8s %9s %9s %8s %8s\n", "scenario", "runs", "km change", "far", "back", "maxdet", "rider", "timeout", "max ms")
	for _, name := range order {
		a := byScenario[name]
		change := 0.0
		if a.baseKm > 0 {
			change = (a.curKm - a.baseKm) / a.baseKm * 100
		}
		fmt.Fprintf(&out, "%-32s %5d %+9.2f%% %3d->%-3d %3d->%-3d %4.0f->%-4.0f %4.0f->%-4.0f %3d->%-3d %8d\n", name, a.runs, change, a.baseFar, a.curFar, a.baseBack, a.curBack, a.baseMaxDet, a.curMaxDet, a.baseLongest, a.curLongest, a.baseTimeouts, a.curTimeouts, a.maxSolve)
	}
	return out.String()
}
