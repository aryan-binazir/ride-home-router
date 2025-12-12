# Ride Home Router - Implementation Plan

## Project Overview

A local-first transport routing system that optimizes driver assignments for taking participants home after events. Solves a simplified Capacitated Vehicle Routing Problem (CVRP) using greedy heuristics.

**Privacy-first**: All data stored locally in SQLite. No cloud, no hosted solutions.

---

## Technical Stack

| Component | Choice | Status |
|-----------|--------|--------|
| Language | Go 1.25 | ✅ Implemented |
| Architecture | Local web server serving HTML/CSS/JS | ✅ Implemented |
| Database | SQLite with WAL mode | ✅ Implemented |
| Frontend | htmx with HTML fragments | ✅ Implemented |
| Distance Calculation | OSRM public API | ✅ Implemented |
| Geocoding | Nominatim API | ✅ Implemented |
| Routing Algorithm | Greedy nearest-neighbor | ✅ Implemented |
| Target Platforms | Windows (amd64), macOS (amd64, arm64) | ✅ Makefile ready |

---

## Data Model

### Settings
- Institute address (string, geocoded to lat/long)

### Participants
- ID (auto-generated)
- Name (string)
- Address (string, geocoded to lat/long)

### Drivers
- ID (auto-generated)
- Name (string)
- Address (string) - their home address
- Vehicle capacity (integer)
- Is institute vehicle (boolean) - exactly one allowed

### Event History
- Event ID
- Date/timestamp
- Snapshot of assignments (denormalized for historical accuracy)

### Distance Cache
- Caches OSRM lookups to reduce API calls
- Coordinates rounded to 5 decimal places (~1m precision)

---

## Routing Rules

