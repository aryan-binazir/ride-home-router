# Code Review: GPT Event/Route Refactor

**Scope:** 21 files, +2632/-984 lines. Core change: `EventAssignment` (flat per-stop rows) → `EventRoute` + `EventRouteStop` (hierarchical), plus extraction of shared routing logic into `route_metrics.go`.

**Build status:** Compiles, `go vet` clean, all tests pass.

---

## CRITICAL

### 1. Template panic: `gt .DetourSecs 0` — float64 vs int comparison
**`web/templates/partials/event_detail.html:98`**

```html
{{if gt .DetourSecs 0}}
```

`DetourSecs` is `float64`. Go templates require matching types — `gt float64 int` panics at runtime: `"incompatible types for comparison"`. This will crash every event detail view for events saved with the new snapshot format.

**Fix:** `{{if gt .DetourSecs 0.0}}`

(`route_results.html:196` already does this correctly with `gt .Summary.MaxDetourSecs 0.0`.)

---

## HIGH

### 2. "Load More" pagination is now O(n²) in queries
**`web/templates/partials/event_list.html:47`**

Old: `offset={{len .Events}}&limit=20` with `hx-swap="beforeend"` (fetch next page, append).
New: `limit={{add (len .Events) 20}}` with `hx-swap="innerHTML"` (refetch everything, replace).

Combined with the N+1 `GetByID` calls in `buildEventListView`, clicking "Load More" the 5th time runs ~100 full `GetByID` queries (each fetching routes + stops + summary). This scales quadratically with clicks.

### 3. N+1 query in `buildEventListView` (pre-existing, wider blast radius)
**`internal/handlers/events.go:304-343`**

Each event triggers a full `GetByID` just to extract the summary. With the new schema, `GetByID` is heavier (routes + route_stops). Now called from 3 places (list, delete-HTMX, history page). 20 events = ~23 queries; 100 events = ~103 queries.

---

## MEDIUM

### 4. `TotalDistanceMeters` semantics changed silently
**`internal/handlers/events.go` — `buildEventSnapshots`**

Old: `TotalDistanceMeters` came from `req.Routes.Summary.TotalDropoffDistanceMeters` (excludes driver-home leg).
New: sums `route.TotalDistanceMeters` across routes (includes driver-home leg).

New events will have larger `TotalDistanceMeters` than old events. When displayed side-by-side in history, this creates inconsistent comparisons with no way to distinguish which metric was used.

### 5. Dead `EventAssignment` type still in models.go
**`internal/models/models.go:94-108`**

Zero references in the codebase. The entire point of this diff was to replace it with `EventRoute`/`EventRouteStop`. Should be removed to avoid confusion.

### 6. `HasLegacyArchived` computed but never rendered
**`internal/handlers/events.go`**

Extra DB query (`HasLegacyArchive`) runs on every list/delete/history request. No production template references the field. Either wire it into `event_list.html` or remove the query.

### 7. Nil-dereference risk on `route.Driver` in `HandleAddDriver`
**`internal/handlers/route_edit.go:574`**

```go
if route.Driver.ID == req.DriverID {
```

No nil guard. `getUnusedDrivers` (line 519) was defensively fixed to check `route.Driver != nil`, but this loop wasn't. Same issue in `HandleSwapDrivers` capacity fallback (lines 410-417).

### 8. `NullInt64` comparison without `.Valid` check in backfill grouping
**`internal/sqlite/store.go:622-623`**

```go
current.orgVehicleID.Int64 != assignment.orgVehicleID.Int64
```

Compares `.Int64` directly without checking `.Valid`. NULL vehicle (Valid=false, Int64=0) would be indistinguishable from vehicle ID 0. Safe in practice (SQLite autoincrement starts at 1), but semantically wrong.

### 9. No pickup-mode test coverage for `distanceMinimizer`
**`internal/routing/distance_minimizer_test.go`**

All tests use dropoff mode. The pickup-mode objective (which includes the terminal leg via `objectiveIncludesTerminal()`) is never exercised for the distance minimizer. Same gap exists for `groupInsertionDeltaDuration` in pickup mode.

---

## LOW

### 10. "Load More" collapses expanded event details
**`web/templates/partials/event_list.html`**

`hx-swap="innerHTML"` replaces the entire list DOM, destroying any expanded event detail panes. Old `hx-swap="beforeend"` preserved them.

### 11. Delete handler resets pagination to 20
**`internal/handlers/events.go:292`**

After delete, HTMX response always fetches `limit=20, offset=0`. If user had loaded 60 events, view resets to 20. Pre-existing behavior, not a regression.

### 12. `DistanceToDriverHomeMeters` field name misleading in pickup mode
**`web/templates/partials/event_detail.html:136-143`**

Field is named `DistanceToDriverHomeMeters` but in pickup mode it represents distance to activity location. UI label is correct ("Activity location" for pickup), but field name is confusing.

### 13. Nested open result sets on same transaction in backfill
**`internal/sqlite/store.go:451-509`**

`backfillLegacyRoutes` opens a second `rows` cursor while the outer migration cursor is still open on the same `tx`. Works with modernc/sqlite driver but fragile if driver changes.

### 14. No test for `HandleCreateEvent` success path
**`internal/handlers/events_test.go`**

Only tests the missing-routes validation error. No test creates an event through the handler and verifies persistence.

### 15. Test name misleading: "WithoutLegacyNotice"
**`internal/handlers/events_test.go`**

`TestHandleListEvents_HTMXRendersHTMLWithoutLegacyNoticeAndIncludesMigratedEvents` asserts absence of text the test template literally cannot produce. Passes for the wrong reason.

### 16. Duplicate test logic across packages
**`internal/handlers/route_edit_test.go` vs `internal/routing/route_metrics_test.go`**

`TestRecalculateRoutePickupUsesModeAwareMetrics` duplicates `TestPopulateRouteMetrics_PickupIncludesActivityDestination` — same setup, same assertions, duplicate mock calculator.

---

## What's Good

- **Concurrency fix:** Extracting `instituteCoords`/`mode` from mutable struct fields to per-call `routeContext` eliminates a real race condition.
- **Clean extraction:** Every function removed from `balanced_router.go` and `distance_minimizer.go` has a correct replacement in `route_metrics.go`. Nothing important was lost.
- **2-opt copy fix:** Old code mutated the caller's slice in-place. New code copies first (`twoOptByDelta`).
- **Deterministic output:** `buildResult` now iterates routes in sorted driver-ID order instead of random map order.
- **Proper error propagation:** Old code silently swallowed distance calculation errors. New code propagates them.
- **Backup/restore pattern:** Edit handlers now `deepCopyRoutes` before mutation and restore on failure.
- **SQL is correct:** Column counts match scans, parameterized queries throughout, transactions properly committed/rolled back.
- **Schema migration is solid:** v1→v2→v3 with idempotent column additions, legacy table renames, and ID collision handling.

---

## Action Items (Priority Order)

1. **Fix the template panic** — `0` → `0.0` in `event_detail.html:98`. Ship-blocker.
2. **Restore append-based pagination** or add a batch summary query. The current approach will noticeably degrade with moderate event counts.
3. **Decide on `HasLegacyArchived`** — either use it in the template or stop querying for it.
4. **Add nil guard** for `route.Driver` in `HandleAddDriver` and `HandleSwapDrivers` capacity fallback.
5. **Delete dead `EventAssignment` type.**
6. **Document or normalize** the `TotalDistanceMeters` semantics change.
