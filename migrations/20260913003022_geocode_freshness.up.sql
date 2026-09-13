ALTER TABLE participants ADD COLUMN geocoded_at timestamptz;
ALTER TABLE drivers ADD COLUMN geocoded_at timestamptz;
ALTER TABLE activity_locations ADD COLUMN geocoded_at timestamptz;
UPDATE participants SET geocoded_at = now();
UPDATE drivers SET geocoded_at = now();
UPDATE activity_locations SET geocoded_at = now();
ALTER TABLE distance_cache ADD COLUMN cached_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX distance_cache_cached_at_idx ON distance_cache (cached_at);
