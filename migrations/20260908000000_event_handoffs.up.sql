BEGIN;

ALTER TABLE event_routes
    ADD COLUMN driver_handoff TEXT NOT NULL DEFAULT '',
    ADD COLUMN parent_handoff TEXT NOT NULL DEFAULT '';

COMMIT;
