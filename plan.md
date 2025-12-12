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
| Routing Algorithm | Seed-then-cluster + 2-opt + inter-route swaps | ✅ Implemented |
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

## Algorithm: Seed-then-Cluster with Optimization

### Overview

The algorithm solves the Capacitated Vehicle Routing Problem (CVRP) using a multi-phase approach:
1. **Seeding**: Spread drivers geographically using home-aware assignment
2. **Clustering**: Greedy expansion of each driver's cluster
3. **Route Building**: Order stops with nearest-neighbor + 2-opt refinement
4. **Inter-route Optimization**: Swap boundary participants between routes

### Phase 1: Home-Aware Seeding

```
1. Select N spread-out seeds (one per driver):
   - First seed: nearest participant to institute
   - Subsequent seeds: farthest from all already-selected seeds (k-means++ style)
2. Assign seeds to drivers from the seed's perspective:
   - Each seed goes to the driver whose home is closest to it
   - This ensures drivers build clusters toward their own homes
```

**Why seed-centric assignment?** If done driver-centric (each driver grabs closest seed), Driver A might steal a seed that Driver B *really* needs. Seed-centric avoids this conflict.

### Phase 2: Greedy Clustering

```
While unassigned participants remain:
   For each driver (round-robin):
      If driver has capacity and has a seed:
         Pick the unassigned participant nearest to ANY of driver's current stops
         Add to driver's cluster
```

**Key insight**: "Nearest to any stop" (not just last stop) keeps clusters geographically tight.

### Phase 3: Route Building with 2-Opt

```
For each driver's cluster:
   1. Order stops using nearest-neighbor from institute
   2. Apply 2-opt optimization:
      - Try reversing every segment [i..j]
      - Keep reversal if it reduces total distance
      - Repeat until no improvement
```

### Phase 4: Inter-Route Boundary Optimization

```
After routes are ordered (so "last stop" = geographic boundary):
   Repeat until no improvement:
      For each pair of routes (A, B):
         Try relocating A's last stop to B (if B has capacity)
         Try relocating B's last stop to A (if A has capacity)
         Try swapping last stops between A and B
      Accept any change that reduces combined distance
      Re-run 2-opt on modified routes
```

**Why after route building?** Before ordering, "last participant" is whoever was added last during clustering (arbitrary). After 2-opt ordering, it's the geographic boundary of the route.

### Fallback: Institute Vehicle

```
If participants remain after all drivers are full:
   Use institute vehicle with nearest-neighbor assignment
   Institute vehicle returns to institute after all drop-offs
```

### Algorithm Properties

| Property | Value |
|----------|-------|
| Time Complexity | O(n² × d) for seeding/clustering, O(n³) for 2-opt per route |
| Optimality | Heuristic (not guaranteed optimal) |
| Fairness | Driver order randomized each run |
| Geographic Coherence | Guaranteed by seeding + cluster expansion |
| Capacity Handling | Native (each driver tracks own capacity) |

### Considered but Rejected

1. **Clarke-Wright Savings**: Better for homogeneous fleets; our heterogeneous capacities + driver homes don't fit well
2. **Pure k-means clustering**: Doesn't respect capacity constraints during clustering
3. **Full bipartite matching for seed assignment**: Overkill for ≤10 drivers; greedy is sufficient

---

## UI/UX Flow

### Main Event Page (`/`)
- [x] Display all participants with checkboxes (attendance)
- [x] Display all drivers with checkboxes (availability)
- [x] Select institute vehicle driver if needed
- [x] "Calculate Routes" button
- [x] Results section showing driver assignments
- [x] Client-side validation (warn if no selection)
- [x] Client-side capacity validation (warn if participants > seats)
- [x] "Save Event" button (after calculation)
- [x] "Clear" button resets selections

### Participants Roster (`/participants`)
- [x] Table listing all participants
- [x] Add participant form (auto-closes on success)
- [x] Edit/Delete per row
- [x] htmx for CRUD (no page reloads)

### Drivers Roster (`/drivers`)
- [x] Table listing all drivers
- [x] Add driver form (auto-closes on success)
- [x] Edit/Delete per row
- [x] Institute vehicle checkbox (only one allowed)
- [x] htmx for CRUD

### Settings (`/settings`)
- [x] Form to set institute address
- [x] Shows geocoded coordinates
- [x] Miles/km toggle with persistence
- [x] Save button with htmx

### History (`/history`)
- [x] List of past events with summaries
- [x] Click to expand/view details
- [x] Delete button per event (with refresh)

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
7. Fixed route results template name mismatch
8. Added miles/km setting with persistence
9. Fixed event save to parse form data (not just JSON)
10. Fixed history page to include event summaries
11. Fixed event detail expand on click
12. Added auto-close for add forms after successful submission
13. Added capacity validation warning (participants > available seats)
14. **Major**: Redesigned routing algorithm for multi-driver load distribution

### Routing Algorithm Improvements (This Session)
1. **Seed-then-Cluster**: Replaced single-driver-fills-first with geographic spreading
2. **Home-Aware Seeding**: Seeds assigned to drivers based on proximity to driver homes
3. **Intra-Route 2-Opt**: Each route optimized by segment reversal
4. **Inter-Route Boundary Swaps**: Relocate/swap last stops between routes after ordering
5. **Bug Fix**: Inter-route optimization now runs after route ordering (not before)
6. **Bug Fix**: Seed assignment is seed-centric (avoids driver conflicts)

### Known Issues / TODO
1. **Error Handling** - Some edge cases may not have user-friendly messages
2. **Mobile Responsiveness** - Not fully tested on mobile devices

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
| POST | `/api/v1/events` | Save event | ✅ |
| GET | `/api/v1/events/{id}` | Get event details | ✅ |
| DELETE | `/api/v1/events/{id}` | Delete event | ✅ |

### Utility
| Method | Path | Description | Status |
|--------|------|-------------|--------|
| GET | `/api/v1/health` | Health check | ✅ |

---

## Next Steps

1. **End-to-End Testing**
   - Test with real addresses and multiple drivers
   - Verify algorithm produces sensible geographic clusters
   - Check that 2-opt and inter-route swaps are triggering

2. **Polish**
   - Better error messages for edge cases
   - Loading states for long calculations
   - Mobile responsiveness testing

3. **Future Enhancements (Optional)**
   - Try all participants (not just last) for inter-route swaps
   - Add route visualization on a map
   - Export routes to PDF/CSV
   - SMS/Email notification to drivers

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
