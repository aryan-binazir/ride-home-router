# Ride Home Router

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)

Self-hosted pickup and dropoff route planning for events.

> **Use at your own risk.** This app calculates routes. It does not vet drivers or verify route safety. You are responsible for both. See the [disclaimer](#disclaimer).

## How it runs

The browser is the client. A Go server provides the UI and API. Postgres stores rosters, settings, cached distances, and saved events. There is no Wails app or offline client.

The server uses Google Maps Platform for everything location related: Places Autocomplete for address suggestions, the Geocoding API for coordinates, and the Routes API for distances. One Google Maps key, configured by an admin in Settings, covers all three. Settings are shared per deployment. Postgres also stores active route edits, mobile plan drafts, and spreadsheet imports. Any application instance can continue them after a restart; no sticky sessions or application volume are needed. The browser retains desktop selections and UI preferences.

Clerk authenticates every application page, fragment, API, import, export, and mutation on the server. Only verified emails listed in `ADMIN_EMAILS` or approved in Postgres have access. All approved users share the same functionality and data; only access management is admin-only. Host checks are additional request validation.

Import workflow payloads are limited to 24 MiB after parsing; oversized uploads return HTTP 413. Headers and file warnings remain available in previews, and invalid rows do not block saving valid rows. Provider calls have a 30-second work budget; temporary failures remain pending and retry after at least 30 seconds and any shared provider cooldown. Jobs stop when the import expires. Process shutdown leaves the job recoverable. Shared `Retry-After` deadlines are honored across instances and expire automatically; restarting the app does not bypass an upstream cooldown.

Every response sends X-Content-Type-Options, X-Frame-Options, Referrer-Policy, Permissions-Policy, and a Content-Security-Policy that blocks framing, external base URLs, and embedded objects. Inline scripts and styles remain supported.

Database connections default to a 30-second statement timeout and a 60-second idle transaction timeout. Explicit connection-string settings take precedence.


## Run locally

Requires Go 1.27. Podman runs the local Postgres 18 container. Node 24 is only needed for tests.

```bash
make postgres-up
# Export the Clerk settings below first, using a development Clerk instance.
make serve
# Sign in as an admin and add the Google Maps key in Settings.
```

Open <http://127.0.0.1:8080>.

`make serve` applies pending migrations before starting the server. Migration failure stops the target before the server binds. `make postgres-up` creates development and test databases on port 5434. `make postgres-down` deletes the container and both databases.

## Configuration

| Value | Purpose |
| --- | --- |
| `CLERK_SECRET_KEY` | Required Clerk Backend API secret. Server only. |
| `CLERK_PUBLISHABLE_KEY` | Required publishable key from the same Clerk instance. |
| `CLERK_JWT_KEY` | Required RSA PEM JWT public key from that instance. Multiline or literal `\n` accepted. |
| `CLERK_AUTHORIZED_PARTIES` | Required comma-separated exact browser origins, e.g. `https://router.example.com`. No trailing slash. HTTP allowed only for loopback development. |
| `ADMIN_EMAILS` | Required comma-separated admin emails. Whitespace trimmed, case normalized. At least one valid email required. |
| `DATABASE_URL` | Postgres connection string. The Makefile supplies a local default. |
| `TRUST_CF_ACCESS_HEADER` | Set to `true` only behind a trusted Cloudflare Access proxy to use its email header for route feedback attribution. Default: off. |
| `PORT` | Loopback port when `--addr` is absent. Default: `8080`. |
| `--addr` | Listen address. |
| `--allowed-hosts` | Proxy hostnames or IPs accepted in `Host` and `Origin`. Required for non-loopback listeners. |
| `ALLOWED_HOSTS` | Docker entrypoint value for `--allowed-hosts`. |

Allowed hosts omit schemes, ports, and paths. Unlisted hosts get `403`; add the platform health-check hostname when needed. Host checks are not authentication.

## Deploy

The Docker image contains the server and `migrate` binaries, supports `amd64` and `arm64`, runs the server as a non-root user, and checks `/api/v1/ready`. The readiness endpoint requires the applied database migration version to exactly match the image's latest embedded migration. `/api/v1/health` remains a database connectivity check for liveness.

Set `DATABASE_URL`, `ALLOWED_HOSTS`, and all five Clerk/access variables above. After signing in as an admin, configure the Google Maps API key in Settings. The key's Google Cloud project must have the Routes API, Geocoding API, and Places API (New) enabled, and any API restriction on the key must allow all three; without them address suggestions and saves report that address lookup is not configured. Address suggestions display the Google Maps logo as Google's terms require. The platform normally supplies `PORT`.

Configure the platform's pre-deploy command as exactly `migrate` before deploying a revision that depends on a new schema. A non-zero migration exit must stop the deployment before the new server revision starts. The direct `ride-home-router` binary does not apply migrations or gate startup on them; against an unprepared schema it can start successfully but remains unready and returns database errors from application requests.

Keep Postgres private and back it up with your provider's tools. Missing or malformed authentication configuration prevents application startup. Migration commands do not require Clerk configuration.

### Railway

The checked-in `railway.toml` uses the Dockerfile, runs `migrate` before deployment, gates activation on `/api/v1/ready`, and allows 30 seconds for draining. Set `DATABASE_URL` to the shared Postgres service and `ALLOWED_HOSTS` to the application hostname plus `healthcheck.railway.app`. Railway supplies `PORT`. Configure the five Clerk/access variables above on the application service, with the same values on every replica. Keep the image's default start command and do not attach an application volume. See [Railway configuration](https://docs.railway.com/config-as-code/reference) and [healthchecks](https://docs.railway.com/deployments/healthchecks).

Replicas must run compatible application/schema versions against the same database. Readiness deliberately requires the exact migration version: after a pre-deploy migration, older images report unready. Railway checks readiness before activating the replacement, not continuously. Other load balancers must account for this transition. This first transition cannot recover drafts still held by an older, in-memory server; save those as events before upgrading.

### Accounts and access

Enable public account creation and the Google social connection in the Clerk instance. Configure Google OAuth credentials for production and ensure any required account fields can be supplied during the sign-in flow. Enable Clerk's bot protection. Users sign in or create a Clerk identity at `/sign-in`; this alone grants no application access. An admin adds the exact Google-verified email in Settings, and the backend rejects every protected request unless that email is approved. Gmail and Google Workspace addresses both work; configure other Clerk sign-in methods according to your deployment policy. The application does not send invitations or provision accounts.

`ADMIN_EMAILS=first@example.com,second@example.com` grants both configured admins access automatically. Match any verified email returned by Clerk, including secondary verified addresses. Neither JWT email/custom-role claims, request headers, nor Clerk user-editable metadata grants access or admin status. In Settings, admins can add or remove approved non-admin emails. Ordinary users can use all other settings and shared app functions. There is no UI to grant admin status. Change `ADMIN_EMAILS` in Railway and restart all replicas to change administrators. Successful admin authentication records the matching verified email in `verified_admin_emails` with its first verification time. These historical records are retained for migration, never used to grant access, and not editable through Settings. Removing an admin from the environment removes their access unless their email was separately approved as a non-admin. Email matching ignores case and surrounding whitespace, but does not merge Gmail dots or `+` aliases; approve the exact address returned by Google.

Every protected request uses Clerk's Go SDK to verify the RSA signature and token times, checks the exact issuer derived from the publishable key and the authorized browser origin, then checks session status and verified user emails through Clerk's Backend API, using a bounded per-instance cache for up to 30 seconds. Pending/revoked sessions and banned/locked users are rejected. Non-admin approvals are queried from Postgres on every request with no instance-local cache. Removing an approved email blocks subsequent requests using that email on every instance, including already signed-in sessions. A user with another approved verified email still has access. Requests already authorized and in progress can finish.

Clerk session and user status is re-checked at most every 30 seconds; approval is checked on every request. Each cache miss costs two Clerk API calls. Clerk API or approval-database failures deny access; valid cached identities can continue until their 30-second expiry. Admin-record persistence failures are logged and do not deny access. With open account creation, unapproved users can obtain valid sessions and consume those API calls before admission is denied; account-creation bot protection reduces abuse but is not a request rate limiter. The public JWT key avoids remote key fetches for forged requests. Rolling back this migration deletes approved-email records; back them up first, and never expose an older image without authentication. When rotating the Clerk signing key, update `CLERK_JWT_KEY` on all replicas and redeploy them together. Old or mismatched keys fail closed.

Only GET/HEAD requests to `/sign-in`, `/auth/config` (publishable configuration only), `/static/*`, `/api/v1/health`, and `/api/v1/ready` bypass authentication. Unknown routes are protected by default. API denials return generic 401/403 errors; HTML navigation redirects to sign-in and HTMX receives a full-page redirect without protected fragments. Protected responses use `Cache-Control: no-store`. ClerkJS refreshes the session cookie on desktop and mobile pages. Cookie-authenticated writes require a configured, matching `Origin`; direct API clients can instead send a Clerk session token as `Authorization: Bearer ...`. Existing Host, content-type, and origin checks also apply. Authentication and admission run before request bodies are buffered, including multipart imports; admitted requests still enforce the existing body-size limits.

Implementation follows the verified-email and strict issuer/origin patterns in bball-lab-go and the session-aware sign-in flow in bball-lab-ts, using the [supported Clerk Go verification API](https://clerk.com/docs/guides/sessions/verifying) and [ClerkJS SignIn](https://clerk.com/docs/js-frontend/reference/components/authentication/sign-in) without React.

### Moving to another Clerk instance

Keep the same Postgres database, including `approved_emails` and `verified_admin_emails`, and retain the intended `ADMIN_EMAILS` configuration. Provision the new Clerk instance with Google sign-in and public account creation as above, without relying on old Clerk user or session IDs. Treat the Clerk Dashboard, Backend API secret, and any account-import workflow as trusted identity infrastructure: an operator who can assert ownership of an approved email can effectively grant that identity access. Only preserve verified ownership you can trust during import.

Switch `CLERK_SECRET_KEY`, `CLERK_PUBLISHABLE_KEY`, and `CLERK_JWT_KEY` together on every replica, and confirm `CLERK_AUTHORIZED_PARTIES` matches the app origin. Drain/stop old replicas before exposing the new configuration; do not run mixed old/new authentication configurations through the cutover. Old-instance tokens must stop working. Users sign in again with Google on the new instance; a new Clerk user ID with the same verified approved email retains app access. Unapproved users remain denied. Stored historical admin emails do not grant admin rights, and app data requires no identity-ID migration because it is shared.

### Concurrent planning

Each mobile browser cookie and desktop route-session ID identifies a separate plan. Shared roster records, settings, and saved history remain shared. Route edits use optimistic concurrency: if another request saves the same route session during calculation, the stale request receives `409 SESSION_CONFLICT` and must reload. Event saves and import commits consume their session in the same transaction as the data write, so retries cannot insert a second result. This does not add account ownership or collaborative editing of shared roster forms.

Idle mobile drafts expire after eight hours, route sessions after eight hours, and imports after thirty minutes. Restarts preserve work within those limits. A bounded cleanup sweep removes expired records. The deployment permits 256 live mobile drafts, 256 live route sessions, and four live import sessions; new drafts and routes replace the least recently used unfinished plan of the same kind when full; imports keep their hard capacity limit. Completed session markers survive until expiry to reject duplicate saves.

Import rows and address jobs are separate database records. Workers claim one address for up to one minute and geocode outside transactions. Shutdown releases a claim; after an abrupt failure, another instance retries it when the lease expires. Imports continue without an open browser tab while at least one instance is running. All instances share one Google cooldown: when Google answers with a quota or availability error, every instance pauses address lookup until the cooldown passes. Google Routes distance caching remains in Postgres.

## Database migrations

The paired timestamped SQL files in `migrations/` are the Postgres schema history. Clean retries skip versions already recorded in `schema_migrations`. Concurrent runners serialize through golang-migrate's Postgres advisory lock, with a 10-second advisory-lock wait, a nine-second default wait for other database locks, and a five-minute statement limit. A migration that deliberately needs longer for a table lock can use `SET LOCAL lock_timeout`, but it remains subject to the statement limit.

Use the local database defaults through Make:

```bash
make migrate
make migrate-version
make migrate-create name=add_route_notes
```

`make migrate-create` creates one `.up.sql` and one `.down.sql` file. The generated down file is disabled until it is replaced with a real, tested rollback. Keep applied migration files immutable. Add a new fix-forward migration instead of editing deployed schema history. The current history has two safety-only exceptions: removal of the baseline's session-only `SET lock_timeout`, and removal of executable SQL from its disabled down file. Neither changes an applied schema.

Down migrations are destructive. The Make target is pinned to the fixed local development URL and requires explicit confirmation:

```bash
make migrate-down CONFIRM=yes
```

It rolls back exactly one version and preflights the down file before changing migration state. Missing, disabled, and comment-only down files are refused. The lower-level `migrate down --confirm` command uses the loaded `DATABASE_URL`; do not run it against a database you intend to keep without a verified backup and matching application rollback. If rollback succeeds but the follow-up version read fails, the error explicitly says the rollback already applied; inspect the database instead of retrying blindly.

A failed migration can leave `schema_migrations` dirty. Later up or down operations refuse that state and report the version. Inspect `SELECT version, dirty FROM schema_migrations;`, the failed statement, and the database contents. Do not simply clear `dirty`: golang-migrate records the target version before running SQL, so a rolled-back transaction can leave that target recorded even though the schema stayed at its prior version.

After proving the migration transaction fully rolled back, repair the row to match the verified schema. For a failed up to version `V`, restore the previous applied version `P` with `UPDATE schema_migrations SET version = P, dirty = false WHERE version = V AND dirty = true;`. If the failed up was the first migration and the schema is still empty, use `DELETE FROM schema_migrations WHERE version = V AND dirty = true;`. For a failed down from `V` toward `P`, restore `V` with `UPDATE schema_migrations SET version = V, dirty = false WHERE version = P AND dirty = true;`. Require the statement to affect exactly one row, then run `migrate version` before retrying. If any schema change remains, the direction or versions are uncertain, or the repair affects anything other than one dirty row, stop and restore a verified backup. Never add `IF NOT EXISTS` guards to hide a partial migration.

## Use

1. Add an activity location.
2. Add or import participants and drivers.
3. Add shared vans if needed.
4. Select riders, drivers, mode, time, and van assignments.
5. Calculate, adjust, copy, and optionally save the routes.

For the phone-focused workflow, open `/m`. Choose the location, riders, drivers, vans, time, and route mode from the Plan tab. Calculate routes, move riders or swap drivers if needed, copy handoffs, then save the event. People, Places, and History remain available from the bottom tabs. New saved events retain the exact driver and parent handoffs, including times and Maps links, even after roster edits. Longer routes use numbered Maps legs; follow them in order. Older events retain their existing history text.

Pickup routes end at the activity. Dropoff routes start there. The solver respects capacity, keeps households together when possible, uses selected drivers, minimizes corridor spread, then compares completion time, detour, and drive time. It is deterministic, not globally optimal.

## Verify

```bash
make check       # all checks, requires the test database
make check-unit  # skips database-backed tests
```

Set `BROWSER_TEST_BINARY` to a Chrome/Chromium executable to include the actual-browser mobile filter tests, for example `BROWSER_TEST_BINARY=/usr/bin/chromium make check`. They use temporary files and synthetic responses, with no running app server or npm dependencies. Without that variable, these browser tests are skipped.

## Data

Postgres stores names, addresses, coordinates, settings, cached distances, and event history. Spreadsheet parsing happens on the server, and imported addresses are geocoded automatically. Ride Home Router has no analytics or tracking.

Deleted people and places are removed permanently after 30 days. Saved event snapshots survive roster cleanup.

Address lookups share a one-request-per-second budget and honor cooldowns up to 15 minutes. Google Routes billing and quotas apply; each route calculation permits at most 60,000 uncached distance elements, with at most four calculations preparing distances concurrently.

## Disclaimer

This software is provided "as is" without warranty. Verify every driver, address, and route. You are responsible for authentication, personal data, database security, backups, third-party API use, and any harm or loss caused by deployment or use of this software.

## License

MIT

Authentication troubleshooting: empty entries (including trailing commas) in `ADMIN_EMAILS` or `CLERK_AUTHORIZED_PARTIES` are rejected at startup. Authentication logs report fixed failure categories without tokens or raw Clerk responses; access changes log the verified Clerk actor ID and target email. Clerk lookup outages return 503 without a sign-in redirect. Mobile POST forms refresh their session before submitting; planner mutations retry at most once after a 401 and preserve the current page when sign-in is required. Network failures and 5xx responses are never automatically retried.

### Google Maps credential

Administrators can save, replace, or delete the Google Maps API key in Settings. The Google Cloud project must have the Routes API enabled. The credential lives only in Postgres, separately from public preferences. `GOOGLE_MAPS_API_KEY` is no longer read; existing installations must enter their key in Settings after this migration. There is no environment fallback or automatic import.

Only admins can access credential status or management endpoints. After saving, the UI shows a fixed mask and Configured; it never returns the key or a prefix, including to admins. The replacement field is empty after each save. Deleting the key disables new route calculations that require the provider, including cached nonzero distances, until an admin saves a replacement. Requests already in flight may complete. Every server instance reads the current database value without a process-local key cache or restart.

The application must be able to read the credential to call Google, so this is write-only access through the application, not a one-way hash. Database operators and backups remain trusted and must be protected as secrets. The key is not part of public settings, app exports or workflow data. Credential queries and writes report generic errors; no keys are written to application logs. The migration CLI checks for a configured key before beginning a rollback, so refusal leaves the current migration version clean. Explicitly delete the key first if rolling back is intended. The SQL migration also refuses to drop a populated table; stop application writes during rollback, since a concurrent key save or an out-of-band migration runner can still trigger that SQL guard and require migration-state repair.
