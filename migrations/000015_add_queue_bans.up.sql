-- One row per Discord account: BanPlayer upserts with ON CONFLICT (discord_id),
-- which needs a unique constraint to resolve against.
CREATE TABLE IF NOT EXISTS queue_bans (
    id SERIAL PRIMARY KEY,
    discord_id VARCHAR(64) NOT NULL UNIQUE,
    reason VARCHAR(255) DEFAULT '',
    banned_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Expired bans are purged by the lobby sweeper, which scans on banned_until.
CREATE INDEX IF NOT EXISTS idx_queue_bans_banned_until ON queue_bans(banned_until);
