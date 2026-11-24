DROP INDEX IF EXISTS idx_player_aliases_player_id;
DROP INDEX IF EXISTS idx_player_aliases_alias;
DROP TABLE IF EXISTS player_aliases;

DROP INDEX IF EXISTS idx_matches_lobby_match_id;
ALTER TABLE matches DROP COLUMN IF EXISTS lobby_match_id;
