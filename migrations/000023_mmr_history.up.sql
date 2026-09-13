-- players.current_mmr is a single column that UpdatePlayerMMR overwrites, so a
-- rating had no past: /history could show kills and a result but never the
-- "+18" players actually care about, no graph was possible, and a dispute over
-- a drop could not be settled because the previous value was already gone.
--
-- One row per player per rating change, written in the same transaction as the
-- update itself.
CREATE TABLE IF NOT EXISTS mmr_history (
    id         SERIAL PRIMARY KEY,
    player_id  INT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    -- The lobby match this change came from. Nullable: an admin correction or a
    -- season reset moves a rating without a match behind it.
    match_id   INT REFERENCES lobby_matches(id) ON DELETE SET NULL,
    mmr_before INT NOT NULL,
    mmr_after  INT NOT NULL,
    -- Stored rather than derived: a later correction to either endpoint must not
    -- silently rewrite what a past match was worth.
    delta      INT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- The profile view reads one player's changes newest first.
CREATE INDEX IF NOT EXISTS idx_mmr_history_player
    ON mmr_history(player_id, created_at DESC);

-- One rating change per player per match: replaying a settlement must not
-- append a second row for work that was already recorded.
CREATE UNIQUE INDEX IF NOT EXISTS idx_mmr_history_player_match
    ON mmr_history(player_id, match_id) WHERE match_id IS NOT NULL;
