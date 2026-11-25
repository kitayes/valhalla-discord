-- Valhalla Discord & Telegram Bot — unified initial schema.

CREATE EXTENSION IF NOT EXISTS vector;

-- =====================================================================
-- Settings, server licenses, and queue moderation
-- =====================================================================

CREATE TABLE bot_settings (
    key   VARCHAR(50) PRIMARY KEY,
    value TEXT
);

INSERT INTO bot_settings (key, value)
VALUES ('season_start_date', '2000-01-01T00:00:00Z')
ON CONFLICT DO NOTHING;

CREATE TABLE player_resets (
    player_name VARCHAR(255) PRIMARY KEY,
    reset_date  TIMESTAMPTZ NOT NULL
);

CREATE TABLE discord_servers (
    id                 SERIAL PRIMARY KEY,
    guild_id           VARCHAR(64) NOT NULL UNIQUE,
    license_status     VARCHAR(16) NOT NULL DEFAULT 'TRIAL'
        CHECK (license_status IN ('TRIAL', 'PRO', 'EXPIRED')),
    license_expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days'),
    created_at         TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE queue_bans (
    id           SERIAL PRIMARY KEY,
    discord_id   VARCHAR(64) NOT NULL UNIQUE,
    reason       VARCHAR(255) DEFAULT '',
    banned_until TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_queue_bans_banned_until ON queue_bans(banned_until);

-- =====================================================================
-- Players, aliases, nickname history, and Discord/Telegram profile links
-- =====================================================================

CREATE TABLE players (
    id             SERIAL PRIMARY KEY,
    name           VARCHAR(255) NOT NULL UNIQUE,
    created_at     TIMESTAMPTZ DEFAULT NOW(),
    is_deleted     BOOLEAN DEFAULT FALSE,
    deleted_at     TIMESTAMPTZ,
    discord_id     VARCHAR(64),
    name_embedding vector(768),
    current_mmr    INT DEFAULT 1000,
    points         INT DEFAULT 100,
    tg_id          BIGINT,
    mvp_count      INT DEFAULT 0,
    svp_count      INT DEFAULT 0
);

CREATE INDEX idx_players_is_deleted ON players(is_deleted);
CREATE INDEX idx_players_name_embedding ON players USING hnsw (name_embedding vector_cosine_ops);
CREATE UNIQUE INDEX idx_players_discord_id_unique ON players(discord_id) WHERE discord_id IS NOT NULL;
CREATE UNIQUE INDEX idx_players_tg_id_unique ON players(tg_id) WHERE tg_id IS NOT NULL;

CREATE TABLE player_aliases (
    id         SERIAL PRIMARY KEY,
    player_id  INT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    alias      VARCHAR(255) NOT NULL,
    source     VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_player_aliases_alias ON player_aliases(LOWER(TRIM(alias)));
CREATE INDEX idx_player_aliases_player_id ON player_aliases(player_id);

CREATE TABLE player_nickname_history (
    id         SERIAL PRIMARY KEY,
    player_id  INT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    old_name   VARCHAR(255) NOT NULL,
    new_name   VARCHAR(255) NOT NULL,
    changed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_nickname_history_player_id ON player_nickname_history(player_id);

CREATE TABLE profile_links (
    id                SERIAL PRIMARY KEY,
    discord_player_id INT REFERENCES players(id) ON DELETE CASCADE,
    telegram_id       BIGINT UNIQUE,
    telegram_username VARCHAR(64),
    game_nickname     VARCHAR(64),
    game_id           VARCHAR(32),
    zone_id           VARCHAR(10),
    stars             INT DEFAULT 0,
    main_role         VARCHAR(20),
    linked_at         TIMESTAMPTZ DEFAULT NOW(),
    updated_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_profile_links_discord ON profile_links(discord_player_id);
CREATE INDEX idx_profile_links_telegram ON profile_links(telegram_id);

CREATE TABLE link_codes (
    code              VARCHAR(10) PRIMARY KEY,
    discord_player_id INT REFERENCES players(id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    expires_at        TIMESTAMPTZ DEFAULT (NOW() + INTERVAL '10 minutes')
);

CREATE INDEX idx_link_codes_expires ON link_codes(expires_at);

-- =====================================================================
-- Lobby mixes, bets, MMR history, and parsed match scoreboards
-- =====================================================================

CREATE TABLE lobby_matches (
    id                SERIAL PRIMARY KEY,
    guild_id          VARCHAR(64) NOT NULL,
    captain_a_id      INT NOT NULL REFERENCES players(id),
    captain_b_id      INT NOT NULL REFERENCES players(id),
    team_a_ids        INT[] NOT NULL DEFAULT '{}',
    team_b_ids        INT[] NOT NULL DEFAULT '{}',
    winner            VARCHAR(8) DEFAULT NULL
        CHECK (winner IS NULL OR winner IN ('Team A', 'Team B')),
    status            VARCHAR(16) NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('ACTIVE', 'PROCESSING', 'FINISHED')),
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    thread_id         VARCHAR(64),
    betting_open      BOOLEAN DEFAULT FALSE,
    mvp_name          VARCHAR(255),
    svp_name          VARCHAR(255),
    betting_closes_at TIMESTAMPTZ,
    tg_bet_chat_id    BIGINT,
    tg_bet_message_id BIGINT
);

CREATE INDEX idx_lobby_matches_guild_id ON lobby_matches(guild_id);
CREATE INDEX idx_lobby_matches_status ON lobby_matches(status);
CREATE INDEX idx_lobby_matches_thread_id ON lobby_matches(thread_id);
CREATE INDEX idx_lobby_matches_betting_open ON lobby_matches(betting_closes_at) WHERE betting_open;

CREATE TABLE match_bets (
    id          SERIAL PRIMARY KEY,
    match_id    INT NOT NULL REFERENCES lobby_matches(id) ON DELETE CASCADE,
    tg_user_id  BIGINT NOT NULL,
    team_chosen VARCHAR(8) NOT NULL CHECK (team_chosen IN ('Team A', 'Team B')),
    amount      INT NOT NULL CHECK (amount > 0),
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    settled_at  TIMESTAMPTZ,
    payout      INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_match_bets_match_id ON match_bets(match_id);
CREATE INDEX idx_match_bets_tg_user_id ON match_bets(tg_user_id);
CREATE INDEX idx_match_bets_unsettled ON match_bets(match_id) WHERE settled_at IS NULL;
CREATE UNIQUE INDEX idx_match_bets_one_per_user ON match_bets(match_id, tg_user_id) WHERE settled_at IS NULL;

CREATE TABLE mmr_history (
    id          SERIAL PRIMARY KEY,
    player_id   INT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    match_id    INT REFERENCES lobby_matches(id) ON DELETE SET NULL,
    mmr_before  INT NOT NULL,
    mmr_after   INT NOT NULL,
    delta       INT NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    base_delta  INT NOT NULL DEFAULT 0,
    kda_mod     INT NOT NULL DEFAULT 0,
    medal_bonus INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_mmr_history_player ON mmr_history(player_id, created_at DESC);
CREATE UNIQUE INDEX idx_mmr_history_player_match ON mmr_history(player_id, match_id) WHERE match_id IS NOT NULL;

CREATE TABLE matches (
    id              SERIAL PRIMARY KEY,
    file_hash       VARCHAR(255) NOT NULL UNIQUE,
    match_signature TEXT NOT NULL UNIQUE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    is_deleted      BOOLEAN DEFAULT FALSE,
    deleted_at      TIMESTAMPTZ,
    mvp_player_id   INT REFERENCES players(id),
    svp_player_id   INT REFERENCES players(id),
    lobby_match_id  INT REFERENCES lobby_matches(id) ON DELETE SET NULL
);

CREATE INDEX idx_matches_is_deleted ON matches(is_deleted);
CREATE INDEX idx_matches_mvp_player_id ON matches(mvp_player_id) WHERE mvp_player_id IS NOT NULL;
CREATE INDEX idx_matches_svp_player_id ON matches(svp_player_id) WHERE svp_player_id IS NOT NULL;
CREATE UNIQUE INDEX idx_matches_lobby_match_id ON matches(lobby_match_id)
    WHERE lobby_match_id IS NOT NULL AND is_deleted = FALSE;

CREATE TABLE player_results (
    id          SERIAL PRIMARY KEY,
    match_id    INT REFERENCES matches(id) ON DELETE CASCADE,
    player_name VARCHAR(255) NOT NULL,
    result      VARCHAR(10) NOT NULL,
    kills       INT DEFAULT 0,
    deaths      INT DEFAULT 0,
    assists     INT DEFAULT 0,
    champion    VARCHAR(255),
    is_deleted  BOOLEAN DEFAULT FALSE,
    player_id   INT NOT NULL,
    CONSTRAINT fk_player_results_player_id FOREIGN KEY (player_id) REFERENCES players(id)
);

CREATE INDEX idx_player_results_is_deleted ON player_results(is_deleted);
CREATE INDEX idx_player_results_player_id ON player_results(player_id);

-- =====================================================================
-- Telegram tournaments, teams, brackets, match reports, and match desk
-- =====================================================================

CREATE TABLE telegram_settings (
    key   VARCHAR(64) PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO telegram_settings (key, value)
VALUES ('registration_open', 'true')
ON CONFLICT DO NOTHING;

CREATE TABLE telegram_teams (
    id                       SERIAL PRIMARY KEY,
    name                     VARCHAR(64) NOT NULL UNIQUE,
    is_checked_in            BOOLEAN DEFAULT FALSE,
    created_at               TIMESTAMPTZ DEFAULT NOW(),
    updated_at               TIMESTAMPTZ DEFAULT NOW(),
    status                   VARCHAR(16) NOT NULL DEFAULT 'active',
    challonge_participant_id BIGINT
);

CREATE TABLE telegram_players (
    id                SERIAL PRIMARY KEY,
    telegram_id       BIGINT UNIQUE,
    telegram_username VARCHAR(64),
    first_name        VARCHAR(64),
    game_nickname     VARCHAR(64),
    game_id           VARCHAR(32),
    zone_id           VARCHAR(10),
    stars             INT DEFAULT 0,
    main_role         VARCHAR(20) DEFAULT '',
    is_captain        BOOLEAN DEFAULT FALSE,
    is_substitute     BOOLEAN DEFAULT FALSE,
    fsm_state         VARCHAR(64) DEFAULT '',
    team_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    updated_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_telegram_players_telegram_id ON telegram_players(telegram_id);
CREATE INDEX idx_telegram_players_team_id ON telegram_players(team_id);

CREATE TABLE telegram_tournaments (
    id                SERIAL PRIMARY KEY,
    name              VARCHAR(128) NOT NULL,
    slug              VARCHAR(64) UNIQUE,
    status            VARCHAR(32) NOT NULL DEFAULT 'registration',
    tournament_time   TIMESTAMPTZ,
    challonge_id      BIGINT,
    challonge_url     TEXT NOT NULL DEFAULT '',
    challonge_for     TIMESTAMPTZ,
    winner_team_id    INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    winner_team_name  VARCHAR(128) NOT NULL DEFAULT '',
    is_active         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    tournament_type   VARCHAR(32) NOT NULL DEFAULT 'single elimination',
    hold_third_place  BOOLEAN NOT NULL DEFAULT FALSE,
    seeding_type      VARCHAR(32) NOT NULL DEFAULT 'stars',
    stage             VARCHAR(16) NOT NULL DEFAULT 'standalone',
    advance_count     INT NOT NULL DEFAULT 0,
    format_rounds     SMALLINT NOT NULL DEFAULT 1,
    format_semifinal  SMALLINT NOT NULL DEFAULT 3,
    format_final      SMALLINT NOT NULL DEFAULT 3,
    bracket_size      INT NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX idx_tournaments_active ON telegram_tournaments(is_active) WHERE is_active = TRUE;

INSERT INTO telegram_tournaments (name, slug, status, is_active)
VALUES ('Valhalla Tournament #1', 'valhalla_tournament_1', 'registration', TRUE)
ON CONFLICT DO NOTHING;

CREATE TABLE telegram_tournament_teams (
    id                       SERIAL PRIMARY KEY,
    tournament_id            INT NOT NULL REFERENCES telegram_tournaments(id) ON DELETE CASCADE,
    team_id                  INT NOT NULL REFERENCES telegram_teams(id) ON DELETE CASCADE,
    is_checked_in            BOOLEAN NOT NULL DEFAULT FALSE,
    status                   VARCHAR(32) NOT NULL DEFAULT 'registered',
    challonge_participant_id BIGINT,
    seed                     INT,
    placement                INT,
    points                   INT NOT NULL DEFAULT 0,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source                   VARCHAR(16) NOT NULL DEFAULT 'registered',
    qualified_from           INT REFERENCES telegram_tournaments(id) ON DELETE SET NULL,
    checked_in_at            TIMESTAMPTZ,
    checked_in_by            BIGINT,
    UNIQUE (tournament_id, team_id)
);

CREATE INDEX idx_tourney_teams_tourney ON telegram_tournament_teams(tournament_id);
CREATE INDEX idx_tourney_teams_team ON telegram_tournament_teams(team_id);

CREATE TABLE telegram_bracket_matches (
    id                 SERIAL PRIMARY KEY,
    challonge_match_id BIGINT NOT NULL UNIQUE,
    round              INT NOT NULL,
    play_order         INT NOT NULL,
    team1_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    team2_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    winner_id          INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    state              VARCHAR(16) NOT NULL,
    scores_csv         VARCHAR(32) NOT NULL DEFAULT '',
    both_notified      BOOLEAN NOT NULL DEFAULT FALSE,
    synced_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    desk_revision      BIGINT NOT NULL DEFAULT 0,
    tournament_id      INT REFERENCES telegram_tournaments(id) ON DELETE CASCADE
);

CREATE INDEX idx_bracket_matches_team1 ON telegram_bracket_matches(team1_id);
CREATE INDEX idx_bracket_matches_team2 ON telegram_bracket_matches(team2_id);
CREATE INDEX idx_bracket_matches_tourney ON telegram_bracket_matches(tournament_id);

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

CREATE TRIGGER match_desk_revision
    BEFORE UPDATE ON telegram_bracket_matches
    FOR EACH ROW EXECUTE FUNCTION bump_match_desk_revision();

CREATE TABLE telegram_match_reports (
    id                   SERIAL PRIMARY KEY,
    reporter_telegram_id BIGINT NOT NULL,
    winner_team_id       INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    loser_team_id        INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    score                VARCHAR(16) NOT NULL,
    photo_file_ids       TEXT[] NOT NULL,
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    bracket_match_id     INT REFERENCES telegram_bracket_matches(id) ON DELETE SET NULL,
    synced_at            TIMESTAMPTZ,
    status               VARCHAR(16) NOT NULL DEFAULT 'confirmed',
    expires_at           TIMESTAMPTZ,
    resolved_at          TIMESTAMPTZ
);

CREATE INDEX idx_match_reports_winner ON telegram_match_reports(winner_team_id);
CREATE INDEX idx_match_reports_loser ON telegram_match_reports(loser_team_id);
CREATE INDEX idx_match_reports_open ON telegram_match_reports(bracket_match_id)
    WHERE status IN ('pending', 'disputed');

CREATE TABLE telegram_match_games (
    id                   SERIAL PRIMARY KEY,
    bracket_match_id     INT NOT NULL REFERENCES telegram_bracket_matches(id) ON DELETE CASCADE,
    match_revision       BIGINT NOT NULL DEFAULT 0,
    game_no              INT NOT NULL,
    winner_team_id       INT NOT NULL REFERENCES telegram_teams(id) ON DELETE CASCADE,
    reporter_telegram_id BIGINT NOT NULL,
    photo_file_ids       TEXT[] NOT NULL DEFAULT '{}',
    status               VARCHAR(16) NOT NULL DEFAULT 'pending',
    expires_at           TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at          TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_match_games_slot
    ON telegram_match_games(bracket_match_id, match_revision, game_no)
    WHERE status <> 'annulled';
CREATE INDEX idx_match_games_pending
    ON telegram_match_games(expires_at)
    WHERE status = 'pending';

CREATE TABLE telegram_match_desks (
    tournament_id TEXT PRIMARY KEY,
    data          JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
