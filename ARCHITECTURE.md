# Ride Home Router - Architecture Document

## Executive Summary

This document defines the complete architecture for a local-first transport routing system that optimizes driver assignments for taking participants home after events. The system uses a greedy nearest-neighbor heuristic to solve a simplified Capacitated Vehicle Routing Problem (CVRP).

**Key Architectural Decisions:**
- Single SQLite database file for all data (portable, no external dependencies)
- Aggressive distance caching to minimize OSRM API calls
- Server-side rendering with htmx for reactive UI without JavaScript complexity
- Clean separation between routing logic and I/O concerns via interfaces

---

## 1. SQLite Schema DDL

```sql
-- schema.sql
-- Ride Home Router Database Schema
-- Version: 1.0.0

PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

-- ============================================================================
-- SETTINGS
-- ============================================================================
CREATE TABLE IF NOT EXISTS settings (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Pre-populate with required settings
INSERT OR IGNORE INTO settings (key, value) VALUES
    ('institute_address', ''),
    ('institute_lat', '0'),
    ('institute_lng', '0');

-- ============================================================================
-- PARTICIPANTS
-- ============================================================================
CREATE TABLE IF NOT EXISTS participants (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    address     TEXT NOT NULL,
    lat         REAL NOT NULL DEFAULT 0,
    lng         REAL NOT NULL DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_participants_name ON participants(name);

-- ============================================================================
-- DRIVERS
-- ============================================================================
CREATE TABLE IF NOT EXISTS drivers (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    name                    TEXT NOT NULL,
    address                 TEXT NOT NULL,
    lat                     REAL NOT NULL DEFAULT 0,
    lng                     REAL NOT NULL DEFAULT 0,
    vehicle_capacity        INTEGER NOT NULL DEFAULT 4 CHECK (vehicle_capacity > 0),
    is_institute_vehicle    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at              DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_drivers_name ON drivers(name);
CREATE UNIQUE INDEX idx_drivers_institute_vehicle ON drivers(is_institute_vehicle)
    WHERE is_institute_vehicle = TRUE;

-- ============================================================================
-- EVENTS (History)
-- ============================================================================
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_date  DATE NOT NULL,
    notes       TEXT DEFAULT '',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_events_date ON events(event_date DESC);

-- ============================================================================
-- EVENT ASSIGNMENTS (Denormalized snapshot)
-- ============================================================================
-- Stores the complete assignment snapshot for historical reference.
-- Driver and participant data is copied at event time for immutability.
CREATE TABLE IF NOT EXISTS event_assignments (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id            INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    driver_id           INTEGER NOT NULL,  -- Original driver ID (may not exist later)
    driver_name         TEXT NOT NULL,     -- Snapshot of driver name
    driver_address      TEXT NOT NULL,     -- Snapshot of driver home address
    route_order         INTEGER NOT NULL,  -- Order in this driver's route (0 = first stop)
    participant_id      INTEGER NOT NULL,  -- Original participant ID
    participant_name    TEXT NOT NULL,     -- Snapshot of participant name
    participant_address TEXT NOT NULL,     -- Snapshot of participant address
    distance_from_prev  REAL NOT NULL,     -- Distance in meters from previous stop
    used_institute_vehicle BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX idx_event_assignments_event ON event_assignments(event_id);
CREATE INDEX idx_event_assignments_driver ON event_assignments(event_id, driver_id, route_order);

-- ============================================================================
-- EVENT SUMMARY (Aggregate stats per event)
-- ============================================================================
CREATE TABLE IF NOT EXISTS event_summaries (
    event_id                INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    total_participants      INTEGER NOT NULL,
    total_drivers           INTEGER NOT NULL,
    total_distance_meters   REAL NOT NULL,
    used_institute_vehicle  BOOLEAN NOT NULL DEFAULT FALSE,
    institute_vehicle_driver_name TEXT  -- If institute vehicle was used, who drove it
);

-- ============================================================================
-- DISTANCE CACHE
-- ============================================================================
-- Caches OSRM distance lookups. Key is ordered pair of coordinates.
-- Cache is bidirectional-aware: we store both directions if they differ.
CREATE TABLE IF NOT EXISTS distance_cache (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    origin_lat      REAL NOT NULL,
    origin_lng      REAL NOT NULL,
    dest_lat        REAL NOT NULL,
    dest_lng        REAL NOT NULL,
    distance_meters REAL NOT NULL,
    duration_secs   REAL NOT NULL,
    cached_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Composite index for lookups (rounded to 5 decimal places = ~1m precision)
CREATE UNIQUE INDEX idx_distance_cache_coords ON distance_cache(
    ROUND(origin_lat, 5),
    ROUND(origin_lng, 5),
    ROUND(dest_lat, 5),
    ROUND(dest_lng, 5)
);

-- ============================================================================
-- TRIGGERS
-- ============================================================================

-- Auto-update updated_at on participants
CREATE TRIGGER trg_participants_updated_at
    AFTER UPDATE ON participants
    FOR EACH ROW
BEGIN
    UPDATE participants SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

-- Auto-update updated_at on drivers
CREATE TRIGGER trg_drivers_updated_at
    AFTER UPDATE ON drivers
    FOR EACH ROW
BEGIN
    UPDATE drivers SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

-- Auto-update updated_at on settings
CREATE TRIGGER trg_settings_updated_at
    AFTER UPDATE ON settings
    FOR EACH ROW
BEGIN
    UPDATE settings SET updated_at = CURRENT_TIMESTAMP WHERE key = OLD.key;
END;
```

