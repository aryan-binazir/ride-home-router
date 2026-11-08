---
name: verify-workflow-tla
description: Verify durable import jobs, route-session edits/saves and workflow capacity with TLC and real-code replay/mutation gates. Use when changing their stores, SQL, event saving or migrations, or checking concurrent workflow guarantees.
---

# Verify durable workflows

Read `verification/README.md` for the promises, harm, code coverage, bounds and assumptions. Sync commit: `effb501f54071ab9f70e2a4b2a9208d93bf3aac9`, merged #137. Move it forward only after confirming the models describe the new code.

## Check whether coverage changed

```sh
git diff effb501f54071ab9f70e2a4b2a9208d93bf3aac9 -- internal/postgres/import_jobs.go internal/postgres/workflows.go internal/postgres/workflow_writes.go internal/postgres/events.go internal/importer/persistent.go internal/importer/store.go internal/importer/selection_patch.go internal/routesession/persistent.go internal/routesession/store.go internal/handlers/events.go migrations/
```

Follow moved/renamed functions and find new writers to `import_jobs`, `import_rows.payload/selected`, `workflow_sessions.revision/consumed` and `events.route_session_id`. A clean diff does not discover new writers elsewhere.

## Run

```sh
make tla
make verification-mutations
make check-branch
```

The last command is the full local branch gate and requires `TEST_DATABASE_URL` pointing to a local test Postgres database and `BROWSER_TEST_BINARY` pointing to Chromium. It includes both preceding commands, the existing full checks and planner evaluation. See `verification/README.md` for tools, resource limits and retained evidence. A filtered TLC or mutation run is partial evidence.

Read every TLC result with `/statechart`. Replay violations against actual code before calling them product bugs. Queue safety allows lease expiry in every phase; queue progress has an explicit stronger scheduling assumption. The `arbitrary-stalls-progress` control demonstrates that assumption boundary.

## Maintain

1. Read the code before changing the model. One atomic SQL operation/transaction is one action; unlocked work and separate transactions remain separate actions.
2. Preserve the property unless a documented intended promise changed. If every counterexample step can occur, report a product finding. If a step is impossible, correct the model and explain why. Keep product fixes separate from verification updates.
3. Add a negative model control for a new promise, and a real-code replay at the repository/handler interface. A model guard mutation alone does not link the check to Go/SQL.
4. Extend `verification/code-mutations.json` when a real guard needs a repeatable compiler-overlay control. Require a clean passing test and the named assertion failure from its mutant; compilation errors, unrelated failures and skipped database tests are not kills.
5. After reviewing coverage, guards, properties and bounds, run `python3 scripts/tla_gate.py --refresh-manifest`, then the full branch gate. Do not refresh to hide accidental coverage reduction. Update the sync commit here and in `verification/README.md` only for code actually checked.

For accumulated drift, use `/maintain-formal-verification-skill`. A passing handwritten model remains bounded evidence, not a proof of arbitrary implementation changes or browser response delivery.
