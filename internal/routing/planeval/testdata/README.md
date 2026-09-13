# Planner evaluation baseline

`baseline.json` holds the planner's results for every evaluation scenario, seed
and mode (see `../scenario.go`), captured with `make eval` and
`UPDATE_PLANNER_BASELINE=1`. `make eval` fails when any run is materially worse
than this file (`Compare` in `../evaluate.go`). Solve times are not stored;
timeouts are.

Two metrics describe whether a plan reads as sensible to a coordinator, beyond
distance and detour: driver burden per rider (the planner's estimated detour
minutes divided by riders served; the 95th percentile by nearest rank and the
worst car, stored and gated unrounded; gates +1 and +2 min/rider), and
home-pass cars (the car's great-circle path in venue-to-riders order comes
within 2 km, inclusive, of the driver's home on a leg while a rider more than
5 km from that home is still to be served; gate +1 car, or +0 in the
drivers-in-durham shapes). Home-pass is straight-line geometry, not road
evidence. In the summary, `home` is a sum over a scenario's runs and `b95` /
`bmax` are the worst run. Baseline regenerated on 2026-09-13 to add them; the
planner was unchanged at that point.

The same day, five candidate assignment changes were measured against this
baseline and none earned shipment. Measured by Codex on all 126 runs: a
driver-corridor seed, households handed to drivers by insertion cost into the
venue-to-home trip (122 of 126 runs fail gates; far drivers +19%, driving
+7.4%, burden p95 worse in every Durham-driver run; Claude's independent
prototype of the same idea saw far drivers flat, driving +5%, worst rider
+38%, backtracking 9 → 46); a seed portfolio picked by the comparator (4 runs
fail, one with max detour 13.6 → 34.0 min); a burden tier in the comparator
(burden totals improve but 7 runs fail and the Durham shapes do not improve);
and a guarded global driver reassignment after Phase 4 (passes every gate;
p95 burden improves in 18 runs but by at least one minute in only two). Measured
by Claude: reordering the comparator so detour outranks corridor spread made
every total worse (driving +1.1%, detour +5%). The design exchange transcripts
were kept under `_scratch/_reviews/rr/` in the driver-matching worktree
(gitignored, temporary); this paragraph is the durable record.

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
   (relocations), which applies to dropoff too. Committed file vs the
   pre-branch planner (80cdf24; identical to the post-repair baseline apart
   from the two tight-seats timeouts), pickup, 63 runs: backtracking cars
   194 → 10, summed worst rider time 3,642 → 3,105 min, summed worst detour
   3,031 → 2,741 min, far drivers 225 → 197, driving 106,148 → 105,852 km;
   no pickup run's longest rider got worse. Pickup runs that got worse on
   something: chapel-hill-venue-100 seed 2 (max detour 42.3 → 58.1 min,
   distance +2.7%), durham-heavy-100 seed 2 (distance +5.4%, detour +3.6
   min), chapel-hill-carrboro-50 seed 3 (distance +3.6%, detour +5.0 min),
   vans-500 seed 3 (detour +4.9 min), carrboro-durham-500 seed 2 (far
   drivers 7 → 10), and several small rosters +1–3% distance. Dropoff, 63
   runs: 42 plans changed (driving −0.14%, backtracking 19 → 9, worst rider
   unchanged); two tight-seats-500 dropoff rows got worse on a metric after
   neighbour-swap relocations were skipped (seed 2 max detour 67.2 → 69.4
   min; seed 3 max detour 67.3 → 73.2 min and far drivers 7 → 10, with 66 km
   less driving). The owner accepted these trade-offs as a policy change
   after reviewing them. Pickup plans are now essentially dropoff plans in
   reverse (the household-free reference fixture is the same plan in both
   modes). A car re-ordered on its own by the edit flow can still differ
   from the whole-plan order in either mode (about 6% of cars in small
   rosters, same as dropoff always was); the reference fixtures happen to
   agree and the fixture test asserts it.

Known remaining
defect: `drivers-in-durham-*` has many far drivers because every selected
driver is used and groups are not yet matched to driver homes.
