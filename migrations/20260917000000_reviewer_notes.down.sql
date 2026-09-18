ALTER TABLE route_feedback
    DROP COLUMN IF EXISTS changes,
    DROP COLUMN IF EXISTS reviewer_note;

ALTER TABLE settings
    DROP COLUMN IF EXISTS collect_reviewer_notes;
