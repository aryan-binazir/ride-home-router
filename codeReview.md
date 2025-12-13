# Ride Home Router — Brutally Honest Code Review

Reviewed: 2025-12-13  
Repo: `ride-home-router` @ `6d3b4ba51d68f3f585fd66549b8ee43610cf9b56` (working tree was already dirty when reviewed)

## Executive Summary (read this first)

You have a genuinely useful product idea and a mostly-clean Go codebase, but right now it’s “works on my machine” quality, not “ship to strangers” quality.

The biggest problems are **security/privacy footguns** (localhost web app vulnerabilities + XSS + permissive file permissions + logging PII) and **architecture inconsistency** (two persistence implementations + dead/unused code + inconsistent error semantics). None of these are glamorous fixes, but they’re the difference between “nice personal tool” and “responsible release”.

If you shipped this today:
- It would probably run.
- It would probably be pleasant to use.
- It would also be **too easy to leak or exfiltrate private addresses** on a typical user machine.

## What’s solid (credit where it’s due)

- **Clear product narrative and docs.** `README.md` and `plan.md` explain what it does and how it’s structured.
- **Simple, understandable separation of concerns.** `internal/{handlers,routing,distance,geocoding,database}` is a sensible shape for a Go app.
- **The app actually compiles cleanly.** `go test ./...` and `go vet ./...` are clean (there are just no tests).
- **Practical algorithm approach.** You’re not pretending to solve CVRP optimally; you use heuristics + caching + OSRM tables, which is the right mindset for this domain.
- **Embedded assets/templates + HTMX** is a pragmatic stack for a small team / solo dev.

## P0: Stop-ship issues

### 1) Localhost API is wide open (CORS + no auth)

In `internal/server/server.go`, you set:

- `Access-Control-Allow-Origin: *`
- and you expose a full CRUD API on `127.0.0.1`

That combination is a classic “**malicious website steals your localhost app data**” pattern. With CORS `*`, any site the user visits can potentially read responses from your local server if it can reach it (standalone server mode on `127.0.0.1:8080` is especially easy to target).

Even without CORS, a hostile site can still *send* requests (CSRF-to-localhost). With CORS `*`, you make *reading* the results easier too.

What to do:
- Default stance: **no CORS at all** unless you have a real cross-origin client.
- Add a per-install secret and require it via a header (simple and effective for localhost apps).
- Consider binding to a **random port + random path prefix**, and avoid printing it anywhere sensitive.

### 2) XSS is very likely (server + client)

You sometimes render HTML by doing `fmt.Fprintf(..., "%s", message)` in handlers instead of using `html/template`.

Examples:
- `internal/handlers/handlers.go`: `renderError()`, `handleValidationErrorHTMX()`, `handleNotFoundHTMX()` interpolate unescaped strings into HTML.
- `internal/handlers/settings.go`: success HTML includes `location.Name` (user-controlled) without escaping.
- `web/static/js/route-copy.js`: toast rendering uses `innerHTML` and interpolates message content directly.

Because addresses/names can be user input, and error messages can include those values (and/or third-party error bodies), **this is a real XSS vector** inside your desktop webview / browser mode.

What to do:
- Never build HTML with string formatting for dynamic content. Use templates or `html.EscapeString`.
- In the frontend, build DOM nodes and set `textContent`, not `innerHTML`, for user-/network-derived content.

### 3) “Privacy-first” claim doesn’t match implementation

Current reality:
- You log **names and addresses** in many places (`internal/handlers/*`, `internal/geocoding/nominatim.go`, routing logs, etc.).
- You write user data to `~/.ride-home-router/` with `0755` directory perms and `0644` file perms (`internal/database/paths.go`, `internal/database/json_store.go`, `internal/database/file_distance_cache.go`). On multi-user systems, that’s potentially readable by other local users.
- You send addresses/coordinates to third parties (Nominatim + OSRM). That’s inherent to this design, but it needs to be framed honestly.

What to do:
- Default log level should not include PII. Add structured logging + redaction or “safe logging” helpers.
- Use `0700` for the app directory and `0600` for data/cache files where it makes sense.
- Be explicit in docs/UI: “Offline after geocoding” is not actually true if you still need OSRM distances for new pairs.

## P1: Big correctness/UX problems (users will notice)

### 1) JSON store “not found” errors map to 500s

Handlers check `err == sql.ErrNoRows` (`checkNotFound`) to emit 404s.

But the JSON repositories frequently return `fmt.Errorf("... not found")` instead of `sql.ErrNoRows` (e.g., delete methods in `internal/database/json_store.go`).

