BEGIN;

-- Preserve the copyable instructions as part of the immutable event snapshot.
-- Empty values identify legacy events whose original plan context is unavailable.
ALTER TABLE event_routes
    ADD COLUMN driver_handoff TEXT NOT NULL DEFAULT '',
    ADD COLUMN parent_handoff TEXT NOT NULL DEFAULT '';

COMMIT;
