ALTER TABLE participants ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE drivers ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE activity_locations ADD COLUMN deleted_at TIMESTAMPTZ;
