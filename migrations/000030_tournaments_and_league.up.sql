CREATE TABLE telegram_tournaments (
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    slug VARCHAR(64) UNIQUE,
    status VARCHAR(32) NOT NULL DEFAULT 'registration',
    tournament_time TIMESTAMPTZ,
    challonge_id BIGINT,
    challonge_url TEXT NOT NULL DEFAULT '',
    challonge_for TIMESTAMPTZ,
    winner_team_id INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    winner_team_name VARCHAR(128) NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_tournaments_active ON telegram_tournaments(is_active) WHERE is_active = TRUE;

CREATE TABLE telegram_tournament_teams (
    id SERIAL PRIMARY KEY,
    tournament_id INT NOT NULL REFERENCES telegram_tournaments(id) ON DELETE CASCADE,
    team_id INT NOT NULL REFERENCES telegram_teams(id) ON DELETE CASCADE,
    is_checked_in BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL DEFAULT 'registered',
    challonge_participant_id BIGINT,
    seed INT,
    placement INT,
    points INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tournament_id, team_id)
);

CREATE INDEX idx_tourney_teams_tourney ON telegram_tournament_teams(tournament_id);
CREATE INDEX idx_tourney_teams_team ON telegram_tournament_teams(team_id);

ALTER TABLE telegram_bracket_matches ADD COLUMN tournament_id INT REFERENCES telegram_tournaments(id) ON DELETE CASCADE;
CREATE INDEX idx_bracket_matches_tourney ON telegram_bracket_matches(tournament_id);

INSERT INTO telegram_tournaments (name, slug, status, tournament_time, challonge_id, challonge_url, challonge_for, is_active)
VALUES (
    'Valhalla Tournament #1',
    'valhalla_tournament_1',
    'registration',
    (SELECT NULLIF(value,'')::timestamptz FROM telegram_settings WHERE key='tournament_time'),
    (SELECT NULLIF(value,'')::bigint FROM telegram_settings WHERE key='challonge_tournament_id'),
    COALESCE((SELECT value FROM telegram_settings WHERE key='challonge_tournament_url'), ''),
    (SELECT NULLIF(value,'')::timestamptz FROM telegram_settings WHERE key='challonge_tournament_for'),
    TRUE
);

INSERT INTO telegram_tournament_teams (tournament_id, team_id, is_checked_in, status, challonge_participant_id)
SELECT 1, id, is_checked_in, status, challonge_participant_id FROM telegram_teams
ON CONFLICT DO NOTHING;

UPDATE telegram_bracket_matches SET tournament_id = 1 WHERE tournament_id IS NULL;
