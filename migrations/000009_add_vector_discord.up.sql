-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Add discord_id column for hard account binding
ALTER TABLE players ADD COLUMN IF NOT EXISTS discord_id VARCHAR(64);

-- Add name_embedding column (768-dimensional vector from nomic-embed-text)
ALTER TABLE players ADD COLUMN IF NOT EXISTS name_embedding vector(768);

-- Create HNSW index for high-performance vector search
CREATE INDEX IF NOT EXISTS idx_players_name_embedding ON players USING hnsw (name_embedding vector_cosine_ops);