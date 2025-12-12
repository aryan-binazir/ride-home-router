# Backend Integration Guide

Quick reference for integrating the htmx frontend with the Go backend.

## Template Setup

```go
package main

import (
    "html/template"
    "encoding/json"
    "reflect"
    "time"
)

// Load templates with custom functions
func loadTemplates() *template.Template {
    funcMap := template.FuncMap{
        // Arithmetic
        "add": func(a, b int) int {
            return a + b
        },
        "divideFloat": func(a, b float64) float64 {
            if b == 0 {
                return 0
            }
            return a / b
        },

        // Formatting
        "formatDate": func(t time.Time) string {
            return t.Format("Jan 2, 2006")
        },
        "currentDate": func() string {
            return time.Now().Format("2006-01-02")
        },
        "printf": fmt.Sprintf,

        // JSON
        "toJSON": func(v interface{}) string {
            b, err := json.Marshal(v)
            if err != nil {
                return "{}"
            }
            return string(b)
        },

        // Comparisons
        "eq": reflect.DeepEqual,
        "ne": func(a, b interface{}) bool {
            return !reflect.DeepEqual(a, b)
        },
        "gt": func(a, b int) bool {
            return a > b
        },
    }

    tmpl := template.New("").Funcs(funcMap)
    tmpl = template.Must(tmpl.ParseGlob("web/templates/*.html"))
    tmpl = template.Must(tmpl.ParseGlob("web/templates/partials/*.html"))

    return tmpl
}
```

## Static File Serving

```go
func main() {
    // Serve static files
    fs := http.FileServer(http.Dir("web/static"))
    http.Handle("/static/", http.StripPrefix("/static/", fs))

    // ... other routes
}
```

## htmx Request Detection

```go
func isHtmxRequest(r *http.Request) bool {
    return r.Header.Get("HX-Request") == "true"
}

func renderTemplate(w http.ResponseWriter, r *http.Request, fullTemplate, partialTemplate string, data interface{}) {
    var tmplName string
    if isHtmxRequest(r) {
        tmplName = partialTemplate
    } else {
        tmplName = fullTemplate
    }

    if err := templates.ExecuteTemplate(w, tmplName, data); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}
```

## Page Handlers (Full HTML)

### Index Page
```go
func handleIndex(w http.ResponseWriter, r *http.Request) {
    data := struct {
        Title              string
        ActivePage         string
        Participants       []models.Participant
        Drivers            []models.Driver
        HasInstituteVehicle bool
    }{
        Title:      "Event Planning",
        ActivePage: "home",
        // ... fetch data from DB
    }

    templates.ExecuteTemplate(w, "index.html", data)
}
```

### Participants Page
```go
func handleParticipantsPage(w http.ResponseWriter, r *http.Request) {
    participants, _ := participantRepo.List(r.Context(), "")

    data := struct {
        Title        string
        ActivePage   string
        Participants []models.Participant
    }{
        Title:        "Participants",
        ActivePage:   "participants",
        Participants: participants,
    }

    templates.ExecuteTemplate(w, "participants.html", data)
}
```

### Similar for drivers.html, settings.html, history.html

## API Handlers (HTML Fragments for htmx)

### Participants

#### GET /api/v1/participants/new
```go
func handleGetParticipantForm(w http.ResponseWriter, r *http.Request) {
    data := struct {
        Participant models.Participant
    }{}

    templates.ExecuteTemplate(w, "participant_form", data)
}
```

#### GET /api/v1/participants/{id}/edit
```go
func handleEditParticipantForm(w http.ResponseWriter, r *http.Request) {
    id := getIDFromPath(r)
    participant, err := participantRepo.GetByID(r.Context(), id)
    if err != nil {
        http.Error(w, "Not found", http.StatusNotFound)
        return
    }

    data := struct {
        Participant *models.Participant
    }{
        Participant: participant,
    }

    templates.ExecuteTemplate(w, "participant_form", data)
}
```

#### POST /api/v1/participants
```go
func handleCreateParticipant(w http.ResponseWriter, r *http.Request) {
    // Parse form
    name := r.FormValue("name")
    address := r.FormValue("address")

    // Geocode
    coords, err := geocoder.Geocode(r.Context(), address)
    if err != nil {
        http.Error(w, "Geocoding failed", http.StatusUnprocessableEntity)
        return
    }

    // Create participant
    p := &models.Participant{
        Name:    name,
        Address: address,
        Coords:  coords.Coords,
    }

    created, err := participantRepo.Create(r.Context(), p)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Return updated list
    participants, _ := participantRepo.List(r.Context(), "")
    data := struct {
        Participants []models.Participant
    }{
        Participants: participants,
    }

    templates.ExecuteTemplate(w, "participant_list", data)
}
```

#### PUT /api/v1/participants/{id}
```go
func handleUpdateParticipant(w http.ResponseWriter, r *http.Request) {
    id := getIDFromPath(r)

    // Similar to create, but call Update instead
    // Return participant_list template
}
```

