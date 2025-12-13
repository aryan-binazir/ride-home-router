# Code Quality Analysis: Ride Home Router

**Date:** 2025-12-13
**Analyst:** Claude Code (multi-agent analysis)

---

## Executive Summary

This codebase analysis found **1 critical architectural issue**, **4 high-severity problems**, and **multiple medium-severity code duplications**. The good news: no unused imports, no dead JavaScript, and all templates are actively used. The bad news: there's approximately **500+ lines of duplicated/redundant code** and a confusing dual-architecture design.

---

## Critical Finding: Dual Architecture

### The Problem
You have **TWO parallel entry points** serving identical functionality:
- `main.go` + `app.go` → Wails desktop app
- `cmd/server/main.go` → Standalone web server

Both do the exact same thing. The Wails wrapper literally just navigates to `http://localhost` - it adds zero desktop features:

```go
// app.go - This is ALL it does
runtime.WindowExecJS(ctx, fmt.Sprintf(`window.location.href = "%s"`, a.url))
```

### Decision Required
**Pick one:**
- **Option A:** Remove Wails entirely → Delete `main.go`, `app.go`, `frontend/` (~500 lines saved)
- **Option B:** Remove standalone server → Delete `cmd/server/main.go`
- **Option C:** Keep both, document why (requires justification)

Currently Wails provides no value over just running the web server.

---

## High Severity Findings

### 1. Coordinate Rounding Bug (Inconsistent Implementations)

**Location:**
- `internal/distance/osrm.go:68-69`
- `internal/database/file_distance_cache.go:173-175`

**Problem:** Two different implementations that will produce different results:

```go
// osrm.go - uses proper rounding
roundLat := func(v float64) float64 { return float64(int(v*100000+0.5)) / 100000 }

// file_distance_cache.go - truncates instead of rounding
func roundCoord(f float64) float64 {
    return float64(int(f*100000)) / 100000  // BUG: no +0.5
}
```

**Fix:** Create shared utility:
```go
func RoundCoordinate(coord float64) float64 {
    return math.Round(coord*100000) / 100000
}
```

---

### 2. Massive CRUD Boilerplate (500+ lines → ~100 lines)

**Location:** `internal/database/json_store.go` (776 lines total)

**Problem:** Five nearly identical repository implementations for Participants, Drivers, Events, ActivityLocations, and OrganizationVehicles. Each has:
- `List()` - same sorting logic
- `GetByID()` - same loop-and-match pattern
- `GetByIDs()` - same idSet map pattern
- `Create()` - same ID assignment, timestamps, append
- `Update()` - same loop, timestamp update
- `Delete()` - same slice manipulation

**Example of copy-paste:**
```go
// Participant GetByID (lines 240-249)
for _, p := range r.store.data.Participants {
    if p.ID == id { return &p, nil }
}

// Driver GetByID (lines 361-370) - IDENTICAL STRUCTURE
for _, d := range r.store.data.Drivers {
    if d.ID == id { return &d, nil }
}
```

**Fix:** Use Go generics:
```go
type BaseRepository[T any, ID comparable] struct {
    store    *JSONStore
    getSlice func(*JSONData) []T
    setSlice func(*JSONData, []T)
}
```

---

### 3. Driver/Participant Form Templates (95% duplicate)

**Location:**
- `web/templates/partials/driver_form.html`
- `web/templates/partials/participant_form.html`

**Problem:** These forms are almost identical:
- Same card structure
- Same conditional hx-put/hx-post logic
- Same name input field
- **Identical 15-line address autocomplete block**
- Same submit button with loading indicator

Only difference: driver_form has a capacity field.

**Fix:** Create generic `entity_form.html` with conditional fields.

---

### 4. Address Autocomplete Block (Repeated 3x)

**Location:**
- `web/templates/partials/driver_form.html:31-50`
- `web/templates/partials/participant_form.html:31-50`
- `web/templates/settings.html:29-46`

**Same 15-line block copied 3 times:**
```html
<div class="address-autocomplete-wrapper">
    <input type="text"
           name="address"
           class="form-input"
           placeholder="Start typing an address..."
           hx-get="/api/v1/address-search"
           hx-trigger="input delay:500ms"
           hx-target="next .address-suggestions"
           hx-swap="innerHTML"
           hx-sync="this:replace"
           hx-include="this"
           autocomplete="off"
           required>
    <div class="address-suggestions"></div>
</div>
```

**Fix:** Create `partials/address_input.html` partial.

---

## Medium Severity Findings

### 5. HTMX Request Handling (74 occurrences)

**Pattern:** `if h.isHTMX(r)` appears 74 times with similar error handling logic.