---

## 2. REST API Specification

### Base URL
All endpoints are relative to `http://localhost:8080/api/v1`

### Content Types
- Request: `application/json`
- Response: `application/json`

### Error Response Format
```json
{
    "error": {
        "code": "VALIDATION_ERROR",
        "message": "Human-readable error message",
        "details": {}  // Optional additional context
    }
}
```

### Error Codes
| Code | HTTP Status | Description |
|------|-------------|-------------|
| `VALIDATION_ERROR` | 400 | Invalid request data |
| `NOT_FOUND` | 404 | Resource not found |
| `CONFLICT` | 409 | Resource conflict (e.g., duplicate institute vehicle) |
| `GEOCODING_FAILED` | 422 | Could not geocode address |
| `ROUTING_FAILED` | 422 | Could not calculate valid route |
| `INTERNAL_ERROR` | 500 | Server error |

---

### Settings Endpoints

#### GET /settings
Get all settings.

**Response 200:**
```json
{
    "institute_address": "123 Main St, City, State",
    "institute_lat": 40.7128,
    "institute_lng": -74.0060
}
```

#### PUT /settings
Update settings. Geocodes institute address automatically.

**Request:**
```json
{
    "institute_address": "456 New Address, City, State"
}
```

**Response 200:**
```json
{
    "institute_address": "456 New Address, City, State",
    "institute_lat": 40.7589,
    "institute_lng": -73.9851
}
```

**Response 422:** Geocoding failed

---

### Participants Endpoints

#### GET /participants
List all participants.

**Query Parameters:**
- `search` (optional): Filter by name (case-insensitive substring match)

**Response 200:**
```json
{
    "participants": [
        {
            "id": 1,
            "name": "Alice Smith",
            "address": "123 Oak St, City",
            "lat": 40.7128,
            "lng": -74.0060,
            "created_at": "2024-01-15T10:30:00Z",
            "updated_at": "2024-01-15T10:30:00Z"
        }
    ],
    "total": 1
}
```

#### POST /participants
Create a new participant. Geocodes address automatically.

**Request:**
```json
{
    "name": "Bob Jones",
    "address": "456 Elm St, City"
}
```

**Response 201:**
```json
{
    "id": 2,
    "name": "Bob Jones",
    "address": "456 Elm St, City",
    "lat": 40.7589,
    "lng": -73.9851,
    "created_at": "2024-01-15T11:00:00Z",
    "updated_at": "2024-01-15T11:00:00Z"
}
```

**Response 422:** Geocoding failed

#### GET /participants/{id}
Get a single participant.

**Response 200:** Same as single item in list response

**Response 404:** Participant not found

#### PUT /participants/{id}
Update a participant. Re-geocodes if address changed.

**Request:**
```json
{
    "name": "Bob Jones Jr",
    "address": "789 Pine St, City"
}
```

**Response 200:** Updated participant object

**Response 404:** Participant not found
**Response 422:** Geocoding failed

#### DELETE /participants/{id}
Delete a participant.

**Response 204:** No content

**Response 404:** Participant not found

---

### Drivers Endpoints

#### GET /drivers
List all drivers.

**Query Parameters:**
- `search` (optional): Filter by name

**Response 200:**
```json
{
    "drivers": [
        {
            "id": 1,
            "name": "Charlie Driver",
            "address": "100 Driver Lane, City",
            "lat": 40.7128,
            "lng": -74.0060,
            "vehicle_capacity": 4,
            "is_institute_vehicle": false,
            "created_at": "2024-01-15T10:30:00Z",
            "updated_at": "2024-01-15T10:30:00Z"
        },
        {
            "id": 2,
            "name": "Institute Van",
            "address": "123 Main St, City",
            "lat": 40.7589,
            "lng": -73.9851,
            "vehicle_capacity": 12,
            "is_institute_vehicle": true,
            "created_at": "2024-01-15T10:30:00Z",
            "updated_at": "2024-01-15T10:30:00Z"
        }
    ],
    "total": 2
}
```

#### POST /drivers
Create a new driver.

**Request:**
```json
{
    "name": "Dave Driver",
    "address": "200 Driver Ave, City",
    "vehicle_capacity": 5,
    "is_institute_vehicle": false
}
```

**Response 201:** Created driver object

**Response 409:** Institute vehicle already exists (if `is_institute_vehicle: true`)
**Response 422:** Geocoding failed

#### GET /drivers/{id}
Get a single driver.

**Response 200:** Single driver object

**Response 404:** Driver not found

#### PUT /drivers/{id}
Update a driver.

**Request:**
```json
{
    "name": "Dave Driver Updated",
    "address": "200 Driver Ave, City",
    "vehicle_capacity": 6,
    "is_institute_vehicle": false
}
```

**Response 200:** Updated driver object

**Response 404:** Driver not found
**Response 409:** Institute vehicle conflict
**Response 422:** Geocoding failed

#### DELETE /drivers/{id}
Delete a driver.

**Response 204:** No content

**Response 404:** Driver not found

---

### Routing Endpoints

#### POST /routes/calculate
Calculate optimal routes for an event. Does NOT save to history.

**Request:**
```json
{
    "participant_ids": [1, 2, 3, 4, 5],
    "driver_ids": [1, 2],
    "institute_vehicle_driver_id": 3  // Required only if institute vehicle might be needed
}
```

