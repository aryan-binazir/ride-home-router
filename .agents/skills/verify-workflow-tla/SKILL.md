---
name: verify-workflow-tla
description: Model-check the durable import geocoding queue and route-session saving with TLA+ (make tla). Use when changing import_jobs.go, importer/persistent.go, workflows.go, routesession/persistent.go, event saving, or their migrations, or when asked whether those still hold under concurrency.
---

# Verify the durable workflows with TLA+

Two TLC models check promises that unit tests rarely reach. The bugs they guard against need a crash, a lease expiry, or two requests landing at the wrong moment, across several server instances sharing Postgres.

| Spec | Promise | Harm if broken |
|---|---|---|
| `ImportGeocodeQueue` | `AtMostOneResultPerJob`: an address's geocode result lands once. | A stale worker overwrites a good match with an error and deselects the rows. |
| | `ProviderRoundsBounded`: a job runs at most `maxGeocodeRounds` (3) provider rounds, even with unbounded crashes and stalls inside the call. | A poison address burns provider quota forever. |
| | `GeocodingKeepsSelection`: a geocoded row keeps the person's latest selection. | A row the person excluded is imported anyway. |
| | `CommitOnlyWhenResolved`: commit only happens once no row is pending. | `createBatch` skips rows without coordinates, so the import silently drops rows that were still geocoding. |
| | `ImportEventuallyCommittable` (liveness): while the import stays open, every row finishes geocoding. | The import shows "geocoding" forever and can never be committed. |
| `RouteSessionSave` | `SessionMatchesAckedEdits`: the live session equals the successful edits and resets, applied in the order their writes committed. | A concurrent edit silently undoes one the person saw succeed. |
| | `SavedEventMatchesAckedEdits`: the saved Event equals that state at the moment of saving. | The saved routes differ from what the person approved. |
| | `NoEditAckedAfterSave`: no edit succeeds after the save. | A stale tab's edit reports success but never reaches the Event. |
| | `EventSavedAtMostOnce`: one session makes at most one Event, even after that Event is deleted. | A double click or a stale tab creates a duplicate, or brings a deleted Event back. |

## Does the change touch verified code?

Run this, and read the coverage record below for each file it lists:

```bash
git diff --stat 6d6112c -- internal/postgres/import_jobs.go internal/postgres/workflows.go internal/importer/persistent.go internal/importer/store.go internal/importer/selection_patch.go internal/routesession/persistent.go internal/routesession/store.go internal/handlers/events.go internal/postgres/events.go migrations/
```

`6d6112c` is the **sync commit**: the last commit at which the models were confirmed to describe this code. Also check for new code that writes the guarded state: `import_jobs`, `import_rows.payload` or `selected`, `workflow_sessions.revision` or `consumed`, and `events.route_session_id`. A covered function that is renamed or moved is still covered.

## Run

```bash
make tla            # every spec; same as scripts/tla.sh
scripts/tla.sh Route   # only configs whose file name contains "Route"
```

