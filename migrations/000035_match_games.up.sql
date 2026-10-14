-- One row per game of a series, reported by a captain with screenshots and
-- confirmed by the other. match_revision is the bracket row's desk_revision
-- when the game was played: a referee's reset bumps it and leaves old games
-- behind.
CREATE TABLE IF NOT EXISTS telegram_match_games (
    id                   SERIAL PRIMARY KEY,
    bracket_match_id     INT NOT NULL REFERENCES telegram_bracket_matches(id) ON DELETE CASCADE,
    match_revision       BIGINT NOT NULL DEFAULT 0,
    game_no              INT NOT NULL,
    winner_team_id       INT NOT NULL REFERENCES telegram_teams(id) ON DELETE CASCADE,
    reporter_telegram_id BIGINT NOT NULL,
    photo_file_ids       TEXT[] NOT NULL DEFAULT '{}',
    status               VARCHAR(16) NOT NULL DEFAULT 'pending',
    expires_at           TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at          TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_match_games_slot
    ON telegram_match_games (bracket_match_id, match_revision, game_no) WHERE status <> 'annulled';
CREATE INDEX IF NOT EXISTS idx_match_games_pending
    ON telegram_match_games (expires_at) WHERE status = 'pending';
