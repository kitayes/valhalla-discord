CREATE TABLE matches (
                         id SERIAL PRIMARY KEY,
                         file_hash VARCHAR(64) UNIQUE NOT NULL,
                         match_signature VARCHAR(64) UNIQUE NOT NULL,
                         created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE player_results (
                                id SERIAL PRIMARY KEY,
                                match_id INT REFERENCES matches(id) ON DELETE CASCADE,
                                player_name VARCHAR(255) NOT NULL,
                                result VARCHAR(10) NOT NULL,
                                kills INT DEFAULT 0,
                                deaths INT DEFAULT 0,
                                assists INT DEFAULT 0,
                                champion VARCHAR(255)
);