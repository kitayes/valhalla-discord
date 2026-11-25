DROP TABLE IF EXISTS telegram_match_desks;
DROP TABLE IF EXISTS telegram_match_games;
DROP TABLE IF EXISTS telegram_match_reports;
DROP TRIGGER IF EXISTS match_desk_revision ON telegram_bracket_matches;
DROP FUNCTION IF EXISTS bump_match_desk_revision();
DROP TABLE IF EXISTS telegram_bracket_matches;
DROP TABLE IF EXISTS telegram_tournament_teams;
DROP TABLE IF EXISTS telegram_tournaments;
DROP TABLE IF EXISTS telegram_players;
DROP TABLE IF EXISTS telegram_teams;
DROP TABLE IF EXISTS telegram_settings;

DROP TABLE IF EXISTS player_results;
DROP TABLE IF EXISTS matches;
DROP TABLE IF EXISTS mmr_history;
DROP TABLE IF EXISTS match_bets;
DROP TABLE IF EXISTS lobby_matches;

DROP TABLE IF EXISTS link_codes;
DROP TABLE IF EXISTS profile_links;
DROP TABLE IF EXISTS player_nickname_history;
DROP TABLE IF EXISTS player_aliases;
DROP TABLE IF EXISTS players;

DROP TABLE IF EXISTS queue_bans;
DROP TABLE IF EXISTS discord_servers;
DROP TABLE IF EXISTS player_resets;
DROP TABLE IF EXISTS bot_settings;
