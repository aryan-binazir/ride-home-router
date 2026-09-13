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

Captured 2026-09-13 at the merge of the estimate-then-measure planner
(main de65d24). Known baseline defects that later work should improve here:
`tight-seats-500` times out or plans very poorly on some seeds (the
bearing-sweep seed fails and the round-robin fallback is slow); pickup mode
backtracks far more than dropoff; `drivers-in-durham-*` has many far drivers
because every selected driver is used.
