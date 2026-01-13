CREATE TABLE telegram_teams (
    id SERIAL PRIMARY KEY,
    name VARCHAR(64) UNIQUE NOT NULL,
    is_checked_in BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE telegram_players (
    id SERIAL PRIMARY KEY,
    telegram_id BIGINT UNIQUE,
    telegram_username VARCHAR(64),
    first_name VARCHAR(64),
    game_nickname VARCHAR(64),
    game_id VARCHAR(32),
    zone_id VARCHAR(10),
    stars INT DEFAULT 0,
    main_role VARCHAR(20) DEFAULT '',
    is_captain BOOLEAN DEFAULT FALSE,
    is_substitute BOOLEAN DEFAULT FALSE,
    fsm_state VARCHAR(64) DEFAULT '',
    team_id INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_telegram_players_telegram_id ON telegram_players(telegram_id);
CREATE INDEX idx_telegram_players_team_id ON telegram_players(team_id);

CREATE TABLE telegram_settings (
    key VARCHAR(64) PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO telegram_settings (key, value) VALUES ('registration_open', 'true');
