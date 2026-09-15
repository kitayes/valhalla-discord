ALTER TABLE telegram_match_reports DROP COLUMN synced_at, DROP COLUMN bracket_match_id;
ALTER TABLE telegram_teams DROP COLUMN challonge_participant_id;
DROP TABLE telegram_bracket_matches;
