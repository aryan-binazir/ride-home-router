# Routing performance references

The `reference-*.json` files contain complete `CalculateRoutes` results. They
were first captured at commit `82c63845b25362c8fc08a69976a9926307858c23`, before
the CPU changes, and regenerated on 2026-09-13 when the assignment search gained
whole-car driver swaps (a deliberate route-selection change: a driver who lives
near another car's riders now takes that car). In that regeneration one
plan changed: the household-free dropoff total drive fell 5515 → 5290 s with
the maximum detour unchanged. The other three plans were unchanged. Regenerate only for a decided
solver change, with `UPDATE_ROUTING_REFERENCES=1 go test ./internal/routing -run
TestRoutingPreservesReferenceResults`.
The `edited-*.json` files capture the same results after calling
`OptimizeRouteOrder` on each route. Tests compare every field exactly, including
floating-point metrics, stop order, and route order.

The fixture in `performance_test.go` uses a fixed Chapel Hill/Durham-area seed,
asymmetric travel costs, unsorted driver IDs, both route modes, and singleton or
paired households. Do not regenerate references merely to make a changed solver
pass. A route-selection change requires an explicit behavior decision.

Run the public-seam performance benchmarks with fixture construction outside the
measurement:

```sh
go test ./internal/routing -run '^$' -bench BenchmarkCalculateRoutesWarm -benchmem -benchtime=3x -cpu=1,4
```

The allocation regression test runs separately from race builds. Cancellation
and parallelism tests exercise the public calculation interface under the race
detector, including repeated cancellation of concurrent calculations. Wall-clock
thresholds belong in benchmark comparisons, not unit tests.
