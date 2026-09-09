-- Only the constraint is reversible. The duplicate bets the up migration
-- refunded stay settled: re-creating them would mean taking the points back off
-- the players who were credited.
DROP INDEX IF EXISTS idx_match_bets_one_per_user;
