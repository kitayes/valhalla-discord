-- Mixes are captain drafts now, and a finished draft is an ordinary lobby
-- match with its thread on lobby_matches.thread_id. The manual mix that kept
-- its roster here is gone.
DROP TABLE IF EXISTS mix_threads;
