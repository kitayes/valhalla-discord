-- Only the constraint is dropped. players.tg_id keeps the values backfilled
-- from profile_links: clearing it would unlink every bettor, and the data it
-- holds is still readable from profile_links anyway.
DROP INDEX IF EXISTS idx_players_tg_id_unique;
