# Verification coverage and maintenance

Use this record when covered code changes. `verification/README.md` holds bounds and gate commands; `verification/code-mutations.json` names the executable guard controls.

## Code to model actions

| Model | Code | Action |
|---|---|---|
| Import queue | `postgres/import_jobs.go:Claim` | `Claim`, reserving the worker round before provider work |
| | `RowsByIndices`, `importer/persistent.go:processJob` | `ReadRows`, `ProviderReturns`, `ProviderCrashes`, `Crash` |
| | `importJobRepository.Finish` | `Finish`, authorizing and committing all result rows in one transaction |
| | `importJobRepository.Release` | `Release`, conditional on the current claim token |
| | `claimed_until`, provider timeout/backoff | `LeaseExpires`; safety allows expiry in every phase |
| | `SelectRowsContext`, `SelectImportRows`, `PatchImportSelections` | `Select`, under the workflow lock |
| | `Store.commitPersistent` | `Commit`, rejecting pending rows under the same lock |
| | cancellation, deletion, TTL | `Abandon`; the model keeps closed job data as an overapproximation, not a physical cascade |
| Route save | `routesession/persistent.go:update`, `workflowRepository.Load` | `EditLoads`, with computation outside the transaction |
| | `workflowRepository.CompareAndSwap` | `EditWrites`, checking revision and consumption |
| | `Store.CommitEvent`, `commitEventSession`, `workflowWrites.CreateEvent` | `Save`, with event creation and consumption sharing the transaction |
| | transaction failure after callback | `SaveCommitFails`; the split-transaction control leaks an unconsumed Event |
| | event deletion and workflow disposal | `DeleteEvent`, `Discard` |
| Capacity | `workflowRepository.Create` advisory lock | `Begin`, scoped to schema and kind |
| | count and eviction SQL statements | `Count`, `Decide`; the advisory lock remains held |
| | insertion and transaction commit/abort | `Finish`; deletion stays private until commit |
| | deletion moved outside transaction | `DeleteOutsideTransaction`, negative control only |
| | consumption or expiry freeing an import slot | `Reuse` |

The capacity model's rows 7 and 8 are older inactive routes. `GuardEvictionSkipsInactive` controls whether they can be selected. The real replay fills the actual route limit with older expired/consumed rows present, then checks that replacement evicts the oldest live row and keeps the live count at 256. Background workflow cleanup is outside this guarantee.

## Outcome checks and guard pins

`CompletedJobsResolved`, `SessionMatchesAckedEdits`, `EventRequiresConsumedSession` and `CommittedCapacity` observe stored outcomes against independent expectations. Queue `FinishHasAuthority` and `NoWriteAfterClose` are guard-presence pins: they record whether a write had the specified token, lease and workflow authority. Do not count them as independent proof that removing each guard corrupts user-visible results. Real token, lease and expired-workflow replays bind these pins to SQL behavior.

Several guards back each other up:

- Finish's `NOT done` is redundant for at-most-once results while Claim rejects done jobs and Finish clears the token. The model's `done-jobs-reopen` control removes that group.
- A stale Finish's lease check can be redundant for final-result safety because token replacement fences it. The explicit expired-lease replay still requires it to reject an expired claim before another worker reclaims it.
- Event uniqueness is redundant while consumption rejects a second save. Conversely, uniqueness alone allows saving again after the first Event is deleted. Clearing the payload also protects the real handler because snapshot validation rejects missing routes; a permissive `CommitEvent` callback does not have that backstop.
- Release's token check affects progress: a stale release can repeatedly clear the current holder's claim.

## Adding or changing a control

1. Write the model action from the actual operation or transaction boundary. State any omitted failure behavior or scheduling assumption.
2. Set each product `Guard*` to `TRUE` in every positive config. Copy a relevant config into `mutations/`, disable the guard or guard group, and declare exactly one expected check:

   ```tla
   \* expect: invariant CommittedCapacity
   INVARIANT CommittedCapacity
   ```

   Temporal controls use `\* expect: property Name` and only `PROPERTY Name`, with no invariants or additional properties. Assumption-boundary controls retain product guards and name the changed assumption.
3. Show the broken model fails on that check. Add a real-code replay and compiler-overlay mutation when the guard has observable behavior. Removing a redundant guard alone need not break the user-visible outcome.
4. Review properties, guards, bounds and code mapping, then refresh `checks.json`. It locks both specs and configs. Filtered runs work during development; full gates require a clean commit.
5. Run the complete gate on the committed head. Move the sync commit only after verifying fidelity at that code revision.

The route/auth mutation inventory also covers `internal/routesession/store.go`, `internal/routing/balanced_router.go`, `internal/access/access.go` and `internal/postgres/provider_throttle.go`. A refactor of these sites requires reassessing the test and updating its overlay, even when no TLA action changes.

Plan-draft handlers are retired from the production mux. Model draft adoption if they become reachable again; they are not the next live target simply because `internal/plandraft` still exists.
