-- Discards app approvals; back them up before an explicitly confirmed rollback.
-- ADMIN_EMAILS remains environment configuration. Never expose a pre-auth image.
DROP TABLE approved_emails;
