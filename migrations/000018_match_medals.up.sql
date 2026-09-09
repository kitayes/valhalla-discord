-- The AI already reads the MVP/SVPG medals off the scoreboard, but there was
-- nowhere to keep them. Record who earned each medal per match so awards can be
-- counted per season, and carry them onto the lobby match so the Elo bonus can
-- be applied when a referee closes it.
ALTER TABLE matches ADD COLUMN IF NOT EXISTS mvp_player_id INT REFERENCES players(id);
ALTER TABLE matches ADD COLUMN IF NOT EXISTS svp_player_id INT REFERENCES players(id);

CREATE INDEX IF NOT EXISTS idx_matches_mvp_player_id ON matches(mvp_player_id)
    WHERE mvp_player_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_matches_svp_player_id ON matches(svp_player_id)
    WHERE svp_player_id IS NOT NULL;

ALTER TABLE lobby_matches ADD COLUMN IF NOT EXISTS mvp_name VARCHAR(255);
ALTER TABLE lobby_matches ADD COLUMN IF NOT EXISTS svp_name VARCHAR(255);