This needs Java 11 or later (`JAVA`, `java` on PATH, or mise's `java@temurin-25`). The first run downloads the pinned `tla2tools.jar` 1.7.4 to `~/.cache/ride-home-router/` and checks its checksum. A full run takes about two minutes on 24 cores. The import progress check and the `release-steals-claim` mutation are the slowest, at about a minute each.

Each line of output must be `ok`:

- `verification/tla/<Spec>.cfg` and `<Spec>.liveness.cfg` model the code as it is, and must pass with no error.
- `verification/tla/mutations/<Spec>.<break>.cfg` turn off one guard (a `Guard*` constant) and must fail on the property named in their `\* expect:` line. This shows the passing check can still fail. If a mutation passes, the model has lost the ability to see that bug.

Read every TLC result with the `/statechart` skill. For a violation, it tells the shortest path in plain words. When a check passes, it draws how the system moves between states.

## Bug or modelling error?

When a passing check fails after your change, replay the counterexample against the code, one step at a time. Each action in the spec names the function and the SQL it stands for.

- **Every step can happen in the code:** you found a bug. Fix the code, not the model. Never weaken a property or a guard to make a check pass.
- **A step cannot happen in the code:** the model is wrong. Fix the model, and say what was wrong.

## Update the model for an intended change

1. Change the action that matches the code you changed, working from the code rather than from the result. One transaction or one SQL statement is one action. Code that runs between transactions is a separate action.
2. When the code adds a guard, add a `Guard*` constant for it. Set it to `TRUE` in the main configs, and add a mutation config that sets it to `FALSE` with a `\* expect:` line. When guards back each other up, as with the two consumed checks on edits, use one constant for the group.
3. Change a property only when the promise changed on purpose. Cite the evidence (ADR, test or commit), and record the old and new wording in the PR.
4. Run `make tla`. Then move the sync commit in this file and in `verification/tla/README.md` to the commit you confirmed.

Only move the sync commit after you confirm that the models describe the code at that commit. For drift that built up over many commits, use `/maintain-formal-verification-skill`.

## Coverage record

Sync commit: `6d6112c`.

`ImportGeocodeQueue` (`verification/tla/ImportGeocodeQueue.tla`):

| Code | Spec |
|---|---|
| `importJobRepository.Claim` (`internal/postgres/import_jobs.go`) | `Claim` |
| `importJobRepository.RowsByIndices` | `ReadRows` |
| `importJobRepository.Finish` | `Finish` (+ `WriteRows`, which only runs in a mutation) |
| `importJobRepository.Release` | `Release` |
| `Store.processJob` and `durableWorker` (`internal/importer/persistent.go`) | `Claim`, `ProviderReturns`, `ProviderCrashes`, `Crash` |
| `Store.commitPersistent` | `Commit` |
| `Store.CancelContext`, `workflowRepository.Delete`, TTL | `Abandon` |
| `SelectRowsContext`, `PatchImportSelections`, `SelectImportRows` | `Select` |
| `geocodeGroups` (`internal/importer/store.go`) | `JobRows` (each row in at most one job) |
| `claimed_until`, 60s lease, 30s `workCtx`, Release/Finish backoff | `leaseLive`, `LeaseExpires`, `ProviderStalls` |

`RouteSessionSave` (`verification/tla/RouteSessionSave.tla`):

| Code | Spec |
|---|---|
| `Store.update` + `workflowRepository.Load` (`internal/routesession/persistent.go`, `internal/postgres/workflows.go`) | `EditLoads` |
| `workflowRepository.CompareAndSwap` | `EditWrites` |
| `Store.CommitEvent`, `commitEventSession`, `persistEvent`, `CreateEvent` | `Save` |
| `events_route_session_id_idx` (`migrations/20260912211512_event_route_session.up.sql`) | `GuardUniqueEventPerSession` |
| `HandleDeleteEvent`, `eventRepository.Delete` | `DeleteEvent` |
| `Store.DeleteContext`, `CleanupWorkflows`, capacity eviction in `Create` | `Discard` |

## Bounds and assumptions

- Import: 2 workers; 2 jobs, one of them shared by two rows; the person toggles one row's selection; `maxGeocodeRounds` = 3; one crash outside the provider call; unbounded crashes and lease overruns inside it.
- Import liveness assumes the workers keep polling, time passes, and the provider throttle reopens. It also assumes that a holder can overrun its lease only inside the provider call. After the call, persisting has a 5s timeout and a 5s `lock_timeout`, and the 30s `workCtx` leaves at least 25s of the 60s lease. Database errors are not modelled: they return early, and the deferred Release runs.
- Claim tokens are modelled as worker ids. This is sound because a worker runs one job at a time, and a restarted process starts with no job.
- Route: 2 edits, 1 reset, 2 saves, from any tabs or instances. Event deletion and session discard can happen at any time. The payload is abstracted to route edits: the reviewer note (kept by a reset, left out of the Event) and the cleared `{}` payload's failure to validate on a second save are not modelled. "Acknowledged" means the edit's write committed. HTTP responses may reach the browser in a different order, or after a later save.

## Not covered

- The paid-request count inside one round (`GeocodeWithRetry` retries), and the Google usage ledger.
- Mapping and restaging (`ApplyMapping` CAS), review paging, and the import roster upsert itself.
- Plan drafts (`internal/plandraft`) and how they adopt route sessions. This is the next candidate for a model.
- Distance calculation, routing quality, and anything in-memory.

## Guards that back each other up

Each of these was checked by removing the guard from a scratch copy of the model, at the sync commit:

- **Redundant.** Removing it alone keeps every property:
  - `AND NOT done` in Finish. Claim's `NOT j.done`, and Finish clearing the token, still prevent a second result. Removing all the `NOT done` clauses breaks `AtMostOneResultPerJob` (mutation `done-jobs-reopen`).
  - `claimed_until > clock_timestamp()` in Finish. A stale worker's Finish still loses to the token and `NOT done` checks.
  - The unique index on `events.route_session_id`, as long as the consumed check in `CommitEvent` stays.
- **Not redundant**, although it looks covered by another guard:
  - The consumed check in `CommitEvent` is not covered by the unique index. In the model, once the person deletes the Event, a stale tab can save it again (mutation `save-ignores-consumed`). In the code, a third backstop the model leaves out also stops that save: the save clears the payload to `{}`, and `eventsnapshot.Build` rejects its empty route mode. So the consumed check is what keeps `CommitEvent`'s promise for any callback, not only for one that validates its input.
  - The consumed checks on edits are not covered by the revision check. An edit that loads after the save sees the save's new revision (mutation `edit-ignores-save`).
  - Release's `claim_token=$3` is needed for progress. Without it, a stale worker clears the current holder's claim, so that holder's Finish is rejected. Two workers can keep doing this to each other forever, and the import never finishes geocoding (mutation `release-steals-claim`).
