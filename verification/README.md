# Local verification gates

Run this on a clean, committed branch head you intend to merge:

```sh
TEST_DATABASE_URL=postgres://... BROWSER_TEST_BINARY=/path/to/chromium make check-branch
```

Use a local synthetic test database. The existing test fixture creates and drops an isolated schema per test. `check-branch` requires both environment variables and runs the existing lint, module, vulnerability, vet, JavaScript, browser and Go race checks, planner evaluation, TLC checks, and real-code guard mutations. It adds no GitHub Actions, hooks or deployment steps. The gate refuses uncommitted changes and verifies HEAD, working-tree status and all tracked inputs again at completion. The command is usable on PR and production branches; this repository does not yet enforce its execution remotely.

Python 3, Node, Go, the repository-pinned golangci-lint, Java 11+, Postgres and Chromium are required. `make check-unit` deliberately omits database tests and is insufficient for merging.

## What each layer establishes

- `make tla` explores every state in each checked-in finite model/config. Safety, progress assumptions and negative controls are described below. It verifies the model, not arbitrary Go code.
- Ordinary Go tests replay concurrency and rollback at the actual repository/handler interfaces, including PostgreSQL transactions. Bounded route tests exercise the actual router and editor against independent rider, driver, capacity and rollback oracles. Virtual-clock auth tests enforce identity-cache and JWT expiry.
- `make verification-mutations` uses compiler overlays to remove real guards, without editing source files. Each clean control must pass, then its mutant must fail the named test and assertion. Build failures, unrelated failures, missing tests and skipped tests do not count as catching a mutation. `verification/code-mutations.json` lists the exact changes and test seams. Mutations include SQL claim tokens/leases, live selection, CAS, eviction rollback, save consumption, event/workflow transaction sharing, route batch rollback, identity/JWT expiry, rider conservation and capacity serialization. The serialization probe also delays the count/insert gap to expose the race.

These layers reduce the amount of interleaving reasoning a reviewer must repeat. They do not establish equivalence between a handwritten model and future implementation changes. Reassess the model and replay when a covered function changes, moves, gains a new writer, or changes transaction boundaries. Do not automatically refresh a manifest to hide a failure.

## TLC runner

```sh
make tla
scripts/tla.sh Route             # partial developer run, never full-gate evidence
python3 scripts/tla_gate.py --refresh-manifest
```

Refresh only after confirming the intended spec/config inventory, bounds, guards and properties. `verification/tla/checks.json` locks both spec and config contents; removing a config, changing a spec or reducing a property declaration fails before TLC starts. It is a coverage-change alarm, not proof that a deliberate manifest update is correct.

The runner downloads TLC 1.7.4 on first use and checks SHA-256, including when `TLA2TOOLS` points to a supplied jar. `JAVA` selects Java; otherwise the runner tries PATH then mise's `java@temurin-25`. Defaults are two workers, 2048 MiB heap and 900 seconds per config. `TLA_WORKERS`, `TLA_HEAP_MB` and `TLA_TIMEOUT_SECONDS` can lower or raise those within the runner's limits. A timeout or tool error fails the gate; a restricted sandbox denying TLC's local RMI socket is a tool error and requires a permitted execution context.

Passing positive checks require exit zero, completion, a positive distinct-state count and an empty work queue. Negative controls require TLC's exact safety/liveness exit code and matching diagnostics; temporal controls isolate one property. Unexpected errors fail even when expected text also appears.

Each run retains logs and `receipt.json` under `_scratch/tla/run-*`, including head, dirty state, all tracked input hashes, tool hashes, resource limits and check results. The combined command also retains a branch receipt under `_scratch/branch-gates/`. TLC state directories are removed after each check; logs and receipts remain. A retained input, working-tree or HEAD change invalidates it and prints the changed inputs. Receipts are evidence for that run, never reusable cached proof. Real-code mutation logs/receipts live under `_scratch/verification-mutations/run-*`. Run the gate again after changing the branch head.

## Model coverage

Sync commit: `c38758d0b4a31274e55063b15b9d78329d7c4cbd` (merged #138, extending #137). Product code is unchanged by this verification change.

| Model | Code and guarantees | Bounds and exclusions |
|---|---|---|
| `ImportGeocodeQueue` | `postgres/import_jobs.go`, `importer/persistent.go`, selection writes and `postgres/workflows.go`: current token/live lease at Finish, one result per job, bounded provider rounds, selection preservation, no write after closure, completed jobs have resolved rows, commit rejects pending rows. | Two synchronous workers, two jobs/three rows, one selectable row, three provider rounds, one outside-provider crash; unbounded provider crashes and stalls. Safety allows lease expiry in every phase. Fixed staged jobs, no provider billing, remapping, arrivals or roster-membership proof. |
| `RouteSessionSave` | `routesession/persistent.go`, `postgres/workflows.go`, event writes and unique index: CAS preserves commit-ordered edits, saved snapshot matches those edits, no edit after consumption, one historical event, an Event always has a consumed session. SQL replays separately check failed-save rollback. | Two edits, one reset, two saves; deletion/discard and one failed save per request. Abstract route-edit payload, no reviewer-note or browser-delivery model. The permissive callback contract differs from the handler, whose cleared payload fails validation for missing routes. |
| `WorkflowCapacity` | `postgres/workflows.go:Create`: advisory lock spans count/eviction/insert, committed capacity bounded, failed insertion cannot evict, only same kind evicted, eviction skips older inactive routes. | One schema, two kinds with capacity two, three creates, fixed expiry ordering, initial consumed/expired rows and one slot release. No arbitrary MVCC proof, advisory-hash collisions, row-lock contention, moving expiry order or liveness. Real SQL tests use the actual limits of 4 and 256. |

Finish authorization is linearized at its SQL authority checks. The model does not promise that a lease or TTL remains live until the eventual transaction commit.

Import progress is conditional on continued polling/time/throttle progress, bounded crashes outside provider calls, successful database operations and finalization before a current holder's lease expires outside the provider call. Request timeouts do not prove that scheduling assumption. Safety does not need it. The `arbitrary-stalls-progress` negative control keeps all product guards and demonstrates why progress cannot be promised under perpetual pauses. It is an assumption boundary, not a product finding.

Read TLC results with `/statechart`; retain and replay each violation against real code before calling it a product bug. Model mutations show model sensitivity. Compiler-overlay mutations show specific real-code test sensitivity. Neither proves every possible bug is caught.

## Audit findings outside this change

The live-path matrix quota bypass, ambiguous edit-response save eligibility and settings field-preservation race were fixed separately in #138. This verification branch does not codify those bad outcomes as passing behavior. Mobile draft pruning also loses a newer selection when invoked directly, but its handlers are retired from the production mux; it is not a live-path claim. Month-of-dispatch billing across Pacific midnight and stale save acknowledgements need further runtime evidence. Planner integrity tests add driver uniqueness and membership oracles absent from the existing quality-evaluation validator; no solver defect was established.

A broken queue guard can overwrite a match or lose a row selection. A broken save guard can discard an acknowledged edit or create a duplicate Event. A broken capacity guard can admit excess work or delete a session even though its replacement failed. The replay tests make those consequences observable through stored rows and handler results.

See [coverage and maintenance](model-mapping.md) for code-to-action mappings, guard-presence pins, redundant guards and negative-control syntax. Full TLC and mutation runs require a clean committed tree; filtered runs are explicitly partial developer evidence. `make check` remains the minimum for other changes. Workflow, covered verification-site and gate changes use `make check-branch`.
