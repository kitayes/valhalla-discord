-- The bracket lives in Challonge; this table is a read cache of its matches so
-- "who is my opponent" and /bracket cost no API requests (500/month free).
-- It is rewritten whole after every write to Challonge.
CREATE TABLE telegram_bracket_matches (
    id                 SERIAL PRIMARY KEY,
    challonge_match_id BIGINT NOT NULL UNIQUE,
    round              INT NOT NULL,
    play_order         INT NOT NULL,
    team1_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    team2_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    winner_id          INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    state              VARCHAR(16) NOT NULL,
    scores_csv         VARCHAR(32) NOT NULL DEFAULT '',
    both_notified      BOOLEAN NOT NULL DEFAULT FALSE,
    synced_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_bracket_matches_team1 ON telegram_bracket_matches(team1_id);
CREATE INDEX idx_bracket_matches_team2 ON telegram_bracket_matches(team2_id);

ALTER TABLE telegram_teams ADD COLUMN challonge_participant_id BIGINT;

ALTER TABLE telegram_match_reports
    ADD COLUMN bracket_match_id INT REFERENCES telegram_bracket_matches(id) ON DELETE SET NULL,
    ADD COLUMN synced_at TIMESTAMPTZ;
