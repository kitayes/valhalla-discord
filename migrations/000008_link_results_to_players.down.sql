ALTER TABLE player_results DROP CONSTRAINT IF EXISTS fk_player_results_player_id;
DROP INDEX IF EXISTS idx_player_results_player_id;
ALTER TABLE player_results DROP COLUMN IF EXISTS player_id;
