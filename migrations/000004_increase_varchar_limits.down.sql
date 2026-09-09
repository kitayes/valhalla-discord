-- Narrowing these columns again truncates data, so the rollback only restores
-- the original declared types where that is lossless in practice.
ALTER TABLE players ALTER COLUMN name TYPE VARCHAR(255);

ALTER TABLE player_results ALTER COLUMN champion TYPE VARCHAR(255);
ALTER TABLE player_results ALTER COLUMN player_name TYPE VARCHAR(255);

ALTER TABLE matches ALTER COLUMN match_signature TYPE VARCHAR(64);
ALTER TABLE matches ALTER COLUMN file_hash TYPE VARCHAR(64);
