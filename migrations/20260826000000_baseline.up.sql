SET lock_timeout = 0;

CREATE TABLE activity_locations (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    lat DOUBLE PRECISION NOT NULL,
    lng DOUBLE PRECISION NOT NULL
);

CREATE TABLE participants (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    address_name TEXT,
    lat DOUBLE PRECISION NOT NULL,
    lng DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_participants_name ON participants (name);

CREATE TABLE drivers (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    address_name TEXT,
    lat DOUBLE PRECISION NOT NULL,
    lng DOUBLE PRECISION NOT NULL,
    vehicle_capacity INTEGER NOT NULL DEFAULT 4,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_drivers_name ON drivers (name);

CREATE TABLE organization_vehicles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    capacity INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE labels (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE participant_labels (
    label_id BIGINT NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    participant_id BIGINT NOT NULL REFERENCES participants (id) ON DELETE CASCADE,
    PRIMARY KEY (label_id, participant_id)
);
CREATE INDEX idx_participant_labels_participant ON participant_labels (participant_id);

CREATE TABLE driver_labels (
    label_id BIGINT NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    driver_id BIGINT NOT NULL REFERENCES drivers (id) ON DELETE CASCADE,
    PRIMARY KEY (label_id, driver_id)
);
CREATE INDEX idx_driver_labels_driver ON driver_labels (driver_id);

-- Single-row settings shared by every coordinator using this deployment.
CREATE TABLE settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    selected_activity_location_id BIGINT REFERENCES activity_locations (id) ON DELETE SET NULL,
    use_miles BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO settings (id, use_miles) VALUES (1, TRUE);

CREATE TABLE events (
    id BIGSERIAL PRIMARY KEY,
    event_date TIMESTAMPTZ NOT NULL,
    notes TEXT,
    mode TEXT NOT NULL DEFAULT 'dropoff',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_date ON events (event_date DESC);

CREATE TABLE event_routes (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    route_order INTEGER NOT NULL,
    driver_id BIGINT NOT NULL,
    driver_name TEXT NOT NULL,
    driver_address TEXT NOT NULL,
    driver_address_name TEXT,
    effective_capacity INTEGER NOT NULL DEFAULT 0,
    org_vehicle_id BIGINT,
    org_vehicle_name TEXT,
    total_dropoff_distance_meters DOUBLE PRECISION NOT NULL DEFAULT 0,
    distance_to_driver_home_meters DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_distance_meters DOUBLE PRECISION NOT NULL DEFAULT 0,
    baseline_duration_secs DOUBLE PRECISION NOT NULL DEFAULT 0,
    route_duration_secs DOUBLE PRECISION NOT NULL DEFAULT 0,
    detour_secs DOUBLE PRECISION NOT NULL DEFAULT 0,
    mode TEXT NOT NULL DEFAULT 'dropoff',
    snapshot_version INTEGER NOT NULL DEFAULT 2,
    metrics_complete BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_event_routes_event ON event_routes (event_id);

CREATE TABLE event_route_stops (
    id BIGSERIAL PRIMARY KEY,
    event_route_id BIGINT NOT NULL REFERENCES event_routes (id) ON DELETE CASCADE,
    route_order INTEGER NOT NULL,
    participant_id BIGINT NOT NULL,
    participant_name TEXT NOT NULL,
    participant_address TEXT NOT NULL,
    participant_address_name TEXT,
    distance_from_prev_meters DOUBLE PRECISION NOT NULL DEFAULT 0,
    cumulative_distance_meters DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_from_prev_secs DOUBLE PRECISION NOT NULL DEFAULT 0,
    cumulative_duration_secs DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX idx_event_route_stops_route ON event_route_stops (event_route_id);

CREATE TABLE event_summaries (
    event_id BIGINT PRIMARY KEY REFERENCES events (id) ON DELETE CASCADE,
    total_participants INTEGER NOT NULL DEFAULT 0,
    total_drivers INTEGER NOT NULL DEFAULT 0,
    total_distance_meters DOUBLE PRECISION NOT NULL DEFAULT 0,
    org_vehicles_used INTEGER NOT NULL DEFAULT 0,
    mode TEXT NOT NULL DEFAULT 'dropoff'
);

-- Coordinates are rounded to five decimals before they are stored or looked up,
-- so exact double-precision equality is a stable cache key.
CREATE TABLE distance_cache (
    origin_lat DOUBLE PRECISION NOT NULL,
    origin_lng DOUBLE PRECISION NOT NULL,
    dest_lat DOUBLE PRECISION NOT NULL,
    dest_lng DOUBLE PRECISION NOT NULL,
    distance_meters DOUBLE PRECISION NOT NULL,
    duration_secs DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (origin_lat, origin_lng, dest_lat, dest_lng)
);
