package repository

import (
	"database/sql"
	"fmt"
	"strings"

	"blackwatch/internal/models"

	"github.com/lib/pq"
)

type LobbyMatchPostgres struct {
	db *sql.DB
}

func NewLobbyMatchPostgres(db *sql.DB) *LobbyMatchPostgres {
	return &LobbyMatchPostgres{db: db}
}

// Create inserts a new lobby match and returns its ID.
func (r *LobbyMatchPostgres) Create(req models.CreateLobbyMatchRequest) (int, error) {
	var id int
	err := r.db.QueryRow(
		`INSERT INTO lobby_matches (guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		req.GuildID,
		req.CaptainAID,
		req.CaptainBID,
		pq.Array(req.TeamAIDs),
		pq.Array(req.TeamBIDs),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to insert lobby match: %w", err)
	}
	return id, nil
}

// GetByID retrieves a lobby match by its ID.
func (r *LobbyMatchPostgres) GetByID(id int) (*models.LobbyMatch, error) {
	var m models.LobbyMatch
	var teamA, teamB []sql.NullInt64

	err := r.db.QueryRow(
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids, winner, status, created_at
		 FROM lobby_matches WHERE id = $1`, id,
	).Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
		pq.Array(&teamA), pq.Array(&teamB),
		&m.Winner, &m.Status, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("lobby match %d not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get lobby match: %w", err)
	}

	m.TeamAIDs = nullIntsToInts(teamA)
	m.TeamBIDs = nullIntsToInts(teamB)
	return &m, nil
}

// AtomicSetWinner performs an atomic state transition from ACTIVE → PROCESSING → FINISHED.
// Returns true if the transition was successful (prevents double-clicks and race conditions).
func (r *LobbyMatchPostgres) AtomicSetWinner(matchID int, winner string) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Step 1: ACTIVE → PROCESSING (acquire lock)
	res, err := tx.Exec(
		`UPDATE lobby_matches SET status = 'PROCESSING'
		 WHERE id = $1 AND status = 'ACTIVE'`,
		matchID,
	)
	if err != nil {
		return false, fmt.Errorf("failed to acquire processing lock: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		// Already being processed or finished — silently reject duplicate
		return false, tx.Rollback()
	}

	// Step 2: Set winner + FINISHED
	_, err = tx.Exec(
		`UPDATE lobby_matches SET winner = $1, status = 'FINISHED'
		 WHERE id = $2 AND status = 'PROCESSING'`,
		winner, matchID,
	)
	if err != nil {
		return false, fmt.Errorf("failed to finalize match: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit: %w", err)
	}

	return true, nil
}

// GetAllByGuild retrieves all matches for a guild, ordered by created_at DESC.
func (r *LobbyMatchPostgres) GetAllByGuild(guildID string, limit int) ([]models.LobbyMatch, error) {
	rows, err := r.db.Query(
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids,
		        winner, status, created_at
		 FROM lobby_matches WHERE guild_id = $1
		 ORDER BY created_at DESC LIMIT $2`, guildID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query lobby matches: %w", err)
	}
	defer rows.Close()

	var matches []models.LobbyMatch
	for rows.Next() {
		var m models.LobbyMatch
		var teamA, teamB []sql.NullInt64
		if err := rows.Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
			pq.Array(&teamA), pq.Array(&teamB),
			&m.Winner, &m.Status, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan lobby match: %w", err)
		}
		m.TeamAIDs = nullIntsToInts(teamA)
		m.TeamBIDs = nullIntsToInts(teamB)
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// GetActiveMatchByChannel retrieves the currently ACTIVE lobby match for a guild (for button UI).
func (r *LobbyMatchPostgres) GetActiveByGuild(guildID string) (*models.LobbyMatch, error) {
	var m models.LobbyMatch
	var teamA, teamB []sql.NullInt64

	err := r.db.QueryRow(
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids, winner, status, created_at
		 FROM lobby_matches
		 WHERE guild_id = $1 AND status = 'ACTIVE'
		 ORDER BY created_at DESC LIMIT 1`, guildID,
	).Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
		pq.Array(&teamA), pq.Array(&teamB),
		&m.Winner, &m.Status, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // No active match
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get active match: %w", err)
	}

	m.TeamAIDs = nullIntsToInts(teamA)
	m.TeamBIDs = nullIntsToInts(teamB)
	return &m, nil
}

