# Architecture

See [CONTEXT.md](../CONTEXT.md) for domain terms and the [local development runbook](runbooks/local-development.md) for a seeded environment.

## Request path

[`cmd/server`](../cmd/server) starts the Go HTTP server. [`internal/server/server.go`](../internal/server/server.go) installs panic recovery, security headers, request security, Clerk access checks, and body limits around the mux, in that order. Request security checks the Host. For writes, it also checks any supplied Origin and accepts HTMX, JSON, or form requests. The mux dispatches application requests to [`handlers.Handler`](../internal/handlers/handlers.go). Handlers call the domain stores or the repositories on `database.DataStore`. [`postgres.Store`](../internal/postgres/store.go) implements those repositories. The server wires both workflow stores to its Postgres repositories.

For dual-format actions such as [roster writes](../internal/handlers/roster_person.go), check the [`HX-Request: true`](../internal/httpx/httpx.go) form or fragment path and the JSON path. The [planner picker](../internal/handlers/planner_picker.go), [route editor page](../internal/handlers/route_editor.go), and [address search](../internal/handlers/geocoding.go) return HTML on success. Address search requires HTMX.

[`web/embed.go`](../web/embed.go) embeds the templates, partials, CSS, and scripts. Phone layouts use those same pages. `/m` bookmarks [redirect to the shared pages](../internal/server/server.go). The [browser tests](../internal/handlers/planner_browser_test.go) cover 390px and desktop widths with Chromium over [CDP](../web/static/js/frontend-browser.test.js).

## Packages

| Package | Owns | ADR |
| --- | --- | --- |
| [`internal/access`](../internal/access) | Clerk token and identity checks, email approval, and access management. | |
| [`internal/access/accesstest`](../internal/access/accesstest) | Local Clerk-compatible auth fixture for Go tests. | |
| [`internal/credentials`](../internal/credentials) | Encryption and decryption for stored credentials. | |
| [`internal/database`](../internal/database) | Repository contracts, workflow records, and shared persistence errors. | [0007](adr/0007-durable-roster-import-lifecycle.md), [0008](adr/0008-route-session-transition-ownership.md) |
| [`internal/database/workflowtest`](../internal/database/workflowtest) | In-memory `WorkflowRepository` fake for tests. | |
| [`internal/distance`](../internal/distance) | Solver distance estimates and the legacy Google matrix provider. | [0006](adr/0006-routing-cost-and-google-terms.md) |
| [`internal/eventsnapshot`](../internal/eventsnapshot) | Converts route results into versioned event snapshots. | [0006](adr/0006-routing-cost-and-google-terms.md) |
| [`internal/geocoding`](../internal/geocoding) | Google address matching, autocomplete, and retries. | [0006](adr/0006-routing-cost-and-google-terms.md) |
| [`internal/handlers`](../internal/handlers) | Page and API adapters, request validation, and application orchestration. | See workflows and change table below. |
| [`internal/httpx`](../internal/httpx) | HTTP helpers for HTMX detection, JSON decoding, and origin checks. | |
| [`internal/importer`](../internal/importer) | CSV and XLSX parsing, mapping, review, and durable geocoding and commit. | [0007](adr/0007-durable-roster-import-lifecycle.md), [0012](adr/0012-import-review-ownership.md), [0015](adr/0015-import-commit-preparation.md), [0017](adr/0017-roster-workbook-intake.md) |
| [`internal/logutil`](../internal/logutil) | Escapes request-derived strings for logging. | |
| [`internal/models`](../internal/models) | Shared domain records, route modes, and roster identity normalization. | |
| [`internal/orderedroute`](../internal/orderedroute) | Google Compute Routes client for measuring fixed stop orders. | [0006](adr/0006-routing-cost-and-google-terms.md) |
| [`internal/postgres`](../internal/postgres) | SQL repositories for application data, workflow transactions, and provider usage and cooldown. | [0002](adr/0002-explicit-database-migrations.md), [0007](adr/0007-durable-roster-import-lifecycle.md), [0008](adr/0008-route-session-transition-ownership.md), [0014](adr/0014-roster-create-ownership.md) |
| [`internal/postgres/postgrestest`](../internal/postgres/postgrestest) | Isolated Postgres schemas and migrations for tests. | |
| [`internal/routefeedback`](../internal/routefeedback) | Reviewer attribution and feedback records comparing proposed and final routes. | [0001](adr/0001-capture-route-feedback-as-a-best-effort-side-record.md) |
| [`internal/routesession`](../internal/routesession) | Route state, edits, reset, and event commit. | [0008](adr/0008-route-session-transition-ownership.md) |
| [`internal/routing`](../internal/routing) | Balanced route solving, stop ordering, and timing projection. | [0006](adr/0006-routing-cost-and-google-terms.md) |
| [`internal/routing/planeval`](../internal/routing/planeval) | Synthetic planner scenarios, quality metrics, and baselines. | |
| [`internal/server`](../internal/server) | Dependency wiring, middleware, mux, and process lifecycle. | |
| [`internal/templates`](../internal/templates) | Loads and renders page and fragment templates. | |
| [`internal/templateutil`](../internal/templateutil) | Functions available to Go HTML templates. | |

## Planning

