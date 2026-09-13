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
ordering pass (pickup backtracking cars 194 → 18 across the suite, worst
rider trips shorter in 30 runs and longer in 1; seven runs gave up 1–3%
distance or a few minutes of detour, accepted by the owner). Known remaining
defect: `drivers-in-durham-*` has many far drivers because every selected
driver is used and groups are not yet matched to driver homes.
