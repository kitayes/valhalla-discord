package repository

import (
	"database/sql"
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
		return 0, err
	}

	var matchID int
	query := "INSERT INTO matches (file_hash, match_signature) VALUES ($1, $2) RETURNING id"
	err = tx.QueryRow(query, match.FileHash, match.MatchSignature).Scan(&matchID)
	if err != nil {
		tx.Rollback()
		return 0, err
	}

	for _, p := range match.Players {
		pQuery := `INSERT INTO player_results (match_id, player_name, result, kills, deaths, assists, champion) 
		           VALUES ($1, $2, $3, $4, $5, $6, $7)`
		_, err := tx.Exec(pQuery, matchID, p.PlayerName, p.Result, p.Kills, p.Deaths, p.Assists, p.Champion)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
	}
	return matchID, tx.Commit()
}

func (r *MatchPostgres) Exists(fileHash, matchSignature string) (bool, error) {
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM matches WHERE file_hash=$1 OR match_signature=$2)"
	err := r.db.QueryRow(query, fileHash, matchSignature).Scan(&exists)
	return exists, err
}

func (r *MatchPostgres) GetAllAfter(date time.Time) ([]models.Match, error) {
	query := `
		SELECT m.id, m.created_at, 
		       p.match_id, p.player_name, p.result, p.kills, p.deaths, p.assists, p.champion
		FROM matches m
		JOIN player_results p ON m.id = p.match_id
		WHERE m.created_at >= $1
		ORDER BY m.created_at DESC
	`

	rows, err := r.db.Query(query, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := []models.Match{}
	matchesMap := make(map[int]*models.Match)

	for rows.Next() {
		var mID int
		var mDate time.Time
		var p models.PlayerResult

		if err := rows.Scan(&mID, &mDate, &p.MatchID, &p.PlayerName, &p.Result, &p.Kills, &p.Deaths, &p.Assists, &p.Champion); err != nil {
			continue
		}

		if _, exists := matchesMap[mID]; !exists {
			newMatch := models.Match{
				ID:        mID,
				CreatedAt: mDate,
				Players:   []models.PlayerResult{},
			}
			matches = append(matches, newMatch)
			matchesMap[mID] = &matches[len(matches)-1]
		}

		matchesMap[mID].Players = append(matchesMap[mID].Players, p)
	}

	return matches, nil
}

func (r *MatchPostgres) SetSeasonStartDate(date time.Time) error {
	_, err := r.db.Exec(`
		INSERT INTO bot_settings (key, value) VALUES ('season_start_date', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, date.Format(time.RFC3339))
	return err
}

func (r *MatchPostgres) GetSeasonStartDate() (time.Time, error) {
	var dateStr string
	err := r.db.QueryRow("SELECT value FROM bot_settings WHERE key = 'season_start_date'").Scan(&dateStr)
	if err != nil {
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), nil
	}
	return time.Parse(time.RFC3339, dateStr)
}

func (r *MatchPostgres) SetPlayerResetDate(playerName string, date time.Time) error {
	_, err := r.db.Exec(`
		INSERT INTO player_resets (player_name, reset_date) VALUES ($1, $2)
		ON CONFLICT (player_name) DO UPDATE SET reset_date = EXCLUDED.reset_date
	`, playerName, date)
	return err
}

func (r *MatchPostgres) GetPlayerResetDates() (map[string]time.Time, error) {
	rows, err := r.db.Query("SELECT player_name, reset_date FROM player_resets")
	if err != nil {
		return nil, err
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