1. All routes **start at the institute**
2. Drivers drop off participants, then **go home**
3. Optimization goal: **minimize total drop-off distance** (driver's commute home excluded)
4. **Institute vehicle is last resort**:
   - Only use if no valid solution with regular drivers
   - Must return to institute after all drop-offs
   - Any available driver can be selected to drive it
5. Respect vehicle capacity constraints

---

## Algorithm: Greedy Nearest-Neighbor

```
1. Start with all participants needing assignment
2. For each driver (excluding institute vehicle initially):
   - From current location (starting at institute), pick nearest unassigned participant
   - Repeat until vehicle full
3. If participants remain, use institute vehicle
4. If still unsolvable, report error
```

---

## UI/UX Flow

### Main Event Page (`/`)
- [x] Display all participants with checkboxes (attendance)
- [x] Display all drivers with checkboxes (availability)
- [x] Select institute vehicle driver if needed
- [x] "Calculate Routes" button
- [x] Results section showing driver assignments
- [x] Client-side validation (warn if no selection)
- [ ] "Save Event" button (after calculation)
- [x] "Clear" button resets selections

### Participants Roster (`/participants`)
- [x] Table listing all participants
- [x] Add participant form
- [x] Edit/Delete per row
- [x] htmx for CRUD (no page reloads)

### Drivers Roster (`/drivers`)
- [x] Table listing all drivers
- [x] Add driver form
- [x] Edit/Delete per row
- [x] Institute vehicle checkbox (only one allowed)
- [x] htmx for CRUD

### Settings (`/settings`)
- [x] Form to set institute address
- [x] Shows geocoded coordinates
- [x] Save button with htmx

### History (`/history`)
- [x] List of past events
- [ ] Click to expand/view details
- [ ] Delete button per event

---

## Project Structure

```
ride-home-router/
├── cmd/server/
│   └── main.go                 # Entry point, DI wiring, server startup, template loading
├── internal/
│   ├── database/
│   │   ├── db.go               # SQLite connection, migrations
│   │   ├── schema.sql          # DDL (embedded with go:embed)
│   │   ├── participant.go      # ParticipantRepository
│   │   ├── driver.go           # DriverRepository
│   │   ├── settings.go         # SettingsRepository
│   │   ├── event.go            # EventRepository
│   │   └── distance_cache.go   # DistanceCacheRepository
│   ├── geocoding/
│   │   └── nominatim.go        # Nominatim client with rate limiting
│   ├── distance/
│   │   └── osrm.go             # OSRM client with caching
│   ├── routing/
│   │   └── greedy.go           # Greedy nearest-neighbor algorithm
│   ├── handlers/
│   │   ├── handlers.go         # Common utilities, template rendering
│   │   ├── pages.go            # Page handlers (/, /participants, etc.)
│   │   ├── participants.go     # Participant CRUD handlers
│   │   ├── drivers.go          # Driver CRUD handlers
│   │   ├── settings.go         # Settings handlers
│   │   ├── routes.go           # Route calculation handler
│   │   └── events.go           # Event history handlers
│   └── models/
│       └── models.go           # All domain types
├── web/
│   ├── static/
│   │   ├── css/style.css       # All styles
│   │   └── js/htmx.min.js      # htmx library
│   └── templates/
│       ├── layout.html         # Base layout with nav
│       ├── index.html          # Event planning page
│       ├── participants.html   # Participant roster
│       ├── drivers.html        # Driver roster
│       ├── settings.html       # Settings page
│       ├── history.html        # Event history
│       └── partials/           # htmx fragments
│           ├── participant_list.html
│           ├── participant_form.html
│           ├── participant_row.html
│           ├── driver_list.html
│           ├── driver_form.html
│           ├── driver_row.html
│           ├── route_results.html
│           ├── event_list.html
│           └── event_detail.html
├── Makefile                    # Build targets
├── go.mod
├── go.sum
├── ARCHITECTURE.md             # Full architecture document
└── plan.md                     # This file
```

---

## Implementation Progress

### Phase 1: Architecture Design ✅ COMPLETE
- [x] Created ARCHITECTURE.md with full specifications
- [x] SQLite schema DDL
- [x] REST API specification
- [x] Component interface definitions
- [x] Data flow diagrams
- [x] Key design decisions documented

### Phase 2a: Backend Implementation ✅ COMPLETE
- [x] Database layer (SQLite, all repositories)
- [x] Models (all domain types)
- [x] Geocoding service (Nominatim with rate limiting)
- [x] Distance service (OSRM with caching)
- [x] Routing algorithm (greedy nearest-neighbor)
- [x] HTTP handlers (all CRUD + routing)
- [x] Server startup with graceful shutdown
- [x] Auto-open browser on startup

### Phase 2b: Frontend Implementation ✅ COMPLETE
- [x] Base layout with navigation
- [x] All page templates
- [x] All partial templates for htmx
- [x] CSS styling
- [x] htmx integration

### Phase 2c: Build System ✅ COMPLETE
- [x] Makefile with cross-platform targets
- [x] `make build` - current platform
- [x] `make build-all` - all platforms
- [x] `make run` - build and run
- [x] `make test` - run tests
- [x] `make clean` - cleanup

### Phase 3: Integration ✅ COMPLETE
- [x] Wired frontend templates to Go server
- [x] Static file serving at `/static/`
- [x] Page routes rendering full HTML
- [x] htmx requests returning HTML fragments
- [x] Template clone fix (avoid "cannot Clone after executed" error)

### Phase 4: Testing ✅ COMPLETE
- [x] Test suite created (105 tests)
- [x] Database tests (in-memory SQLite)
- [x] Service tests (mock HTTP servers)
- [x] Handler tests (httptest)
- [x] Routing algorithm tests

---

## Current Status

### Working Features
1. **Navigation** - All pages load correctly with proper content
2. **Participants CRUD** - Add, edit, delete participants with geocoding
3. **Drivers CRUD** - Add, edit, delete drivers with geocoding
4. **Settings** - Configure institute address with geocoding
5. **Route Calculation** - Form data parsing, validation, htmx response
6. **Client-side Validation** - Warns when no participants/drivers selected
7. **Verbose Logging** - Structured logging for debugging

### Recent Fixes (This Session)
1. Fixed template rendering (layout + content blocks)
2. Fixed "cannot Clone after executed" template error
3. Fixed route calculation to parse form data (not just JSON)
4. Added htmx HTML fragment responses for route calculation
5. Added client-side validation for route calculation
6. Added verbose logging throughout

### Known Issues / TODO
1. **Save Event** - Button exists but functionality not fully wired
2. **Event History Details** - List shows but expand/delete not working
3. **Route Results Display** - Need to verify template renders correctly
4. **Error Handling** - Some edge cases may not have user-friendly messages

---

## API Endpoints

### Settings
| Method | Path | Description | Status |
|--------|------|-------------|--------|
| GET | `/api/v1/settings` | Get settings | ✅ |
| PUT | `/api/v1/settings` | Update settings | ✅ |

### Participants
| Method | Path | Description | Status |
|--------|------|-------------|--------|
| GET | `/api/v1/participants` | List all | ✅ |
| POST | `/api/v1/participants` | Create | ✅ |
| GET | `/api/v1/participants/{id}` | Get one | ✅ |
| PUT | `/api/v1/participants/{id}` | Update | ✅ |
| DELETE | `/api/v1/participants/{id}` | Delete | ✅ |
| GET | `/api/v1/participants/new` | Form (htmx) | ✅ |
| GET | `/api/v1/participants/{id}/edit` | Edit form (htmx) | ✅ |

### Drivers
| Method | Path | Description | Status |
|--------|------|-------------|--------|
| GET | `/api/v1/drivers` | List all | ✅ |
| POST | `/api/v1/drivers` | Create | ✅ |
| GET | `/api/v1/drivers/{id}` | Get one | ✅ |
| PUT | `/api/v1/drivers/{id}` | Update | ✅ |
| DELETE | `/api/v1/drivers/{id}` | Delete | ✅ |
| GET | `/api/v1/drivers/new` | Form (htmx) | ✅ |
| GET | `/api/v1/drivers/{id}/edit` | Edit form (htmx) | ✅ |

### Routing
| Method | Path | Description | Status |
|--------|------|-------------|--------|
| POST | `/api/v1/routes/calculate` | Calculate routes | ✅ |
| POST | `/api/v1/geocode` | Geocode address | ✅ |

### Events
| Method | Path | Description | Status |
|--------|------|-------------|--------|
| GET | `/api/v1/events` | List events | ✅ |
| POST | `/api/v1/events` | Save event | ⚠️ Needs testing |
| GET | `/api/v1/events/{id}` | Get event details | ⚠️ Needs testing |
| DELETE | `/api/v1/events/{id}` | Delete event | ⚠️ Needs testing |

### Utility
| Method | Path | Description | Status |
|--------|------|-------------|--------|
| GET | `/api/v1/health` | Health check | ✅ |

---

## Next Steps

1. **Test Route Calculation End-to-End**
   - Add sample participants and drivers
   - Configure institute address
   - Select participants and drivers
   - Click "Calculate Routes"
   - Verify results display correctly

2. **Wire Up "Save Event" Button**
   - Save calculated routes to event history
   - Show success message

3. **Event History Details**
   - Click event to expand details
   - Show which driver took which participants
   - Delete event functionality

4. **Polish**
   - Better error messages
   - Loading states
   - Mobile responsiveness testing

---

## Running the Application

```bash
# Build for current platform
make build

# Run locally
make run

# Build for all platforms
make build-all

# Run tests
make test

# Clean build artifacts
make clean
```

Server starts on `http://127.0.0.1:8080` and auto-opens browser.

Override with environment variables:
- `SERVER_ADDR=127.0.0.1:3000`
- `DATABASE_PATH=./data/app.db`
- `TEMPLATES_DIR=./web/templates`
- `STATIC_DIR=./web/static`
