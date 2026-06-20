package repository

import (
	"database/sql"
	"fmt"

	"blackwatch/internal/models"
)

type BetPostgres struct {
	db *sql.DB
}

func NewBetPostgres(db *sql.DB) *BetPostgres {
	return &BetPostgres{db: db}
}

// PlaceBet atomically deducts points from the player and logs the bet.
func (r *BetPostgres) PlaceBet(req models.PlaceBetRequest) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Verify player exists and has enough points
	var currentPoints int
	err = tx.QueryRow(
		`SELECT points FROM players WHERE tg_id = $1 FOR UPDATE`,
		req.TgUserID,
	).Scan(&currentPoints)
	if err == sql.ErrNoRows {
		return fmt.Errorf("Telegram user %d not linked to any player", req.TgUserID)
	}
	if err != nil {
		return fmt.Errorf("failed to get player points: %w", err)
	}

	if currentPoints < req.Amount {
		return fmt.Errorf("insufficient points: have %d, need %d", currentPoints, req.Amount)
	}

	// Deduct points
	_, err = tx.Exec(
		`UPDATE players SET points = points - $1 WHERE tg_id = $2`,
		req.Amount, req.TgUserID,
	)
	if err != nil {
		return fmt.Errorf("failed to deduct points: %w", err)
	}

	// Log the bet
	_, err = tx.Exec(
		`INSERT INTO match_bets (match_id, tg_user_id, team_chosen, amount)
		 VALUES ($1, $2, $3, $4)`,
		req.MatchID, req.TgUserID, req.TeamChosen, req.Amount,
	)
	if err != nil {
		return fmt.Errorf("failed to insert bet: %w", err)
	}

	return tx.Commit()
}

// GetBetsByMatch retrieves all bets for a given match ID.
func (r *BetPostgres) GetBetsByMatch(matchID int) ([]models.Bet, error) {
	rows, err := r.db.Query(
		`SELECT id, match_id, tg_user_id, team_chosen, amount, created_at
		 FROM match_bets WHERE match_id = $1`, matchID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query bets: %w", err)
	}
	defer rows.Close()

	var bets []models.Bet
	for rows.Next() {
		var b models.Bet
		if err := rows.Scan(&b.ID, &b.MatchID, &b.TgUserID, &b.TeamChosen, &b.Amount, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan bet: %w", err)
		}
		bets = append(bets, b)
	}
	return bets, rows.Err()
}

// PayoutWinners credits winning bettors with their winnings.
// winningTeam is "Team A" or "Team B".
// Returns the list of winning bet user IDs and their payout amounts.
func (r *BetPostgres) PayoutWinners(matchID int, winningTeam string) (map[int64]int, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Get all bets for this match
	bets, err := r.getBetsByMatchTx(tx, matchID)
	if err != nil {
		return nil, fmt.Errorf("failed to get bets: %w", err)
	}

	if len(bets) == 0 {
		return nil, tx.Commit() // No bets to pay out
	}

	// Calculate total pool and winning pool
	totalPool := 0
	winningPool := 0
	var winners []models.Bet

	for _, b := range bets {
		totalPool += b.Amount
		if b.TeamChosen == winningTeam {
			winningPool += b.Amount
			winners = append(winners, b)
		}
	}

	payouts := make(map[int64]int)

	if len(winners) == 0 {
		// No winners — return nothing (losing pool stays consumed)
		return payouts, tx.Commit()
	}

	// Distribute winning pool proportionally
	for _, w := range winners {
		var share int
		if winningPool > 0 {
			share = (w.Amount * totalPool) / winningPool
		} else {
			share = w.Amount // Fallback: return original bet
		}

		// Credit points back to winner
		_, err := tx.Exec(
			`UPDATE players SET points = points + $1 WHERE tg_id = $2`,
			share, w.TgUserID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to credit winner %d: %w", w.TgUserID, err)
		}

		payouts[w.TgUserID] = share
	}

	return payouts, tx.Commit()
}

// getBetsByMatchTx is an internal transaction-aware variant.
func (r *BetPostgres) getBetsByMatchTx(tx *sql.Tx, matchID int) ([]models.Bet, error) {
	rows, err := tx.Query(
		`SELECT id, match_id, tg_user_id, team_chosen, amount, created_at
		 FROM match_bets WHERE match_id = $1
		 ORDER BY created_at`, matchID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bets []models.Bet
	for rows.Next() {
		var b models.Bet
		if err := rows.Scan(&b.ID, &b.MatchID, &b.TgUserID, &b.TeamChosen, &b.Amount, &b.CreatedAt); err != nil {
			return nil, err
		}
		bets = append(bets, b)
	}
	return bets, rows.Err()
}

// GetPlayerPoints returns the current points for a player by Telegram ID.
func (r *BetPostgres) GetPlayerPoints(tgUserID int64) (int, error) {
	var points int
	err := r.db.QueryRow(
		`SELECT points FROM players WHERE tg_id = $1`,
		tgUserID,
	).Scan(&points)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("Telegram user %d not linked to any player", tgUserID)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get player points: %w", err)
	}
	return points, nil
}
