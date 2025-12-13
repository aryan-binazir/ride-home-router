# Ride Home Router - Technical Specification

## Overview

A local-first desktop application that optimizes driver assignments for transporting participants home after events. Solves a Capacitated Vehicle Routing Problem (CVRP) using greedy heuristics with optimization passes.

**Privacy-first**: All data stored locally. No accounts, no cloud sync.

---

## Technical Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.22+ |
| Desktop Framework | Wails v2 |
| Frontend | HTML templates + HTMX |
| Storage | JSON files (`~/.ride-home-router/`) |
| Distance Calculation | OSRM public API |
| Geocoding | Nominatim (OpenStreetMap) |
| Routing Algorithm | Seed-then-cluster + 2-opt + inter-route swaps |

---

## Architecture

### How It Works

Wails wraps an internal HTTP server that serves the HTMX frontend:

```
┌─────────────────────────────────────────────────────┐
│                  Wails Desktop App                   │
│  ┌───────────────────────────────────────────────┐  │
│  │              WebView (WebKit/Edge)             │  │
│  │                                                │  │
│  │   Loads from http://127.0.0.1:{random_port}   │  │
│  │                                                │  │
│  └───────────────────────────────────────────────┘  │
│                         │                            │
│                         ▼                            │
│  ┌───────────────────────────────────────────────┐  │
│  │           Internal HTTP Server                 │  │
│  │                                                │  │
│  │  • Serves HTML templates                       │  │
│  │  • Handles API requests                        │  │
│  │  • Returns HTMX fragments                      │  │
│  └───────────────────────────────────────────────┘  │
│                         │                            │
│                         ▼                            │
│  ┌───────────────────────────────────────────────┐  │
│  │              JSON File Storage                 │  │
│  │         ~/.ride-home-router/data.json          │  │
│  └───────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

### Why This Approach?

- **Preserves HTMX**: All 61 HTMX interactions work unchanged
- **No frontend rewrite**: Same templates, same handlers
- **Standalone fallback**: `cmd/server/main.go` still works for browser-based access
- **Simple**: HTTP is battle-tested, no complex IPC

---

## Project Structure

```
ride-home-router/
├── main.go                      # Wails entry point
├── app.go                       # Wails app lifecycle (startup/shutdown)
├── wails.json                   # Wails configuration
├── frontend/
│   └── index.html               # Loading page (brief, before redirect)
├── build/
│   ├── appicon.png              # 1024x1024 app icon
│   ├── darwin/Info.plist        # macOS bundle config
│   └── windows/icon.ico         # Windows icon
├── cmd/server/
│   └── main.go                  # Standalone HTTP server (browser mode)
├── internal/
│   ├── server/
│   │   └── server.go            # Reusable HTTP server package
│   ├── database/
│   │   ├── paths.go             # Path management (~/.ride-home-router/)
│   │   ├── json_store.go        # JSON file storage + repositories
│   │   └── file_distance_cache.go  # Distance cache
│   ├── handlers/
│   │   ├── handlers.go          # Common utilities
│   │   ├── pages.go             # Page handlers
│   │   ├── participants.go      # Participant CRUD
│   │   ├── drivers.go           # Driver CRUD
│   │   ├── routes.go            # Route calculation
│   │   ├── route_edit.go        # Route editing (move/swap/reset)
│   │   ├── settings.go          # Settings handlers
│   │   ├── events.go            # Event history
│   │   └── activity_locations.go
│   ├── routing/
│   │   ├── distance_minimizer.go  # Router entry point
│   │   ├── greedy.go              # Clustering + route building
│   │   └── fairness.go            # Fairness metrics
│   ├── distance/
│   │   └── osrm.go              # OSRM API client
│   ├── geocoding/
│   │   └── nominatim.go         # Nominatim geocoder
│   └── models/
│       └── models.go            # Domain types
├── web/
│   ├── embed.go                 # Embeds templates + static
│   ├── templates/               # HTML templates
│   │   ├── layout.html
│   │   ├── index.html
│   │   ├── participants.html
│   │   ├── drivers.html
│   │   ├── settings.html
│   │   ├── history.html
│   │   └── partials/            # HTMX fragments
│   └── static/
│       ├── css/style.css
│       └── js/
│           ├── htmx.min.js
│           └── route-copy.js
├── .github/workflows/
│   └── build.yml                # Cross-platform CI builds
├── Makefile
├── go.mod
└── README.md
```

---

## Data Storage

All data stored in `~/.ride-home-router/`:

```
~/.ride-home-router/
├── data.json           # Main data (participants, drivers, settings, events)
└── cache/
    └── distances.json  # Cached OSRM responses
