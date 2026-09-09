-- Make payouts and refunds idempotent: a bet is settled exactly once.
-- settled_at IS NULL  -> bet is still live and must be paid out or refunded
-- settled_at IS NOT NULL -> already processed; payout holds what was credited back
ALTER TABLE match_bets ADD COLUMN IF NOT EXISTS settled_at TIMESTAMPTZ;
ALTER TABLE match_bets ADD COLUMN IF NOT EXISTS payout INT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_match_bets_unsettled
    ON match_bets(match_id) WHERE settled_at IS NULL;
