<p align="center">
  <img src="build/icon.svg" alt="Ride Home Router" width="128" height="128">
</p>

# Ride Home Router

A desktop app that optimizes driver assignments for getting people home after events. Perfect for community groups, religious organizations, schools, or any gathering where you need to coordinate rides.

> **Disclaimer:** This software only calculates routes—it does not vet drivers. You are responsible for screening drivers and verifying all routes. Use at your own risk. See [full disclaimer](#disclaimer).

## The Problem It Solves

Whether you're getting people home after an event or picking them up beforehand, you have:
- A list of participants who need rides
- Several drivers willing to help
- Limited vehicle capacity

Manually figuring out who goes with whom is tedious and often results in inefficient routes. Ride Home Router does the math for you—minimizing total driving distance while respecting vehicle capacities.

## Features

- **Pickup & Dropoff Modes** — Calculate routes for either direction: picking people up (driver home → participants → activity) or dropping them off (activity → participants → driver home)
- **Smart Route Optimization** — Uses cheapest-insertion clustering with 2-opt refinement to minimize total driving distance
- **Capacity Aware** — Respects each driver's vehicle capacity
- **Organization Vehicle Support** — Optionally designate a shared vehicle (van, bus) for overflow when regular drivers can't fit everyone
- **Address Autocomplete** — Search and select addresses with live suggestions from OpenStreetMap
- **Multiple Activity Locations** — Save different starting points (office, place of worship, school, etc.)
- **Event History** — Keep records of past events for reference
- **Manual Adjustments** — Move participants between routes or swap drivers after calculation
- **Preview Routes** — Open any route directly in Google Maps
- **Copy to Clipboard** — Export routes as text or Google Maps links
- **Distance Units** — Toggle between kilometers and miles
- **Local Data Storage** — All data stored on your computer; internet needed for address lookup and route calculations

## Privacy First

**All your data stays on your computer.** Names, addresses, and event history are stored locally in `~/.ride-home-router/`.

The only external services used are:
- **OSRM** — Open source routing service (calculates driving distances between coordinates)
- **Nominatim** — OpenStreetMap geocoder (converts addresses to coordinates)

No accounts. No cloud sync. No tracking.

---

## Installation

### Download (Recommended)

Download the latest release for your platform from the [Releases](../../releases) page:

| Platform | Download |
|----------|----------|
| macOS (Apple Silicon) | `Ride-Home-Router-macOS-arm64.dmg` |
| macOS (Intel) | `Ride-Home-Router-macOS-amd64.dmg` |
| Windows | `Ride-Home-Router-Windows-amd64.exe` |
| Linux | `Ride-Home-Router-Linux-amd64` |

### Build from Source

Requires [Go 1.25+](https://go.dev/dl/) and [Wails v2](https://wails.io/docs/gettingstarted/installation).

```bash
# Install Wails CLI
# Ensure you have GOPATH set in your environment
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Clone and build
git clone https://github.com/yourusername/ride-home-router.git
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

## Usage

### Quick Start

1. **Add an Activity Location** (Settings tab) — This is where your events happen
2. **Add Participants** — People who need rides
3. **Add Drivers** — People with vehicles, including their capacity
4. **Calculate Routes** — Select participants, drivers, and mode (pickup or dropoff), then click Calculate

### Workflow

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Settings  │ ──▶ │ Add People  │ ──▶ │  Calculate  │
│  (location) │     │  & Drivers  │     │   Routes    │
└─────────────┘     └─────────────┘     └─────────────┘
                                               │
                    ┌─────────────┐            │
                    │ Save Event  │ ◀──────────┘
                    │  (optional) │
                    └─────────────┘
```

### Tips

- **Route Modes**: Use **Dropoff** mode after events (activity → homes → driver home) or **Pickup** mode before events (driver home → homes → activity).
- **Organization Vehicle**: If you have a shared vehicle (van, bus), add it as an organization vehicle. It's used as overflow when regular drivers can't fit everyone.
- **Editing Routes**: After calculation, you can manually move participants between routes or swap drivers if needed.
- **Google Maps Links**: Click "Copy with Maps Link" to get directions you can paste into Google Maps.

---

## Technical Details

### How the Algorithm Works

The router uses a three-phase optimization approach:

1. **Cheapest Insertion**: Assigns participants to drivers using greedy clustering. Each unassigned participant is placed where it adds the least distance, respecting vehicle capacity.
2. **2-Opt Local Optimization**: Refines each driver's route by iteratively swapping edge pairs to find shorter paths.
3. **Inter-Route Optimization**: Attempts to move participants between routes to reduce total distance (up to 50 iterations).

This is a heuristic solution to the Capacitated Vehicle Routing Problem (CVRP). It won't always find the globally optimal solution, but produces good results quickly.

### Project Structure

```
ride-home-router/
├── cmd/server/          # Standalone HTTP server (browser mode)
├── internal/
│   ├── models/          # Data structures (Participant, Driver, Event, etc.)
│   ├── database/        # JSON file storage, distance caching
│   ├── handlers/        # HTTP request handlers
│   ├── routing/         # Route optimization algorithms
│   ├── distance/        # OSRM API client
│   ├── geocoding/       # Nominatim API client
│   └── server/          # HTTP server setup
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
- **Storage**: JSON files in `~/.ride-home-router/`
- **Routing**: OSRM public API
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
├── data.json              # Participants, drivers, settings, events
└── cache/
    └── distances.json     # Cached OSRM distance calculations
```

---

## Disclaimer

**USE AT YOUR OWN RISK.** This software is provided "as is" without warranty of any kind, express or implied.

- **Driver vetting**: This software only calculates routes—it does not screen or verify drivers. You are solely responsible for vetting all drivers, including performing background checks as appropriate for your organization.
- **Route accuracy**: Route suggestions are approximations based on heuristic algorithms. They may not be optimal, accurate, or safe. Always verify addresses and routes before driving.
- **Third-party services**: This tool relies on OSRM and Nominatim (OpenStreetMap) for routing and geocoding. Accuracy and availability depend on these external services, which are outside our control.
- **Data security**: While data is stored locally on your computer, we make no guarantees about data protection or security. You are responsible for securing your own device and backups.
- **No liability**: The developers are not responsible for any damages, losses, injuries, data breaches, or incidents arising from use of this software or the transportation it helps coordinate.

By using this software, you accept full responsibility for its use.

---

## Contributing

This project is supposed to support a single use case. Feel free to fork and modify as needed.

## License

MIT
