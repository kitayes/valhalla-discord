ALTER TABLE telegram_tournament_teams
    DROP COLUMN IF EXISTS checked_in_by,
    DROP COLUMN IF EXISTS checked_in_at,
    DROP COLUMN IF EXISTS qualified_from,
    DROP COLUMN IF EXISTS source;

ALTER TABLE telegram_tournaments
    DROP COLUMN IF EXISTS bracket_size,
    DROP COLUMN IF EXISTS format_final,
    DROP COLUMN IF EXISTS format_semifinal,
    DROP COLUMN IF EXISTS format_rounds,
    DROP COLUMN IF EXISTS advance_count,
    DROP COLUMN IF EXISTS stage;
