-- players.discord_id gates the whole lobby → match → rating flow, but nothing
-- ever wrote to it: there was no way to bind a Discord account to a player, so
-- the join button always answered "your Discord is not linked to any player".
--
-- Binding is added in the application; this index is what keeps it honest, so
-- one Discord account cannot end up owning two profiles.

-- Clear duplicates before enforcing uniqueness: keep the lowest player id for
-- each Discord account. Normally a no-op, since nothing populated the column.
UPDATE players p
   SET discord_id = NULL
 WHERE discord_id IS NOT NULL
   AND EXISTS (
       SELECT 1 FROM players q
        WHERE q.discord_id = p.discord_id
          AND q.id < p.id
   );

CREATE UNIQUE INDEX IF NOT EXISTS idx_players_discord_id_unique
    ON players(discord_id) WHERE discord_id IS NOT NULL;
