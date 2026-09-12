CREATE TABLE workflow_sessions (
    kind text NOT NULL CHECK (kind IN ('draft', 'route', 'import')),
    id text NOT NULL,
    payload jsonb NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    consumed boolean NOT NULL DEFAULT false,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (kind, id)
);
CREATE INDEX workflow_sessions_expiry_idx ON workflow_sessions (expires_at);

CREATE TABLE import_rows (
    kind text NOT NULL DEFAULT 'import' CHECK (kind = 'import'),
    session_id text NOT NULL,
    row_index integer NOT NULL,
    payload jsonb NOT NULL,
    selected boolean NOT NULL,
    PRIMARY KEY (session_id,row_index),
    FOREIGN KEY (kind,session_id) REFERENCES workflow_sessions(kind,id) ON DELETE CASCADE
);
CREATE TABLE import_jobs (
    kind text NOT NULL DEFAULT 'import' CHECK (kind = 'import'),
    session_id text NOT NULL,
    job_index integer NOT NULL,
    address text NOT NULL,
    row_indices integer[] NOT NULL,
    done boolean NOT NULL DEFAULT false,
    claim_token text,
    claimed_until timestamptz,
    PRIMARY KEY (session_id,job_index),
    FOREIGN KEY (kind,session_id) REFERENCES workflow_sessions(kind,id) ON DELETE CASCADE
);
CREATE INDEX import_jobs_pending_idx ON import_jobs(claimed_until) WHERE NOT done;

CREATE TABLE provider_throttles (
    name text PRIMARY KEY,
    next_at timestamptz NOT NULL
);
INSERT INTO provider_throttles(name,next_at) VALUES('nominatim',clock_timestamp());
