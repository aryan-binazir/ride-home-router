CREATE TABLE approved_emails (
    email text PRIMARY KEY CHECK (email = lower(btrim(email)) AND length(email) BETWEEN 3 AND 254),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Historical verified identity records, never consulted for authorization.
CREATE TABLE verified_admin_emails (
    email text PRIMARY KEY CHECK (email = lower(btrim(email)) AND length(email) BETWEEN 3 AND 254),
    first_verified_at timestamptz NOT NULL DEFAULT now()
);