**Response 200:**
```json
{
    "routes": [
        {
            "driver": {
                "id": 1,
                "name": "Charlie Driver",
                "address": "100 Driver Lane",
                "vehicle_capacity": 4,
                "is_institute_vehicle": false
            },
            "stops": [
                {
                    "order": 0,
                    "participant": {
                        "id": 1,
                        "name": "Alice Smith",
                        "address": "123 Oak St"
                    },
                    "distance_from_prev_meters": 2500,
                    "cumulative_distance_meters": 2500
                },
                {
                    "order": 1,
                    "participant": {
                        "id": 3,
                        "name": "Carol White",
                        "address": "789 Pine St"
                    },
                    "distance_from_prev_meters": 1200,
                    "cumulative_distance_meters": 3700
                }
            ],
            "total_dropoff_distance_meters": 3700,
            "distance_to_driver_home_meters": 4100
        }
    ],
    "summary": {
        "total_participants": 5,
        "total_drivers_used": 2,
        "total_dropoff_distance_meters": 8500,
        "used_institute_vehicle": false,
        "unassigned_participants": []
    },
    "warnings": []
}
```

**Response 422:**
```json
{
    "error": {
        "code": "ROUTING_FAILED",
        "message": "Cannot assign all participants to available drivers",
        "details": {
            "unassigned_count": 3,
            "total_capacity": 8,
            "total_participants": 11
        }
    }
}
```

---

### Events (History) Endpoints

#### GET /events
List past events.

**Query Parameters:**
- `limit` (optional, default 20): Max events to return
- `offset` (optional, default 0): Pagination offset

**Response 200:**
```json
{
    "events": [
        {
            "id": 1,
            "event_date": "2024-01-15",
            "notes": "Regular Monday pickup",
            "created_at": "2024-01-15T22:00:00Z",
            "summary": {
                "total_participants": 12,
                "total_drivers": 3,
                "total_distance_meters": 45000,
                "used_institute_vehicle": false
            }
        }
    ],
    "total": 1,
    "limit": 20,
    "offset": 0
}
```

#### POST /events
Save a calculated route as an event.

**Request:**
```json
{
    "event_date": "2024-01-15",
    "notes": "Regular Monday pickup",
    "routes": {
        // Same structure as /routes/calculate response
    }
}
```

**Response 201:**
```json
{
    "id": 2,
    "event_date": "2024-01-15",
    "notes": "Regular Monday pickup",
    "created_at": "2024-01-15T22:00:00Z"
}
```

#### GET /events/{id}
Get full event details including all assignments.

**Response 200:**
```json
{
    "id": 1,
    "event_date": "2024-01-15",
    "notes": "Regular Monday pickup",
    "created_at": "2024-01-15T22:00:00Z",
    "assignments": [
        {
            "driver_name": "Charlie Driver",
            "driver_address": "100 Driver Lane",
            "used_institute_vehicle": false,
            "stops": [
                {
                    "route_order": 0,
                    "participant_name": "Alice Smith",
                    "participant_address": "123 Oak St",
                    "distance_from_prev_meters": 2500
                }
            ]
        }
    ],
    "summary": {
        "total_participants": 12,
        "total_drivers": 3,
        "total_distance_meters": 45000,
        "used_institute_vehicle": false,
        "institute_vehicle_driver_name": null
    }
}
```

#### DELETE /events/{id}
Delete an event and its assignments.

**Response 204:** No content

**Response 404:** Event not found

---

### Utility Endpoints

#### POST /geocode
Geocode an address (for testing/debugging).

**Request:**
```json
{
    "address": "123 Main St, City, State"
}
```

**Response 200:**
```json
{
    "address": "123 Main St, City, State",
    "lat": 40.7128,
    "lng": -74.0060,
    "display_name": "123 Main Street, City, State, 12345, USA"
}
```

#### GET /health
Health check endpoint.

**Response 200:**
```json
{
    "status": "ok",
    "version": "1.0.0",
    "database": "connected"
}
```

---

## 3. Component Interface Definitions

