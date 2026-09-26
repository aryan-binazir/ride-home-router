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

const SolveTimeout = 30 * time.Second

type Result struct {
	Key       string    `json:"key"`
	Scenario  string    `json:"scenario"`
	Mode      string    `json:"mode"`
	Seed      uint64    `json:"seed"`
	Riders    int       `json:"riders"`
	Drivers   int       `json:"drivers"`
	Metrics   Metrics   `json:"metrics"`
	WorstCars []CarNote `json:"-"`
}

type CarNote struct {
	Driver       string
	DriverRegion Region
	RiderRegions string
	Riders       int
	DistanceKm   float64
	DetourMin    float64
}

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

func validPlan(plan *models.RoutingResult, roster Roster) error {
	seen := make(map[int64]int, len(roster.Participants))
	for _, route := range plan.Routes {
		if len(route.Stops) > route.EffectiveCapacity && route.EffectiveCapacity > 0 {
			return fmt.Errorf("car %d carries %d riders with %d seats", route.Driver.ID, len(route.Stops), route.EffectiveCapacity)
		}
		for _, stop := range route.Stops {
			if stop.Participant == nil {
				return fmt.Errorf("route contains a stop with missing participant data")
			}
			seen[stop.Participant.ID]++
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

const (
	distanceRelativeTolerance = 0.01
	detourToleranceMin        = 2.0
	riderToleranceMin         = 2.0
	burdenP95ToleranceMin     = 1.0
	burdenMaxToleranceMin     = 2.0
	homePassTolerance         = 1
	countRelativeTolerance    = 0.10
)

func countAllowance(base int) int {
	return max(1, int(math.Ceil(float64(base)*countRelativeTolerance)))
}

func indexByKey(results []Result) map[string]Result {
	byKey := make(map[string]Result, len(results))
	for _, r := range results {
		byKey[r.Key] = r
	}
	return byKey
}

func Compare(baseline, current []Result) []string {
	if len(current) == 0 {
		return []string{"no runs to compare"}
	}
	var regressions []string
	baseSeen := make(map[string]bool, len(baseline))
	for _, r := range baseline {
		if baseSeen[r.Key] {
			regressions = append(regressions, r.Key+": duplicate baseline run; the baseline file is corrupt")
		}
		baseSeen[r.Key] = true
	}
	base := indexByKey(baseline)
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
			continue
		}
		if c.TotalDistanceKm > b.TotalDistanceKm*(1+distanceRelativeTolerance)+0.05 {
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
		if c.BurdenP95Min > b.BurdenP95Min+burdenP95ToleranceMin {
			add("burden p95 %.2f -> %.2f min/rider", b.BurdenP95Min, c.BurdenP95Min)
		}
		if c.BurdenMaxMin > b.BurdenMaxMin+burdenMaxToleranceMin {
			add("burden max %.2f -> %.2f min/rider", b.BurdenMaxMin, c.BurdenMaxMin)
		}
		allowed := homePassTolerance
		if strings.HasPrefix(r.Scenario, "drivers-in-durham") {
			allowed = 0
		}
		if c.HomePassCars > b.HomePassCars+allowed {
			add("home-pass cars %d -> %d", b.HomePassCars, c.HomePassCars)
		}
		if c.CarsUsed != b.CarsUsed {
			add("cars used %d -> %d", b.CarsUsed, c.CarsUsed)
		}
	}
	return regressions
}

func Table(results []Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-32s %-7s %4s %5s %5s %9s %8s %8s %8s %4s %4s %4s %7s %7s %5s %7s\n", "scenario", "mode", "seed", "rider", "drvr", "km", "maxdet", "avgdet", "rider", "far", "back", "hh", "b95", "bmax", "home", "ms")
	for _, r := range results {
		m := r.Metrics
		if m.TimedOut {
			fmt.Fprintf(&b, "%-32s %-7s %4d %5d %5d %9s %8s %8s %8s %4s %4s %4s %7s %7s %5s %7d\n", r.Scenario, r.Mode, r.Seed, r.Riders, r.Drivers, "TIMEOUT", "", "", "", "", "", "", "", "", "", m.SolveMs)
			continue
		}
		fmt.Fprintf(&b, "%-32s %-7s %4d %5d %5d %9.1f %8.1f %8.1f %8.1f %4d %4d %4d %7.2f %7.2f %5d %7d\n", r.Scenario, r.Mode, r.Seed, r.Riders, r.Drivers, m.TotalDistanceKm, m.MaxDetourMin, m.AverageDetourMin, m.LongestRiderMin, m.FarDrivers, m.BacktrackingCars, m.SplitHouseholds, m.BurdenP95Min, m.BurdenMaxMin, m.HomePassCars, m.SolveMs)
	}
	return b.String()
}

func Summary(baseline, current []Result) string {
	base := indexByKey(baseline)
	type agg struct {
		baseKm, curKm             float64
		baseFar, curFar           int
		baseBack, curBack         int
		baseTimeouts, curTimeouts int
		baseHome, curHome         int
		baseB95, curB95           float64
		baseBMax, curBMax         float64
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
		if b.TimedOut || c.TimedOut {
			continue
		}
		a.baseKm += b.TotalDistanceKm
		a.curKm += c.TotalDistanceKm
		a.baseFar += b.FarDrivers
		a.curFar += c.FarDrivers
		a.baseBack += b.BacktrackingCars
		a.curBack += c.BacktrackingCars
		a.baseHome += b.HomePassCars
		a.curHome += c.HomePassCars
		a.baseB95 = max(a.baseB95, b.BurdenP95Min)
		a.curB95 = max(a.curB95, c.BurdenP95Min)
		a.baseBMax = max(a.baseBMax, b.BurdenMaxMin)
		a.curBMax = max(a.curBMax, c.BurdenMaxMin)
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
	fmt.Fprintf(&out, "%-32s %5s %10s %8s %8s %9s %9s %8s %12s %12s %8s %8s\n", "scenario", "runs", "km change", "far", "back", "maxdet", "rider", "home", "b95 (max)", "bmax (max)", "timeout", "max ms")
	for _, name := range order {
		a := byScenario[name]
		change := 0.0
		if a.baseKm > 0 {
			change = (a.curKm - a.baseKm) / a.baseKm * 100
		}
		fmt.Fprintf(&out, "%-32s %5d %+9.2f%% %3d->%-3d %3d->%-3d %4.0f->%-4.0f %4.0f->%-4.0f %3d->%-3d %5.1f->%-5.1f %5.1f->%-5.1f %3d->%-3d %8d\n", name, a.runs, change, a.baseFar, a.curFar, a.baseBack, a.curBack, a.baseMaxDet, a.curMaxDet, a.baseLongest, a.curLongest, a.baseHome, a.curHome, a.baseB95, a.curB95, a.baseBMax, a.curBMax, a.baseTimeouts, a.curTimeouts, a.maxSolve)
	}
	return out.String()
}
