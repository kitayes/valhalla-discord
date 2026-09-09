package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"blackwatch/internal/domain"
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
func (r *LobbyMatchPostgres) Create(ctx context.Context, req models.CreateLobbyMatchRequest) (int, error) {
	var id int
	err := r.db.QueryRowContext(ctx,
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
//
// winner, thread_id, mvp_name and svp_name are all nullable: winner stays NULL
// until a referee closes the match (and is reset to NULL on cancellation), so
// scanning it straight into a string failed for every match that was still
// running. thread_id must be selected here too — callers rely on it to archive
// the match thread once the match is over.
func (r *LobbyMatchPostgres) GetByID(ctx context.Context, id int) (*models.LobbyMatch, error) {
	var m models.LobbyMatch
	var teamA, teamB []sql.NullInt64
	var winner, threadID, mvp, svp sql.NullString

	err := r.db.QueryRowContext(ctx,
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids,
		        winner, status, thread_id, COALESCE(betting_open, FALSE),
		        mvp_name, svp_name, created_at
		 FROM lobby_matches WHERE id = $1`, id,
	).Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
		pq.Array(&teamA), pq.Array(&teamB),
		&winner, &m.Status, &threadID, &m.BettingOpen,
		&mvp, &svp, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lobby match %d: %w", id, domain.ErrMatchNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get lobby match: %w", err)
	}

	m.TeamAIDs = nullIntsToInts(teamA)
	m.TeamBIDs = nullIntsToInts(teamB)
	m.Winner = winner.String
	m.ThreadID = threadID.String
	m.MVP = mvp.String
	m.SVP = svp.String
	return &m, nil
}

// SaveMedals records the MVP/SVPG names read off the match screenshot so the
// bonus can be applied when a referee closes the match. Empty names are kept as
// NULL rather than overwriting a medal that was already recognised.
func (r *LobbyMatchPostgres) SaveMedals(ctx context.Context, matchID int, mvp, svp string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE lobby_matches
		    SET mvp_name = COALESCE(NULLIF($1, ''), mvp_name),
		        svp_name = COALESCE(NULLIF($2, ''), svp_name)
		  WHERE id = $3`,
		mvp, svp, matchID,
	)
	if err != nil {
		return fmt.Errorf("failed to save medals for match %d: %w", matchID, err)
	}
	return nil
}

// AtomicSetWinner performs an atomic state transition from ACTIVE → PROCESSING → FINISHED.
// Returns true if the transition was successful (prevents double-clicks and race conditions).
func (r *LobbyMatchPostgres) AtomicSetWinner(ctx context.Context, matchID int, winner string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Safe to call unconditionally: Rollback after a successful Commit is a no-op.
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	// Step 1: ACTIVE → PROCESSING (acquire lock)
	res, err := tx.ExecContext(ctx,
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
		return false, nil
	}

	// Step 2: Set winner + FINISHED, and shut the betting window in the same
	// transaction so no bet can slip in between closure and payout.
	_, err = tx.ExecContext(ctx,
		`UPDATE lobby_matches SET winner = $1, status = 'FINISHED', betting_open = FALSE
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
func (r *LobbyMatchPostgres) GetAllByGuild(ctx context.Context, guildID string, limit int) ([]models.LobbyMatch, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids,
		        winner, status, created_at
		 FROM lobby_matches WHERE guild_id = $1
		 ORDER BY created_at DESC LIMIT $2`, guildID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query lobby matches: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var matches []models.LobbyMatch
	for rows.Next() {
		var m models.LobbyMatch
		var teamA, teamB []sql.NullInt64
		var winner sql.NullString
		if err := rows.Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
			pq.Array(&teamA), pq.Array(&teamB),
			&winner, &m.Status, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan lobby match: %w", err)
		}
		m.TeamAIDs = nullIntsToInts(teamA)
		m.TeamBIDs = nullIntsToInts(teamB)
		m.Winner = winner.String
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// GetActiveMatchByChannel retrieves the currently ACTIVE lobby match for a guild (for button UI).
func (r *LobbyMatchPostgres) GetActiveByGuild(ctx context.Context, guildID string) (*models.LobbyMatch, error) {
	var m models.LobbyMatch
	var teamA, teamB []sql.NullInt64
	var winner, threadID sql.NullString

	err := r.db.QueryRowContext(ctx,
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids,
		        winner, status, thread_id, created_at
		 FROM lobby_matches
		 WHERE guild_id = $1 AND status = 'ACTIVE'
		 ORDER BY created_at DESC LIMIT 1`, guildID,
	).Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
		pq.Array(&teamA), pq.Array(&teamB),
		&winner, &m.Status, &threadID, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // No active match
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get active match: %w", err)
	}

	m.TeamAIDs = nullIntsToInts(teamA)
	m.TeamBIDs = nullIntsToInts(teamB)
	m.Winner = winner.String
	m.ThreadID = threadID.String
	return &m, nil
}

// GetPlayerMMRsBatch returns MMR values for a list of player IDs.
func (r *LobbyMatchPostgres) GetPlayerMMRsBatch(ctx context.Context, playerIDs []int) (map[int]int, error) {
	if len(playerIDs) == 0 {
		return make(map[int]int), nil
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, current_mmr FROM players WHERE id = ANY($1)`,
		pq.Array(playerIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query MMRs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

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
func (r *LobbyMatchPostgres) UpdatePlayerMMR(ctx context.Context, playerID, newMMR int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE players SET current_mmr = $1 WHERE id = $2`,
		newMMR, playerID,
	)
	if err != nil {
		return fmt.Errorf("failed to update MMR for player %d: %w", playerID, err)
	}
	return nil
}

// SaveThreadID updates the thread_id for an existing lobby match.
func (r *LobbyMatchPostgres) SaveThreadID(ctx context.Context, matchID int, threadID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE lobby_matches SET thread_id = $1 WHERE id = $2`,
		threadID, matchID,
	)
	if err != nil {
		return fmt.Errorf("failed to save thread_id for match %d: %w", matchID, err)
	}
	return nil
}

// GetByThreadID retrieves a lobby match by its Discord thread ID.
func (r *LobbyMatchPostgres) GetByThreadID(ctx context.Context, threadID string) (*models.LobbyMatch, error) {
	var m models.LobbyMatch
	var teamA, teamB []sql.NullInt64
	var winner, thID, mvp, svp sql.NullString

	err := r.db.QueryRowContext(ctx,
		`SELECT id, guild_id, captain_a_id, captain_b_id, team_a_ids, team_b_ids,
		        winner, status, thread_id, mvp_name, svp_name, created_at
		 FROM lobby_matches WHERE thread_id = $1
		 ORDER BY created_at DESC LIMIT 1`, threadID,
	).Scan(&m.ID, &m.GuildID, &m.CaptainAID, &m.CaptainBID,
		pq.Array(&teamA), pq.Array(&teamB),
		&winner, &m.Status, &thID, &mvp, &svp, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("thread %s: %w", threadID, domain.ErrMatchNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get match by thread_id: %w", err)
	}

	m.TeamAIDs = nullIntsToInts(teamA)
	m.TeamBIDs = nullIntsToInts(teamB)
	m.Winner = winner.String
	m.ThreadID = thID.String
	m.MVP = mvp.String
	m.SVP = svp.String
	return &m, nil
}

// GetPlayerNamesByMatchID returns the display names of all 10 players in a match.
func (r *LobbyMatchPostgres) GetPlayerNamesByMatchID(ctx context.Context, matchID int) ([]string, error) {
	match, err := r.GetByID(ctx, matchID)
	if err != nil {
		return nil, fmt.Errorf("failed to get match %d for player names: %w", matchID, err)
	}

	// Fresh slice: append(match.TeamAIDs, ...) would write into TeamAIDs' array.
	allIDs := make([]int, 0, len(match.TeamAIDs)+len(match.TeamBIDs))
	allIDs = append(allIDs, match.TeamAIDs...)
	allIDs = append(allIDs, match.TeamBIDs...)
	if len(allIDs) == 0 {
		return nil, nil
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name FROM players WHERE id = ANY($1) AND is_deleted = FALSE`,
		pq.Array(allIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query player names: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

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

// OpenBetting opens the betting window and records when it closes, so the
// deadline outlives the process that set it.
func (r *LobbyMatchPostgres) OpenBetting(ctx context.Context, matchID int, window time.Duration) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE lobby_matches
		    SET betting_open = TRUE, betting_closes_at = NOW() + make_interval(secs => $2)
		  WHERE id = $1`,
		matchID, int(window.Seconds()),
	)
	return err
}

// CloseBetting sets betting_open = false for a match.
func (r *LobbyMatchPostgres) CloseBetting(ctx context.Context, matchID int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE lobby_matches SET betting_open = FALSE, betting_closes_at = NULL WHERE id = $1`, matchID)
	return err
}

// CloseExpiredBetting shuts every window whose deadline has passed. Run at
// startup to clean up after a restart that happened mid-window, and periodically
// so a lost timer goroutine cannot leave a match open indefinitely.
//
// A NULL deadline is treated as expired — a row left behind by the code that
// predates betting_closes_at. PlaceBet reads NULL the same way, so such a row
// takes no bets in the meantime either.
func (r *LobbyMatchPostgres) CloseExpiredBetting(ctx context.Context) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE lobby_matches
		    SET betting_open = FALSE
		  WHERE betting_open
		    AND (betting_closes_at IS NULL OR betting_closes_at <= NOW())`)
	if err != nil {
		return 0, fmt.Errorf("failed to close expired betting windows: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count closed betting windows: %w", err)
	}
	return int(n), nil
}

// The standalone IsBettingOpen predicate is gone. PlaceBet evaluates the same
// condition inside its own transaction, where it is not subject to a race, and
// the only caller of the standalone version was a Telegram pre-check that used a
// second implementation ignoring betting_closes_at.

// CancelMatch sets the match status to FINISHED with no winner and returns the 10 player IDs.
func (r *LobbyMatchPostgres) CancelMatch(ctx context.Context, matchID int) ([]int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Safe to call unconditionally: Rollback after a successful Commit is a no-op.
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	res, err := tx.ExecContext(ctx,
		`UPDATE lobby_matches SET status = 'FINISHED', winner = NULL, betting_open = FALSE
		 WHERE id = $1 AND status = 'ACTIVE'`, matchID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel match: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		// Separate the two reasons the UPDATE matched nothing. "Match #7 is
		// already finished" and "there is no match #7" send the referee to
		// different places, and the caller cannot tell them apart from a single
		// combined message.
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT TRUE FROM lobby_matches WHERE id = $1`, matchID,
		).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("lobby match %d: %w", matchID, domain.ErrMatchNotFound)
		} else if err != nil {
			return nil, fmt.Errorf("failed to check match %d: %w", matchID, err)
		}
		return nil, fmt.Errorf("lobby match %d: %w", matchID, domain.ErrMatchNotActive)
	}

	// Get player IDs
	var teamA, teamB []sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT team_a_ids, team_b_ids FROM lobby_matches WHERE id = $1`, matchID,
	).Scan(pq.Array(&teamA), pq.Array(&teamB))
	if err != nil {
		return nil, fmt.Errorf("failed to get team IDs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	players := make([]int, 0, 10)
	for _, n := range teamA {
		if n.Valid {
			players = append(players, int(n.Int64))
		}
	}
	for _, n := range teamB {
		if n.Valid {
			players = append(players, int(n.Int64))
		}
	}
	return players, nil
}