```go
// internal/models/models.go
package models

import "time"

// Coordinates represents a geographic point
type Coordinates struct {
    Lat float64 `json:"lat"`
    Lng float64 `json:"lng"`
}

// Participant represents a person to be driven home
type Participant struct {
    ID        int64       `json:"id"`
    Name      string      `json:"name"`
    Address   string      `json:"address"`
    Coords    Coordinates `json:"coords"`
    CreatedAt time.Time   `json:"created_at"`
    UpdatedAt time.Time   `json:"updated_at"`
}

// Driver represents a person who can drive participants home
type Driver struct {
    ID                 int64       `json:"id"`
    Name               string      `json:"name"`
    Address            string      `json:"address"`
    Coords             Coordinates `json:"coords"`
    VehicleCapacity    int         `json:"vehicle_capacity"`
    IsInstituteVehicle bool        `json:"is_institute_vehicle"`
    CreatedAt          time.Time   `json:"created_at"`
    UpdatedAt          time.Time   `json:"updated_at"`
}

// Settings holds application configuration
type Settings struct {
    InstituteAddress string      `json:"institute_address"`
    InstituteCoords  Coordinates `json:"institute_coords"`
}

// Event represents a historical event record
type Event struct {
    ID        int64     `json:"id"`
    EventDate time.Time `json:"event_date"`
    Notes     string    `json:"notes"`
    CreatedAt time.Time `json:"created_at"`
}

// EventAssignment represents a snapshot of a participant assignment
type EventAssignment struct {
    ID                    int64   `json:"id"`
    EventID               int64   `json:"event_id"`
    DriverID              int64   `json:"driver_id"`
    DriverName            string  `json:"driver_name"`
    DriverAddress         string  `json:"driver_address"`
    RouteOrder            int     `json:"route_order"`
    ParticipantID         int64   `json:"participant_id"`
    ParticipantName       string  `json:"participant_name"`
    ParticipantAddress    string  `json:"participant_address"`
    DistanceFromPrev      float64 `json:"distance_from_prev_meters"`
    UsedInstituteVehicle  bool    `json:"used_institute_vehicle"`
}

// EventSummary contains aggregate stats for an event
type EventSummary struct {
    EventID                   int64   `json:"event_id"`
    TotalParticipants         int     `json:"total_participants"`
    TotalDrivers              int     `json:"total_drivers"`
    TotalDistanceMeters       float64 `json:"total_distance_meters"`
    UsedInstituteVehicle      bool    `json:"used_institute_vehicle"`
    InstituteVehicleDriverName string `json:"institute_vehicle_driver_name,omitempty"`
}

// RouteStop represents a single stop in a calculated route
type RouteStop struct {
    Order                    int          `json:"order"`
    Participant              *Participant `json:"participant"`
    DistanceFromPrevMeters   float64      `json:"distance_from_prev_meters"`
    CumulativeDistanceMeters float64      `json:"cumulative_distance_meters"`
}

// CalculatedRoute represents a single driver's route
type CalculatedRoute struct {
    Driver                    *Driver      `json:"driver"`
    Stops                     []RouteStop  `json:"stops"`
    TotalDropoffDistanceMeters float64     `json:"total_dropoff_distance_meters"`
    DistanceToDriverHomeMeters float64     `json:"distance_to_driver_home_meters"`
    UsedInstituteVehicle      bool         `json:"used_institute_vehicle"`
    InstituteVehicleDriverID  int64        `json:"institute_vehicle_driver_id,omitempty"`
}

// RoutingSummary contains aggregate stats for a routing calculation
type RoutingSummary struct {
    TotalParticipants         int     `json:"total_participants"`
    TotalDriversUsed          int     `json:"total_drivers_used"`
    TotalDropoffDistanceMeters float64 `json:"total_dropoff_distance_meters"`
    UsedInstituteVehicle      bool    `json:"used_institute_vehicle"`
    UnassignedParticipants    []int64 `json:"unassigned_participants"`
}

// RoutingResult contains the full result of a route calculation
type RoutingResult struct {
    Routes   []CalculatedRoute `json:"routes"`
    Summary  RoutingSummary    `json:"summary"`
    Warnings []string          `json:"warnings"`
}

// DistanceCacheEntry represents a cached distance lookup
type DistanceCacheEntry struct {
    Origin         Coordinates `json:"origin"`
    Destination    Coordinates `json:"destination"`
    DistanceMeters float64     `json:"distance_meters"`
    DurationSecs   float64     `json:"duration_secs"`
}
```

```go
// internal/database/repository.go
package database

import (
    "context"

    "github.com/yourusername/ride-home-router/internal/models"
)

// ParticipantRepository handles participant persistence
type ParticipantRepository interface {
    // List returns all participants, optionally filtered by search term
    List(ctx context.Context, search string) ([]models.Participant, error)

    // GetByID returns a single participant by ID
    GetByID(ctx context.Context, id int64) (*models.Participant, error)

    // GetByIDs returns multiple participants by their IDs
    GetByIDs(ctx context.Context, ids []int64) ([]models.Participant, error)

    // Create inserts a new participant and returns it with ID populated
    Create(ctx context.Context, p *models.Participant) (*models.Participant, error)

    // Update modifies an existing participant
    Update(ctx context.Context, p *models.Participant) (*models.Participant, error)

    // Delete removes a participant by ID
    Delete(ctx context.Context, id int64) error
}

// DriverRepository handles driver persistence
type DriverRepository interface {
    // List returns all drivers, optionally filtered by search term
    List(ctx context.Context, search string) ([]models.Driver, error)

    // GetByID returns a single driver by ID
    GetByID(ctx context.Context, id int64) (*models.Driver, error)

    // GetByIDs returns multiple drivers by their IDs
    GetByIDs(ctx context.Context, ids []int64) ([]models.Driver, error)

    // GetInstituteVehicle returns the institute vehicle driver if it exists
    GetInstituteVehicle(ctx context.Context) (*models.Driver, error)

    // Create inserts a new driver and returns it with ID populated
    Create(ctx context.Context, d *models.Driver) (*models.Driver, error)

    // Update modifies an existing driver
    Update(ctx context.Context, d *models.Driver) (*models.Driver, error)

    // Delete removes a driver by ID
    Delete(ctx context.Context, id int64) error
}

// SettingsRepository handles settings persistence
type SettingsRepository interface {
    // Get returns all settings
    Get(ctx context.Context) (*models.Settings, error)

    // Update saves settings (upsert behavior)
    Update(ctx context.Context, s *models.Settings) error
}

// EventRepository handles event history persistence
type EventRepository interface {
    // List returns events with pagination
    List(ctx context.Context, limit, offset int) ([]models.Event, int, error)

    // GetByID returns a single event with its assignments
    GetByID(ctx context.Context, id int64) (*models.Event, []models.EventAssignment, *models.EventSummary, error)

    // Create inserts a new event with its assignments
    Create(ctx context.Context, event *models.Event, assignments []models.EventAssignment, summary *models.EventSummary) (*models.Event, error)

    // Delete removes an event and its assignments
    Delete(ctx context.Context, id int64) error
}

// DistanceCacheRepository handles distance cache persistence
type DistanceCacheRepository interface {
    // Get retrieves a cached distance, returns nil if not found
    Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error)

    // GetBatch retrieves multiple cached distances at once
    // Returns a map keyed by "lat,lng->lat,lng" strings
    GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error)

    // Set stores a distance in the cache
    Set(ctx context.Context, entry *models.DistanceCacheEntry) error

    // SetBatch stores multiple distances at once
    SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error

    // Clear removes all cached entries (useful for testing)
    Clear(ctx context.Context) error
}
```