Net effect: deleting a missing participant/driver/event can produce an internal error instead of a clean 404.

What to do:
- Define a package-level sentinel error like `database.ErrNotFound` and use it everywhere (JSON + SQLite).
- Or standardize on `sql.ErrNoRows` across repos (less “clean”, but works).

### 2) Institute vehicle selection logic is inconsistent

In `internal/handlers/routes.go`, route calculation always loads the institute vehicle via `GetInstituteVehicle()` and passes it to the router, regardless of whether the user selected that driver for the event.

The UI implies institute vehicle use is optional per-event. The backend treats it as globally available.

What to do:
- Make institute vehicle participation explicit: only include it if selected for the current calculation.

### 3) Route editing ignores institute vehicle capacity and detour recomputation

In `internal/handlers/route_edit.go`:
- Capacity check is skipped when `toRoute.UsedInstituteVehicle` is true. That lets users overfill the institute vehicle route.
- Distances are recomputed after edits, but **detour/baseline fields aren’t**. UI will show stale detour numbers after edits/swaps.

What to do:
- Enforce capacity uniformly.
- Recompute route duration + detour, or hide detour after edits if it’s no longer valid.

### 4) RouteSessionStore is not concurrency-safe in practice

You protect the session map with a mutex, but you return a pointer to a session and then mutate it without any lock. Concurrent requests (HTMX + fetch + double clicks) can race.

What to do:
- Either keep a lock while mutating, or give each `RouteSession` its own mutex.
- Or avoid mutation: store immutable snapshots and replace atomically.

## P2: Architecture/maintainability problems

### 1) Two persistence layers exist; one is dead code

You have a fairly complete SQLite schema + repositories (`internal/database/db.go`, `schema.sql`, repo implementations) **and** a JSON store (`internal/database/json_store.go`) — but the server always uses JSON (`internal/server/server.go`).

This is a maintenance trap:
- twice the surface area
- different behavior (sorting, uniqueness guarantees, error semantics)
- more code to keep secure and correct

Pick one:
- If you want reliability and performance: finish SQLite migration and delete JSON store.
- If you want simplicity: delete SQLite code and dependencies, and harden JSON store.

Right now you have the costs of both without the benefits of either.

### 2) “Router” implementations sprawl without a clear decision

There are multiple routing strategies (`greedy`, `fairness`, `distance_minimizer`) but only one is wired (`NewDistanceMinimizer` in `internal/server/server.go`).

That can be fine if intentional, but then:
- expose the choice explicitly (config/UI toggle), or
- delete the unused implementations to reduce cognitive load.

### 3) Over-logging and lack of log levels

You log a lot of operational detail and PII at `log.Printf` level across packages.

What to do:
- Add a log interface with levels (debug/info/warn/error).
- Make “debug routing logs” opt-in.

## Build/CI/Release notes

- Go toolchain mismatch: `go.mod` uses `go 1.25.4`, but `.github/workflows/build.yml` installs Go `1.22`. That’s either an oversight or your CI is broken.
- CI installs `wails@latest`. That’s a reproducibility risk; pin versions.
- macOS workflow installs `create-dmg` via brew at build time. Works, but again: reproducibility + speed.

## Testing (currently: none)

There are zero `_test.go` files. That’s the biggest “confidence gap” in the repo.

Minimum tests that would pay off immediately:
- Routing invariants: never exceed capacity; all selected participants assigned; institute vehicle rules.
- JSON store: CRUD + migration + error semantics.
- Distance cache: rounding/keying behavior and Set/Get correctness.

Also consider adding at least one integration-style test that runs route calculation with a fake distance provider (no network).

## Suggested roadmap (pragmatic, not aspirational)

**Week 1 (P0/P1)**
- Remove/lock down CORS, add a localhost auth secret, basic CSRF defense.
- Kill XSS by eliminating all string-built HTML and `innerHTML` usage for dynamic content.
- Stop logging PII by default; reduce logs.
- Fix JSON store not-found semantics + institute vehicle selection consistency.

**Week 2 (P2)**
- Pick JSON *or* SQLite and delete the other path.
- Add tests for routing + persistence invariants.

**Later**
- Configurable OSRM/Nominatim endpoints (self-hosting for orgs that care).
- Better UX around offline mode and external calls (“This will contact OSM/OSRM”).

## Bottom line

This repo is close to being a genuinely solid small app — but it’s currently too casual about security/privacy for the kind of data it handles (names + home addresses). Fix the localhost + XSS + file perms issues first, then unify the persistence story, then add tests so you can keep shipping without fear.
