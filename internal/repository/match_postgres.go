package repository

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"valhalla/internal/models"
)

const (
	similarityThreshold     = 0.85
	defaultSeasonStartYear  = 2025
	defaultSeasonStartMonth = 1
	defaultSeasonStartDay   = 1
	minDeathsForKDA         = 1
)

type MatchPostgres struct {
	db          *sql.DB
	playerCache *PlayerCache
}

func NewMatchPostgres(db *sql.DB) *MatchPostgres {
	cache := NewPlayerCache()

	// Warm up cache with existing players
	rows, err := db.Query("SELECT id, name FROM players WHERE is_deleted = FALSE ORDER BY id")
	if err == nil {
		defer rows.Close()
		var players []models.Player
		for rows.Next() {
			var p models.Player
			if err := rows.Scan(&p.ID, &p.Name); err == nil {
				players = append(players, p)
			}
		}
		cache.LoadAll(players)
	}

	return &MatchPostgres{
		db:          db,
		playerCache: cache,
	}
}

func (r *MatchPostgres) Create(match models.Match) (int, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	var matchID int
	query := "INSERT INTO matches (file_hash, match_signature) VALUES ($1, $2) RETURNING id"
	err = tx.QueryRow(query, match.FileHash, match.MatchSignature).Scan(&matchID)
	if err != nil {
		return 0, fmt.Errorf("failed to insert match: %w", err)
	}

	// Collect all player IDs (using cache for fast lookups)
	playerIDs := make([]int, len(match.Players))
	for i, p := range match.Players {
		playerID, err := r.EnsurePlayerExists(p.PlayerName)
		if err != nil {
			return 0, fmt.Errorf("failed to ensure player exists: %w", err)
		}
		playerIDs[i] = playerID
	}

	// Batch insert all player results in one query
	if err := r.batchInsertPlayerResults(tx, matchID, match.Players, playerIDs); err != nil {
		return 0, fmt.Errorf("failed to insert player results: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return matchID, nil
}

func (r *MatchPostgres) Exists(fileHash, matchSignature string) (bool, error) {
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM matches WHERE (file_hash=$1 OR match_signature=$2) AND is_deleted = FALSE)"
	err := r.db.QueryRow(query, fileHash, matchSignature).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check match existence: %w", err)
	}
	return exists, nil
}

func (r *MatchPostgres) GetAllAfter(date time.Time) ([]models.Match, error) {
	query := `
		SELECT m.id, m.created_at, pr.player_name, pr.result, pr.kills, pr.deaths, pr.assists, pr.player_id
		FROM matches m
		JOIN player_results pr ON m.id = pr.match_id
		WHERE m.created_at >= $1 AND m.is_deleted = FALSE AND pr.is_deleted = FALSE
	`
	rows, err := r.db.Query(query, date)
	if err != nil {
		return nil, fmt.Errorf("failed to query matches: %w", err)
	}
	defer rows.Close()

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

	var result []models.Match
	for _, m := range matchesMap {
		result = append(result, *m)
	}
	return result, nil
}

func (r *MatchPostgres) Delete(id int) error {
	query := "UPDATE matches SET is_deleted = TRUE, deleted_at = NOW() WHERE id = $1 AND is_deleted = FALSE"
	res, err := r.db.Exec(query, id)
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

	_, err = r.db.Exec("UPDATE player_results SET is_deleted = TRUE WHERE match_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to soft delete player results: %w", err)
	}
	return nil
}

func (r *MatchPostgres) Restore(id int) error {
	query := "UPDATE matches SET is_deleted = FALSE, deleted_at = NULL WHERE id = $1 AND is_deleted = TRUE"
	res, err := r.db.Exec(query, id)
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

	_, err = r.db.Exec("UPDATE player_results SET is_deleted = FALSE WHERE match_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to restore player results: %w", err)
	}
	return nil
}

