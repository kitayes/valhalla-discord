ALTER TABLE lobby_matches DROP COLUMN IF EXISTS svp_name;
ALTER TABLE lobby_matches DROP COLUMN IF EXISTS mvp_name;

DROP INDEX IF EXISTS idx_matches_svp_player_id;
DROP INDEX IF EXISTS idx_matches_mvp_player_id;

ALTER TABLE matches DROP COLUMN IF EXISTS svp_player_id;
ALTER TABLE matches DROP COLUMN IF EXISTS mvp_player_id;
