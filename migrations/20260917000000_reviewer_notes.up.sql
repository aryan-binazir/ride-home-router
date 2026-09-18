ALTER TABLE settings
    ADD COLUMN collect_reviewer_notes BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE route_feedback
    ADD COLUMN reviewer_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN changes JSONB NOT NULL DEFAULT '[]';
