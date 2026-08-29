# Ride Home Router

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)

Self-hosted pickup and dropoff route planning for events.

> **Use at your own risk.** This app calculates routes. It does not vet drivers or verify route safety. You are responsible for both. See the [disclaimer](#disclaimer).

## How it runs

The browser is the client. A Go server provides the UI and API. Postgres stores rosters, settings, cached distances, and saved events. There is no Wails app or offline client.

The server sends address searches to Nominatim and coordinates to Google Routes. Settings are shared per deployment. Active route edits, mobile plan drafts, and spreadsheet imports stay in memory and are lost on restart.

There are no user accounts. Run the server behind an authenticating proxy such as Cloudflare Access. Do not expose it directly to the internet.

## Run locally

Requires Go 1.27. Podman runs the local Postgres 18 container. Node 24 is only needed for tests.

```bash
make postgres-up
GOOGLE_MAPS_API_KEY=... make serve
```

Open <http://127.0.0.1:8080>.

`make serve` applies pending migrations before starting the server. Migration failure stops the target before the server binds. `make postgres-up` creates development and test databases on port 5434. `make postgres-down` deletes the container and both databases.

## Configuration

| Value | Purpose |
| --- | --- |
| `DATABASE_URL` | Postgres connection string. The Makefile supplies a local default. |
| `GOOGLE_MAPS_API_KEY` | Key from a project with the Routes API enabled. Required to calculate routes. |
| `PORT` | Loopback port when `--addr` is absent. Default: `8080`. |
| `--addr` | Listen address. |
| `--allowed-hosts` | Proxy hostnames or IPs accepted in `Host` and `Origin`. Required for non-loopback listeners. |
| `ALLOWED_HOSTS` | Docker entrypoint value for `--allowed-hosts`. |

Allowed hosts omit schemes, ports, and paths. Unlisted hosts get `403`; add the platform health-check hostname when needed. Host checks are not authentication.

## Deploy

The Docker image contains the server and `migrate` binaries, supports `amd64` and `arm64`, runs the server as a non-root user, and checks `/api/v1/health`.

Set `DATABASE_URL` and `ALLOWED_HOSTS`. Add `GOOGLE_MAPS_API_KEY` for routing. The platform normally supplies `PORT`.

Configure the platform's pre-deploy command as exactly `migrate` before deploying a revision that depends on a new schema. A non-zero migration exit must stop the deployment before the new server revision starts. The direct `ride-home-router` binary does not apply or inspect migrations; against an unprepared schema it can start successfully and then return database errors from requests.

Keep the app and Postgres private. Use Cloudflare Tunnel with Access configured, or another authenticating proxy. Back up Postgres with your provider's tools.

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

For the phone-focused workflow, open `/m`. Choose the location, riders, drivers, vans, time, and route mode from the Plan tab. Calculate routes, move riders or swap drivers if needed, copy handoffs, then save the event. People, Places, and History remain available from the bottom tabs.

Pickup routes end at the activity. Dropoff routes start there. The solver respects capacity, keeps households together when possible, uses selected drivers, minimizes corridor spread, then compares completion time, detour, and drive time. It is deterministic, not globally optimal.

## Verify

```bash
make check       # all checks, requires the test database
make check-unit  # skips database-backed tests
```

## Data

Postgres stores names, addresses, coordinates, settings, cached distances, and event history. Spreadsheet parsing happens on the server, and imported addresses are geocoded automatically. Ride Home Router has no analytics or tracking.

The public Nominatim service limits requests to one per second. Google Routes billing and quotas apply.

## Disclaimer

This software is provided "as is" without warranty. Verify every driver, address, and route. You are responsible for authentication, personal data, database security, backups, third-party API use, and any harm or loss caused by deployment or use of this software.

## License

MIT
