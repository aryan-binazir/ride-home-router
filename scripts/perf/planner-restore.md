# Planner reopen benchmark

Run against a populated `make dev` stack. The harness uses only its local synthetic providers. Install `playwright-core` in a scratch directory, then run:

```sh
NODE_PATH=/path/to/scratch/node_modules node scripts/perf/planner-restore.cjs /path/to/private/ready.json /path/to/results.json 5
```

The private `ready.json` is under the state directory printed by `make dev`. Do not publish it. `CHROMIUM` can override `/usr/bin/chromium`. The script owns and closes its Chromium process, CDP connection, temporary browser profile, contexts and edge proxy. It creates a real calculated session through the UI, then reopens the planner five times per profile with warm static assets. The proxy supplies Brotli q4 and four-hour static caching to match the finder experiment. An animation-frame sampler records when routes have geometry and `planner-restoring` has cleared. Resource Timing and CDP independently record request starts and durations.

Measured on 2026-11-16, Chromium 155.0.8059.84. Baseline product code is `4f0a26b`; the runnable harness was committed first at `bdb7c1f`. Raw run evidence is in `planner-restore-results.json`. Times below are milliseconds from navigation start.

| Profile | Before median [min, max] | After median [min, max] | Median reduction |
| --- | ---: | ---: | ---: |
| Desktop, 1440×900 | 792 [307, 2120] | 1515 [1294, 1958] | No wall-time gain |
| Mobile, 390×844, DPR 3, 4× CPU, 150 ms RTT | 3952 [1673, 5739] | 1575 [1190, 2477] | 60% |

Mobile bandwidth is 1.44 Mbps down and 675 Kbps up. The machine was busy: one-minute load was 78–89 before and 132–143 for the final candidate. Background session measurement also caused long server responses. The medians include those tails; they do not establish a fixed production speedup or a desktop improvement. Even the fastest final mobile runs improved by 483 ms, or 29%.

Two earlier candidate batches are also retained in the JSON. At load 96–106, the first had desktop/mobile medians of 316/1022 ms, with mobile range 790–1941 ms. At load 126–132, the confirmation had medians of 962/1686 ms, with mobile range 1378–3018 ms. The final batch rebuilt the committed product code at `56935f9`. No runs were dropped.

| Median request start | Desktop before | Desktop after | Mobile before | Mobile after |
| --- | ---: | ---: | ---: | ---: |
| Location editor | 89 | 390 | 404 | 659 |
| Participants picker | 216 | 392 | 684 | 670 |
| Drivers picker | 249 | 396 | 900 | 678 |
| Route session | 290 | 400 | 1335 | 687 |

Each run made four restore requests and rendered 24 route cards. The median first-to-last request-start gap fell from 149 to 10 ms on desktop and from 844 to 28 ms on mobile. This removes three serial network waits without changing request formats or adding a server endpoint. Local vehicle assignment fragments still follow picker application because their request uses the restored selected drivers. No additional optimization was retained or benchmarked.