```go
// internal/geocoding/geocoder.go
package geocoding

import (
    "context"

    "github.com/yourusername/ride-home-router/internal/models"
)

// GeocodingResult contains the result of a geocoding operation
type GeocodingResult struct {
    Coords      models.Coordinates
    DisplayName string // Full formatted address from Nominatim
}

// Geocoder provides address-to-coordinates conversion
type Geocoder interface {
    // Geocode converts an address string to coordinates
    // Returns ErrGeocodingFailed if the address cannot be resolved
    Geocode(ctx context.Context, address string) (*GeocodingResult, error)

    // GeocodeWithRetry attempts geocoding with exponential backoff
    // Useful for handling rate limits
    GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error)
}

// ErrGeocodingFailed is returned when an address cannot be geocoded
type ErrGeocodingFailed struct {
    Address string
    Reason  string
}

func (e *ErrGeocodingFailed) Error() string {
    return "geocoding failed for address: " + e.Address + " - " + e.Reason
}
```

```go
// internal/distance/calculator.go
package distance

import (
    "context"

    "github.com/yourusername/ride-home-router/internal/models"
)

// DistanceResult contains the result of a distance calculation
type DistanceResult struct {
    DistanceMeters float64
    DurationSecs   float64
}

// DistanceCalculator provides distance calculations between coordinates
type DistanceCalculator interface {
    // GetDistance calculates driving distance between two points
    // Uses cache if available, otherwise calls OSRM API
    GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error)

    // GetDistanceMatrix calculates distances between all pairs of points
    // Efficiently batches API calls and uses cache
    // Returns a 2D slice where result[i][j] is distance from points[i] to points[j]
    GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error)

    // GetDistancesFromPoint calculates distances from one point to many
    // Returns distances in same order as destinations slice
    GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error)

    // PrewarmCache fetches and caches distances for anticipated lookups
    // Useful for batch-loading distances before route calculation
    PrewarmCache(ctx context.Context, points []models.Coordinates) error
}

// ErrDistanceCalculationFailed is returned when OSRM API fails
type ErrDistanceCalculationFailed struct {
    Origin  models.Coordinates
    Dest    models.Coordinates
    Reason  string
}

func (e *ErrDistanceCalculationFailed) Error() string {
    return "distance calculation failed: " + e.Reason
}
```

```go
// internal/routing/router.go
package routing

import (
    "context"

    "github.com/yourusername/ride-home-router/internal/models"
)

// RoutingRequest contains the input for route calculation
type RoutingRequest struct {
    // Institute location (start point for all routes)
    InstituteCoords models.Coordinates

    // Participants to be assigned to routes
    Participants []models.Participant

    // Available drivers (excluding institute vehicle initially)
    Drivers []models.Driver

    // Institute vehicle (used as last resort)
    InstituteVehicle *models.Driver

    // Who will drive the institute vehicle if needed
    // Required if InstituteVehicle is provided
    InstituteVehicleDriverID int64
}

// Router provides route optimization
type Router interface {
    // CalculateRoutes optimizes driver assignments using greedy nearest-neighbor
    // Returns ErrRoutingFailed if no valid solution exists
    CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error)
}

// ErrRoutingFailed is returned when no valid route solution exists
type ErrRoutingFailed struct {
    Reason             string
    UnassignedCount    int
    TotalCapacity      int
    TotalParticipants  int
}

func (e *ErrRoutingFailed) Error() string {
    return "routing failed: " + e.Reason
}
```

---

## 4. Data Flow Diagram

### Route Calculation Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           ROUTE CALCULATION FLOW                             │
└─────────────────────────────────────────────────────────────────────────────┘

