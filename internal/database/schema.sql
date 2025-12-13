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
    ('institute_lng', '0'),
    ('selected_activity_location_id', '0'),
    ('use_miles', 'false');

-- ============================================================================
-- ACTIVITY LOCATIONS
-- ============================================================================
CREATE TABLE IF NOT EXISTS activity_locations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    address     TEXT NOT NULL,
    lat         REAL NOT NULL DEFAULT 0,
    lng         REAL NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_activity_locations_name ON activity_locations(name);

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

CREATE INDEX IF NOT EXISTS idx_participants_name ON participants(name);

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

CREATE INDEX IF NOT EXISTS idx_drivers_name ON drivers(name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_drivers_institute_vehicle ON drivers(is_institute_vehicle)
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

CREATE INDEX IF NOT EXISTS idx_events_date ON events(event_date DESC);

-- ============================================================================
-- EVENT ASSIGNMENTS (Denormalized snapshot)
-- ============================================================================
-- Stores the complete assignment snapshot for historical reference.
-- Driver and participant data is copied at event time for immutability.
CREATE TABLE IF NOT EXISTS event_assignments (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id            INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    driver_id           INTEGER NOT NULL,
    driver_name         TEXT NOT NULL,
    driver_address      TEXT NOT NULL,
    route_order         INTEGER NOT NULL,
    participant_id      INTEGER NOT NULL,
    participant_name    TEXT NOT NULL,
    participant_address TEXT NOT NULL,
    distance_from_prev  REAL NOT NULL,
    used_institute_vehicle BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_event_assignments_event ON event_assignments(event_id);
CREATE INDEX IF NOT EXISTS idx_event_assignments_driver ON event_assignments(event_id, driver_id, route_order);

-- ============================================================================
-- EVENT SUMMARY (Aggregate stats per event)
-- ============================================================================
CREATE TABLE IF NOT EXISTS event_summaries (
    event_id                INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    total_participants      INTEGER NOT NULL,
    total_drivers           INTEGER NOT NULL,
    total_distance_meters   REAL NOT NULL,
    used_institute_vehicle  BOOLEAN NOT NULL DEFAULT FALSE,
    institute_vehicle_driver_name TEXT
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
CREATE UNIQUE INDEX IF NOT EXISTS idx_distance_cache_coords ON distance_cache(
    ROUND(origin_lat, 5),
    ROUND(origin_lng, 5),
    ROUND(dest_lat, 5),
    ROUND(dest_lng, 5)
);

-- ============================================================================
-- TRIGGERS
-- ============================================================================

-- Auto-update updated_at on participants
CREATE TRIGGER IF NOT EXISTS trg_participants_updated_at
    AFTER UPDATE ON participants
    FOR EACH ROW
BEGIN
    UPDATE participants SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

-- Auto-update updated_at on drivers
CREATE TRIGGER IF NOT EXISTS trg_drivers_updated_at
    AFTER UPDATE ON drivers
    FOR EACH ROW
BEGIN
    UPDATE drivers SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;

-- Auto-update updated_at on settings
CREATE TRIGGER IF NOT EXISTS trg_settings_updated_at
    AFTER UPDATE ON settings
    FOR EACH ROW
BEGIN
    UPDATE settings SET updated_at = CURRENT_TIMESTAMP WHERE key = OLD.key;
END;
