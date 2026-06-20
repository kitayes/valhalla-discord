-- ============================================================
-- Blackwatch Bot Ecosystem: Matchmaking & Betting Schema
-- Migration 000011
-- ============================================================

-- 1. Multi-tenant SaaS: Guild license tracking
CREATE TABLE IF NOT EXISTS discord_servers (
    id SERIAL PRIMARY KEY,
    guild_id VARCHAR(64) UNIQUE NOT NULL,
    license_status VARCHAR(16) NOT NULL DEFAULT 'TRIAL'
        CHECK (license_status IN ('TRIAL', 'PRO', 'EXPIRED')),
    license_expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days'),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- 2. Extend players table: MMR, betting points, Telegram linkage
ALTER TABLE players ADD COLUMN IF NOT EXISTS current_mmr INT DEFAULT 1000;
ALTER TABLE players ADD COLUMN IF NOT EXISTS points INT DEFAULT 100;
ALTER TABLE players ADD COLUMN IF NOT EXISTS tg_id BIGINT;

-- 3. Lobby matches: Captains, teams, Elo-MMR data, winner
CREATE TABLE IF NOT EXISTS lobby_matches (
    id SERIAL PRIMARY KEY,
    guild_id VARCHAR(64) NOT NULL,
    captain_a_id INT NOT NULL REFERENCES players(id),
    captain_b_id INT NOT NULL REFERENCES players(id),
    team_a_ids INT[] NOT NULL DEFAULT '{}',
    team_b_ids INT[] NOT NULL DEFAULT '{}',
    winner VARCHAR(8) DEFAULT NULL
        CHECK (winner IS NULL OR winner IN ('Team A', 'Team B')),
    status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('ACTIVE', 'PROCESSING', 'FINISHED')),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_lobby_matches_guild_id ON lobby_matches(guild_id);
CREATE INDEX IF NOT EXISTS idx_lobby_matches_status ON lobby_matches(status);

-- 4. Telegram betting: Points economy for live matches
CREATE TABLE IF NOT EXISTS match_bets (
    id SERIAL PRIMARY KEY,
    match_id INT NOT NULL REFERENCES lobby_matches(id) ON DELETE CASCADE,
    tg_user_id BIGINT NOT NULL,
    team_chosen VARCHAR(8) NOT NULL
        CHECK (team_chosen IN ('Team A', 'Team B')),
    amount INT NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_match_bets_match_id ON match_bets(match_id);
CREATE INDEX IF NOT EXISTS idx_match_bets_tg_user_id ON match_bets(tg_user_id);