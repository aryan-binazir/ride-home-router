-- Delete archived rows before dropping deleted_at. A plain column drop would
-- make archived rows live again.
DELETE FROM participants WHERE deleted_at IS NOT NULL;
DELETE FROM drivers WHERE deleted_at IS NOT NULL;
DELETE FROM activity_locations WHERE deleted_at IS NOT NULL;

ALTER TABLE participants DROP COLUMN deleted_at;
ALTER TABLE drivers DROP COLUMN deleted_at;
ALTER TABLE activity_locations DROP COLUMN deleted_at;
