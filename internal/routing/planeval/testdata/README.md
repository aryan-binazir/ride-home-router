# Planner evaluation baseline

`baseline.json` holds the planner's results for every evaluation scenario, seed
and mode (see `../scenario.go`), captured with `make eval` and
`UPDATE_PLANNER_BASELINE=1`. `make eval` fails when any run is materially worse
than this file (`Compare` in `../evaluate.go`). Solve times are not stored;
timeouts are.

Rules:
- Never regenerate the baseline to make a change pass. Regenerate only after a
  deliberate planner change whose before/after numbers have been reviewed; the
  update run prints them against the old baseline first.
- A roster change (generator, scenario list, seed count) shows up as
  "roster changed" or "missing" and also needs a deliberate regeneration.
- The rosters depend on `math/rand/v2` output for a fixed seed; a Go release
  that changes that would change every roster and show up the same way.

First captured 2026-09-13 at the merge of the estimate-then-measure planner
(main de65d24). Regenerated the same day after two deliberate planner changes,
each measured against the baseline just before it (numbers below are from
those intermediate runs, not from the committed 80cdf24 file):

1. The bearing sweep repairs a partial seed instead of falling back to the
   slow round-robin. `tight-seats-500` seeds 1 and 3 went from about 85 s and
   10,400 km (a timeout in production) to under a second and about 3,800 km.
   Every other row was byte-identical.
2. Pickup routes mirror dropoff: a car's latest completion is the first
   rider's time aboard and the aggregate is every rider's time aboard (pickup
   to venue), instead of the whole drive including the driver's leg from
   home; and whole-plan ordering passes also try moving one household block
   (relocations), which applies to dropoff too. Pickup, 63 runs vs the
   post-repair baseline: backtracking cars 194 → 12, summed worst rider time
   3,642 → 3,105 min, summed worst detour 3,031 → 2,743 min, far drivers
   225 → 198, driving 106,148 → 105,878 km. Dropoff, 63 runs: 42 plans
   changed within the gates (driving −0.14%, backtracking 19 → 9, worst rider
   and far drivers unchanged). Runs that got worse on something: about a
   dozen pickup runs gave up 1–6% distance (largest durham-heavy-100 seed 2,
   537 → 568 km), three gained 2–4 far drivers, and a few gained 3–10 min of
   worst detour (largest vans-500 seed 1, 56 → 67 min). The owner accepted
   these trade-offs as a policy change after reviewing them. With this
   definition the planner and the single-car edit flow agree on stop order
   in every reference fixture.

Known remaining
defect: `drivers-in-durham-*` has many far drivers because every selected
driver is used and groups are not yet matched to driver homes.
