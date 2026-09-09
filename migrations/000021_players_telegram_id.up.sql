-- players.tg_id is what the betting code resolves a bettor through — PlaceBet,
-- GetPlayerPoints, PayoutWinners and RefundAllBets all key on it — and nothing
-- in the application ever wrote to it. /link wrote profile_links.telegram_id
-- instead, a different column in a different table, so the two halves of the
-- identity never met: every bet failed with "Telegram not linked to any player"
-- and the whole betting flow has never once completed.
--
-- Same defect as players.discord_id in 000020, one column over.
--
-- This makes players the single entity across both platforms: discord_id and
-- tg_id hang off the same row, one person is one row. profile_links keeps what
-- it is actually good for — the game profile (nickname, game id, zone, stars,
-- role) — and stops being a second identity store.

-- Backfill from the links that already exist. DISTINCT ON keeps one row per
-- player: discord_player_id carries no unique constraint, so a profile can hold
-- several link rows, and the newest one is the binding that is actually live.
UPDATE players p
   SET tg_id = src.telegram_id
  FROM (
      SELECT DISTINCT ON (discord_player_id)
             discord_player_id, telegram_id
        FROM profile_links
       WHERE telegram_id IS NOT NULL
         AND discord_player_id IS NOT NULL
       ORDER BY discord_player_id, linked_at DESC, id DESC
  ) src
 WHERE p.id = src.discord_player_id
   AND p.tg_id IS NULL;

-- Clear duplicates before enforcing uniqueness: keep the lowest player id for
-- each Telegram account. profile_links.telegram_id is already UNIQUE, so this
-- is normally a no-op — it guards the case where the column was populated by
-- hand before the constraint existed.
UPDATE players p
   SET tg_id = NULL
 WHERE p.tg_id IS NOT NULL
   AND EXISTS (
       SELECT 1 FROM players q
        WHERE q.tg_id = p.tg_id
          AND q.id < p.id
   );

CREATE UNIQUE INDEX IF NOT EXISTS idx_players_tg_id_unique
    ON players(tg_id) WHERE tg_id IS NOT NULL;
