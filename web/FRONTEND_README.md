# Ride Home Router - Frontend Implementation

## Overview

This directory contains the complete frontend implementation for the Ride Home Router application using htmx and server-side rendering with Go templates.

## Technology Stack

- **htmx 2.0.4** - For dynamic interactions without custom JavaScript
- **HTML5** - Semantic markup
- **CSS3** - Custom styling with CSS variables
- **Go html/template** - Server-side templating

## Directory Structure

```
web/
├── static/
│   ├── css/
│   │   └── style.css              # Complete application styles
│   └── js/
│       └── htmx.min.js            # htmx library v2.0.4
└── templates/
    ├── layout.html                # Base layout with navigation
    ├── index.html                 # Event planning page
    ├── participants.html          # Participant roster management
    ├── drivers.html               # Driver roster management
    ├── settings.html              # Settings page
    ├── history.html               # Event history
    └── partials/
        ├── participant_list.html  # Participant table
        ├── participant_form.html  # Add/edit participant form
        ├── participant_row.html   # Single participant row
        ├── driver_list.html       # Driver table
        ├── driver_form.html       # Add/edit driver form
        ├── driver_row.html        # Single driver row
        ├── route_results.html     # Calculated routes display
        ├── event_list.html        # Event history list
        └── event_detail.html      # Single event detail view
```

## Pages

### 1. Event Planning (index.html)
**Route:** `/`

Main page for creating a new event:
- Select participants (checkboxes)
- Select available drivers (checkboxes)
- Special handling for institute vehicle driver selection
- Calculate routes button with loading indicator
- Display calculated routes
- Save event to history

**Key Features:**
- Select all/clear all buttons
- Dynamic institute vehicle driver dropdown (shows only when institute vehicle is selected)
- Real-time route calculation via htmx
- Inline event saving after calculation

### 2. Participants Roster (participants.html)
**Route:** `/participants`

CRUD interface for participants:
- List all participants with name, address, coordinates
- Add new participant (inline form)
- Edit existing participant (inline form)
- Delete participant (with confirmation)
- Real-time geocoding on add/edit

**htmx Patterns:**
- GET `/api/v1/participants/new` → Load add form
- GET `/api/v1/participants/{id}/edit` → Load edit form
- POST `/api/v1/participants` → Create participant
- PUT `/api/v1/participants/{id}` → Update participant
- DELETE `/api/v1/participants/{id}` → Delete participant

### 3. Drivers Roster (drivers.html)
**Route:** `/drivers`

CRUD interface for drivers:
- List all drivers with name, address, capacity, type
- Add new driver (inline form)
- Edit existing driver (inline form)
- Delete driver (with confirmation)
- Institute vehicle toggle (only one allowed)

**htmx Patterns:**
- GET `/api/v1/drivers/new` → Load add form
- GET `/api/v1/drivers/{id}/edit` → Load edit form
- POST `/api/v1/drivers` → Create driver
- PUT `/api/v1/drivers/{id}` → Update driver
- DELETE `/api/v1/drivers/{id}` → Delete driver

### 4. Settings (settings.html)
**Route:** `/settings`

Configure application settings:
- Set institute address
- Automatic geocoding on save
- Display current coordinates

**htmx Patterns:**
- PUT `/api/v1/settings` → Update settings

### 5. Event History (history.html)
**Route:** `/history`

View past events:
- List all events with date, summary stats
- Expandable detail view (lazy loaded)
- Delete events with confirmation
- Pagination support

**htmx Patterns:**
- GET `/api/v1/events` → Load events list
- GET `/api/v1/events/{id}` → Load event details
- DELETE `/api/v1/events/{id}` → Delete event

## Template System

### Layout Template
`layout.html` defines the base structure:
- HTML head with CSS and htmx
- Header with navigation
- Main content area
- Uses `{{template "content" .}}` for page-specific content

### Content Templates
Each page defines a `content` block that gets injected into the layout.

### Partials
Reusable fragments that return HTML for htmx requests:
- Lists (participant_list, driver_list, event_list)
- Forms (participant_form, driver_form)
- Rows (participant_row, driver_row)
- Results (route_results, event_detail)

## htmx Integration

### Core Attributes Used

- `hx-get` - HTTP GET request
- `hx-post` - HTTP POST request
- `hx-put` - HTTP PUT request
- `hx-delete` - HTTP DELETE request
- `hx-target` - Element to update
- `hx-swap` - How to swap content (innerHTML, outerHTML, beforeend)
- `hx-indicator` - Loading indicator element
- `hx-confirm` - Confirmation dialog before action
- `hx-trigger` - When to trigger request

### Request/Response Pattern

**Server expectations:**
1. Detect htmx requests via `HX-Request` header
2. Return HTML fragments for htmx requests (not full pages)
3. Return full pages for normal browser requests

**Example:**
```go
if r.Header.Get("HX-Request") == "true" {
    // Return partial template
    tmpl.ExecuteTemplate(w, "participant_row", data)
} else {
    // Return full page
    tmpl.ExecuteTemplate(w, "participants.html", data)
}
```

### Loading States

All forms/buttons show loading indicators:
```html
<button hx-post="/api/v1/participants"
        hx-indicator="#loading">
    Add Participant
</button>
<span class="loading htmx-indicator" id="loading"></span>
```

CSS automatically shows/hides indicators:
```css
.htmx-indicator { display: none; }
.htmx-request .htmx-indicator { display: inline-block; }
```

## CSS Architecture

### Design System

