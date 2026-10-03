-- A /create_mix thread has no lobby_matches row: a mix is not a 5v5 match, and
-- a row there would drag in the rating, betting and team A/B handling. Its
-- roster used to live only in the bot's memory, so after a restart screenshots
-- posted in the thread were no longer recognised as belonging to it.
--
-- player_ids keeps the referee's selection order; names are resolved from
-- players at read time so a rename is picked up.
CREATE TABLE IF NOT EXISTS mix_threads (
    thread_id VARCHAR(64) PRIMARY KEY,
    player_ids INTEGER[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