┌──────────┐     POST /routes/calculate      ┌──────────────┐
│  htmx    │ ──────────────────────────────> │   Handler    │
│  Client  │                                 │ (handlers/)  │
└──────────┘                                 └──────┬───────┘
                                                    │
                    ┌───────────────────────────────┼───────────────────────────┐
                    │                               │                           │
                    ▼                               ▼                           ▼
           ┌────────────────┐            ┌─────────────────┐          ┌─────────────────┐
           │ Participant    │            │    Driver       │          │    Settings     │
           │ Repository     │            │   Repository    │          │   Repository    │
           │ (database/)    │            │  (database/)    │          │  (database/)    │
           └───────┬────────┘            └────────┬────────┘          └────────┬────────┘
                   │                              │                            │
                   │ GetByIDs()                   │ GetByIDs()                 │ Get()
                   ▼                              ▼                            ▼
           ┌────────────────┐            ┌─────────────────┐          ┌─────────────────┐
           │   []Participant│            │    []Driver     │          │    Settings     │
           │   (with coords)│            │  (with coords)  │          │ (institute loc) │
           └───────┬────────┘            └────────┬────────┘          └────────┬────────┘
                   │                              │                            │
                   └──────────────────────────────┼────────────────────────────┘
                                                  │
                                                  ▼
                                    ┌─────────────────────────┐
                                    │    Routing Service      │
                                    │      (routing/)         │
                                    │                         │
                                    │  1. Build location list │
                                    │  2. Request distances   │
                                    │  3. Run greedy algo     │
                                    │  4. Try institute veh   │
                                    │     if needed           │
                                    └────────────┬────────────┘
                                                 │
                                    ┌────────────┴────────────┐
                                    │                         │
                                    ▼                         │
                        ┌───────────────────────┐             │
                        │  Distance Calculator  │             │
                        │     (distance/)       │             │
                        └───────────┬───────────┘             │
                                    │                         │
                    ┌───────────────┼───────────────┐         │
                    │               │               │         │
                    ▼               │               ▼         │
         ┌──────────────────┐      │     ┌──────────────────┐ │
         │  Distance Cache  │      │     │    OSRM API      │ │
         │   Repository     │◄─────┘     │ (HTTP Client)    │ │
         │   (database/)    │            └──────────────────┘ │
         └──────────────────┘                                 │
                                                              │
                                    ┌─────────────────────────┘
                                    │
                                    ▼
                        ┌───────────────────────┐
                        │   RoutingResult       │
                        │   - Routes[]          │
                        │   - Summary           │
                        │   - Warnings          │
                        └───────────┬───────────┘
                                    │
                                    ▼
                        ┌───────────────────────┐
                        │      Handler          │
                        │  - Format response    │
                        │  - Render template    │
                        └───────────┬───────────┘
                                    │
                                    ▼
                        ┌───────────────────────┐
                        │    htmx Client        │
                        │  - Display routes     │
                        │  - Save button        │
                        └───────────────────────┘
```

### Greedy Algorithm Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      GREEDY NEAREST-NEIGHBOR ALGORITHM                       │
└─────────────────────────────────────────────────────────────────────────────┘

    START
      │
      ▼
┌──────────────────────────────────────────────────────────────────────────┐
│  PHASE 1: Regular Drivers                                                 │
│                                                                           │
│  unassigned = all participants                                            │
│  FOR each driver (excluding institute vehicle):                           │
│      current_location = institute                                         │
│      route = []                                                           │
│      WHILE len(route) < driver.capacity AND len(unassigned) > 0:          │
│          nearest = find_nearest(current_location, unassigned)             │
│          route.append(nearest)                                            │
│          unassigned.remove(nearest)                                       │
│          current_location = nearest.coords                                │
│      END WHILE                                                            │
│      driver.route = route                                                 │
│  END FOR                                                                  │
└──────────────────────────────────────────────────────────────────────────┘
      │
      ▼
┌─────────────────────────┐
│ unassigned.length > 0?  │──── NO ────────────────────────────────────────┐
└─────────────────────────┘                                                │
      │                                                                    │
     YES                                                                   │
      │                                                                    │
      ▼                                                                    │
┌──────────────────────────────────────────────────────────────────────────┐
│  PHASE 2: Institute Vehicle (if available)                               │
│                                                                           │
│  IF institute_vehicle exists AND institute_vehicle_driver selected:       │
│      current_location = institute                                         │
│      route = []                                                           │
│      WHILE len(route) < institute_vehicle.capacity                        │
│            AND len(unassigned) > 0:                                       │
│          nearest = find_nearest(current_location, unassigned)             │
│          route.append(nearest)                                            │
│          unassigned.remove(nearest)                                       │
│          current_location = nearest.coords                                │
│      END WHILE                                                            │
│      institute_vehicle.route = route                                      │
│      NOTE: Route ends at institute, not driver's home                     │
│  END IF                                                                   │
└──────────────────────────────────────────────────────────────────────────┘
      │
      ▼
┌─────────────────────────┐
│ unassigned.length > 0?  │──── YES ───> RETURN ERROR (capacity exceeded)
└─────────────────────────┘
      │
      NO
      │
      ▼
┌──────────────────────────────────────────────────────────────────────────┐
│  PHASE 3: Calculate Final Distances                                       │
│                                                                           │
│  FOR each driver with non-empty route:                                    │
│      total_dropoff_distance = sum of inter-stop distances                 │
│      IF NOT using institute vehicle:                                      │
│          distance_to_home = distance(last_stop, driver.home)              │
│      ELSE:                                                                │
│          distance_to_institute = distance(last_stop, institute)           │
│      END IF                                                               │
│  END FOR                                                                  │
└──────────────────────────────────────────────────────────────────────────┘
      │
      ▼
   RETURN RoutingResult
```

### Participant/Driver CRUD Flow (with Geocoding)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CREATE/UPDATE ENTITY FLOW                             │
└─────────────────────────────────────────────────────────────────────────────┘

