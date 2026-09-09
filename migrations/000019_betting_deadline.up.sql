-- The betting window was enforced only by an in-memory timer goroutine. A
-- restart during the window lost the timer, leaving betting_open = TRUE with
-- nothing left to close it: bets could then be placed on a match whose outcome
-- was already known.
--
-- Storing the deadline makes the window survive restarts, and lets a startup
-- sweep close whatever expired while the process was down.
ALTER TABLE lobby_matches ADD COLUMN IF NOT EXISTS betting_closes_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_lobby_matches_betting_open
    ON lobby_matches(betting_closes_at) WHERE betting_open;
