-- Migration 000012: Add thread_id to lobby_matches for Discord thread tracking
ALTER TABLE lobby_matches ADD COLUMN IF NOT EXISTS thread_id VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_lobby_matches_thread_id ON lobby_matches(thread_id);