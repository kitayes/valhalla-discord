ALTER TABLE telegram_tournaments
    DROP COLUMN IF EXISTS tournament_type,
    DROP COLUMN IF EXISTS hold_third_place,
    DROP COLUMN IF EXISTS seeding_type;
