BEGIN;
LOCK TABLE google_maps_credentials IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM google_maps_credentials) THEN
        RAISE EXCEPTION 'Delete the configured Google Maps credential explicitly before rolling back';
    END IF;
END $$;
DROP TABLE google_maps_credentials;
COMMIT;
