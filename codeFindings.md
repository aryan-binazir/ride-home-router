# Code Findings: Ride Home Router (redundancy / unused / repetition)

**Date:** 2025-12-13  
**Scope:** `/internal`, `/cmd`, `/web`, `/frontend`, repo root artifacts  
**Method:** I split the review into focused “sub-agent” passes (backend/server, data layer, routing/integrations, and web/assets) and then merged the overlap.

---

## Executive Summary (brutally honest)

- This repo works, but it’s already **copy/paste-driven**: the same patterns are repeated dozens of times with tiny variations. That’s manageable at ~10–20 endpoints; it becomes a tax at 50+.
- You have a lot of “looks fine today” duplication that will **cause drift** (different handlers doing “the same thing” but responding differently, especially for HTMX vs JSON).
- There’s **real dead/unused code** (template funcs, handler helpers, Wails runtime artifacts, likely-unused endpoints) and at least one **inconsistent coordinate rounding strategy** that can produce cache misses and edge-case bugs.

---

## Sub-agent 1 — Backend entrypoints & server wiring

### Redundant / duplicated behavior

- **Two entrypoints wrap the same server**:
  - Desktop: `main.go` + `app.go` (Wails) starts `internal/server` on a random port and then navigates to it.
  - Browser mode: `cmd/server/main.go` starts the same `internal/server` and opens a browser.
  - This can be totally valid (README documents both), but the *implementation* is redundant: both mains build/start the server and contain “open URL” logic.

- **Duplicate “open URL” logic**:
  - `cmd/server/main.go:openBrowser()` duplicates platform switching logic with `internal/server/server.go:handleOpenURL()`.
  - You already pull in `github.com/pkg/browser` indirectly in `go.mod`; using it (or a shared helper) would remove platform-specific duplication and reduce drift.

### Unused-ish code

- `internal/server/server.go` stores fields that are never read:
  - `Server.handler` is assigned but not referenced.
  - `Server.listener` is stored but not referenced (and `Shutdown()` doesn’t use it).
  - These aren’t “broken”, but they’re currently **dead weight** and make the type look more complex than it is.

### Heavily repeated code (>3x)

- `internal/server/server.go`’s route wiring is extremely repetitive:
  - Same `switch r.Method` + `http.Error("Method not allowed")` pattern is repeated across almost every route.
  - Same “/new endpoint”, same “/id endpoint with optional /edit” logic repeated for participants and drivers.
  - This is boilerplate you’ll keep paying for (and forgetting to update consistently).

---

## Sub-agent 2 — HTTP handlers (HTMX + JSON)

### Heavily repeated code (>3x)

- **HTMX branching is everywhere**:
  - `if h.isHTMX(r)` appears ~**74 times** across `internal/handlers/*`.
  - The duplication isn’t just the `if`: it’s the *slightly different* ways handlers return HTML vs JSON vs HX-Trigger toasts.
  - Result: inconsistent UX and inconsistent status codes (see below).

- **Toast trigger patterns are repeated**:
  - `HX-Trigger` header usage shows up in **20+ places** across handlers.
  - Many handlers manually build JSON-in-a-string for the header, which is error-prone and easy to break.

- **Route editing response block is copy/pasted**:
  - Rendering `route_results` with the same `map[string]interface{}{Routes, Summary, UseMiles, ActivityLocation, SessionID, IsEditing, UnusedDrivers}` appears **6 times** (`internal/handlers/routes.go`, `internal/handlers/route_edit.go`).
  - This is a prime candidate for a single helper response method or a small view-model struct.

- **Path ID parsing is duplicated and inconsistent**:
  - Some handlers use `strings.TrimPrefix(...)+strconv.ParseInt` (participants/drivers/events).
  - Others use `strings.Split(strings.Trim(...), "/")` and index into the array (org vehicles, activity locations).
  - Same idea, multiple implementations = drift + bugs.

### Redundant code / near-copies

- `internal/handlers/participants.go` and `internal/handlers/drivers.go` are **near clones**:
  - List, Get, Create, Update, Delete, “Form” endpoints all follow the same structure with small differences.
  - Any non-trivial change (validation rules, error UX, logging) will need to be made twice (and will eventually diverge).

### Unused / dead code

- `internal/handlers/handlers.go:handleConflict()` is defined but never called.

### Likely-unused endpoints (unused by the shipped UI)

These are *real* handlers/routes, but I couldn’t find any references from templates or JS:

- `POST /api/v1/geocode` (`internal/handlers/routes.go:HandleGeocodeAddress`) — no usage found in `web/templates` or `web/static/js`.
- `GET /api/v1/activity-locations` (`internal/handlers/activity_locations.go:HandleListActivityLocations`) — UI lists locations server-side, uses only POST/DELETE via HTMX.
- `GET /api/v1/org-vehicles` (`internal/handlers/org_vehicles.go:HandleListOrgVehicles`) — same story: UI uses POST/DELETE.
- `PUT /api/v1/org-vehicles/{id}` (`internal/handlers/org_vehicles.go:HandleUpdateOrgVehicle`) — no template/JS references; currently looks like an unused feature.

