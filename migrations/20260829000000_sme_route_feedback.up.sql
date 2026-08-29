ALTER TABLE settings
    ADD COLUMN sme_email TEXT NOT NULL DEFAULT '';

CREATE TABLE route_feedback (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    session_id TEXT NOT NULL UNIQUE,
    sme_email TEXT NOT NULL,
    schema_version INTEGER NOT NULL DEFAULT 1,
    mode TEXT NOT NULL,
    input JSONB NOT NULL,
    proposed JSONB NOT NULL,
    final JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
