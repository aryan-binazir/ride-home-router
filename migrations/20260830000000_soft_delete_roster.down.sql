LOCK TABLE participants, drivers, activity_locations IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM participants WHERE deleted_at IS NOT NULL) THEN
        RAISE EXCEPTION 'soft-delete rollback refused: archived participants exist; export or explicitly purge archived rows before retrying, then follow the README dirty-state repair guidance';
    END IF;
    IF EXISTS (SELECT 1 FROM drivers WHERE deleted_at IS NOT NULL) THEN
        RAISE EXCEPTION 'soft-delete rollback refused: archived drivers exist; export or explicitly purge archived rows before retrying, then follow the README dirty-state repair guidance';
    END IF;
    IF EXISTS (SELECT 1 FROM activity_locations WHERE deleted_at IS NOT NULL) THEN
        RAISE EXCEPTION 'soft-delete rollback refused: archived activity locations exist; export or explicitly purge archived rows before retrying, then follow the README dirty-state repair guidance';
    END IF;
END
$$;

ALTER TABLE participants DROP COLUMN deleted_at;
ALTER TABLE drivers DROP COLUMN deleted_at;
ALTER TABLE activity_locations DROP COLUMN deleted_at;
