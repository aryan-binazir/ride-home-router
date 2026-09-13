-- Google Geocoding and Places Autocomplete share one cross-instance cooldown.
-- The legacy provider row stays inert so replicas still on the previous image keep working during rollout.
INSERT INTO provider_throttles(name,next_at) VALUES('google_geocoding',clock_timestamp()) ON CONFLICT (name) DO NOTHING;
