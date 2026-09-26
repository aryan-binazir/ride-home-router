BEGIN;
LOCK TABLE google_maps_credentials IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM google_maps_credentials) THEN
        RAISE EXCEPTION 'Delete the configured Google Maps credential explicitly before changing credential storage';
    END IF;
END $$;
DROP TABLE google_maps_credentials;
CREATE TABLE google_maps_credentials (
    id integer PRIMARY KEY CHECK (id = 1),
    api_key text NOT NULL CHECK (length(api_key) BETWEEN 1 AND 4096)
);
COMMIT;