#### DELETE /api/v1/participants/{id}
```go
func handleDeleteParticipant(w http.ResponseWriter, r *http.Request) {
    id := getIDFromPath(r)

    if err := participantRepo.Delete(r.Context(), id); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Return empty (htmx will swap outerHTML to remove the row)
    w.WriteHeader(http.StatusOK)
}
```

### Drivers (Similar Pattern)

#### GET /api/v1/drivers/new
```go
func handleGetDriverForm(w http.ResponseWriter, r *http.Request) {
    data := struct {
        Driver models.Driver
    }{}

    templates.ExecuteTemplate(w, "driver_form", data)
}
```

#### POST /api/v1/drivers
```go
func handleCreateDriver(w http.ResponseWriter, r *http.Request) {
    // Parse form
    name := r.FormValue("name")
    address := r.FormValue("address")
    capacityStr := r.FormValue("vehicle_capacity")
    isInstituteVehicle := r.FormValue("is_institute_vehicle") == "true"

    capacity, _ := strconv.Atoi(capacityStr)

    // Geocode address
    coords, err := geocoder.Geocode(r.Context(), address)
    if err != nil {
        http.Error(w, "Geocoding failed", http.StatusUnprocessableEntity)
        return
    }

    // Create driver
    d := &models.Driver{
        Name:               name,
        Address:            address,
        Coords:             coords.Coords,
        VehicleCapacity:    capacity,
        IsInstituteVehicle: isInstituteVehicle,
    }

    created, err := driverRepo.Create(r.Context(), d)
    if err != nil {
        // Check for institute vehicle conflict
        http.Error(w, err.Error(), http.StatusConflict)
        return
    }

    // Return updated list
    drivers, _ := driverRepo.List(r.Context(), "")
    data := struct {
        Drivers []models.Driver
    }{
        Drivers: drivers,
    }

    templates.ExecuteTemplate(w, "driver_list", data)
}
```

### Settings

#### PUT /api/v1/settings
```go
func handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
    address := r.FormValue("institute_address")

    // Geocode
    coords, err := geocoder.Geocode(r.Context(), address)
    if err != nil {
        // Return error message fragment
        w.WriteHeader(http.StatusUnprocessableEntity)
        fmt.Fprintf(w, `<div class="alert alert-danger">Could not geocode address: %s</div>`, err)
        return
    }

    // Update settings
    settings := &models.Settings{
        InstituteAddress: address,
        InstituteCoords:  coords.Coords,
    }

    if err := settingsRepo.Update(r.Context(), settings); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Return success message
    fmt.Fprintf(w, `<div class="alert alert-success">Settings saved successfully! Coordinates: %.6f, %.6f</div>`,
        coords.Coords.Lat, coords.Coords.Lng)
}
```

### Routes

#### POST /api/v1/routes/calculate
```go
func handleCalculateRoutes(w http.ResponseWriter, r *http.Request) {
    // Parse form
    r.ParseForm()
    participantIDs := parseIntSlice(r.Form["participant_ids"])
    driverIDs := parseIntSlice(r.Form["driver_ids"])
    instituteVehicleDriverIDStr := r.FormValue("institute_vehicle_driver_id")

    var instituteVehicleDriverID int64
    if instituteVehicleDriverIDStr != "" {
        instituteVehicleDriverID, _ = strconv.ParseInt(instituteVehicleDriverIDStr, 10, 64)
    }

    // Fetch data
    participants, _ := participantRepo.GetByIDs(r.Context(), participantIDs)
    drivers, _ := driverRepo.GetByIDs(r.Context(), driverIDs)
    settings, _ := settingsRepo.Get(r.Context())
    instituteVehicle, _ := driverRepo.GetInstituteVehicle(r.Context())

    // Calculate routes
    req := &routing.RoutingRequest{
        InstituteCoords:          settings.InstituteCoords,
        Participants:             participants,
        Drivers:                  drivers,
        InstituteVehicle:         instituteVehicle,
        InstituteVehicleDriverID: instituteVehicleDriverID,
    }

    result, err := router.CalculateRoutes(r.Context(), req)
    if err != nil {
        // Return error fragment
        data := struct {
            Error struct {
                Message string
                Details map[string]interface{}
            }
        }{
            Error: struct {
                Message string
                Details map[string]interface{}
            }{
                Message: err.Error(),
                Details: map[string]interface{}{
                    "UnassignedCount":   routingErr.UnassignedCount,
                    "TotalCapacity":     routingErr.TotalCapacity,
                    "TotalParticipants": routingErr.TotalParticipants,
                },
            },
        }

        w.WriteHeader(http.StatusUnprocessableEntity)
        templates.ExecuteTemplate(w, "route_results", data)
        return
    }

    // Return route results
    templates.ExecuteTemplate(w, "route_results", result)
}
```

### Events

#### GET /api/v1/events
```go
func handleGetEvents(w http.ResponseWriter, r *http.Request) {
    limitStr := r.URL.Query().Get("limit")
    offsetStr := r.URL.Query().Get("offset")

    limit, _ := strconv.Atoi(limitStr)
    offset, _ := strconv.Atoi(offsetStr)

    if limit == 0 {
        limit = 20
    }

    events, total, _ := eventRepo.List(r.Context(), limit, offset)

    data := struct {
        Events []models.Event
        Total  int
    }{
        Events: events,
        Total:  total,
    }

    templates.ExecuteTemplate(w, "event_list", data)
}
```

