DROP INDEX IF EXISTS idx_match_reports_open;
ALTER TABLE telegram_match_reports
    DROP COLUMN IF EXISTS resolved_at,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS status;