1. **Calculate.** [`routes.go`](../internal/handlers/routes.go) handles calculation requests. [`route_calculation.go`](../internal/handlers/route_calculation.go) loads the selected records and vans, refreshes stale coordinates when possible, and calls [`balanced_router.go`](../internal/routing/balanced_router.go). [`routing/doc.go`](../internal/routing/doc.go) describes the phases and their lexicographic objective.
2. **Start a route session.** A successful solve creates a session through [`routesession.Store`](../internal/routesession/persistent.go). The store uses one implementation and receives a Postgres `WorkflowRepository` in production. For tests that need a session without Postgres, pass [`workflowtest.New()`](../internal/database/workflowtest/repository.go) to the same constructor. Use Postgres for expiry, capacity, and atomic event writes.
3. **Edit.** [`event-planner.js`](../web/static/js/event-planner.js) owns current plan eligibility and queues browser edits. The [form adapter](../internal/handlers/route_editor.go) and [JSON adapters](../internal/handlers/route_edit.go) share move, swap, and add execution. The store loads the session, applies the change, and saves it with a revision check. A stale edit can return `409 SESSION_CONFLICT`. See [planner lifecycle](adr/0004-desktop-planner-lifecycle.md), [plan selection](adr/0011-plan-selection-ownership.md), [edit orchestration](adr/0016-route-edit-orchestration.md), and [edit execution](adr/0018-route-editor-execution.md).
4. **Save.** [`events.go`](../internal/handlers/events.go) builds the event from the live session's final routes. [Workflow SQL](../internal/postgres/workflows.go) [writes the event](../internal/postgres/workflow_writes.go) and consumes the session in one transaction. [`eventsnapshot`](../internal/eventsnapshot/snapshot.go) omits provider metrics.

## Imports

1. [`imports.go`](../internal/handlers/imports.go) accepts an XLSX workbook or CSV. The user selects a sheet when needed and maps columns. [`importer.Store.ApplyMapping`](../internal/importer/persistent.go) validates rows against the roster and stages rows and geocoding jobs together.
2. Workers use [`postgres/import_jobs.go`](../internal/postgres/import_jobs.go) to claim jobs with leases and write address results back to the staged rows. [`imports_panel.go`](../internal/handlers/imports_panel.go) renders progress, paged review, and row selections.
3. The importer rejects a commit while geocoding is pending. It applies the final selection, upserts the roster, and consumes the workflow in the same transaction. See [`importer/persistent.go`](../internal/importer/persistent.go) and the [workflow writes](../internal/postgres/workflow_writes.go).

## Where to change things

Set the Postgres and browser variables from the [local gate](../AGENTS.md#verification-before-merging) when running focused checks that use them.

| To change | Edit | Run |
| --- | --- | --- |
| Routes, methods, middleware | [`server.go`](../internal/server/server.go), then the relevant handler | `go test ./internal/server ./internal/handlers` |
| Roster people | Shared behavior in [`roster_person.go`](../internal/handlers/roster_person.go). Per-kind options live in the [`participantRoster`](../internal/handlers/participants.go) and [`driverRoster`](../internal/handlers/drivers.go) tables. See [edit preparation](../internal/handlers/roster_edit.go) ([0005](adr/0005-roster-edit-preparation.md)), [paging](../internal/handlers/roster_paging.go) ([0010](adr/0010-roster-read-ownership.md)), and [manual writes](adr/0014-roster-create-ownership.md). | `go test ./internal/handlers ./internal/postgres` |
| Plan selection, restore, or van assignment | [`event-planner.js`](../web/static/js/event-planner.js), [`planner_picker.go`](../internal/handlers/planner_picker.go), and [van resolution](../internal/handlers/org_vehicle_assignments.go) ([ADR 0013](adr/0013-van-assignment-resolution.md)) | `go test ./internal/handlers` and `node --test web/static/js/*.test.js` |
| Solver, scoring, or stop order | Phase files under [`internal/routing`](../internal/routing), starting with the [package doc](../internal/routing/doc.go). | `go test ./internal/routing -run TestBalancedRouterSeededInvariants` and `make check-branch` |
| Route editor, event save, import lifecycle, or workflow SQL | Follow the [planning](#planning) or [import](#imports) flow above. | `make check-branch` |
| Handoff wording | Live copy text in [`event-planner.js`](../web/static/js/event-planner.js). Saved text in [`event_handoff.go`](../internal/handlers/event_handoff.go). | `go test ./internal/handlers -run Handoff` and `node --test web/static/js/event-planner.test.js` |
| Clerk identity cache, JWT checks, or provider cooldown | [`access.go`](../internal/access/access.go) or [`provider_throttle.go`](../internal/postgres/provider_throttle.go) | `make check-branch` |
| Database schema | Add a paired migration in [`migrations/`](../migrations). Applied SQL is immutable. See [ADR 0002](adr/0002-explicit-database-migrations.md). | `make check-branch` |
| Templates, CSS, or browser scripts | [`web/templates`](../web/templates), [`web/static`](../web/static), and the handler that renders the page or fragment | `go test ./internal/handlers` and `node --test web/static/js/*.test.js` |
| Verification gates | [`Makefile`](../Makefile), [`scripts/`](../scripts), and [`verification/`](../verification) | `make check-branch` |

Use the [full local gate in AGENTS.md](../AGENTS.md#verification-before-merging) after focused checks. Planner behavior changes also need `make eval`, which compares synthetic rosters against the committed baseline and writes `_scratch/planner-eval-report.md`. For covered changes, [`verify-workflow-tla`](../.agents/skills/verify-workflow-tla/SKILL.md) and the [verification guide](../verification/README.md) locate the affected code and checks. `make check-branch` already includes the planner evaluation.

`make check-unit` clears `TEST_DATABASE_URL`. The [Postgres fixture](../internal/postgres/postgrestest/postgrestest.go) skips those tests, which can leave a green run with no SQL coverage. The [full race runner](../scripts/test_go.py) rejects unexpected skips. Run one full gate at a time. If a timing test fails when `uptime` reports load around 30 or higher, rerun once at lower load before investigating.