// GetPlayerMMRsBatch returns MMR values for a list of player IDs.
func (r *LobbyMatchPostgres) GetPlayerMMRsBatch(playerIDs []int) (map[int]int, error) {
	if len(playerIDs) == 0 {
		return make(map[int]int), nil
	}

	// Build IN clause with placeholders
	placeholders := make([]string, len(playerIDs))
	args := make([]interface{}, len(playerIDs))
	for i, id := range playerIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT id, current_mmr FROM players WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query MMRs: %w", err)
	}
	defer rows.Close()

	result := make(map[int]int)
	for rows.Next() {
		var id, mmr int
		if err := rows.Scan(&id, &mmr); err != nil {
			return nil, fmt.Errorf("failed to scan MMR: %w", err)
		}
		result[id] = mmr
	}
	return result, rows.Err()
}

// UpdatePlayerMMR updates a single player's MMR in the database.
func (r *LobbyMatchPostgres) UpdatePlayerMMR(playerID, newMMR int) error {
	_, err := r.db.Exec(
		`UPDATE players SET current_mmr = $1 WHERE id = $2`,
		newMMR, playerID,
	)
	if err != nil {
		return fmt.Errorf("failed to update MMR for player %d: %w", playerID, err)
	}
	return nil
}

// SaveThreadID updates the thread_id for an existing lobby match.
func (r *LobbyMatchPostgres) SaveThreadID(matchID int, threadID string) error {
	_, err := r.db.Exec(
		`UPDATE lobby_matches SET thread_id = $1 WHERE id = $2`,
		threadID, matchID,
	)
	if err != nil {
		return fmt.Errorf("failed to save thread_id for match %d: %w", matchID, err)
	}
	return nil
}

// GetByThreadID retrieves a lobby match by its Discord thread ID.
func (r *LobbyMatchPostgres) GetByThreadID(threadID string) (*models.LobbyMatch, error) {
	var m models.LobbyMatch
	var teamA, teamB []sql.NullInt64
	var thID sql.NullString

	err := r.db.QueryRow(
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids,
		        winner, status, thread_id, created_at
		 FROM lobby_matches WHERE thread_id = $1
		 ORDER BY created_at DESC LIMIT 1`, threadID,
	).Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
		pq.Array(&teamA), pq.Array(&teamB),
		&m.Winner, &m.Status, &thID, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("match not found for thread %s", threadID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get match by thread_id: %w", err)
	}

	m.TeamAIDs = nullIntsToInts(teamA)
	m.TeamBIDs = nullIntsToInts(teamB)
	if thID.Valid {
		m.ThreadID = thID.String
	}
	return &m, nil
}

// GetPlayerNamesByMatchID returns the display names of all 10 players in a match.
func (r *LobbyMatchPostgres) GetPlayerNamesByMatchID(matchID int) ([]string, error) {
	match, err := r.GetByID(matchID)
	if err != nil {
		return nil, fmt.Errorf("failed to get match %d for player names: %w", matchID, err)
	}

	allIDs := append(match.TeamAIDs, match.TeamBIDs...)
	placeholders := make([]string, len(allIDs))
	args := make([]interface{}, len(allIDs))
	for i, id := range allIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT id, name FROM players WHERE id IN (%s) AND is_deleted = FALSE`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query player names: %w", err)
	}
	defer rows.Close()

	idToName := make(map[int]string)
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		idToName[id] = name
	}

	// Preserve order: Team A first, then Team B
	var names []string
	for _, id := range match.TeamAIDs {
		if name, ok := idToName[id]; ok {
			names = append(names, name)
		}
	}
	for _, id := range match.TeamBIDs {
		if name, ok := idToName[id]; ok {
			names = append(names, name)
		}
	}
	return names, rows.Err()
}

// nullIntsToInts converts a slice of sql.NullInt64 to []int.
func nullIntsToInts(nulls []sql.NullInt64) []int {
	result := make([]int, 0, len(nulls))
	for _, n := range nulls {
		if n.Valid {
			result = append(result, int(n.Int64))
		}
	}
	return result
}

// OpenBetting sets betting_open = true for a match.
func (r *LobbyMatchPostgres) OpenBetting(matchID int) error {
	_, err := r.db.Exec(`UPDATE lobby_matches SET betting_open = TRUE WHERE id = $1`, matchID)
	return err
}

// CloseBetting sets betting_open = false for a match.
func (r *LobbyMatchPostgres) CloseBetting(matchID int) error {
	_, err := r.db.Exec(`UPDATE lobby_matches SET betting_open = FALSE WHERE id = $1`, matchID)
	return err
}

// IsBettingOpen checks if betting is still open for a match.
func (r *LobbyMatchPostgres) IsBettingOpen(matchID int) (bool, error) {
	var open bool
	err := r.db.QueryRow(`SELECT betting_open FROM lobby_matches WHERE id = $1`, matchID).Scan(&open)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("match %d not found", matchID)
	}
	return open, err
}
