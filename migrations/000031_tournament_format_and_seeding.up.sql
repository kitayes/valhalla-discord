ALTER TABLE telegram_tournaments
    ADD COLUMN IF NOT EXISTS tournament_type VARCHAR(32) NOT NULL DEFAULT 'single elimination',
    ADD COLUMN IF NOT EXISTS hold_third_place BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS seeding_type VARCHAR(32) NOT NULL DEFAULT 'stars';