func (r *MatchPostgres) WipeAll() error {
	_, err := r.db.Exec("UPDATE matches SET is_deleted = TRUE, deleted_at = NOW() WHERE is_deleted = FALSE")
	if err != nil {
		return fmt.Errorf("failed to soft delete all matches: %w", err)
	}
	_, err = r.db.Exec("UPDATE player_results SET is_deleted = TRUE WHERE is_deleted = FALSE")
	if err != nil {
		return fmt.Errorf("failed to soft delete all player results: %w", err)
	}
	_, err = r.db.Exec("UPDATE players SET is_deleted = TRUE, deleted_at = NOW() WHERE is_deleted = FALSE")
	if err != nil {
		return fmt.Errorf("failed to soft delete all players: %w", err)
	}

	// Clear entire cache since all players are wiped
	r.playerCache.Clear()

	return nil
}

func (r *MatchPostgres) SetSeasonStartDate(date time.Time) error {
	_, err := r.db.Exec(`
       INSERT INTO bot_settings (key, value) VALUES ('season_start_date', $1)
       ON CONFLICT (key) DO UPDATE SET value = $1
    `, date.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("failed to set season start date: %w", err)
	}
	return nil
}

func (r *MatchPostgres) GetSeasonStartDate() (time.Time, error) {
	var val string
	err := r.db.QueryRow("SELECT value FROM bot_settings WHERE key = 'season_start_date'").Scan(&val)
	if err == sql.ErrNoRows {
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

func (r *MatchPostgres) SetPlayerResetDate(playerName string, date time.Time) error {
	_, err := r.db.Exec(`
		INSERT INTO player_resets (player_name, reset_date) VALUES ($1, $2)
		ON CONFLICT (player_name) DO UPDATE SET reset_date = $2
	`, playerName, date)
	if err != nil {
		return fmt.Errorf("failed to set player reset date: %w", err)
	}
	return nil
}

func (r *MatchPostgres) GetPlayerResetDates() (map[string]time.Time, error) {
	rows, err := r.db.Query("SELECT player_name, reset_date FROM player_resets")
	if err != nil {
		return nil, fmt.Errorf("failed to get player reset dates: %w", err)
	}
	defer rows.Close()

	res := make(map[string]time.Time)
	for rows.Next() {
		var name string
		var date time.Time
		if err := rows.Scan(&name, &date); err == nil {
			res[name] = date
		}
	}
	return res, nil
}

func (r *MatchPostgres) GetHistory(playerID int, limit int) ([]models.Match, error) {
	query := `
		SELECT m.id, m.created_at, pr.result, pr.kills, pr.deaths, pr.assists, pr.player_name
		FROM matches m
		JOIN player_results pr ON m.id = pr.match_id
		WHERE pr.player_id = $1 AND m.is_deleted = FALSE AND pr.is_deleted = FALSE
		ORDER BY m.created_at DESC
		LIMIT $2
	`
	rows, err := r.db.Query(query, playerID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get player history: %w", err)
	}
	defer rows.Close()

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
	return matches, nil
}

func (r *MatchPostgres) EnsurePlayerExists(name string) (int, error) {
	normalizedInput := normalizeForComparison(name)

	// Fast path: check cache first (O(1))
	if id, found := r.playerCache.Get(normalizedInput); found {
		return id, nil
	}

	// Cache miss: check database for exact or similar matches
	existingPlayers, err := r.GetAllPlayers()
	if err == nil && len(existingPlayers) > 0 {
		for _, p := range existingPlayers {
			normalizedExisting := normalizeForComparison(p.Name)

			// Exact match
			if normalizedInput == normalizedExisting {
				r.playerCache.Set(normalizedInput, p.ID)
				return p.ID, nil
			}

			// Fuzzy match (similarity)
			if similarityScore(normalizedInput, normalizedExisting) > similarityThreshold {
				r.playerCache.Set(normalizedInput, p.ID)
				return p.ID, nil
			}
		}
	}

	// Player not found - insert new player
	var id int
	err = r.db.QueryRow(`
		INSERT INTO players (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`, name).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf("failed to ensure player exists: %w", err)
	}

	// Cache the newly created player
	r.playerCache.Set(normalizedInput, id)

	return id, nil
}

// batchInsertPlayerResults inserts all player results in a single query
func (r *MatchPostgres) batchInsertPlayerResults(
	tx *sql.Tx,
	matchID int,
	players []models.PlayerResult,
	playerIDs []int,
) error {
	if len(players) == 0 {
		return nil
	}

	// Build batch INSERT query with multiple VALUES
	query := `INSERT INTO player_results 
              (match_id, player_id, player_name, result, kills, deaths, assists) 
              VALUES `

	values := make([]interface{}, 0, len(players)*7)
	placeholders := make([]string, 0, len(players))

	for i, p := range players {
		// Generate placeholders: ($1, $2, ..., $7), ($8, $9, ..., $14), ...
		offset := i * 7
		placeholders = append(placeholders,
			fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				offset+1, offset+2, offset+3, offset+4,
				offset+5, offset+6, offset+7))

		// Add values in correct order
		values = append(values,
			matchID,
			playerIDs[i],
			p.PlayerName,
			p.Result,
			p.Kills,
			p.Deaths,
			p.Assists)
	}

	// Complete query: INSERT ... VALUES (...), (...), (...)
	query += strings.Join(placeholders, ", ")

	// Execute batch insert
	_, err := tx.Exec(query, values...)
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

func (r *MatchPostgres) GetAllPlayers() ([]models.Player, error) {
	rows, err := r.db.Query("SELECT id, name FROM players WHERE is_deleted = FALSE ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("failed to get all players: %w", err)
	}
	defer rows.Close()

	var players []models.Player
	for rows.Next() {
		var p models.Player
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			continue
		}
		players = append(players, p)
	}
	return players, nil
}