If these are meant as public API endpoints, great — but then they need tests/docs. If they’re not, they’re just maintenance surface.

### “Copy/paste drift” examples (practical risk)

- Not-found detection is inconsistent:
  - Some handlers use `errors.Is(err, database.ErrNotFound)` via `h.checkNotFound(err)`.
  - Others do `strings.Contains(err.Error(), "not found")` (org vehicles, activity locations).
  - This is exactly how subtle behavior differences creep in.

- Validation errors for HTMX are inconsistent:
  - Some handlers return proper 4xx + HTMX-friendly HTML (`handleValidationErrorHTMX`).
  - Others call `renderError()` for validation, which forces a **500** response with an “error” alert, even when it’s user input.

---

## Sub-agent 3 — Data layer (JSON store + distance cache)

### Duplicate / inconsistent coordinate rounding (bug risk)

- Coordinate normalization is implemented multiple times, differently:
  - `internal/distance/osrm.go` does “round to 5 decimals” via `int(v*100000+0.5)/100000` for the “same point” shortcut (this is also **wrong for negative numbers**).
  - `internal/database/file_distance_cache.go` “rounds” by **truncating** with `int(f*100000)/100000` for cache matching and cache keys.
- If you intended “5-decimal rounding tolerance”, this inconsistency can create **cache misses**, extra OSRM calls, and edge-case behavior differences depending on hemisphere (negative longitudes are common).
- This is textbook “duplicated logic with drift”: it should be one shared helper with one policy (ideally `math.Round`, not `int()` tricks).

### Heavily repeated code (>3x)

- `internal/database/json_store.go` contains **copy/paste CRUD repositories**:
  - Participant, Driver, OrganizationVehicle are effectively the same repository with different types/fields.
  - Patterns repeated: `List` with optional search, `GetByID` loop, `GetByIDs` with `idSet`, `Create` assigns IDs + timestamps, `Update` preserves CreatedAt, `Delete` slice splice.
  - This is “fine” until you need to add a new entity or change behavior: you’ll be copy/pasting the same bugs forward.

- `idSet := make(map[int64]bool)` / `GetByIDs` logic is duplicated across at least **3 repositories** (participants, drivers, org vehicles).

### Redundant duplication

- Atomic “write temp file then rename” logic appears in at least two places:
  - `internal/database/json_store.go` (`saveUnlocked`)
  - `internal/database/file_distance_cache.go` (`saveUnlocked`)
  - Shared helper would be cleaner and reduce subtle file-permission / error-handling drift.

### “Unused but shipped” configuration baggage

- Deprecated settings fields are still in multiple layers:
  - `models.Settings` includes `InstituteAddress/Lat/Lng` marked deprecated.
  - `internal/database/json_store.go` still persists them (`JSONSettings`) and runs migration logic.
  - If migration is still required, keep it; if not, this is dead compatibility baggage.

---

## Sub-agent 4 — Web templates, JS, and repo hygiene

### Heavily repeated code (>3x)

- Page header markup is repeated **5 times**:
  - `web/templates/index.html`, `participants.html`, `drivers.html`, `settings.html`, `history.html`
  - Same structure, different text/buttons. This should be a partial if you care about maintainability.

- There’s a lot of near-duplicate “forms with address autocomplete” markup across pages/partials. It’s not always >3 copies, but it’s already trending there.

### Unused / dead code

- Template funcs defined in `internal/server/server.go` but unused in templates:
  - `divideFloat` (no occurrences in `web/templates`)
  - `distanceUnit` (no occurrences in `web/templates`)

- Wails runtime artifacts look unused:
  - `frontend/wailsjs/runtime/*` exists, but there are **no references** to `wailsjs` anywhere in the repo, and `frontend/index.html` doesn’t load it.
  - Since `main.go` embeds `frontend/*`, this also increases embedded asset payload for no apparent benefit.

### Repo hygiene / redundant artifacts

- `build/bin/Ride Home Router` is committed even though `.gitignore` ignores `/build/bin/`.
  - It’s a 12MB Linux ELF binary sitting in source control. This is noise and encourages accidental “works on my machine” releases.
- `assets/` directory exists but is empty.

---

## Highest ROI cleanups (if you want to reduce duplication fast)

1. Create a small HTMX-aware response helper in `internal/handlers` (unify status codes + toast triggering + HTML vs JSON).
2. Create shared helpers for:
   - parsing `{id}` from a path prefix
   - form-vs-JSON decoding
   - emitting common template responses (e.g., the `route_results` view-model)
3. Consolidate repository boilerplate in `internal/database/json_store.go` (even without generics, helpers can remove a lot of duplication).
4. Remove truly unused code/artifacts (template funcs, `handleConflict`, unused endpoints if not meant to be public, Wails runtime folder if not used, committed binary, empty assets).
