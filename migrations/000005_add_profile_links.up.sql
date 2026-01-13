CREATE TABLE profile_links (
    id SERIAL PRIMARY KEY,
    discord_player_id INT REFERENCES players(id) ON DELETE CASCADE,
    telegram_id BIGINT UNIQUE,
    telegram_username VARCHAR(64),
    game_nickname VARCHAR(64),
    game_id VARCHAR(32),
    zone_id VARCHAR(10),
    stars INT DEFAULT 0,
    main_role VARCHAR(20),
    linked_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_profile_links_discord ON profile_links(discord_player_id);
CREATE INDEX idx_profile_links_telegram ON profile_links(telegram_id);

CREATE TABLE link_codes (
    code VARCHAR(10) PRIMARY KEY,
    discord_player_id INT REFERENCES players(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    expires_at TIMESTAMPTZ DEFAULT NOW() + INTERVAL '10 minutes'
);

CREATE INDEX idx_link_codes_expires ON link_codes(expires_at);
