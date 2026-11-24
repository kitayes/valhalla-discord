ALTER TABLE mmr_history
    DROP COLUMN IF EXISTS medal_bonus,
    DROP COLUMN IF EXISTS kda_mod,
    DROP COLUMN IF EXISTS base_delta;
