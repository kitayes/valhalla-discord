-- Reports filed from the mini app wait for the opposing captain before the
-- bracket moves. Existing rows were applied immediately, hence 'confirmed'.
ALTER TABLE telegram_match_reports
    ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'confirmed',
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD COLUMN resolved_at TIMESTAMPTZ;

CREATE INDEX idx_match_reports_open
    ON telegram_match_reports (bracket_match_id)
    WHERE status IN ('pending', 'disputed');
