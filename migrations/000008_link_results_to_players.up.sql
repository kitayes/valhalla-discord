-- Add player_id column
ALTER TABLE player_results ADD COLUMN IF NOT EXISTS player_id INT;

-- Populate player_id based on existing names
-- We use a subquery/update to link existing rows
UPDATE player_results pr
SET player_id = p.id
FROM players p
WHERE pr.player_name = p.name;

-- Standardize names in player_results to match the canonical name in players (optional, but good for consistency)
UPDATE player_results pr
SET player_name = p.name
FROM players p
WHERE pr.player_id = p.id;

-- Make player_id NOT NULL after population (ensures strict integrity moving forward)
-- Note: If you have garbage data that doesn't match a player, this might fail.
-- In that case, we should probably delete or handle those orphans. 
-- For now, let's assume all names in player_results exist in players because we populated players FROM player_results previously.
ALTER TABLE player_results ALTER COLUMN player_id SET NOT NULL;

-- Add Foreign Key constraint
ALTER TABLE player_results 
ADD CONSTRAINT fk_player_results_player_id 
FOREIGN KEY (player_id) REFERENCES players(id);

-- Create Index for performance
CREATE INDEX IF NOT EXISTS idx_player_results_player_id ON player_results(player_id);
