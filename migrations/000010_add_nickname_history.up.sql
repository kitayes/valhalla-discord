-- Create player nickname history table for tracking name changes
CREATE TABLE IF NOT EXISTS player_nickname_history (
    id SERIAL PRIMARY KEY,
    player_id INT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    old_name VARCHAR(255) NOT NULL,
    new_name VARCHAR(255) NOT NULL,
    changed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_nickname_history_player_id ON player_nickname_history(player_id);