func (r *MatchPostgres) GetPlayerNameByID(id int) (string, error) {
	var name string
	err := r.db.QueryRow("SELECT name FROM players WHERE id = $1 AND is_deleted = FALSE", id).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("player with ID %d not found: %w", id, err)
	}
	return name, nil
}

func (r *MatchPostgres) WipePlayerByID(id int) error {
	name, err := r.GetPlayerNameByID(id)
	if err != nil {
		return fmt.Errorf("игрок с ID %d не найден: %w", id, err)
	}

	_, err = r.db.Exec("UPDATE player_results SET is_deleted = TRUE WHERE player_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to soft delete player results: %w", err)
	}

	_, err = r.db.Exec("DELETE FROM player_resets WHERE player_name = $1", name)
	if err != nil {
		return fmt.Errorf("failed to delete player resets: %w", err)
	}

	_, err = r.db.Exec("UPDATE players SET is_deleted = TRUE, deleted_at = NOW() WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to soft delete player: %w", err)
	}

	// Invalidate cache entry for deleted player
	normalized := normalizeForComparison(name)
	r.playerCache.Delete(normalized)

	return nil
}

func (r *MatchPostgres) RestorePlayer(id int) error {
	_, err := r.db.Exec("UPDATE players SET is_deleted = FALSE, deleted_at = NULL WHERE id = $1 AND is_deleted = TRUE", id)
	if err != nil {
		return fmt.Errorf("failed to restore player: %w", err)
	}

	_, err = r.db.Exec("UPDATE player_results SET is_deleted = FALSE WHERE player_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to restore player results: %w", err)
	}

	// Re-cache the restored player
	name, err := r.GetPlayerNameByID(id)
	if err == nil {
		normalized := normalizeForComparison(name)
		r.playerCache.Set(normalized, id)
	}

	return nil
}

func (r *MatchPostgres) RenamePlayer(id int, newName string) error {
	// Get old name before renaming to invalidate old cache entry
	oldName, err := r.GetPlayerNameByID(id)
	if err != nil {
		return fmt.Errorf("failed to get player name: %w", err)
	}

	result, err := r.db.Exec("UPDATE players SET name = $1 WHERE id = $2 AND is_deleted = FALSE", newName, id)
	if err != nil {
		return fmt.Errorf("failed to rename player: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("игрок с ID %d не найден", id)
	}

	// Update cache: remove old name, add new name
	oldNormalized := normalizeForComparison(oldName)
	newNormalized := normalizeForComparison(newName)

	r.playerCache.Delete(oldNormalized)
	r.playerCache.Set(newNormalized, id)

	return nil
}
