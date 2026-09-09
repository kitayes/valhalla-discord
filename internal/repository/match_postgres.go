package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"blackwatch/internal/ai"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/lib/pq"
)

// pgUniqueViolation is the Postgres SQLSTATE for a unique constraint breach.
const pgUniqueViolation = "23505"

// ErrDiscordIDTaken reports that the Discord account is already bound to a
// different player. Callers turn it into a message the user can act on.
var ErrDiscordIDTaken = errors.New("this Discord account is already linked to another player")

// ErrTelegramIDTaken is ErrDiscordIDTaken's counterpart for players.tg_id.
var ErrTelegramIDTaken = errors.New("this Telegram account is already linked to another player")

const (
	cosineSimilarityThreshold = 0.28
	defaultSeasonStartYear    = 2025
	defaultSeasonStartMonth   = 1
	defaultSeasonStartDay     = 1
	minDeathsForKDA           = 1
)

type MatchPostgres struct {
	db              *sql.DB
	playerCache     *PlayerCache
	embeddingClient *ai.EmbeddingClient
}

// NewMatchPostgres builds the repository and warms the player-name cache.
//
// A failed warm-up is now an error rather than a silent empty cache: every
// player lookup falls back to a query plus an Ollama embedding round trip on a
// cache miss, so starting cold and never knowing it is exactly the failure that
// hides until the matching path is slow for everyone.
func NewMatchPostgres(ctx context.Context, db *sql.DB, cacheSize int, embeddingClient *ai.EmbeddingClient) (*MatchPostgres, error) {
	cache, err := NewPlayerCache(cacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create player cache: %w", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT id, name FROM players WHERE is_deleted = FALSE ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("failed to warm player cache: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var players []models.Player
	for rows.Next() {
		var p models.Player
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, fmt.Errorf("failed to scan player for cache: %w", err)
		}
		players = append(players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read players for cache: %w", err)
	}
	cache.LoadAll(players)

	return &MatchPostgres{
		db:              db,
		playerCache:     cache,
		embeddingClient: embeddingClient,
	}, nil
}

func (r *MatchPostgres) Create(ctx context.Context, match models.Match) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Safe to call unconditionally: Rollback after a successful Commit is a
	// no-op. The previous `if err != nil` guard missed every early return where
	// err was shadowed by a `:=` inside a block, leaking the connection.
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var matchID int
	query := "INSERT INTO matches (file_hash, match_signature) VALUES ($1, $2) RETURNING id"
	err = tx.QueryRowContext(ctx, query, match.FileHash, match.MatchSignature).Scan(&matchID)
	if err != nil {
		return 0, fmt.Errorf("failed to insert match: %w", err)
	}

	// Collect all player IDs (using cache for fast lookups)
	playerIDs := make([]int, len(match.Players))
	for i, p := range match.Players {
		playerID, err := r.EnsurePlayerExists(ctx, p.PlayerName)
		if err != nil {
			return 0, fmt.Errorf("failed to ensure player exists: %w", err)
		}
		playerIDs[i] = playerID
	}

	// Batch insert all player results in one query
	if err := r.batchInsertPlayerResults(ctx, tx, matchID, match.Players, playerIDs); err != nil {
		return 0, fmt.Errorf("failed to insert player results: %w", err)
	}

	if err := r.recordMedals(ctx, tx, matchID, match, playerIDs); err != nil {
		return 0, fmt.Errorf("failed to record medals: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return matchID, nil
}

// recordMedals stores the MVP/SVPG winners on the match and bumps the players'
// lifetime counters.
//
// A medal name is only accepted when it matches somebody on this match's
// scoreboard — the name is OCR output and must never create or reassign a
// player, unlike the roster itself which goes through EnsurePlayerExists.
func (r *MatchPostgres) recordMedals(ctx context.Context, tx *sql.Tx, matchID int, match models.Match, playerIDs []int) error {
	mvpID := resolveMedalPlayerID(match.MVP, match.Players, playerIDs)
	svpID := resolveMedalPlayerID(match.SVP, match.Players, playerIDs)

	if mvpID == 0 && svpID == 0 {
		return nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE matches SET mvp_player_id = $1, svp_player_id = $2 WHERE id = $3`,
		nullableID(mvpID), nullableID(svpID), matchID,
	); err != nil {
		return fmt.Errorf("failed to set medal winners: %w", err)
	}

	if mvpID != 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE players SET mvp_count = mvp_count + 1 WHERE id = $1`, mvpID,
		); err != nil {
			return fmt.Errorf("failed to increment mvp_count for player %d: %w", mvpID, err)
		}
	}
	if svpID != 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE players SET svp_count = svp_count + 1 WHERE id = $1`, svpID,
		); err != nil {
			return fmt.Errorf("failed to increment svp_count for player %d: %w", svpID, err)
		}
	}
	return nil
}

// resolveMedalPlayerID maps a medal name onto one of the match's own players.
// Returns 0 when the name is empty or does not belong to this scoreboard.
func resolveMedalPlayerID(medalName string, players []models.PlayerResult, playerIDs []int) int {
	if strings.TrimSpace(medalName) == "" || len(playerIDs) != len(players) {
		return 0
	}
	normalized := normalizeForComparison(medalName)
	for i, p := range players {
		if normalizeForComparison(p.PlayerName) == normalized {
			return playerIDs[i]
		}
	}
	return 0
}

func nullableID(id int) interface{} {
	if id == 0 {
		return nil
	}
	return id
}

// GetMedalCountsAfter returns per-player medal tallies for matches played since
// the given date, so awards can be reported per season rather than for all time.
func (r *MatchPostgres) GetMedalCountsAfter(ctx context.Context, date time.Time) (map[int]models.Medals, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT player_id,
		        SUM(is_mvp) AS mvp,
		        SUM(is_svp) AS svp
		   FROM (
		        SELECT mvp_player_id AS player_id, 1 AS is_mvp, 0 AS is_svp
		          FROM matches
		         WHERE mvp_player_id IS NOT NULL AND is_deleted = FALSE AND created_at >= $1
		        UNION ALL
		        SELECT svp_player_id AS player_id, 0 AS is_mvp, 1 AS is_svp
		          FROM matches
		         WHERE svp_player_id IS NOT NULL AND is_deleted = FALSE AND created_at >= $1
		   ) medals
		  GROUP BY player_id`, date,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query medal counts: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	counts := make(map[int]models.Medals)
	for rows.Next() {
		var playerID, mvp, svp int
		if err := rows.Scan(&playerID, &mvp, &svp); err != nil {
			return nil, fmt.Errorf("failed to scan medal counts: %w", err)
		}
		counts[playerID] = models.Medals{MVP: mvp, SVP: svp}
	}
	return counts, rows.Err()
}

// GetLifetimeMedals returns a player's all-time medal counters.
func (r *MatchPostgres) GetLifetimeMedals(ctx context.Context, playerID int) (models.Medals, error) {
	var m models.Medals
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(mvp_count, 0), COALESCE(svp_count, 0) FROM players WHERE id = $1`,
		playerID,
	).Scan(&m.MVP, &m.SVP)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Medals{}, nil
	}
	if err != nil {
		return models.Medals{}, fmt.Errorf("failed to get lifetime medals for player %d: %w", playerID, err)
	}
	return m, nil
}

func (r *MatchPostgres) Exists(ctx context.Context, fileHash, matchSignature string) (bool, error) {
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM matches WHERE (file_hash=$1 OR match_signature=$2) AND is_deleted = FALSE)"
	err := r.db.QueryRowContext(ctx, query, fileHash, matchSignature).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check match existence: %w", err)
	}
	return exists, nil
}

func (r *MatchPostgres) GetAllAfter(ctx context.Context, date time.Time) ([]models.Match, error) {
	query := `
		SELECT m.id, m.created_at, pr.player_name, pr.result, pr.kills, pr.deaths, pr.assists, pr.player_id
		FROM matches m
		JOIN player_results pr ON m.id = pr.match_id
		WHERE m.created_at >= $1 AND m.is_deleted = FALSE AND pr.is_deleted = FALSE
	`
	rows, err := r.db.QueryContext(ctx, query, date)
	if err != nil {
		return nil, fmt.Errorf("failed to query matches: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	matchesMap := make(map[int]*models.Match)
	for rows.Next() {
		var id int
		var createdAt time.Time
		var pr models.PlayerResult
		if err := rows.Scan(&id, &createdAt, &pr.PlayerName, &pr.Result, &pr.Kills, &pr.Deaths, &pr.Assists, &pr.PlayerID); err != nil {
			continue
		}
		if _, ok := matchesMap[id]; !ok {
			matchesMap[id] = &models.Match{
				ID:        id,
				CreatedAt: createdAt,
				Players:   []models.PlayerResult{},
			}
		}
		matchesMap[id].Players = append(matchesMap[id].Players, pr)
	}
	// A connection that drops mid-iteration ends the loop just like a clean
	// finish. Unchecked, this fed a truncated match set into the season stats
	// and silently produced a wrong leaderboard.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read matches: %w", err)
	}

	var result []models.Match
	for _, m := range matchesMap {
		result = append(result, *m)
	}
	return result, nil
}

func (r *MatchPostgres) Delete(ctx context.Context, id int) error {
	query := "UPDATE matches SET is_deleted = TRUE, deleted_at = NOW() WHERE id = $1 AND is_deleted = FALSE"
	res, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to soft delete match: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	_, err = r.db.ExecContext(ctx, "UPDATE player_results SET is_deleted = TRUE WHERE match_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to soft delete player results: %w", err)
	}
	return nil
}

func (r *MatchPostgres) Restore(ctx context.Context, id int) error {
	query := "UPDATE matches SET is_deleted = FALSE, deleted_at = NULL WHERE id = $1 AND is_deleted = TRUE"
	res, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to restore match: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	_, err = r.db.ExecContext(ctx, "UPDATE player_results SET is_deleted = FALSE WHERE match_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to restore player results: %w", err)
	}
	return nil
}

func (r *MatchPostgres) WipeAll(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "UPDATE matches SET is_deleted = TRUE, deleted_at = NOW() WHERE is_deleted = FALSE")
	if err != nil {
		return fmt.Errorf("failed to soft delete all matches: %w", err)
	}
	_, err = r.db.ExecContext(ctx, "UPDATE player_results SET is_deleted = TRUE WHERE is_deleted = FALSE")
	if err != nil {
		return fmt.Errorf("failed to soft delete all player results: %w", err)
	}
	_, err = r.db.ExecContext(ctx, "UPDATE players SET is_deleted = TRUE, deleted_at = NOW() WHERE is_deleted = FALSE")
	if err != nil {
		return fmt.Errorf("failed to soft delete all players: %w", err)
	}

	// Clear entire cache since all players are wiped
	r.playerCache.Clear()

	return nil
}

func (r *MatchPostgres) SetSeasonStartDate(ctx context.Context, date time.Time) error {
	_, err := r.db.ExecContext(ctx, `
       INSERT INTO bot_settings (key, value) VALUES ('season_start_date', $1)
       ON CONFLICT (key) DO UPDATE SET value = $1
    `, date.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("failed to set season start date: %w", err)
	}
	return nil
}

func (r *MatchPostgres) GetSeasonStartDate(ctx context.Context) (time.Time, error) {
	var val string
	err := r.db.QueryRowContext(ctx, "SELECT value FROM bot_settings WHERE key = 'season_start_date'").Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Date(defaultSeasonStartYear, defaultSeasonStartMonth, defaultSeasonStartDay, 0, 0, 0, 0, time.UTC), nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get season start date: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse season start date: %w", err)
	}
	return parsed, nil
}

func (r *MatchPostgres) SetPlayerResetDate(ctx context.Context, playerName string, date time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO player_resets (player_name, reset_date) VALUES ($1, $2)
		ON CONFLICT (player_name) DO UPDATE SET reset_date = $2
	`, playerName, date)
	if err != nil {
		return fmt.Errorf("failed to set player reset date: %w", err)
	}
	return nil
}

