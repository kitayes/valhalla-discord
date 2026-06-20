-- Drop new features
DROP TABLE IF EXISTS match_bets;
DROP TABLE IF EXISTS lobby_matches;
DROP TABLE IF EXISTS discord_servers;

-- Remove added columns from players
ALTER TABLE players DROP COLUMN IF EXISTS points;
ALTER TABLE players DROP COLUMN IF EXISTS current_mmr;
ALTER TABLE players DROP COLUMN IF EXISTS tg_id;