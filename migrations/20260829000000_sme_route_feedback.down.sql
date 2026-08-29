DROP TABLE IF EXISTS route_feedback;

ALTER TABLE settings
    DROP COLUMN IF EXISTS sme_email;