func (r *MatchPostgres) GetPlayerResetDates(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT player_name, reset_date FROM player_resets")
	if err != nil {
		return nil, fmt.Errorf("failed to get player reset dates: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	res := make(map[string]time.Time)
	for rows.Next() {
		var name string
		var date time.Time
		if err := rows.Scan(&name, &date); err == nil {
			res[name] = date
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read player reset dates: %w", err)
	}
	return res, nil
}

func (r *MatchPostgres) GetHistory(ctx context.Context, playerID int, limit int) ([]models.Match, error) {
	query := `
		SELECT m.id, m.created_at, pr.result, pr.kills, pr.deaths, pr.assists, pr.player_name
		FROM matches m
		JOIN player_results pr ON m.id = pr.match_id
		WHERE pr.player_id = $1 AND m.is_deleted = FALSE AND pr.is_deleted = FALSE
		ORDER BY m.created_at DESC
		LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, query, playerID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get player history: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var matches []models.Match
	for rows.Next() {
		var m models.Match
		var pr models.PlayerResult
		err := rows.Scan(&m.ID, &m.CreatedAt, &pr.Result, &pr.Kills, &pr.Deaths, &pr.Assists, &pr.PlayerName)
		if err != nil {
			continue
		}
		pr.PlayerID = playerID
		m.Players = []models.PlayerResult{pr}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read player history: %w", err)
	}
	return matches, nil
}

func (r *MatchPostgres) EnsurePlayerExists(ctx context.Context, name string) (int, error) {
	normalizedInput := normalizeForComparison(name)

	if id, found := r.playerCache.Get(normalizedInput); found {
		return id, nil
	}

	var id int
	exactQuery := `
		SELECT id FROM players 
		WHERE LOWER(TRIM(name)) = $1 AND is_deleted = FALSE
		LIMIT 1
	`
	err := r.db.QueryRowContext(ctx, exactQuery, normalizedInput).Scan(&id)
	if err == nil {
		r.playerCache.Set(normalizedInput, id)
		return id, nil
	}

	// Vector-based cosine similarity search via Ollama embeddings
	if r.embeddingClient != nil {
		embedding, embErr := r.embeddingClient.GetEmbedding(ctx, name)
		if embErr == nil && len(embedding) > 0 {
			embeddingStr := ai.FormatEmbeddingForPG(embedding)
			vectorQuery := `
				SELECT id, name FROM players 
				WHERE name_embedding IS NOT NULL AND is_deleted = FALSE
				  AND name_embedding <=> $1 < $2
				ORDER BY name_embedding <=> $1
				LIMIT 1
			`
			var candidateID int
			var candidateName string
			err := r.db.QueryRowContext(ctx, vectorQuery, embeddingStr, cosineSimilarityThreshold).Scan(&candidateID, &candidateName)
			if err == nil {
				r.playerCache.Set(normalizedInput, candidateID)
				return candidateID, nil
			}
		}
	}

	// Fallback: fuzzy Levenshtein search for cases where embedding is not yet generated
	fuzzyQuery := `
		SELECT id, name FROM players 
		WHERE LOWER(TRIM(name)) LIKE $1 AND is_deleted = FALSE
		LIMIT 10
	`
	likePattern := "%" + normalizedInput[:min(len(normalizedInput), 3)] + "%"

	rows, err := r.db.QueryContext(ctx, fuzzyQuery, likePattern)
	if err == nil {
		defer rows.Close() //nolint:errcheck // best-effort cleanup

		for rows.Next() {
			var candidateID int
			var candidateName string
			if err := rows.Scan(&candidateID, &candidateName); err != nil {
				continue
			}

			normalizedCandidate := normalizeForComparison(candidateName)
			if similarityScore(normalizedInput, normalizedCandidate) > 0.85 {
				r.playerCache.Set(normalizedInput, candidateID)
				return candidateID, nil
			}
		}
		// A truncated candidate list here only means the fuzzy match found
		// nothing, so fall through to creating the player — but log it, since
		// that silently creates a duplicate of an existing one.
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("failed to scan fuzzy name candidates: %w", err)
		}
	}

	// Generate embedding for the new player
	var embeddingStr string
	if r.embeddingClient != nil {
		embedding, embErr := r.embeddingClient.GetEmbedding(ctx, name)
		if embErr == nil && len(embedding) > 0 {
			embeddingStr = ai.FormatEmbeddingForPG(embedding)
		}
	}

	if embeddingStr != "" {
		err = r.db.QueryRowContext(ctx, `
			INSERT INTO players (name, name_embedding) VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id`, name, embeddingStr).Scan(&id)
	} else {
		err = r.db.QueryRowContext(ctx, `
			INSERT INTO players (name) VALUES ($1)
			ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id`, name).Scan(&id)
	}

	if err != nil {
		return 0, fmt.Errorf("failed to ensure player exists: %w", err)
	}

	r.playerCache.Set(normalizedInput, id)

	return id, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (r *MatchPostgres) batchInsertPlayerResults(
	ctx context.Context,
	tx *sql.Tx,
	matchID int,
	players []models.PlayerResult,
	playerIDs []int,
) error {
	if len(players) == 0 {
		return nil
	}
	query := `INSERT INTO player_results 
              (match_id, player_id, player_name, result, kills, deaths, assists) 
              VALUES `

	values := make([]interface{}, 0, len(players)*7)
	placeholders := make([]string, 0, len(players))

	for i, p := range players {
		offset := i * 7
		placeholders = append(placeholders,
			fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				offset+1, offset+2, offset+3, offset+4,
				offset+5, offset+6, offset+7))

		values = append(values,
			matchID,
			playerIDs[i],
			p.PlayerName,
			p.Result,
			p.Kills,
			p.Deaths,
			p.Assists)
	}

	query += strings.Join(placeholders, ", ")

	_, err := tx.ExecContext(ctx, query, values...)
	if err != nil {
		return fmt.Errorf("batch insert failed: %w", err)
	}

	return nil
}

func normalizeForComparison(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)

	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			result.WriteRune(r)
		}
	}
	return strings.TrimSpace(result.String())
}

func similarityScore(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	distance := levenshteinDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	return 1.0 - float64(distance)/float64(maxLen)
}

func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
		matrix[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		matrix[0][j] = j
	}

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			del := matrix[i-1][j] + 1
			ins := matrix[i][j-1] + 1
			sub := matrix[i-1][j-1] + cost

			minVal := del
			if ins < minVal {
				minVal = ins
			}
			if sub < minVal {
				minVal = sub
			}
			matrix[i][j] = minVal
		}
	}

	return matrix[len(a)][len(b)]
}

func (r *MatchPostgres) GetAllPlayers(ctx context.Context) ([]models.Player, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, name FROM players WHERE is_deleted = FALSE ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("failed to get all players: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var players []models.Player
	for rows.Next() {
		var p models.Player
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, fmt.Errorf("failed to scan player: %w", err)
		}
		players = append(players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read players: %w", err)
	}
	return players, nil
}

func (r *MatchPostgres) GetPlayerNameByID(ctx context.Context, id int) (string, error) {
	var name string
	err := r.db.QueryRowContext(ctx, "SELECT name FROM players WHERE id = $1 AND is_deleted = FALSE", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("player %d: %w", id, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("failed to get name for player %d: %w", id, err)
	}
	return name, nil
}

// GetPlayerNamesByIDs resolves many players in one round trip. The per-player
// lookup it replaces ran once per participant, so building a single match embed
// cost ten queries and closing a match cost another ten.
func (r *MatchPostgres) GetPlayerNamesByIDs(ctx context.Context, ids []int) (map[int]string, error) {
	names := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}

	rows, err := r.db.QueryContext(ctx,
		"SELECT id, name FROM players WHERE id = ANY($1) AND is_deleted = FALSE",
		pq.Array(ids),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get player names: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("failed to scan player name: %w", err)
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read player names: %w", err)
	}
	return names, nil
}

func (r *MatchPostgres) WipePlayerByID(ctx context.Context, id int) error {
	name, err := r.GetPlayerNameByID(ctx, id)
	if err != nil {
		return fmt.Errorf("игрок с ID %d не найден: %w", id, err)
	}

	_, err = r.db.ExecContext(ctx, "UPDATE player_results SET is_deleted = TRUE WHERE player_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to soft delete player results: %w", err)
	}

	_, err = r.db.ExecContext(ctx, "DELETE FROM player_resets WHERE player_name = $1", name)
	if err != nil {
		return fmt.Errorf("failed to delete player resets: %w", err)
	}

	_, err = r.db.ExecContext(ctx, "UPDATE players SET is_deleted = TRUE, deleted_at = NOW() WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to soft delete player: %w", err)
	}

	normalized := normalizeForComparison(name)
	r.playerCache.Delete(normalized)

	return nil
}

func (r *MatchPostgres) RestorePlayer(ctx context.Context, id int) error {
	_, err := r.db.ExecContext(ctx, "UPDATE players SET is_deleted = FALSE, deleted_at = NULL WHERE id = $1 AND is_deleted = TRUE", id)
	if err != nil {
		return fmt.Errorf("failed to restore player: %w", err)
	}

	_, err = r.db.ExecContext(ctx, "UPDATE player_results SET is_deleted = FALSE WHERE player_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to restore player results: %w", err)
	}

	name, err := r.GetPlayerNameByID(ctx, id)
	if err == nil {
		normalized := normalizeForComparison(name)
		r.playerCache.Set(normalized, id)
	}

	return nil
}

func (r *MatchPostgres) RenamePlayer(ctx context.Context, id int, newName string) error {
	oldName, err := r.GetPlayerNameByID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get player name: %w", err)
	}

	// Generate embedding for the new name
	var embeddingStr string
	if r.embeddingClient != nil {
		embedding, embErr := r.embeddingClient.GetEmbedding(ctx, newName)
		if embErr == nil && len(embedding) > 0 {
			embeddingStr = ai.FormatEmbeddingForPG(embedding)
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Safe to call unconditionally: Rollback after a successful Commit is a no-op.
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	// Log the name change in history
	_, err = tx.ExecContext(ctx,
		"INSERT INTO player_nickname_history (player_id, old_name, new_name) VALUES ($1, $2, $3)",
		id, oldName, newName,
	)
	if err != nil {
		return fmt.Errorf("failed to log nickname history: %w", err)
	}

	// Update player name and embedding
	var playerResult sql.Result
	if embeddingStr != "" {
		playerResult, err = tx.ExecContext(ctx,
			"UPDATE players SET name = $1, name_embedding = $2 WHERE id = $3 AND is_deleted = FALSE",
			newName, embeddingStr, id,
		)
	} else {
		playerResult, err = tx.ExecContext(ctx,
			"UPDATE players SET name = $1 WHERE id = $2 AND is_deleted = FALSE",
			newName, id,
		)
	}
	if err != nil {
		return fmt.Errorf("failed to rename player: %w", err)
	}

	rows, err := playerResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("игрок с ID %d не найден", id)
	}

	// Cascade: update player_name in all past matches (player_results table)
	_, err = tx.ExecContext(ctx,
		"UPDATE player_results SET player_name = $1 WHERE player_id = $2",
		newName, id,
	)
	if err != nil {
		return fmt.Errorf("failed to cascade player name: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rename transaction: %w", err)
	}

	// Invalidate cache
	oldNormalized := normalizeForComparison(oldName)
	newNormalized := normalizeForComparison(newName)

	r.playerCache.Delete(oldNormalized)
	r.playerCache.Set(newNormalized, id)

	return nil
}

// GetDiscordIDByPlayerID returns the discord_id for a given player ID.
func (r *MatchPostgres) GetDiscordIDByPlayerID(ctx context.Context, playerID int) (string, error) {
	var discordID sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT discord_id FROM players WHERE id = $1 AND is_deleted = FALSE`, playerID,
	).Scan(&discordID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("failed to get discord_id for player %d: %w", playerID, err)
	}
	if !discordID.Valid {
		return "", fmt.Errorf("player %d: %w", playerID, domain.ErrDiscordNotLinked)
	}
	return discordID.String, nil
}

// SetDiscordID binds a Discord account to a player.
//
// The column is guarded by a unique index, so a Discord account that already
// owns another profile is rejected here rather than silently owning two.
func (r *MatchPostgres) SetDiscordID(ctx context.Context, playerID int, discordID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE players SET discord_id = $1 WHERE id = $2 AND is_deleted = FALSE`,
		discordID, playerID,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolation {
			return ErrDiscordIDTaken
		}
		return fmt.Errorf("failed to bind discord_id to player %d: %w", playerID, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to count bound players: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("player %d not found", playerID)
	}
	return nil
}

// ClearDiscordID releases a player's Discord binding.
func (r *MatchPostgres) ClearDiscordID(ctx context.Context, playerID int) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE players SET discord_id = NULL WHERE id = $1 AND is_deleted = FALSE`, playerID,
	)
	if err != nil {
		return fmt.Errorf("failed to unbind player %d: %w", playerID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to count unbound players: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("player %d not found", playerID)
	}
	return nil
}

// FindPlayerByExactName resolves a nickname to a player, matching only on the
// name itself.
//
// EnsurePlayerExists is deliberately not reused here. It falls back to an
// embedding similarity search, which is right for screenshot OCR — the parser
// mangles nicknames and a near match is almost always the same person. It is
// wrong for a self-service claim: somebody typing "Gunnar" would be handed the
// existing "Gunnarr" profile, which belongs to somebody else.
//
// Returns domain.ErrPlayerNotFound when no player carries that name.
func (r *MatchPostgres) FindPlayerByExactName(ctx context.Context, name string) (int, string, error) {
	var id int
	var stored string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name FROM players
		  WHERE LOWER(TRIM(name)) = LOWER(TRIM($1)) AND is_deleted = FALSE
		  ORDER BY id LIMIT 1`, name,
	).Scan(&id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("player %q: %w", name, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, "", fmt.Errorf("failed to look up player %q: %w", name, err)
	}
	return id, stored, nil
}

// SuggestPlayerNames lists players whose name matches the prefix, for Discord's
// autocomplete. Prefix matches come first so typing the start of a nickname
// surfaces it before any substring hit.
//
// Discord caps a response at 25 choices and expects it within three seconds, so
// the cap belongs in the query rather than in a filter over every player.
func (r *MatchPostgres) SuggestPlayerNames(ctx context.Context, prefix string, limit int) ([]models.Player, error) {
	if limit <= 0 || limit > 25 {
		limit = 25
	}
	pattern := strings.ToLower(strings.TrimSpace(prefix))

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name FROM players
		  WHERE is_deleted = FALSE
		    AND ($1 = '' OR LOWER(name) LIKE '%' || $1 || '%')
		  ORDER BY (LOWER(name) LIKE $1 || '%') DESC, name
		  LIMIT $2`, pattern, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to suggest player names: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var players []models.Player
	for rows.Next() {
		var p models.Player
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, fmt.Errorf("failed to scan suggestion: %w", err)
		}
		players = append(players, p)
	}
	return players, rows.Err()
}

// CreatePlayerWithDiscord creates a player already bound to a Discord account.
//
// One statement, so a refused binding cannot leave an empty profile behind: the
// alternative — insert, then bind, then delete on failure — is a compensating
// path that stops compensating the moment the process dies between the two.
// A Discord account that already owns a profile trips the unique index and
// comes back as ErrDiscordIDTaken.
func (r *MatchPostgres) CreatePlayerWithDiscord(ctx context.Context, name, discordID string) (int, error) {
	var id int
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO players (name, discord_id) VALUES ($1, $2) RETURNING id`,
		strings.TrimSpace(name), discordID,
	).Scan(&id)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolation {
			return 0, ErrDiscordIDTaken
		}
		return 0, fmt.Errorf("failed to create player %q: %w", name, err)
	}

	r.playerCache.Set(normalizeForComparison(name), id)
	return id, nil
}

// SetTelegramID binds a Telegram account to a player.
//
// players.tg_id is the column the betting code resolves a bettor through. It is
// guarded by a unique index, so a Telegram account that already owns another
// profile is rejected here rather than silently owning two — the same shape as
// SetDiscordID above, because it is the same kind of binding.
func (r *MatchPostgres) SetTelegramID(ctx context.Context, playerID int, telegramID int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE players SET tg_id = $1 WHERE id = $2 AND is_deleted = FALSE`,
		telegramID, playerID,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolation {
			return ErrTelegramIDTaken
		}
		return fmt.Errorf("failed to bind tg_id to player %d: %w", playerID, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to count bound players: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	return nil
}

// ClearTelegramID releases a player's Telegram binding.
func (r *MatchPostgres) ClearTelegramID(ctx context.Context, playerID int) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE players SET tg_id = NULL WHERE id = $1 AND is_deleted = FALSE`, playerID,
	)
	if err != nil {
		return fmt.Errorf("failed to unlink player %d: %w", playerID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to count unlinked players: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	return nil
}

// GetPlayerByTelegramID returns the player bound to a Telegram account.
func (r *MatchPostgres) GetPlayerByTelegramID(ctx context.Context, telegramID int64) (int, string, error) {
	var id int
	var name string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name FROM players WHERE tg_id = $1 AND is_deleted = FALSE`, telegramID,
	).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("tg_id %d: %w", telegramID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, "", fmt.Errorf("failed to look up player by tg_id: %w", err)
	}
	return id, name, nil
}

// GetPlayerByDiscordID returns the player ID and name for a given discord_id.
// This is an O(1) direct SQL lookup instead of iterating all players in memory.
func (r *MatchPostgres) GetPlayerByDiscordID(ctx context.Context, discordID string) (int, string, error) {
	var id int
	var name string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name FROM players WHERE discord_id = $1 AND is_deleted = FALSE`, discordID,
	).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("discord_id %s: %w", discordID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, "", fmt.Errorf("failed to get player by discord_id %s: %w", discordID, err)
	}
	return id, name, nil
}
