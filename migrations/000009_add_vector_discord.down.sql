-- Drop the HNSW index
DROP INDEX IF EXISTS idx_players_name_embedding;

-- Drop the embedding column
ALTER TABLE players DROP COLUMN IF EXISTS name_embedding;

-- Drop the discord_id column
ALTER TABLE players DROP COLUMN IF EXISTS discord_id;