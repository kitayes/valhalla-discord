ALTER TABLE matches
    ADD COLUMN IF NOT EXISTS lobby_match_id INT REFERENCES lobby_matches(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_matches_lobby_match_id
    ON matches(lobby_match_id)
    WHERE lobby_match_id IS NOT NULL AND is_deleted = FALSE;

CREATE TABLE IF NOT EXISTS player_aliases (
    id         SERIAL PRIMARY KEY,
    player_id  INT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    alias      VARCHAR(255) NOT NULL,
    source     VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_player_aliases_alias
    ON player_aliases(LOWER(TRIM(alias)));

CREATE INDEX IF NOT EXISTS idx_player_aliases_player_id
    ON player_aliases(player_id);
