---
name: verify
description: Run the real ride-home-router server locally with a synthetic Clerk backend and drive the browser UI headlessly to verify a change at runtime.
---

# Verify a change at runtime

Clerk is required to start the server, so the real binary cannot run without a Clerk instance. Instead run the real server package (`server.New`: real mux, handlers, templates, Postgres) from a Go test that supplies the `accesstest` synthetic Clerk backend. Everything above `main()`'s env parsing is the production code path.

## 1. Scratch database (Podman Postgres on 5434)

```bash
podman start ride-home-router-postgres 2>/dev/null; true
podman exec ride-home-router-postgres psql -U postgres -c "create database ride_home_router_verify"
DATABASE_URL='postgres://postgres:postgres@localhost:5434/ride_home_router_verify?sslmode=disable' go run ./cmd/migrate
```

Seed rows directly (geocoding needs Google, so give coordinates): `activity_locations(name,address,lat,lng,geocoded_at)`, `drivers(... ,vehicle_capacity, ...)`, `participants(...)`. Boston-area coordinates ~0.01° apart work with the default estimate routing engine.

## 2. Server harness

Copy `harness_test.go.txt` from this directory to `_scratch/verify/harness_test.go` (gitignored; `_`-prefixed dirs are skipped by `./...` but run when named explicitly). It listens on 127.0.0.1:8099, writes an admin bearer token to `/tmp/verify-token`, and stops when `/tmp/verify-stop` exists.

```bash
export DATABASE_URL='postgres://postgres:postgres@localhost:5434/ride_home_router_verify?sslmode=disable'
export TRUST_CF_ACCESS_HEADER=true CREDENTIAL_ENCRYPTION_KEY="$(head -c 32 /dev/urandom | base64)"
setsid nohup go test ./_scratch/verify/ -run TestServe -count=1 -timeout 2h -v > /tmp/verify-server.log 2>&1 < /dev/null &
curl -s -H "Authorization: Bearer $(cat /tmp/verify-token)" http://127.0.0.1:8099/settings | head
```

The admin is `admin@example.test`. Every request needs `Authorization: Bearer <token>` (Bearer skips the Origin requirement for writes). The reviewer identity is the separate `Cf-Access-Authenticated-User-Email` header.

## 3. Drive the UI headlessly

No Playwright is installed; `drive.mjs` is a ~100-line CDP driver over `/usr/bin/chromium` using Node's built-in WebSocket. `node drive.mjs <cf-email|-> <scenario>`; screenshots land in `/tmp/verify-shots/`. Extend the scenario switch for new flows.

Gotchas that cost time:
- Block `*/js/auth.js*` (`Network.setBlockedURLs`); the Clerk script cannot load and the browser tests strip it too.
- **Synthetic `element.click()` does not trigger htmx `hx-trigger="click[...]"` buttons.** Use `Input.dispatchMouseEvent` at the element's box (`realClick` in the driver). `form.requestSubmit()` is fine for htmx forms.
- Toasts stack over the bottom-right of the page and swallow real clicks on the Save event button. Remove `.toast` nodes first.
- The planner does not preselect the settings' activity location; click "Choose location" and pick one through the `#route-editor` dialog.

## 4. Stop and clean up

```bash
touch /tmp/verify-stop
podman exec ride-home-router-postgres psql -U postgres -c "drop database ride_home_router_verify"
```
