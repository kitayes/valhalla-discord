ALTER TABLE telegram_tournaments
    ADD COLUMN IF NOT EXISTS stage VARCHAR(16) NOT NULL DEFAULT 'standalone',
    ADD COLUMN IF NOT EXISTS advance_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS format_rounds SMALLINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS format_semifinal SMALLINT NOT NULL DEFAULT 3,
    ADD COLUMN IF NOT EXISTS format_final SMALLINT NOT NULL DEFAULT 3,
    -- Challonge bracket size (a power of two) recorded at build time. A
    -- qualifier's cache drops the rounds past its advance line, so the layout
    -- cannot be derived from the cached matches afterwards.
    ADD COLUMN IF NOT EXISTS bracket_size INT NOT NULL DEFAULT 0;

ALTER TABLE telegram_tournament_teams
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'registered',
    ADD COLUMN IF NOT EXISTS qualified_from INT REFERENCES telegram_tournaments(id) ON DELETE SET NULL,
    -- Last check-in change and who made it (telegram id, 0 for an admin).
    ADD COLUMN IF NOT EXISTS checked_in_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS checked_in_by BIGINT;
