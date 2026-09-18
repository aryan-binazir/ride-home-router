# Ride Home Router

Self-hosted event pickup and dropoff planning. Go server, browser UI, Postgres, Clerk authentication. Shared rosters, settings and history; no offline client.

## Run locally

Requires Go 1.27 and Podman. Checks also need Node 24 and `golangci-lint` from [.golangci-lint-version](.golangci-lint-version).

1. Configure a development Clerk instance; export the four `CLERK_*` values and `ADMIN_EMAILS` below.
2. Generate a key once with `openssl rand -base64 32`; export it as `CREDENTIAL_ENCRYPTION_KEY`. Keep it in a secret manager and reuse it across restarts.
3. Run:

```sh
make postgres-up
make serve
```

Open <http://127.0.0.1:8080>, sign in as an admin, and add the Google Maps key in Settings.

`make serve` migrates before starting. Postgres uses port 5434 with development and test databases. `make postgres-down` deletes both.

## Configuration

The first six values are required. Missing or malformed authentication/encryption configuration prevents startup.

| Value | Purpose |
| --- | --- |
| `CLERK_SECRET_KEY` | Clerk Backend API secret. |
| `CLERK_PUBLISHABLE_KEY` | Publishable key from the same instance. |
| `CLERK_JWT_KEY` | That instance's RSA PEM public key; multiline or literal `\n`. |
| `CLERK_AUTHORIZED_PARTIES` | Comma-separated exact browser origins; no trailing slash. Local example: `http://127.0.0.1:8080`. HTTP requires loopback. |
| `ADMIN_EMAILS` | Comma-separated verified admin emails. No empty entries or trailing commas. |
| `CREDENTIAL_ENCRYPTION_KEY` | Base64-encoded 32-byte key, identical on every replica and stored outside Postgres. |
| `DATABASE_URL` | Postgres URL; Make supplies local defaults. |
| `PORT` | Default loopback port: `8080`. |
| `--addr` | Override listen address. |
| `--allowed-hosts` | Accepted hosts; required for non-loopback listeners. Omit schemes, ports and paths. |
| `ALLOWED_HOSTS` | Docker value for `--allowed-hosts`. |
| `GOOGLE_USAGE_SEED` | Existing monthly usage, e.g. `routes=2026-09:7000,geocoding=2026-09:120`. Other months are ignored. |
| `ROUTING_ENGINE` | Default `estimate`; `matrix` is for legacy quality comparisons. |
| `TRUST_CF_ACCESS_HEADER` | Default off. Enable only behind trusted Cloudflare Access for feedback attribution. |

## Access and credentials

Enable Clerk public signup, Google sign-in and bot protection; configure production Google OAuth credentials. Signup at `/sign-in` creates an identity, not app access. Admins approve exact verified emails in Settings. Email matching ignores case/whitespace, but preserves dots and `+` aliases. All admitted users share data and settings; access and Google-key management require an admin. Change `ADMIN_EMAILS` and restart every replica to change admins.

The server verifies token signature, times, issuer and origin, then Clerk session/user status. Clerk identity checks cache for up to 30 seconds; database approvals are checked on every request. Removal blocks subsequent requests unless another verified email grants access. In-flight requests can finish. Clerk/database lookup failures deny access; Clerk outages return 503 once cached identity checks expire. Historical `verified_admin_emails` never grant access.

Only GET/HEAD requests to `/sign-in`, `/auth/config`, `/static/*`, `/healthz`, `/api/v1/health` and `/api/v1/ready` are public. Protected responses use `no-store`. Cookie writes require a configured matching Origin; API clients can use `Authorization: Bearer <Clerk session token>`. Host checks also apply.

To change Clerk instances, preserve Postgres and approved/admin emails, stop old replicas, switch all three Clerk keys together, and verify authorized origins. Users sign in again; access follows verified email, not Clerk IDs. Rotate signing keys consistently across replicas.

The Google key must allow Routes API, Geocoding API and Places API (New). Only admins can save, replace or delete it. Postgres stores it encrypted with AES-256-GCM; the UI never returns it. `GOOGLE_MAPS_API_KEY` is ignored. Replicas read changes without restart. Deleting the key prevents new provider lookups; estimate-based planning can still produce itineraries without timings.

Back up the encryption key separately. Losing it requires restoring it or setting a new key on every replica and re-entering the Google credential. Before migrating an old plaintext credential table, explicitly remove its key through the old app and stop old replicas; migration refuses populated credentials. Rollback also requires deleting the credential first and stopping writes.

