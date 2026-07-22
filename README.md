<p align="center">
  <img src="build/icon.svg" alt="Ride Home Router" width="128" height="128">
</p>

# Ride Home Router

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Wails v2](https://img.shields.io/badge/Wails-v2-red)](https://wails.io/)

A desktop app that optimizes driver assignments for getting people home after events. Perfect for community groups, religious organizations, schools, or any gathering where you need to coordinate rides.

> **Disclaimer:** This software only calculates routes—it does not vet drivers. You are responsible for screening drivers and verifying all routes. Use at your own risk. See [full disclaimer](#disclaimer).

## Table of Contents

- [The Problem It Solves](#the-problem-it-solves)
- [Features](#features)
- [Privacy First](#privacy-first)
- [Installation](#installation)
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
- **Local Data Storage** — All data stored on your computer; internet needed for address lookup and route calculations

## Privacy First

**Persistent app data stays on your computer.** Names, addresses, API configuration, and event history are stored locally in `~/.ride-home-router/`. During route calculation, coordinates are sent to Google Routes; during address search, the search text is sent to Nominatim.

The only external services used are:
- **Google Routes API** — Calculates driving distances and durations between coordinates. Route calculation requires a Google Maps API key saved in Settings.
- **Nominatim** — OpenStreetMap geocoder (converts addresses to coordinates)

No Ride Home Router account. No cloud sync. No tracking.

---

## Installation

### Download (Recommended)

Download the latest release for your platform from the [Releases](../../releases) page:

| Platform | Download |
|----------|----------|
| macOS (Apple Silicon) | `Ride-Home-Router-macOS-arm64.dmg` |
| Windows | `Ride-Home-Router-Windows-amd64.exe` |
| Linux | `Ride-Home-Router-Linux-amd64` |

#### macOS Installation

1. **Double-click** the downloaded `.dmg` file to mount it
2. **Drag** the `Ride Home Router.app` to your **Applications** folder
3. **Remove the quarantine attribute** by running the following command in Terminal:

```bash
xattr -d com.apple.quarantine /Applications/ride-home-router.app
```

**Why is this necessary?** macOS places a quarantine flag on applications downloaded from the internet. Since this app is not signed with an Apple Developer certificate, macOS Gatekeeper will block it from running. The command above removes this quarantine attribute, allowing the app to launch.

**⚠️ Important:** Before running this command, you should:
- **Understand the risk**: Removing the quarantine attribute bypasses macOS security checks. Only do this for software you trust and have downloaded from a verified source.
- **Read the licenses**: Review the [MIT License](LICENSE) and understand that this software is provided "as is" without warranty. See the [Disclaimer](#disclaimer) section for full details.

### Build from Source

Requires [Go 1.25+](https://go.dev/dl/) and [Wails v2](https://wails.io/docs/gettingstarted/installation).

```bash
# Install Wails CLI
# Ensure you have GOPATH set in your environment
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Clone and build
git clone https://github.com/aryan-binazir/ride-home-router.git
cd ride-home-router
wails build
```

The built application will be in `build/bin/`.

#### Linux Dependencies

On Linux, you'll need WebKit2GTK:

```bash
# Arch
sudo pacman -S webkit2gtk gtk3

# Ubuntu/Debian
sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev

# Fedora
sudo dnf install gtk3-devel webkit2gtk4.0-devel
```

---

## Google Routes API Setup

Route calculation now uses Google's Routes API instead of the public OSRM demo server. Address search still uses Nominatim/OpenStreetMap.

Before calculating routes:

1. Create or choose a Google Cloud project.
2. Enable the **Routes API** for that project.
3. Create a Google Maps API key with permission to call the Routes API.
4. Open **Settings** in Ride Home Router.
5. Paste the key under **Routing Provider** and click **Save API Key**.

The key is stored in `~/.ride-home-router/config.json` as `google_maps_api_key`. Saving a new key clears cached distances so future route calculations use the new provider credentials. If no key is configured, route calculation fails with a Settings prompt; address autocomplete still works.

---

## Usage

### Quick Start

1. **Add an Activity Location** — Save where your events happen on the Activity Locations page
2. **Add Participants** — People who need rides
3. **Add Drivers** — People with vehicles, including their capacity
4. **Add Vans** (Optional) — Save shared vans on the Vans page for overflow events
5. **Configure Google Routes** — Add a Google Maps API key in Settings before the first route calculation
6. **Calculate Routes** — Select participants, drivers, activity location, optional van assignments, and mode, then click Calculate

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
├── cmd/server/          # Standalone HTTP server (browser mode)
├── internal/
│   ├── models/          # Data structures (Participant, Driver, Event, etc.)
│   ├── database/        # Storage interfaces and repository contracts
│   ├── sqlite/          # SQLite storage implementation
│   ├── handlers/        # HTTP request handlers
│   ├── routing/         # Route optimization algorithms
│   ├── distance/        # Google Routes distance provider and legacy OSRM client
│   ├── geocoding/       # Nominatim API client
│   ├── httpx/           # HTTP constants and helpers
│   ├── templateutil/    # Shared template helper functions
│   └── server/          # HTTP server setup and routing
├── web/                 # Frontend (HTML templates, CSS, JS)
│   ├── templates/       # Go html/template files
│   └── static/          # CSS, JavaScript (HTMX)
├── frontend/            # Wails loading page
├── build/               # App icons and platform configs
├── main.go              # Wails entry point
└── app.go               # Wails app lifecycle
```

### Technology Stack

- **Backend**: Go (standard library HTTP server)
- **Frontend**: HTML templates + [HTMX](https://htmx.org/) for dynamic updates
- **Desktop**: [Wails v2](https://wails.io/) (Go + WebView)
- **Storage**: SQLite database in `~/.ride-home-router/`
- **Routing**: Google Routes API `computeRouteMatrix`
- **Geocoding**: Nominatim (OpenStreetMap)

### Development

```bash
# Run in development mode (hot reload)
wails dev

# Build for current platform
wails build

# Build standalone server (opens in browser)
go run cmd/server/main.go

# Run tests
go test ./...
```

### Data Storage

All data is stored in `~/.ride-home-router/`:

```
~/.ride-home-router/
├── config.json            # App config, including database path and Google Maps API key
└── data.db                # SQLite database (participants, drivers, settings, events, distance cache)
```

---

## API Usage & Limits

This app uses external APIs that have usage limits:

- **Google Routes API**: Route distance and duration calculations use `routes.googleapis.com/distanceMatrix/v2:computeRouteMatrix`. Google Cloud billing, quotas, and API key restrictions apply. The app caches distance results in SQLite and batches route matrix calls up to 625 elements per request.

- **Nominatim (OpenStreetMap Geocoding)**: The public Nominatim service has a [usage policy](https://operations.osmfoundation.org/policies/nominatim/) limiting requests to 1 per second. The app includes built-in delays to respect this limit.

For typical community group usage, address search should stay within Nominatim's public limits. Route calculation depends on your Google Cloud quota and billing configuration. If route calculation fails, first verify the API key in Settings, that the Routes API is enabled, and that the key is allowed to call it.

---

## Disclaimer

**USE AT YOUR OWN RISK.** This software is provided "as is" without warranty of any kind, express or implied.

- **Driver vetting**: This software only calculates routes—it does not screen or verify drivers. You are solely responsible for vetting all drivers, including performing background checks as appropriate for your organization.
- **Route accuracy**: Route suggestions are approximations based on heuristic algorithms. They may not be optimal, accurate, or safe. Always verify addresses and routes before driving.
- **Third-party services**: This tool relies on Google Routes and Nominatim (OpenStreetMap) for routing and geocoding. Accuracy, availability, quotas, and costs depend on these external services, which are outside our control.
- **Data security**: While data is stored locally on your computer, we make no guarantees about data protection or security. You are responsible for securing your own device and backups.
- **No liability**: The developers are not responsible for any damages, losses, injuries, data breaches, or incidents arising from use of this software or the transportation it helps coordinate.

By using this software, you accept full responsibility for its use.

---

## Contributing

This project is supposed to support a single use case. Feel free to fork and modify as needed.

## License

MIT
