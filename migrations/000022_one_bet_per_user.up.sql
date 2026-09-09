-- "One bet per user per match" was enforced only by a COUNT(*) inside PlaceBet,
-- read without a lock: two taps on the Telegram button raced past it and the
-- same user ended up backing both teams, guaranteeing themselves a share of the
-- pool regardless of the outcome. The invariant belongs in the schema.
--
-- The index is partial on unsettled bets. That is the only window in which a
-- second bet can exist at all — PlaceBet refuses anything but an ACTIVE match
-- with an open window, and both settlement paths close it — and it keeps the
-- constraint off already-settled history, which must not be rewritten.

-- Settle duplicates that already landed: keep the earliest bet of each
-- (match, user) pair and refund the rest. They are marked settled rather than
-- deleted, which is the same transition RefundAllBets performs — the row stays
-- auditable with the refunded amount in payout, and no betting history is
-- destroyed by a migration.
--
-- This MUST run after 000021, which backfills players.tg_id. The refund below
-- credits the bettor by joining on that column, and with it still NULL the join
-- matches nobody: the duplicate is stamped settled with payout set while the
-- points are credited to no one and simply disappear. Do not renumber these two
-- past each other.
WITH extras AS (
    SELECT id, tg_user_id, amount
      FROM (
          SELECT id, tg_user_id, amount,
                 ROW_NUMBER() OVER (PARTITION BY match_id, tg_user_id ORDER BY id) AS rn
            FROM match_bets
           WHERE settled_at IS NULL
      ) ranked
     WHERE rn > 1
), refunded AS (
    UPDATE players p
       SET points = points + e.total
      FROM (SELECT tg_user_id, SUM(amount)::int AS total FROM extras GROUP BY tg_user_id) e
     WHERE p.tg_id = e.tg_user_id
    RETURNING p.tg_id
)
UPDATE match_bets
   SET settled_at = NOW(), payout = amount
 WHERE id IN (SELECT id FROM extras);

CREATE UNIQUE INDEX IF NOT EXISTS idx_match_bets_one_per_user
    ON match_bets(match_id, tg_user_id) WHERE settled_at IS NULL;
