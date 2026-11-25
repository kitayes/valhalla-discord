package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/lib/pq"
)

type BetPostgres struct {
	db *sql.DB
}

func NewBetPostgres(db *sql.DB) *BetPostgres {
	return &BetPostgres{db: db}
}

// PlaceDiscordBet atomically verifies the betting window, checks roster
// membership, deducts ValCoins (and optional insurance coupon), and logs the bet.
func (r *BetPostgres) PlaceDiscordBet(ctx context.Context, req models.PlaceDiscordBetRequest) (int, int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	teamA, teamB, err := lockOpenBettingMatch(ctx, tx, req.MatchID)
	if err != nil {
		return 0, 0, err
	}

	var currentPoints, insuranceCount int
	err = tx.QueryRowContext(ctx,
		`SELECT points, bet_insurance_count
		   FROM players
		  WHERE id = $1 AND is_deleted = FALSE
		    FOR UPDATE`,
		req.PlayerID,
	).Scan(&currentPoints, &insuranceCount)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, fmt.Errorf("player %d: %w", req.PlayerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("failed to lock player for bet: %w", err)
	}

	if isRosterMember(req.PlayerID, teamA, teamB) {
		return 0, 0, fmt.Errorf("player %d in match %d: %w", req.PlayerID, req.MatchID, domain.ErrBettingOnOwnMatch)
	}
	if currentPoints < req.Amount {
		return 0, 0, fmt.Errorf("have %d, need %d: %w", currentPoints, req.Amount, domain.ErrInsufficientPoints)
	}
	if req.UseInsurance && insuranceCount <= 0 {
		return 0, 0, domain.ErrNoBetInsurance
	}

	newBalance := currentPoints - req.Amount
	if req.UseInsurance {
		_, err = tx.ExecContext(ctx,
			`UPDATE players SET points = $1, bet_insurance_count = bet_insurance_count - 1 WHERE id = $2`,
			newBalance, req.PlayerID,
		)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE players SET points = $1 WHERE id = $2`,
			newBalance, req.PlayerID,
		)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("failed to deduct coins for bet: %w", err)
	}

	var betID int
	err = tx.QueryRowContext(ctx,
		`INSERT INTO match_bets (match_id, player_id, team_chosen, amount, insured)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		req.MatchID, req.PlayerID, req.TeamChosen, req.Amount, req.UseInsurance,
	).Scan(&betID)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolation {
			return 0, 0, fmt.Errorf("match %d: %w", req.MatchID, domain.ErrAlreadyBet)
		}
		return 0, 0, fmt.Errorf("failed to insert bet: %w", err)
	}

	note := fmt.Sprintf("Ставка на %s (матч #%d)", req.TeamChosen, req.MatchID)
	if req.UseInsurance {
		note += " 🛡️"
	}
	if _, err := insertLedgerTx(ctx, tx, req.PlayerID, -req.Amount, newBalance, domain.LedgerReasonBetPlace, fmt.Sprintf("bet:%d", betID), note); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("failed to commit bet: %w", err)
	}
	return betID, newBalance, nil
}

// CancelDiscordBet cancels a player's live unsettled bet while the match's
// betting window is still open, returning the stake and any insurance coupon.
func (r *BetPostgres) CancelDiscordBet(ctx context.Context, matchID, playerID int) (int, bool, int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, 0, fmt.Errorf("failed to begin bet cancel tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	if _, _, err := lockOpenBettingMatch(ctx, tx, matchID); err != nil {
		return 0, false, 0, err
	}

	var betID, amount int
	var insured bool
	err = tx.QueryRowContext(ctx,
		`SELECT id, amount, insured
		   FROM match_bets
		  WHERE match_id = $1 AND player_id = $2 AND settled_at IS NULL
		    FOR UPDATE`,
		matchID, playerID,
	).Scan(&betID, &amount, &insured)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, 0, domain.ErrBetNotFound
	}
	if err != nil {
		return 0, false, 0, fmt.Errorf("failed to lock bet for cancel: %w", err)
	}

	insInc := 0
	if insured {
		insInc = 1
	}
	var newBalance int
	if err := tx.QueryRowContext(ctx,
		`UPDATE players
		    SET points = points + $1,
		        bet_insurance_count = bet_insurance_count + $2
		  WHERE id = $3
		  RETURNING points`,
		amount, insInc, playerID,
	).Scan(&newBalance); err != nil {
		return 0, false, 0, fmt.Errorf("failed to refund cancelled bet: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM match_bets WHERE id = $1`, betID); err != nil {
		return 0, false, 0, fmt.Errorf("failed to delete cancelled bet: %w", err)
	}

	note := fmt.Sprintf("Отмена ставки (матч #%d)", matchID)
	if _, err := insertLedgerTx(ctx, tx, playerID, amount, newBalance, domain.LedgerReasonBetCancel, fmt.Sprintf("bet:%d", betID), note); err != nil {
		return 0, false, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, false, 0, fmt.Errorf("failed to commit bet cancel: %w", err)
	}
	return amount, insured, newBalance, nil
}

