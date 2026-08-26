# Ride Home Router

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)

A web app that optimizes driver assignments for getting people home after events. Perfect for community groups, religious organizations, schools, or any gathering where you need to coordinate rides. Coordinators use it from any browser; the Go server and its Postgres database run wherever you host them.

> **Disclaimer:** This software only calculates routes—it does not vet drivers. You are responsible for screening drivers and verifying all routes. Use at your own risk. See [full disclaimer](#disclaimer).

## Table of Contents

- [The Problem It Solves](#the-problem-it-solves)
- [Features](#features)
- [Privacy](#privacy)
- [Running It](#running-it)
- [Deploying It](#deploying-it)
- [Google Routes API Setup](#google-routes-api-setup)
- [Usage](#usage)
- [Technical Details](#technical-details)
- [API Usage & Limits](#api-usage--limits)
- [Disclaimer](#disclaimer)
- [Contributing](#contributing)
- [License](#license)

## The Problem It Solves

Whether you're getting people home after an event or picking them up beforehand, you have:
- A list of participants who need rides
- Several drivers willing to help
- Limited vehicle capacity

Manually figuring out who goes with whom is tedious and often results in unfair routes where one driver does most of the work. Ride Home Router does the math for you—getting everyone home as quickly as possible while balancing the load across all drivers.

## Features

- **Pickup & Dropoff Modes** — Calculate routes for either direction: picking people up (driver home → participants → activity) or dropping them off (activity → participants → driver home)
- **Smart Route Optimization** — Balances routes across all drivers to minimize the longest route time, ensuring everyone gets home quickly
- **Household Grouping** — Participants from the same address automatically ride together
- **Capacity Aware** — Respects each driver's vehicle capacity
- **Van Support** — Save shared vans and assign them to drivers during planning when personal vehicles are not enough
- **Address Autocomplete** — Search and select addresses with live suggestions from OpenStreetMap
- **Google Drive-Time Routing** — Uses Google Routes for route distance and duration calculations
- **Multiple Activity Locations** — Save different starting points (office, place of worship, school, etc.)
- **Event History** — Keep records of past events for reference
- **Manual Adjustments** — Move participants between routes or swap drivers after calculation
- **Preview Routes** — Open any route directly in Google Maps
- **Copy to Clipboard** — Export routes as text or Google Maps links
- **Distance Units** — Toggle between kilometers and miles
- **Spreadsheet Import** — Import participant and driver rosters from CSV/XLSX

## Privacy

Names, addresses, and event history live in the Postgres database you host. During route calculation, coordinates are sent to Google Routes; during address search, the search text is sent to Nominatim. Spreadsheet imports are parsed on the server; rows that include coordinates are never sent to a geocoding service.

The external services used are:
- **Google Routes API** — Calculates driving distances and durations between coordinates. Requires a Google Maps API key in the server environment.
- **Nominatim** — OpenStreetMap geocoder (converts addresses to coordinates)

No Ride Home Router account. No tracking.

**The server has no authentication of its own.** It is designed to run behind an access layer that authenticates coordinators—Cloudflare Tunnel with Zero Trust is the intended setup. Never expose it directly to the internet.

---

## Running It

Requirements: Go 1.27, Node 22 (JS tests only), and Postgres 18 (podman for the local container).

```bash
make postgres-up                 # local Postgres 18 in podman on port 5434
GOOGLE_MAPS_API_KEY=... make serve
```

`make serve` runs the server on `http://127.0.0.1:8080` against the local Postgres (`DATABASE_URL` overrides it). Pending schema migrations are applied at startup.

### Configuration

| Setting | How | Notes |
| --- | --- | --- |
| `DATABASE_URL` | env, required | Postgres connection string |
| `GOOGLE_MAPS_API_KEY` | env | Enables route calculation; address search works without it |
| `PORT` | env | Loopback port when `--addr` is not given (default `8080`) |
| `--addr` | flag | Listen address, e.g. `0.0.0.0:8080` in a container |
| `--allowed-hosts` | flag | Comma-separated public hostnames the tunnel or proxy forwards. **Required** for any non-loopback `--addr` |

Requests whose `Host` (or `Origin`, for writes) is not loopback or one of `--allowed-hosts` are rejected with `403`, so a stray public domain in front of the server serves nothing.

### Verifying

```bash
make check        # lint, go mod verify, vet, JS + Go tests (needs TEST_DATABASE_URL, set by make for the local Postgres)
make check-unit   # same without the database-backed tests
```

Database-backed tests create a throwaway schema per test in `TEST_DATABASE_URL`, migrate it, and drop it afterwards.

---

## Deploying It

The `Dockerfile` builds a static binary into a non-root Alpine image. The container expects:

- `DATABASE_URL` — your Postgres
- `ALLOWED_HOSTS` — the public hostname(s) served by the tunnel, e.g. `rides.example.org`
- `GOOGLE_MAPS_API_KEY` — optional
- `PORT` — set by the platform; the server binds `0.0.0.0:$PORT`

A `HEALTHCHECK` polls `/api/v1/health`.

The intended shape is Railway (or any host) with **no public domain on the app service**, plus a `cloudflared` sidecar that connects the tunnel to the app over the private network, with Cloudflare Access policies deciding who gets in. If your platform performs HTTP health checks with its own `Host` header, add that hostname to `ALLOWED_HOSTS` too.

Known limitations of the hosted setup:
- Settings (selected activity location, units) are shared by everyone using the deployment; it is designed for one coordinator at a time.
- In-progress route edits and spreadsheet imports are held in server memory and are lost on restart or deploy. Saved events are in Postgres.
- Back up the Postgres database with your provider's tooling; the app does not export data.

---

## Google Routes API Setup

Route calculation uses Google's Routes API. Address search uses Nominatim/OpenStreetMap.

1. Create or choose a Google Cloud project.
2. Enable the **Routes API** for that project.
3. Create a Google Maps API key with permission to call the Routes API.
4. Set `GOOGLE_MAPS_API_KEY` in the server environment and restart.

If no key is configured, route calculation fails with a clear error; address autocomplete still works. Distance results are cached in Postgres, so rotate the key and clear the `distance_cache` table if you switch providers.

---

## Usage

### Quick Start

1. **Add an Activity Location** — Save where your events happen on the Activity Locations page
2. **Add Participants** — People who need rides (or import a spreadsheet)
3. **Add Drivers** — People with vehicles, including their capacity
4. **Add Vans** (Optional) — Save shared vans on the Vans page for overflow events
5. **Calculate Routes** — Select participants, drivers, activity location, optional van assignments, and mode, then click Calculate

### Workflow

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ Locations & │ ──▶ │ Add People  │ ──▶ │  Calculate  │
│    Vans     │     │  & Drivers  │     │   Routes    │
└─────────────┘     └─────────────┘     └─────────────┘
                                               │
                    ┌─────────────┐            │
                    │ Save Event  │ ◀──────────┘
                    │  (optional) │
                    └─────────────┘
```

### Tips

- **Route Modes**: Use **Dropoff** mode after events (activity → homes → driver home) or **Pickup** mode before events (driver home → homes → activity).
- **Vans**: Drivers use personal vehicles by default. Assign a saved van to a selected driver when you need extra seats for that event.
- **Editing Routes**: After calculation, you can manually move participants between routes or swap drivers if needed.
- **Google Maps Links**: Click "Copy with Maps Link" to get directions you can paste into Google Maps.

---

## Technical Details

### How the Algorithm Works

**Goal:** Minimize when the last participant reaches their destination. For dropoffs, that excludes the driver's final trip home; for pickups, it includes the final trip to the activity.

The router uses a three-phase optimization approach:

1. **Feasible Seed Assignment**: Builds a deterministic round-robin assignment. Participants from the same address stay in one vehicle unless the household is larger than every selected vehicle.
2. **Context-Aware Route Ordering**: Applies household-block reversals and accepts an order only when it improves the complete solution.
3. **Assignment Search**: Repeatedly evaluates whole-household relocations and pairwise swaps, including swaps between full vehicles. Routes may become empty when that improves a higher-priority objective.

Candidates are compared lexicographically: latest participant completion, worst driver detour, aggregate participant completion, and aggregate driving time. Using more selected drivers is only a final tie preference. This is a bounded local-search heuristic for the Capacitated Vehicle Routing Problem (CVRP), so it improves practical results without claiming a globally optimal solution.

### Project Structure

```
ride-home-router/
├── cmd/server/          # Server entry point (flags, env, graceful shutdown)
├── migrations/          # Embedded Postgres schema migrations (golang-migrate)
├── internal/
│   ├── models/          # Data structures (Participant, Driver, Event, etc.)
│   ├── database/        # Storage interfaces and repository contracts
│   ├── postgres/        # Postgres storage implementation (+ postgrestest helpers)
│   ├── handlers/        # HTTP request handlers
│   ├── routing/         # Route optimization algorithms
│   ├── distance/        # Google Routes distance provider
│   ├── geocoding/       # Nominatim API client
│   ├── importer/        # Spreadsheet import staging
│   ├── httpx/           # HTTP constants and helpers
│   └── server/          # HTTP server setup, routing, request security
├── web/                 # Frontend (Go html/template + HTMX, CSS, JS)
├── Dockerfile           # Multi-stage container build
└── Makefile
```

### Technology Stack

- **Backend**: Go (standard library HTTP server)
- **Frontend**: HTML templates + [HTMX](https://htmx.org/) for dynamic updates; the browser is the client
- **Storage**: PostgreSQL via pgx, schema managed by golang-migrate
- **Routing**: Google Routes API `computeRouteMatrix`
- **Geocoding**: Nominatim (OpenStreetMap)

---

## API Usage & Limits

This app uses external APIs that have usage limits:

- **Google Routes API**: Route distance and duration calculations use `routes.googleapis.com/distanceMatrix/v2:computeRouteMatrix`. Google Cloud billing, quotas, and API key restrictions apply. The app caches distance results in Postgres and batches route matrix calls up to 625 elements per request.

- **Nominatim (OpenStreetMap Geocoding)**: The public Nominatim service has a [usage policy](https://operations.osmfoundation.org/policies/nominatim/) limiting requests to 1 per second. The app includes built-in delays to respect this limit. A hosted deployment sends every coordinator's searches from one IP, so keep usage modest.

For typical community group usage, address search should stay within Nominatim's public limits. Route calculation depends on your Google Cloud quota and billing configuration. If route calculation fails, first verify `GOOGLE_MAPS_API_KEY`, that the Routes API is enabled, and that the key is allowed to call it.

---

## Disclaimer

**USE AT YOUR OWN RISK.** This software is provided "as is" without warranty of any kind, express or implied.

- **Driver vetting**: This software only calculates routes—it does not screen or verify drivers. You are solely responsible for vetting all drivers, including performing background checks as appropriate for your organization.
- **Route accuracy**: Route suggestions are approximations based on heuristic algorithms. They may not be optimal, accurate, or safe. Always verify addresses and routes before driving.
- **Third-party services**: This tool relies on Google Routes and Nominatim (OpenStreetMap) for routing and geocoding. Accuracy, availability, quotas, and costs depend on these external services, which are outside our control.
- **Data security**: You host the server and database. We make no guarantees about data protection or security; you are responsible for the access layer in front of the server, the database, and backups.
- **No liability**: The developers are not responsible for any damages, losses, injuries, data breaches, or incidents arising from use of this software or the transportation it helps coordinate.

By using this software, you accept full responsibility for its use.

---

## Contributing

This project is supposed to support a single use case. Feel free to fork and modify as needed.

## License

MIT
