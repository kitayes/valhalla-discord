CREATE TABLE IF NOT EXISTS players (
                                       id SERIAL PRIMARY KEY,
                                       name VARCHAR(255) UNIQUE NOT NULL,
                                       created_at TIMESTAMPTZ DEFAULT NOW()
);

INSERT INTO players (name)
SELECT DISTINCT player_name FROM player_results
ON CONFLICT (name) DO NOTHING;