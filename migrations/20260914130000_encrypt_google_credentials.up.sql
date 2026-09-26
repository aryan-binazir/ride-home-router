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
    encrypted_api_key text NOT NULL CHECK (length(encrypted_api_key) BETWEEN 43 AND 8192 AND left(encrypted_api_key, 3) = 'v1:')
);
COMMIT;
