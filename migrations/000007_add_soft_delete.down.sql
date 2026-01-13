DROP INDEX IF EXISTS idx_matches_is_deleted;
DROP INDEX IF EXISTS idx_players_is_deleted;
DROP INDEX IF EXISTS idx_player_results_is_deleted;

ALTER TABLE matches DROP COLUMN IF EXISTS is_deleted;
ALTER TABLE matches DROP COLUMN IF EXISTS deleted_at;

ALTER TABLE players DROP COLUMN IF EXISTS is_deleted;
ALTER TABLE players DROP COLUMN IF EXISTS deleted_at;

ALTER TABLE player_results DROP COLUMN IF EXISTS is_deleted;