**Example:**
```go
if h.isHTMX(r) {
    w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {...}}`, ...))
    w.WriteHeader(http.StatusBadRequest)
    return
}
```

**Fix:** Create helper methods:
```go
func (h *Handler) respondError(w, r, status, message)
func (h *Handler) respondSuccess(w, r, message, data)
```

---

### 6. URL ID Extraction (10 occurrences)

**Pattern repeated in drivers.go, participants.go, events.go:**
```go
idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
id, err := strconv.ParseInt(idStr, 10, 64)
if err != nil { /* error handling */ }
```

**Fix:** Create helper: `func extractIDFromPath(r, prefix) (int64, error)`

---

### 7. Form Parsing (11 occurrences)

**Pattern:**
```go
if err := r.ParseForm(); err != nil { /* error handling */ }
req.Name = r.FormValue("name")
req.Address = r.FormValue("address")
```

**Fix:** Create generic form parser.

---

### 8. Selection Panels Duplication (index.html)

**Location:** `web/templates/index.html:12-55` and `58-106`

The participants and drivers selection panels are **45 lines each** with nearly identical structure. Only text differs.

**Fix:** Create `selection_panel.html` partial.

---

### 9. List Card Structure (4 occurrences)

**Location:**
- `web/templates/partials/activity_location_row.html`
- `web/templates/partials/org_vehicle_row.html`
- `web/templates/settings.html` (2 places)

Same deletable card pattern repeated.

**Fix:** Create `deletable_list_card.html` partial.

---

### 10. Page Header Structure (5 occurrences)

**Location:** drivers.html, participants.html, settings.html, history.html, index.html

Same pattern:
```html
<div class="page-header">
    <h2 class="page-title">[TITLE]</h2>
    <p class="page-subtitle">[SUBTITLE]</p>
    <div class="page-actions">[BUTTONS]</div>
</div>
```

**Fix:** Create `page_header.html` partial.

---

### 11. Deprecated Fields Still in Use

**Location:** `internal/models/models.go:69-71`

```go
type Settings struct {
    InstituteAddress string  `json:"institute_address"` // Deprecated
    InstituteLat     float64 `json:"institute_lat"`     // Deprecated
    InstituteLng     float64 `json:"institute_lng"`     // Deprecated
    SelectedActivityLocationID int64  // New field
}
```

These deprecated fields are still being read/written in `json_store.go:468-486`.

**Fix:** Complete migration and remove deprecated fields.

---

### 12. Unused API Endpoint

**Location:** `internal/handlers/routes.go:374-409`

`HandleGeocodeAddress` for `/api/v1/geocode` is registered but never called. Actual geocoding uses `/api/v1/address-search` instead.

**Fix:** Delete the unused endpoint (~40 lines).

---

### 13. Browser Opening Duplication

**Location:**
- `cmd/server/main.go:73-86`
- `internal/server/server.go:577-588`

Same `openBrowser()` function implemented twice.

**Fix:** Move to shared utility.

---

## Low Severity Findings

### 14. Filter Functions Duplication (JavaScript)

`filterSelectList` in index.html is nearly identical to `filterTable` in ui.js.

**Fix:** Consolidate into single generic function.

---

### 15. Empty State Messages (7 occurrences)

Same `<div class="empty-state">` pattern repeated across templates.

**Fix:** Create `empty_state.html` partial.

---

### 16. Search Toolbar (2 occurrences)

Same search input structure in drivers.html and participants.html.

**Fix:** Create `search_toolbar.html` partial.

---

## What's Clean

- **No unused imports** (all Go imports are used)
- **No dead JavaScript** (all JS files actively used)
- **No orphaned templates** (all templates rendered somewhere)
- **No unused Go files** (all files reachable from entry points)
- Good separation of concerns in internal packages

---

## Summary Statistics

| Category | Count |
|----------|-------|
| HTMX checks (`if h.isHTMX(r)`) | 74 |
| Form parsing duplication | 11 |
| URL ID extraction duplication | 10 |
| JSON decode duplication | 14 |
| CRUD repository boilerplate | ~500 lines reducible to ~100 |
| Template redundancy | ~200-300 lines |
| Coordinate rounding implementations | 2 (conflicting!) |

---

## Priority Action Items

### Immediate (Bug Fix)
1. Fix coordinate rounding inconsistency in `file_distance_cache.go`

### High Priority (Architecture)
2. Decide on Wails vs standalone server architecture
3. Refactor JSON repository with Go generics (biggest code reduction)
4. Create `address_input.html` partial (used in 3 places)

### Medium Priority (Code Quality)
5. Create shared HTMX response helpers
6. Consolidate driver/participant forms
7. Remove unused `/api/v1/geocode` endpoint
8. Remove deprecated Settings fields

### Low Priority (Polish)
9. Create page_header, empty_state, search_toolbar partials
10. Consolidate browser opening code
11. Merge JS filter functions

---

## Estimated Impact

If all high-priority items are addressed:
- **~600 lines of code removed/consolidated**
- **1 potential bug fixed** (coordinate rounding)
- **Simpler architecture** (single entry point)
- **Easier maintenance** (less copy-paste)
