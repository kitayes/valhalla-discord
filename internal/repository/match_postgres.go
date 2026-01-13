package repository

import (
	"database/sql"
	"fmt"
	"time"
	"valhalla/internal/models"
)

type MatchPostgres struct {
	db *sql.DB
}

func NewMatchPostgres(db *sql.DB) *MatchPostgres {
	return &MatchPostgres{db: db}
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

	for _, p := range match.Players {
		if err := r.EnsurePlayerExists(p.PlayerName); err != nil {
			return 0, fmt.Errorf("failed to ensure player exists: %w", err)
		}

		pQuery := `INSERT INTO player_results (match_id, player_name, result, kills, deaths, assists) 
                   VALUES ($1, $2, $3, $4, $5, $6)`
		_, err = tx.Exec(pQuery, matchID, p.PlayerName, p.Result, p.Kills, p.Deaths, p.Assists)
		if err != nil {
			return 0, fmt.Errorf("failed to insert player result: %w", err)
		}
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
		SELECT m.id, m.created_at, pr.player_name, pr.result, pr.kills, pr.deaths, pr.assists
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
		if err := rows.Scan(&id, &createdAt, &pr.PlayerName, &pr.Result, &pr.Kills, &pr.Deaths, &pr.Assists); err != nil {
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
		return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), nil
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

func (r *MatchPostgres) GetHistory(playerName string, limit int) ([]models.Match, error) {
	query := `
		SELECT m.id, m.created_at, pr.result, pr.kills, pr.deaths, pr.assists
		FROM matches m
		JOIN player_results pr ON m.id = pr.match_id
		WHERE pr.player_name = $1 AND m.is_deleted = FALSE AND pr.is_deleted = FALSE
		ORDER BY m.created_at DESC
		LIMIT $2
	`
	rows, err := r.db.Query(query, playerName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get player history: %w", err)
	}
	defer rows.Close()

	var matches []models.Match
	for rows.Next() {
		var m models.Match
		var pr models.PlayerResult
		err := rows.Scan(&m.ID, &m.CreatedAt, &pr.Result, &pr.Kills, &pr.Deaths, &pr.Assists)
		if err != nil {
			continue
		}
		pr.PlayerName = playerName
		m.Players = []models.PlayerResult{pr}
		matches = append(matches, m)
	}
	return matches, nil
}

func (r *MatchPostgres) EnsurePlayerExists(name string) error {
	_, err := r.db.Exec(`
        INSERT INTO players (name) VALUES ($1)
        ON CONFLICT (name) DO NOTHING`, name)
	if err != nil {
		return fmt.Errorf("failed to ensure player exists: %w", err)
	}
	return nil
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

	_, err = r.db.Exec("UPDATE player_results SET is_deleted = TRUE WHERE player_name = $1", name)
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
	return nil
}

func (r *MatchPostgres) RestorePlayer(id int) error {
	_, err := r.db.Exec("UPDATE players SET is_deleted = FALSE, deleted_at = NULL WHERE id = $1 AND is_deleted = TRUE", id)
	if err != nil {
		return fmt.Errorf("failed to restore player: %w", err)
	}

	var name string
	err = r.db.QueryRow("SELECT name FROM players WHERE id = $1", id).Scan(&name)
	if err != nil {
		return fmt.Errorf("failed to get player name: %w", err)
	}

	_, err = r.db.Exec("UPDATE player_results SET is_deleted = FALSE WHERE player_name = $1", name)
	if err != nil {
		return fmt.Errorf("failed to restore player results: %w", err)
	}
	return nil
}