## Use

Add a place, add/import riders and drivers, assign vans, choose mode/time, calculate, adjust, copy and save. `/m` provides the phone workflow. Pickup ends at the activity; dropoff starts there. Follow numbered Maps legs in order.

## How it runs

The default planner uses straight-line estimates, respects capacity, groups households where possible and balances routes; it is not globally optimal. Google measures occupied cars afterward, including a direct driver baseline for detour. Long routes split into requests with at most ten intermediate waypoints. Edits measure changed cars; reopened live plans offer per-car timings. Missing keys or exhausted usage leave itineraries available without timings.

Measured distances, durations and stop ETAs exist only in the current response. New saved events retain assignments, stop order, addresses, Maps links and user-entered schedule times, including handoff snapshots that survive roster edits. History does not fetch timings; older saved content remains unchanged.

Postgres reserves Compute Routes, Geocoding and Autocomplete attempts before dispatch, with a default ceiling of 8,000 each per Pacific-calendar month. Seed prior usage when deploying mid-month. This ledger does not cover other applications or legacy matrix requests. Matrix mode retains cached distances, a 60,000-uncached-element limit per calculation and four concurrent distance preparations. See the [routing decision](docs/adr/0006-routing-cost-and-google-terms.md).

Coordinates older than 30 days are refreshed before planning; failed refreshes retain prior coordinates. Address lookups share one request/second and cooldowns up to 15 minutes.

Postgres persists drafts, route edits and imports across restarts/replicas; no sticky sessions or app volume. Drafts/routes expire after eight idle hours; imports after 30 minutes. Limits are 256 drafts, 256 routes and four imports. New drafts/routes evict the least recently used unfinished plan when full. Concurrent route edits return `409 SESSION_CONFLICT`; reload. Event saves/import commits consume their session atomically to prevent duplicate writes.

CSV/XLSX imports geocode on the server. Parsed workflow payloads are capped at 24 MiB; valid rows can be saved despite invalid rows. Workers continue without a browser, recover after restart and allow up to nine Google attempts per unique address, with shared cooldowns. Exhausted addresses require a new import or reapplied mapping.

Deleted people/places are purged after 30 days by a daily sweep; event snapshots survive. No app analytics or tracking.

## Deploy

The [Dockerfile](Dockerfile) packages server and `migrate` binaries for amd64/arm64 and runs as non-root. Configure the required values, shared `DATABASE_URL` and `ALLOWED_HOSTS`. Keep Postgres private and backed up.

Run exactly `migrate` as the pre-deploy command; failure must block deployment. Direct server startup does not migrate. `/healthz` aliases `/api/v1/ready` and requires the exact embedded migration version; `/api/v1/health` checks database connectivity. Old images become unready after newer schema migrations. Keep image/schema versions compatible during deployment and rollback.

[railway.toml](railway.toml) configures Docker, migrations, `/healthz` and 30-second draining. Railway supplies `PORT`; include the public hostname and `healthcheck.railway.app` in `ALLOWED_HOSTS`. Keep the default start command; no app volume.

## Migrations

```sh
make migrate
make migrate-version
make migrate-create name=add_route_notes
make migrate-down CONFIRM=yes  # destructive, fixed local database only
```

Keep applied SQL immutable; add paired migrations. Generated down files remain disabled until implemented. Migration commands need no Clerk configuration. The runner serializes migrations and refuses dirty state or missing/disabled/comment-only rollbacks. `migrate down --confirm` targets `DATABASE_URL`; require a verified backup and compatible application rollback.

For dirty state, inspect `schema_migrations`, the failed SQL and actual schema. Repair the version to match the proven schema, never merely clear `dirty`: failed up/down operations may record their target before SQL completes. If uncertain, restore a verified backup. See the [migration decision](docs/adr/0002-explicit-database-migrations.md).

## Verify

```sh
make check       # lint, module checks, vet, JS and Go race tests; needs test DB
make check-unit  # same, skips database tests
make eval        # planner regression suite; several minutes
```

Set `BROWSER_TEST_BINARY` to Chrome/Chromium to enable browser tests; otherwise they skip. No running app or npm dependencies required. `make eval` compares synthetic rosters against a committed baseline and writes `_scratch/planner-eval-report.md`. CI runs it for planner-related changes; passing establishes no material regression, not optimal routes.

## License and disclaimer

[MIT](LICENSE). Provided as is, without warranty. Verify drivers, addresses and routes. You are responsible for safety, authentication, personal data, database security, backups and third-party usage.
