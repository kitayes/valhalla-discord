CREATE TABLE IF NOT EXISTS mix_threads (
    thread_id VARCHAR(64) PRIMARY KEY,
    player_ids INTEGER[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
