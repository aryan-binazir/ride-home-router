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
(main de65d24). Regenerated the same day after two deliberate planner changes:
the bearing sweep repairs a partial seed instead of falling back to the slow
round-robin (`tight-seats-500` no longer times out: seeds 1 and 3 went from
about 85 s and 10,400 km to under a second and about 3,800 km), and pickup
routes are scored by riders' time aboard with single-block relocations in the
ordering pass. Pickup, 63 runs: backtracking cars 194 → 18, summed worst
rider time 3,642 → 3,274 min, far drivers 225 → 198, driving 106,148 →
105,830 km; the worst rider improved in 30 runs and worsened in 1. Seven
pickup runs got worse on something, two of them substantially:
triangle-wide-500 seed 2 worst rider 67.5 → 86.7 min, chapel-hill-venue-100
seed 2 max detour 42.3 → 59.6 min and +3% distance; the other five gave up
1–3% distance or up to 4 min of detour. Dropoff, 63 runs: relocations changed
42 plans within the gates (driving −0.14%, backtracking 19 → 9, worst rider
and far drivers unchanged, two runs +0.2% distance). The owner accepted these
trade-offs as a policy change after reviewing them. Known remaining
defect: `drivers-in-durham-*` has many far drivers because every selected
driver is used and groups are not yet matched to driver homes.