┌──────────┐     POST/PUT /participants/{id}     ┌──────────────┐
│  htmx    │ ──────────────────────────────────> │   Handler    │
│  Client  │                                     └──────┬───────┘
└──────────┘                                            │
                                                        │ 1. Validate input
                                                        │
                                                        ▼
                                           ┌────────────────────────┐
                                           │   Is address changed?  │
                                           └───────────┬────────────┘
                                                       │
                               ┌───────────────────────┴───────────────────────┐
                               │ YES                                           │ NO
                               ▼                                               │
                    ┌───────────────────────┐                                  │
                    │    Geocoder           │                                  │
                    │   (geocoding/)        │                                  │
                    │                       │                                  │
                    │  Nominatim API call   │                                  │
                    │  with rate limiting   │                                  │
                    └───────────┬───────────┘                                  │
                                │                                              │
                    ┌───────────┴───────────┐                                  │
                    │                       │                                  │
                    ▼                       ▼                                  │
              ┌──────────┐           ┌──────────┐                              │
              │ Success  │           │  Failed  │                              │
              │ (coords) │           │  (error) │                              │
              └────┬─────┘           └────┬─────┘                              │
                   │                      │                                    │
                   │                      ▼                                    │
                   │              ┌───────────────┐                            │
                   │              │ Return 422    │                            │
                   │              │ Geocoding Err │                            │
                   │              └───────────────┘                            │
                   │                                                           │
                   └────────────────────────┬──────────────────────────────────┘
                                            │
                                            ▼
                                 ┌───────────────────────┐
                                 │    Repository         │
                                 │  Create/Update        │
                                 └───────────┬───────────┘
                                             │
                                             ▼
                                 ┌───────────────────────┐
                                 │      SQLite           │
                                 │      INSERT/UPDATE    │
                                 └───────────┬───────────┘
                                             │
                                             ▼
                                 ┌───────────────────────┐
                                 │    Return entity      │
                                 │    with new coords    │
                                 └───────────────────────┘
```

---

## 5. Key Design Decisions

### Decision 1: Denormalized Event History

**Choice:** Store snapshots of participant/driver data in event_assignments rather than just foreign keys.

**Rationale:**
- Participants and drivers may be edited or deleted after an event
- Historical records must remain accurate and complete
- Queries for historical data are simpler (no JOINs to possibly-deleted records)

**Trade-off:** More storage space used, but this is negligible for a local-first app.

### Decision 2: Greedy Algorithm Over Optimal CVRP

**Choice:** Use greedy nearest-neighbor heuristic instead of optimal CVRP solvers.

**Rationale:**
- Optimal CVRP is NP-hard; exact solutions don't scale
- For typical event sizes (10-30 participants, 3-8 drivers), greedy produces good results
- Simplicity: no external solver dependencies
- Fast execution: routes calculated in milliseconds

**Trade-off:** Routes may be 10-20% longer than optimal, but this is acceptable for the use case.

### Decision 3: Aggressive Distance Caching

**Choice:** Cache all OSRM distance lookups in SQLite.

**Rationale:**
- OSRM public API has rate limits (unclear, but assumed ~1 req/sec)
- Same routes are often calculated repeatedly (same participants, same institute)
- Coordinates rounded to 5 decimal places (~1m precision) for cache key normalization

**Trade-off:** Cache may grow large over time; consider adding TTL or manual clear.

### Decision 4: Institute Vehicle as Separate Entity

**Choice:** Model institute vehicle as a special Driver with `is_institute_vehicle=true`.

**Rationale:**
- Same data model for all vehicles simplifies code
- Unique constraint ensures exactly one institute vehicle
- Institute vehicle address is set to institute address (returns there after route)

**Trade-off:** Slightly awkward that institute vehicle has a "home address" that isn't used the same way.

### Decision 5: htmx Over SPA Framework

**Choice:** Server-rendered HTML with htmx for interactivity.

**Rationale:**
- Single binary deployment (no separate frontend build)
- Simpler architecture for a local-first app
- No JavaScript framework complexity
- Works offline after initial load

**Trade-off:** Less dynamic than React/Vue, but sufficient for this use case.

### Decision 6: SQLite WAL Mode

**Choice:** Use Write-Ahead Logging (WAL) journal mode.

**Rationale:**
- Better concurrent read performance
- Readers don't block writers
- More resilient to crashes

**Trade-off:** Slightly more complex file management (3 files instead of 1).

### Decision 7: No Background Jobs

**Choice:** Geocoding and distance calculations happen synchronously in request handlers.

**Rationale:**
- Simplicity: no job queue infrastructure needed
- Acceptable latency for typical operations
- htmx loading indicators provide good UX during waits

**Trade-off:** Long operations block the request. Mitigated by:
- Geocoding only happens on entity create/update (rare)
- Distance calculations use cache after first calculation

### Decision 8: Coordinate Precision

**Choice:** Store coordinates as REAL (float64), round to 5 decimal places for cache keys.

**Rationale:**
- 5 decimal places = ~1.1m precision (sufficient for routing)
- Prevents cache misses from floating point variations
- Full precision retained in storage for future flexibility

### Decision 9: Single Database File

**Choice:** All data (settings, entities, cache, history) in one SQLite file.

**Rationale:**
- Simplest deployment and backup story
- Easy to copy/move the entire application state
- Atomic consistency across all data

**Trade-off:** Database file may grow large with extensive cache; consider periodic cache pruning.

### Decision 10: htmx with HTML Fragments

**Choice:** Return HTML fragments directly from Go handlers for htmx requests.

**Rationale:**
- Most htmx-idiomatic approach
- Simpler templating - no JavaScript needed to transform JSON to DOM
- Server-side rendering keeps all UI logic in Go templates
- Faster perceived performance (no client-side rendering step)

**Implementation:**
- Handlers detect htmx requests via `HX-Request` header
- Full page requests return complete HTML (with layout)
- htmx requests return HTML fragments (just the updated section)
- Use Go's `html/template` for all rendering

---

## 6. Implementation Notes

### Rate Limiting for External APIs

Both Nominatim and OSRM have usage policies:

**Nominatim:**
- Max 1 request per second
- Implement with `time.Ticker` or exponential backoff
- Cache aggressively (addresses rarely change)

**OSRM:**
- Public demo server has undefined limits
- Implement backoff on 429 responses
- Cache all results

### Batch Distance Optimization

When calculating routes, we need distances between many points. Optimize by:

1. **Pre-build distance matrix** for all participants + institute before algorithm runs
2. **Use OSRM table service** (`/table/v1/`) for batch requests (returns N x N matrix in one call)
3. **Cache entire matrix** for future calculations with same participants

### Error Handling Strategy

| Error Type | User Message | Logging |
|------------|--------------|---------|
| Geocoding failure | "Could not find address. Please check and try again." | WARN with address |
| OSRM API failure | "Distance calculation failed. Please try again." | ERROR with details |
| No valid route | "Cannot fit all participants in available vehicles." | INFO |
| Database error | "An error occurred. Please try again." | ERROR with stack |

### Cross-Platform Build

```makefile
# Makefile targets for cross-compilation
PLATFORMS := windows/amd64 darwin/amd64 darwin/arm64