// GetOpenBetForPlayer returns the caller's live unsettled bet on matchID, or nil if none.
func (r *BetPostgres) GetOpenBetForPlayer(ctx context.Context, matchID, playerID int) (*models.Bet, error) {
	var b models.Bet
	var settledAt sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT id, match_id, player_id, team_chosen, amount, insured, created_at, settled_at, payout
		   FROM match_bets
		  WHERE match_id = $1 AND player_id = $2 AND settled_at IS NULL`,
		matchID, playerID,
	).Scan(&b.ID, &b.MatchID, &b.PlayerID, &b.TeamChosen, &b.Amount, &b.Insured, &b.CreatedAt, &settledAt, &b.Payout)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query open bet for player %d match %d: %w", playerID, matchID, err)
	}
	return &b, nil
}

func lockOpenBettingMatch(ctx context.Context, tx *sql.Tx, matchID int) ([]sql.NullInt64, []sql.NullInt64, error) {
	var bettingOpen bool
	var status string
	var teamA, teamB []sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT betting_open AND betting_closes_at IS NOT NULL AND betting_closes_at > NOW(),
		        status, team_a_ids, team_b_ids
		   FROM lobby_matches WHERE id = $1 FOR SHARE`,
		matchID,
	).Scan(&bettingOpen, &status, pq.Array(&teamA), pq.Array(&teamB))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("match %d: %w", matchID, domain.ErrMatchNotFound)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check betting window: %w", err)
	}
	if !bettingOpen || string(models.LobbyMatchStatusActive) != status {
		return nil, nil, fmt.Errorf("match %d: %w", matchID, domain.ErrBettingClosed)
	}
	return teamA, teamB, nil
}