**Colors:**
- Primary: Blue (#2563eb)
- Success: Green (#10b981)
- Danger: Red (#ef4444)
- Warning: Orange (#f59e0b)

**Spacing Scale:**
- xs: 0.25rem
- sm: 0.5rem
- md: 1rem
- lg: 1.5rem
- xl: 2rem

### Component Styles

**Buttons:**
- `.btn` - Base button
- `.btn-primary`, `.btn-secondary`, `.btn-success`, `.btn-danger`
- `.btn-sm`, `.btn-lg` - Size variants
- `.btn-outline` - Outline style

**Forms:**
- `.form-group` - Form field container
- `.form-label` - Field label
- `.form-input`, `.form-select`, `.form-textarea` - Form controls
- `.form-error`, `.form-help` - Helper text

**Tables:**
- Responsive with `.table-container`
- Hover states on rows
- `.table-actions` - Action button container

**Cards:**
- `.card` - Card container
- `.card-header` - Card header with title and actions

**Alerts:**
- `.alert-info`, `.alert-success`, `.alert-warning`, `.alert-danger`

**Routes Display:**
- `.route-card` - Individual driver route
- `.route-card.institute-vehicle` - Special styling for institute vehicle
- `.stop-item` - Individual stop in route
- `.route-summary` - Summary statistics

### Responsive Design

Mobile-friendly breakpoint at 768px:
- Stacked forms
- Scrollable tables
- Adjusted navigation

## Template Functions Expected

The backend should provide these template functions:

```go
// Arithmetic
"add": func(a, b int) int { return a + b }
"divideFloat": func(a, b float64) float64 { return a / b }

// Formatting
"formatDate": func(t time.Time) string { return t.Format("Jan 2, 2006") }
"currentDate": func() string { return time.Now().Format("2006-01-02") }

// JSON
"toJSON": func(v interface{}) string { ... }

// Comparisons
"eq": reflect.DeepEqual
"ne": func(a, b interface{}) bool { return !reflect.DeepEqual(a, b) }
"gt": func(a, b int) bool { return a > b }
```

## API Endpoint Mapping

All htmx requests go to REST API endpoints defined in ARCHITECTURE.md:

### Participants
- `GET /api/v1/participants` → List
- `GET /api/v1/participants/new` → New form (frontend route)
- `GET /api/v1/participants/{id}/edit` → Edit form (frontend route)
- `POST /api/v1/participants` → Create
- `PUT /api/v1/participants/{id}` → Update
- `DELETE /api/v1/participants/{id}` → Delete

### Drivers
- `GET /api/v1/drivers` → List
- `GET /api/v1/drivers/new` → New form (frontend route)
- `GET /api/v1/drivers/{id}/edit` → Edit form (frontend route)
- `POST /api/v1/drivers` → Create
- `PUT /api/v1/drivers/{id}` → Update
- `DELETE /api/v1/drivers/{id}` → Delete

### Routes
- `POST /api/v1/routes/calculate` → Calculate routes

### Events
- `GET /api/v1/events` → List events
- `GET /api/v1/events/{id}` → Event details
- `POST /api/v1/events` → Save event
- `DELETE /api/v1/events/{id}` → Delete event

### Settings
- `GET /api/v1/settings` → Get settings
- `PUT /api/v1/settings` → Update settings

## Form Data Handling

### Standard Form Encoding
Most forms use standard `application/x-www-form-urlencoded`:

```html
<form hx-post="/api/v1/participants">
    <input name="name" value="John Doe">
    <input name="address" value="123 Main St">
</form>
```

Backend receives as form values.

### Checkboxes (Multiple Values)
Participant/driver selection uses checkboxes with same name:

```html
<input type="checkbox" name="participant_ids" value="1">
<input type="checkbox" name="participant_ids" value="2">
```

Backend receives as array of IDs.

### Save Event (Complex Data)
The save event form needs to send the calculated routes JSON:

```html
<input type="hidden" name="routes_json" value="{{toJSON .}}">
```

Backend should parse this JSON field.

## Browser Compatibility

- Modern browsers (Chrome, Firefox, Safari, Edge)
- ES5+ JavaScript (for htmx)
- CSS Grid and Flexbox support required
- No IE11 support

## Accessibility

- Semantic HTML elements
- Form labels properly associated
- ARIA attributes where needed
- Keyboard navigation support
- Focus states on interactive elements

## Performance Considerations

- CSS is ~14KB uncompressed
- htmx is ~50KB minified
- No external dependencies
- All assets served locally
- Minimal JavaScript (only htmx + ~20 lines for UI helpers)

## Development Notes

### Testing Templates

To test templates without backend:
1. Create mock data structures matching the Go models
2. Use Go's template testing utilities
3. Verify template syntax with `go run`

### Adding New Pages

1. Create template in `templates/`
2. Define `{{define "content"}}` block
3. Add navigation link in `layout.html`
4. Create any needed partials in `partials/`
5. Add route handler in backend

### Debugging htmx

Enable htmx logging in browser console:
```javascript
htmx.logAll();
```

Check network tab for:
- Request headers (HX-Request: true)
- Response content (HTML fragments)
- Status codes

## Future Enhancements

Potential improvements:
- Add search/filter to participant/driver lists
- Add sorting to tables
- Add keyboard shortcuts
- Add print stylesheet for routes
- Add export to PDF/CSV functionality
- Add map visualization of routes
- Progressive Web App (PWA) support for offline use

## Notes

- All templates use Go's `html/template` syntax
- No frontend build process required
- All JavaScript is provided by htmx (no custom JS)
- UI helpers (select all, clear) use minimal inline JS
- CSS uses modern features but gracefully degrades
- Forms validate on client-side with HTML5, server validates too
