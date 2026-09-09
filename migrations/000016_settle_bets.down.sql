DROP INDEX IF EXISTS idx_match_bets_unsettled;
ALTER TABLE match_bets DROP COLUMN IF EXISTS payout;
ALTER TABLE match_bets DROP COLUMN IF EXISTS settled_at;