// GetBetsByMatch retrieves all bets for a given match ID, settled ones included.
func (r *BetPostgres) GetBetsByMatch(ctx context.Context, matchID int) ([]models.Bet, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, match_id, player_id, team_chosen, amount, insured, created_at, settled_at, payout
		 FROM match_bets WHERE match_id = $1 ORDER BY id`, matchID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query bets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var bets []models.Bet
	for rows.Next() {
		b, err := scanBet(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan bet: %w", err)
		}
		bets = append(bets, b)
	}
	return bets, rows.Err()
}

// BetPool sums the live bets of a match by side.
func (r *BetPostgres) BetPool(ctx context.Context, matchID int) (models.BetPool, error) {
	pool := models.BetPool{MatchID: matchID}

	rows, err := r.db.QueryContext(ctx,
		`SELECT team_chosen, COALESCE(SUM(amount), 0), COUNT(*)
		   FROM match_bets
		  WHERE match_id = $1 AND settled_at IS NULL
		  GROUP BY team_chosen`, matchID)
	if err != nil {
		return pool, fmt.Errorf("failed to query bet pool: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	for rows.Next() {
		var team string
		var amount, count int
		if err := rows.Scan(&team, &amount, &count); err != nil {
			return models.BetPool{}, fmt.Errorf("failed to scan bet pool: %w", err)
		}
		switch team {
		case domain.TeamA:
			pool.AmountA, pool.CountA = amount, count
		case domain.TeamB:
			pool.AmountB, pool.CountB = amount, count
		}
	}
	if err := rows.Err(); err != nil {
		return models.BetPool{}, fmt.Errorf("failed to read bet pool: %w", err)
	}
	return pool, nil
}

// PendingSettlements lists matches that are over but still hold live bets.
func (r *BetPostgres) PendingSettlements(ctx context.Context) ([]models.PendingSettlement, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT m.id, m.winner
		   FROM match_bets b
		   JOIN lobby_matches m ON m.id = b.match_id
		  WHERE b.settled_at IS NULL
		    AND m.status = 'FINISHED'
		  ORDER BY m.id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending settlements: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var pending []models.PendingSettlement
	for rows.Next() {
		var p models.PendingSettlement
		var winner sql.NullString
		if err := rows.Scan(&p.MatchID, &winner); err != nil {
			return nil, fmt.Errorf("failed to scan pending settlement: %w", err)
		}
		p.Winner = winner.String
		p.Cancelled = !winner.Valid || winner.String == ""
		pending = append(pending, p)
	}
	return pending, rows.Err()
}

// PayoutWinnersDetailed settles all unsettled bets for a match, distributes the
// parimutuel pool to winners, applies 50% insurance refunds to losing insured
// bets, and records every credit in currency_ledger.
func (r *BetPostgres) PayoutWinnersDetailed(ctx context.Context, matchID int, winningTeam string) (*models.PayoutSummary, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	bets, err := r.lockUnsettledBets(ctx, tx, matchID)
	if err != nil {
		return nil, fmt.Errorf("failed to get bets: %w", err)
	}

	summary := &models.PayoutSummary{
		PlayerPayouts:    make(map[int]int),
		InsuranceRefunds: make(map[int]int),
		BetCount:         len(bets),
	}
	if len(bets) == 0 {
		return summary, tx.Commit()
	}

	for _, b := range bets {
		summary.TotalPool += b.Amount
	}

	shares := calculatePayoutShares(bets, winningTeam)

	type creditEntry struct {
		betID    int
		playerID int
		amount   int
		reason   string
		note     string
	}
	var credits []creditEntry
	betPayouts := make(map[int]int, len(bets))

	for _, b := range bets {
		if share := shares[b.ID]; share > 0 {
			betPayouts[b.ID] = share
			summary.PlayerPayouts[b.PlayerID] += share
			credits = append(credits, creditEntry{
				betID:    b.ID,
				playerID: b.PlayerID,
				amount:   share,
				reason:   domain.LedgerReasonBetWin,
				note:     fmt.Sprintf("Выигрыш ставки (матч #%d, %s)", matchID, winningTeam),
			})
		} else if b.Insured && b.TeamChosen != winningTeam {
			refund := domain.BetInsuranceRefund(b.Amount)
			if refund > 0 {
				betPayouts[b.ID] = refund
				summary.InsuranceRefunds[b.PlayerID] += refund
				credits = append(credits, creditEntry{
					betID:    b.ID,
					playerID: b.PlayerID,
					amount:   refund,
					reason:   domain.LedgerReasonBetInsurance,
					note:     fmt.Sprintf("Страховка ставки 50%% (матч #%d)", matchID),
				})
			}
		}
	}

	// Sort credits in ascending player_id order so concurrent settlements never deadlock.
	sort.Slice(credits, func(i, j int) bool {
		if credits[i].playerID != credits[j].playerID {
			return credits[i].playerID < credits[j].playerID
		}
		return credits[i].betID < credits[j].betID
	})

	for _, c := range credits {
		if err := creditBetOwner(ctx, tx, c.playerID, c.amount, false, c.reason, fmt.Sprintf("bet:%d", c.betID), c.note); err != nil {
			return nil, err
		}
	}

	for _, b := range bets {
		if _, err := tx.ExecContext(ctx,
			`UPDATE match_bets SET settled_at = NOW(), payout = $1 WHERE id = $2`,
			betPayouts[b.ID], b.ID,
		); err != nil {
			return nil, fmt.Errorf("failed to settle bet %d: %w", b.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit payout: %w", err)
	}
	return summary, nil
}

func creditBetOwner(ctx context.Context, tx *sql.Tx, playerID, amount int, restoreInsurance bool, reason, refKey, note string) error {
	if amount <= 0 && !restoreInsurance {
		return nil
	}

	insInc := 0
	if restoreInsurance {
		insInc = 1
	}

	var newBalance int
	err := tx.QueryRowContext(ctx,
		`UPDATE players
		    SET points = points + $1,
		        bet_insurance_count = bet_insurance_count + $2
		  WHERE id = $3
		  RETURNING points`,
		amount, insInc, playerID,
	).Scan(&newBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("cannot credit %d points to player %d: %w", amount, playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return fmt.Errorf("failed to credit player %d: %w", playerID, err)
	}

	if amount > 0 {
		if _, err := insertLedgerTx(ctx, tx, playerID, amount, newBalance, reason, refKey, note); err != nil {
			return err
		}
	}
	return nil
}

func isRosterMember(playerID int, teams ...[]sql.NullInt64) bool {
	for _, team := range teams {
		for _, id := range team {
			if id.Valid && int(id.Int64) == playerID {
				return true
			}
		}
	}
	return false
}

func calculatePayoutShares(bets []models.Bet, winningTeam string) map[int]int {
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

	shares := make(map[int]int, len(bets))
	if len(winners) == 0 || winningPool == 0 {
		return shares
	}

	distributed := 0
	type remainder struct {
		betID int
		rem   int
	}
	remainders := make([]remainder, 0, len(winners))

	for _, w := range winners {
		gross := w.Amount * totalPool
		share := gross / winningPool
		shares[w.ID] = share
		distributed += share
		remainders = append(remainders, remainder{betID: w.ID, rem: gross % winningPool})
	}

	leftover := totalPool - distributed
	if leftover <= 0 {
		return shares
	}

	sort.Slice(remainders, func(i, j int) bool {
		if remainders[i].rem != remainders[j].rem {
			return remainders[i].rem > remainders[j].rem
		}
		return remainders[i].betID < remainders[j].betID
	})

	for i := 0; i < leftover && i < len(remainders); i++ {
		shares[remainders[i].betID]++
	}
	return shares
}

func (r *BetPostgres) lockUnsettledBets(ctx context.Context, tx *sql.Tx, matchID int) ([]models.Bet, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, match_id, player_id, team_chosen, amount, insured, created_at, settled_at, payout
		 FROM match_bets WHERE match_id = $1 AND settled_at IS NULL
		 ORDER BY id FOR UPDATE`, matchID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var bets []models.Bet
	for rows.Next() {
		b, err := scanBet(rows)
		if err != nil {
			return nil, err
		}
		bets = append(bets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bets, rows.Close()
}

func scanBet(rows *sql.Rows) (models.Bet, error) {
	var b models.Bet
	var settledAt sql.NullTime
	if err := rows.Scan(&b.ID, &b.MatchID, &b.PlayerID, &b.TeamChosen,
		&b.Amount, &b.Insured, &b.CreatedAt, &settledAt, &b.Payout); err != nil {
		return models.Bet{}, err
	}
	if settledAt.Valid {
		t := settledAt.Time
		b.SettledAt = &t
	}
	return b, nil
}

// GetPlayerPointsByID returns the current points for a player by internal player ID.
func (r *BetPostgres) GetPlayerPointsByID(ctx context.Context, playerID int) (int, error) {
	var points int
	err := r.db.QueryRowContext(ctx,
		`SELECT points FROM players WHERE id = $1 AND is_deleted = FALSE`,
		playerID,
	).Scan(&points)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get player points: %w", err)
	}
	return points, nil
}

// RefundAllBets returns every live bet of a match to its owner (restoring any
// consumed insurance coupon as well) and marks the bets settled.
func (r *BetPostgres) RefundAllBets(ctx context.Context, matchID int) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	bets, err := r.lockUnsettledBets(ctx, tx, matchID)
	if err != nil {
		return 0, fmt.Errorf("failed to lock bets: %w", err)
	}
	if len(bets) == 0 {
		return 0, tx.Commit()
	}

	sort.Slice(bets, func(i, j int) bool {
		if bets[i].PlayerID != bets[j].PlayerID {
			return bets[i].PlayerID < bets[j].PlayerID
		}
		return bets[i].ID < bets[j].ID
	})

	for _, b := range bets {
		note := fmt.Sprintf("Возврат ставки (матч #%d отменён)", matchID)
		if err := creditBetOwner(ctx, tx, b.PlayerID, b.Amount, b.Insured, domain.LedgerReasonBetRefund, fmt.Sprintf("bet:%d", b.ID), note); err != nil {
			return 0, err
		}
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE match_bets SET settled_at = NOW(), payout = amount
		  WHERE match_id = $1 AND settled_at IS NULL`, matchID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to mark bets refunded: %w", err)
	}
	refunded, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count refunded bets: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit refund: %w", err)
	}
	return int(refunded), nil
}