#### GET /api/v1/events/{id}
```go
func handleGetEvent(w http.ResponseWriter, r *http.Request) {
    id := getIDFromPath(r)

    event, assignments, summary, err := eventRepo.GetByID(r.Context(), id)
    if err != nil {
        http.Error(w, "Not found", http.StatusNotFound)
        return
    }

    data := struct {
        Event       *models.Event
        Assignments []models.EventAssignment
        Summary     *models.EventSummary
    }{
        Event:       event,
        Assignments: assignments,
        Summary:     summary,
    }

    templates.ExecuteTemplate(w, "event_detail", data)
}
```

#### POST /api/v1/events
```go
func handleSaveEvent(w http.ResponseWriter, r *http.Request) {
    r.ParseForm()

    eventDateStr := r.FormValue("event_date")
    notes := r.FormValue("notes")
    routesJSON := r.FormValue("routes_json")

    // Parse event date
    eventDate, _ := time.Parse("2006-01-02", eventDateStr)

    // Parse routes JSON
    var routes models.RoutingResult
    json.Unmarshal([]byte(routesJSON), &routes)

    // Create event
    event := &models.Event{
        EventDate: eventDate,
        Notes:     notes,
    }

    // Convert routes to assignments
    assignments := convertRoutesToAssignments(routes)
    summary := createEventSummary(routes)

    created, err := eventRepo.Create(r.Context(), event, assignments, summary)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Return success message
    fmt.Fprintf(w, `<div class="alert alert-success">Event saved! <a href="/history">View in history</a></div>`)
}
```

## Router Setup

```go
func main() {
    // Load templates
    templates = loadTemplates()

    // Static files
    fs := http.FileServer(http.Dir("web/static"))
    http.Handle("/static/", http.StripPrefix("/static/", fs))

    // Page routes (full HTML)
    http.HandleFunc("/", handleIndex)
    http.HandleFunc("/participants", handleParticipantsPage)
    http.HandleFunc("/drivers", handleDriversPage)
    http.HandleFunc("/settings", handleSettingsPage)
    http.HandleFunc("/history", handleHistoryPage)

    // API routes (HTML fragments for htmx)
    http.HandleFunc("/api/v1/participants/new", handleGetParticipantForm)
    http.HandleFunc("/api/v1/participants/{id}/edit", handleEditParticipantForm)
    http.HandleFunc("/api/v1/participants", handleParticipantsCRUD)
    http.HandleFunc("/api/v1/participants/{id}", handleParticipantByID)

    http.HandleFunc("/api/v1/drivers/new", handleGetDriverForm)
    http.HandleFunc("/api/v1/drivers/{id}/edit", handleEditDriverForm)
    http.HandleFunc("/api/v1/drivers", handleDriversCRUD)
    http.HandleFunc("/api/v1/drivers/{id}", handleDriverByID)

    http.HandleFunc("/api/v1/settings", handleSettings)
    http.HandleFunc("/api/v1/routes/calculate", handleCalculateRoutes)

    http.HandleFunc("/api/v1/events", handleEventsCRUD)
    http.HandleFunc("/api/v1/events/{id}", handleEventByID)

    // Start server
    http.ListenAndServe(":8080", nil)
}
```

## Helper Functions

```go
func getIDFromPath(r *http.Request) int64 {
    // Extract ID from path
    // Implementation depends on your router
    // Example for standard library:
    pathParts := strings.Split(r.URL.Path, "/")
    idStr := pathParts[len(pathParts)-1]
    if strings.Contains(idStr, "/edit") {
        idStr = strings.TrimSuffix(idStr, "/edit")
    }
    id, _ := strconv.ParseInt(idStr, 10, 64)
    return id
}

func parseIntSlice(strs []string) []int64 {
    var result []int64
    for _, s := range strs {
        i, err := strconv.ParseInt(s, 10, 64)
        if err == nil {
            result = append(result, i)
        }
    }
    return result
}
```

## Important Notes

1. **htmx requests return HTML fragments**, not JSON
2. **Regular browser requests return full HTML pages**
3. **Check `HX-Request` header** to differentiate
4. **Forms use standard form encoding**, not JSON
5. **Template names** must match exactly as defined in partials
6. **Error handling** should return HTML fragments for htmx requests
7. **DELETE requests** typically return empty body (htmx swaps element out)
8. **Success messages** can be HTML fragments that htmx inserts

## Testing

```bash
# Test full page load
curl http://localhost:8080/participants

# Test htmx fragment
curl -H "HX-Request: true" http://localhost:8080/api/v1/participants/new

# Test POST (form data)
curl -X POST -d "name=John&address=123 Main St" \
     -H "HX-Request: true" \
     http://localhost:8080/api/v1/participants

# Test DELETE
curl -X DELETE -H "HX-Request: true" \
     http://localhost:8080/api/v1/participants/1
```
