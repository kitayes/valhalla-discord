DROP TRIGGER match_desk_revision ON telegram_bracket_matches;
DROP FUNCTION bump_match_desk_revision();
ALTER TABLE telegram_bracket_matches DROP COLUMN desk_revision;
DROP TABLE telegram_match_desks;