build-all:
	$(foreach platform,$(PLATFORMS),\
		GOOS=$(word 1,$(subst /, ,$(platform))) \
		GOARCH=$(word 2,$(subst /, ,$(platform))) \
		go build -o bin/ride-home-router-$(word 1,$(subst /, ,$(platform)))-$(word 2,$(subst /, ,$(platform)))$(if $(findstring windows,$(platform)),.exe) ./cmd/server;)
```

### Security Considerations

1. **Input validation:** Sanitize all user inputs (addresses, names)
2. **SQL injection:** Use parameterized queries (Go's `database/sql` does this by default)
3. **Local-only:** Bind to `127.0.0.1` by default, not `0.0.0.0`
4. **No auth:** Local-first app assumes trusted user on trusted machine

---

## 7. File Structure with Responsibilities

```
ride-home-router/
├── cmd/server/
│   └── main.go                 # Entry point, DI wiring, server startup
├── internal/
│   ├── database/
│   │   ├── db.go               # SQLite connection, migrations
│   │   ├── schema.sql          # DDL (embedded with go:embed)
│   │   ├── participant.go      # ParticipantRepository impl
│   │   ├── driver.go           # DriverRepository impl
│   │   ├── settings.go         # SettingsRepository impl
│   │   ├── event.go            # EventRepository impl
│   │   └── distance_cache.go   # DistanceCacheRepository impl
│   ├── geocoding/
│   │   ├── geocoder.go         # Interface definition
│   │   └── nominatim.go        # Nominatim client impl
│   ├── distance/
│   │   ├── calculator.go       # Interface definition
│   │   └── osrm.go             # OSRM client impl (with caching)
│   ├── routing/
│   │   ├── router.go           # Interface definition
│   │   └── greedy.go           # Greedy nearest-neighbor impl
│   ├── handlers/
│   │   ├── handlers.go         # Common handler utilities
│   │   ├── participants.go     # Participant CRUD handlers
│   │   ├── drivers.go          # Driver CRUD handlers
│   │   ├── settings.go         # Settings handlers
│   │   ├── routes.go           # Route calculation handlers
│   │   └── events.go           # Event history handlers
│   └── models/
│       └── models.go           # All domain types
├── web/
│   ├── static/
│   │   ├── css/
│   │   │   └── style.css       # Tailwind or minimal CSS
│   │   └── js/
│   │       └── htmx.min.js     # htmx library
│   └── templates/
│       ├── layout.html         # Base layout with nav
│       ├── index.html          # Main event page
│       ├── participants.html   # Participant roster
│       ├── drivers.html        # Driver roster
│       ├── settings.html       # Settings page
│       ├── history.html        # Event history list
│       └── event_detail.html   # Single event view
├── Makefile                    # Build, test, run targets
├── go.mod
├── go.sum
└── README.md
```

---

## 8. API Response Codes Summary

| Endpoint | Success | Client Error | Server Error |
|----------|---------|--------------|--------------|
| GET /settings | 200 | - | 500 |
| PUT /settings | 200 | 400, 422 | 500 |
| GET /participants | 200 | - | 500 |
| POST /participants | 201 | 400, 422 | 500 |
| GET /participants/{id} | 200 | 404 | 500 |
| PUT /participants/{id} | 200 | 400, 404, 422 | 500 |
| DELETE /participants/{id} | 204 | 404 | 500 |
| GET /drivers | 200 | - | 500 |
| POST /drivers | 201 | 400, 409, 422 | 500 |
| GET /drivers/{id} | 200 | 404 | 500 |
| PUT /drivers/{id} | 200 | 400, 404, 409, 422 | 500 |
| DELETE /drivers/{id} | 204 | 404 | 500 |
| POST /routes/calculate | 200 | 400, 422 | 500 |
| GET /events | 200 | - | 500 |
| POST /events | 201 | 400 | 500 |
| GET /events/{id} | 200 | 404 | 500 |
| DELETE /events/{id} | 204 | 404 | 500 |
| POST /geocode | 200 | 400, 422 | 500 |
| GET /health | 200 | - | 500 |
