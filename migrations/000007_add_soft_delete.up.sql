ALTER TABLE matches ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN DEFAULT FALSE;
ALTER TABLE matches ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

ALTER TABLE players ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN DEFAULT FALSE;
ALTER TABLE players ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

ALTER TABLE player_results ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_matches_is_deleted ON matches(is_deleted);
CREATE INDEX IF NOT EXISTS idx_players_is_deleted ON players(is_deleted);
CREATE INDEX IF NOT EXISTS idx_player_results_is_deleted ON player_results(is_deleted);
