ALTER TABLE events ADD COLUMN route_session_id TEXT;
CREATE UNIQUE INDEX events_route_session_id_idx ON events (route_session_id) WHERE route_session_id IS NOT NULL;
