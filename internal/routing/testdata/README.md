# Routing performance references

The `reference-*.json` files contain complete `CalculateRoutes` results captured
from commit `82c63845b25362c8fc08a69976a9926307858c23`, before the CPU changes.
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
