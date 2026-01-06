CREATE TABLE IF NOT EXISTS bot_settings (
                                            key VARCHAR(50) PRIMARY KEY,
                                            value TEXT
);

INSERT INTO bot_settings (key, value) VALUES ('season_start_date', '2000-01-01T00:00:00Z') ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS player_resets (
                                             player_name VARCHAR(255) PRIMARY KEY,
                                             reset_date TIMESTAMPTZ NOT NULL
);