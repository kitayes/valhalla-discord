-- One atomic operations document per tournament, including pending Telegram
-- notifications. Results and pairings remain in the Challonge cache.
CREATE TABLE telegram_match_desks (
    tournament_id TEXT PRIMARY KEY,
    data JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A match can close and reopen between worker ticks. Keep an epoch in the
-- cache so old captain buttons cannot apply to that new incarnation.
ALTER TABLE telegram_bracket_matches ADD COLUMN desk_revision BIGINT NOT NULL DEFAULT 0;
CREATE FUNCTION bump_match_desk_revision() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.team1_id IS DISTINCT FROM NEW.team1_id
       OR OLD.team2_id IS DISTINCT FROM NEW.team2_id
       OR OLD.state IS DISTINCT FROM NEW.state THEN
        NEW.desk_revision := OLD.desk_revision + 1;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER match_desk_revision BEFORE UPDATE ON telegram_bracket_matches
    FOR EACH ROW EXECUTE FUNCTION bump_match_desk_revision();
