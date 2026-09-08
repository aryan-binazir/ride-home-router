BEGIN;

ALTER TABLE event_routes
    DROP COLUMN driver_handoff,
    DROP COLUMN parent_handoff;

COMMIT;
