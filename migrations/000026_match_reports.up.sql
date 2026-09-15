CREATE TABLE telegram_match_reports (
    id SERIAL PRIMARY KEY,
    reporter_telegram_id BIGINT NOT NULL,
    winner_team_id INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    loser_team_id INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    score VARCHAR(16) NOT NULL,
    photo_file_ids TEXT[] NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_match_reports_winner ON telegram_match_reports(winner_team_id);
CREATE INDEX idx_match_reports_loser ON telegram_match_reports(loser_team_id);