```

### Data Migration

On first run, automatically migrates from old locations:
- `~/institute_transport.json` → `~/.ride-home-router/data.json`
- `~/institute_cache/distances.json` → `~/.ride-home-router/cache/distances.json`

---

## Data Model

### Participants
- ID, Name, Address (geocoded to lat/lng)

### Drivers
- ID, Name, Address (home), Vehicle Capacity
- IsInstituteVehicle (boolean, max one)

### Activity Locations
- ID, Name, Address (geocoded)
- Selected location used as route origin

### Settings
- SelectedActivityLocationID
- UseMiles (boolean)

### Events
- ID, Date, Notes
- Denormalized assignments snapshot
- Summary stats

### Distance Cache
- Origin/Destination coordinates (rounded to 5 decimals)
- Distance (meters), Duration (seconds)

---

## Routing Algorithm

### Goal
Minimize total driving distance while respecting vehicle capacities.

### Phases

1. **Seeding** (Home-Aware)
   - Select N spread-out participants (one per driver)
   - Assign seeds to drivers based on proximity to driver homes

2. **Greedy Clustering**
   - Round-robin: each driver claims nearest unassigned participant
   - Respects capacity limits

3. **Route Building**
   - Nearest-neighbor ordering from activity location
   - 2-opt refinement (reverse segments to reduce distance)

4. **Inter-Route Optimization**
   - Swap boundary participants between routes
   - Re-run 2-opt after changes

### Institute Vehicle
Used as last resort when regular drivers can't fit everyone. Returns to origin after drop-offs.

---

## API Endpoints

### Pages
| Path | Description |
|------|-------------|
| `/` | Event planning (main page) |
| `/participants` | Participant roster |
| `/drivers` | Driver roster |
| `/settings` | Activity locations + preferences |
| `/history` | Event history |
| `/static/*` | CSS, JS assets |

### API (`/api/v1/`)

**Settings**: `GET/PUT /settings`

**Participants**: `GET/POST /participants`, `GET/PUT/DELETE /participants/{id}`

**Drivers**: `GET/POST /drivers`, `GET/PUT/DELETE /drivers/{id}`

**Activity Locations**: `GET/POST /activity-locations`, `DELETE /activity-locations/{id}`

**Routing**:
- `POST /routes/calculate` - Calculate optimal routes
- `POST /routes/edit/move-participant` - Move participant between routes
- `POST /routes/edit/swap-drivers` - Swap drivers
- `POST /routes/edit/reset` - Reset to original calculation

**Events**: `GET/POST /events`, `GET/DELETE /events/{id}`

**Utility**: `GET /health`, `POST /geocode`

---

## Build & Run

### Development
```bash
wails dev                    # Hot reload
go run cmd/server/main.go    # Browser mode (no Wails)
```

### Production Build
```bash
wails build                  # Current platform
wails build -platform darwin/arm64
wails build -platform darwin/amd64
wails build -platform windows/amd64
wails build -platform linux/amd64
```

### Makefile Targets
```bash
make wails-dev          # Development mode
make wails-build        # Build for current platform
make wails-build-all    # Build all platforms
make build              # Standalone server (current platform)
make run                # Run standalone server
```

---

## GitHub Actions

Builds on push to `main`:

| Platform | Runner | Output |
|----------|--------|--------|
| macOS arm64 | macos-14 | `.dmg` |
| macOS amd64 | macos-13 | `.dmg` |
| Windows | windows-latest | `.exe` installer |
| Linux | ubuntu-latest | Binary |

Artifacts uploaded to Actions tab.

---

## Platform-Specific Notes

### Linux
Requires WebKit2GTK:
```bash
# Arch
sudo pacman -S webkit2gtk-4.1 gtk3

# Ubuntu/Debian
sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev

# Fedora
sudo dnf install gtk3-devel webkit2gtk4.0-devel
```

GPU acceleration enabled via `linux.WebviewGpuPolicyAlways`.

### macOS
Bundle ID: `com.ridehomerouter.app`

### Windows
Uses WebView2 (Edge-based).

---

## Key Implementation Details

### Server Startup (Wails)
1. `NewApp()` starts HTTP server on random port (`127.0.0.1:0`)
2. Server ready before window opens
3. `startup()` navigates WebView to server URL via JS

### HTMX Integration
- Handlers detect `HX-Request` header
- Browser requests → full HTML pages
- HTMX requests → HTML fragments only

### Template System
- Go `html/template` with custom functions
- Base layout + partials pre-parsed
- Page templates parsed on demand

### Distance Caching
- Coordinates rounded to 5 decimal places (~1m precision)
- Cache persisted to disk
- Pre-warms cache before route calculation
