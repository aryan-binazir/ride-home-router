# Explicit database migrations

## Context

Ride Home Router originally applied embedded Postgres migrations inside `server.New`. That made every application process a schema owner and left no operator command for version inspection, deliberate rollback, or deployment gating.

The repository has one application and one timestamped migration stream. It does not need bball-lab-go's service-specific streams or Kubernetes migration jobs, but it does need the same retry, dirty-state, and rollback protections.

## Decision

The `migrate` binary owns schema changes. It supports default or explicit `up`, read-only `version`, and one-step `down --confirm`.

The server binary does not apply or inspect migrations. Local `make serve` depends on `make migrate`. Deployments must run `migrate` as a one-shot pre-deploy command and start the new server revision only after it exits successfully.

Migration execution reuses golang-migrate's Postgres advisory lock. The runner caps connections and advisory-lock waits at 10 seconds, other database lock waits at nine seconds, and migration statements at five minutes. It reports clean or dirty version state. Up and down operations refuse a dirty database with recovery guidance.

Every embedded version has paired timestamped up and down files. Repository tests reject malformed or unpaired filenames and executable SQL in disabled down files. `make migrate-create name=<name>` creates both files and marks the down direction disabled. One-step down reads and validates the pending down file before calling golang-migrate, so a missing, comment-only, or disabled file cannot decrement `schema_migrations` or leave the database dirty.

Applied SQL stays immutable. The baseline's session-only `SET lock_timeout = 0` statements are removed because they disabled the runner's bounded wait on fresh databases, and its disabled down file is made inert instead of retaining destructive SQL. Neither safety change affects an applied schema.

## Consequences

- Local startup still migrates first through the Make dependency.
- Direct server startup can succeed against an unprepared schema because the runtime pool only verifies connectivity. Requests that need missing tables then fail. This matches the explicit ownership model and makes the pre-deploy gate mandatory.
- The container must ship both binaries. The platform's pre-deploy command must be configured before this ownership change is merged or deployed.
- Concurrent migration commands serialize, clean retries are idempotent, and lock waits fail within a bound.
- Dirty-state repair must restore the version matching the verified schema; clearing the dirty flag on golang-migrate's recorded target can skip a rolled-back up or misrecord a rolled-back down.
- Production rollback remains a deliberate operator action requiring a verified backup, compatible application revision, and an authored down migration. It is never an automatic deployment rollback.
