ALTER TABLE import_jobs ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0);
