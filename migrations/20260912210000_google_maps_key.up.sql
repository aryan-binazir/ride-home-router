-- Kept separate from public settings and application data exports.
CREATE TABLE google_maps_credentials (
    id integer PRIMARY KEY CHECK (id = 1),
    api_key text NOT NULL CHECK (length(api_key) BETWEEN 1 AND 4096)
);
