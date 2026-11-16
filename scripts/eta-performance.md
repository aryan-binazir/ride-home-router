Run against this worktree's synthetic `make dev` stack. Install `playwright-core` in a scratch directory and set `NODE_PATH` to its `node_modules`. `READY` names the private dev state's `ready.json`; do not publish that file or its login URL.

```sh
READY=/path/to/ready.json NODE_PATH=/path/to/node_modules \
  SOURCE=/path/to/event-planner.js RUNS=5 node scripts/eta-performance.cjs
```

`SOURCE` defaults to the working tree asset. The harness intercepts only that asset, so baseline and candidate share the same server and fixture. Each run uses a fresh browser context, all seeded participants/drivers, 390x844 DPR 3, 4x CPU and slow 4G. It reports click-to-next-frame-after-visible-result, long-task duration, formatter constructions, CPU profile self-time and load averages for Calculate and Move. Counts exclude setup. The fixed 80-stop fixture compares complete serialized results DOM with a per-call formatter oracle for pickup/dropoff, midnight/noon/day rollover and invalid time. It also times ten ETA populations, five repetitions, outside the interaction measurement.

Chromium is driven through Playwright's CDP session rather than `--dump-dom`. The oracle runs after Calculate, so its construction count is excluded from both measured interactions. CPU profiles include Playwright actionability and polling work; compare application frames, not total profile duration. Server time and machine contention remain part of click latency.
