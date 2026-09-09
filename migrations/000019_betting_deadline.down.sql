DROP INDEX IF EXISTS idx_lobby_matches_betting_open;
ALTER TABLE lobby_matches DROP COLUMN IF EXISTS betting_closes_at;
