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

// PlaceBet atomically verifies the betting window, deducts points and logs the bet.
//
// The FOR SHARE on lobby_matches is what orders this against settlement: closing
// a match updates that row, so AtomicSetWinner waits for every in-flight bet and
// every bet started afterwards reads the closed status and is refused.
func (r *BetPostgres) PlaceBet(ctx context.Context, req models.PlaceBetRequest) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Safe to call unconditionally: Rollback after a successful Commit is a no-op.
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	// The window can close between the caller's check and this transaction.
	// The stored deadline is checked alongside the flag: after a restart the
	// flag can still read TRUE with no timer left to clear it, which would let
	// bets land on a match whose result is already known.
	//
	// A NULL deadline counts as closed, the same reading CloseExpiredBetting
	// uses. The two used to disagree — this side treated NULL as "open forever",
	// the sweeper as "expired" — so a row left open by the pre-deadline code took
	// bets until the next sweep happened to run.
	// The roster comes back with the window because the bettor must not be
	// playing in this match — see the membership check below.
	var bettingOpen bool
	var status string
	var teamA, teamB []sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT betting_open AND betting_closes_at IS NOT NULL AND betting_closes_at > NOW(),
		        status, team_a_ids, team_b_ids
		   FROM lobby_matches WHERE id = $1 FOR SHARE`,
		req.MatchID,
	).Scan(&bettingOpen, &status, pq.Array(&teamA), pq.Array(&teamB))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("match %d: %w", req.MatchID, domain.ErrMatchNotFound)
	}
	if err != nil {
		return fmt.Errorf("failed to check betting window: %w", err)
	}
	if !bettingOpen || string(models.LobbyMatchStatusActive) != status {
		return fmt.Errorf("match %d: %w", req.MatchID, domain.ErrBettingClosed)
	}

	// The player id comes back with the balance, so the membership check below
	// costs no extra query. Reading players after lobby_matches also keeps the
	// lock order the settlement paths use.
	var playerID, currentPoints int
	err = tx.QueryRowContext(ctx,
		`SELECT id, points FROM players WHERE tg_id = $1 FOR UPDATE`,
		req.TgUserID,
	).Scan(&playerID, &currentPoints)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("telegram user %d: %w", req.TgUserID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return fmt.Errorf("failed to get player points: %w", err)
	}

	// A participant staking on their own match can back the other side and then
	// throw the game, which is the whole of match fixing in one move. The same
	// people play and bet here, so nothing but this check stands in the way.
	if isRosterMember(playerID, teamA, teamB) {
		return fmt.Errorf("player %d in match %d: %w", playerID, req.MatchID, domain.ErrBettingOnOwnMatch)
	}

	if currentPoints < req.Amount {
		return fmt.Errorf("have %d, need %d: %w", currentPoints, req.Amount, domain.ErrInsufficientPoints)
	}

	// Deduct points
	_, err = tx.ExecContext(ctx,
		`UPDATE players SET points = points - $1 WHERE tg_id = $2`,
		req.Amount, req.TgUserID,
	)
	if err != nil {
		return fmt.Errorf("failed to deduct points: %w", err)
	}

	// Log the bet. "One bet per user per match" is enforced by the partial
	// unique index on (match_id, tg_user_id) WHERE settled_at IS NULL, not by a
	// prior SELECT: a COUNT(*) read here takes no lock on rows that do not exist
	// yet, so two concurrent taps both saw zero and both inserted — the bettor
	// ended up backing both teams and could not lose.
	_, err = tx.ExecContext(ctx,
		`INSERT INTO match_bets (match_id, tg_user_id, team_chosen, amount)
		 VALUES ($1, $2, $3, $4)`,
		req.MatchID, req.TgUserID, req.TeamChosen, req.Amount,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgUniqueViolation {
			return fmt.Errorf("match %d: %w", req.MatchID, domain.ErrAlreadyBet)
		}
		return fmt.Errorf("failed to insert bet: %w", err)
	}

	return tx.Commit()
}

// GetBetsByMatch retrieves all bets for a given match ID, settled ones included.
func (r *BetPostgres) GetBetsByMatch(ctx context.Context, matchID int) ([]models.Bet, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, match_id, tg_user_id, team_chosen, amount, created_at, settled_at, payout
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
//
// Only unsettled rows are counted, which is the same set PayoutWinners will
// distribute — a settled match must not keep advertising a market. A side with
// no bets is absent from the grouping and stays zero, which BetPool.Odds reads
// as "no coefficient yet".
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
//
// Settlement is driven from a Discord interaction, and any single step of that
// path can drop it: the rating update fails and the handler returns before the
// payout is scheduled, the payout goroutine loses its database connection, or
// the process is restarted between cancelling a match and refunding it. In every
// one of those cases the match is already FINISHED, so neither the WIN button
// nor /cancel_match can be replayed to finish the job — the points stayed
// deducted with nothing left to move them.
//
// Winner is invalid for a cancelled match, which is the caller's cue to refund
// rather than pay out.
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

// PayoutWinners credits winning bettors with their share of the pool.
// winningTeam is "Team A" or "Team B".
//
// Only unsettled bets are considered, so calling it twice for the same match
// pays out once. Returns tgUserID -> total points credited.
func (r *BetPostgres) PayoutWinners(ctx context.Context, matchID int, winningTeam string) (map[int64]int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	bets, err := r.lockUnsettledBets(ctx, tx, matchID)
	if err != nil {
		return nil, fmt.Errorf("failed to get bets: %w", err)
	}

	payouts := make(map[int64]int)
	if len(bets) == 0 {
		// Nothing live to settle — either no bets at all, or already paid out.
		return payouts, tx.Commit()
	}

	shares := calculatePayoutShares(bets, winningTeam)

	for _, b := range bets {
		if share := shares[b.ID]; share > 0 {
			payouts[b.TgUserID] += share
		}
	}
	if err := creditPlayers(ctx, tx, payouts); err != nil {
		return nil, err
	}

	for _, b := range bets {
		if _, err := tx.ExecContext(ctx,
			`UPDATE match_bets SET settled_at = NOW(), payout = $1 WHERE id = $2`,
			shares[b.ID], b.ID,
		); err != nil {
			return nil, fmt.Errorf("failed to settle bet %d: %w", b.ID, err)
		}
	}

	return payouts, tx.Commit()
}

// isRosterMember reports whether the player is drafted into this match.
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

// creditPlayers adds points to each account in ascending tg_id order.
//
// The order matters. PayoutWinners and RefundAllBets both take their row locks
// match_bets -> players, which serialises them against each other for one match,
// but says nothing about two different matches settling at the same time. With
// the credits issued in map-iteration order, two such transactions sharing a
// pair of bettors grabbed the same players rows in opposite orders and
// deadlocked; Postgres then aborted one of the settlements.
func creditPlayers(ctx context.Context, tx *sql.Tx, credits map[int64]int) error {
	tgIDs := make([]int64, 0, len(credits))
	for tgID := range credits {
		tgIDs = append(tgIDs, tgID)
	}
	sort.Slice(tgIDs, func(i, j int) bool { return tgIDs[i] < tgIDs[j] })

	for _, tgID := range tgIDs {
		amount := credits[tgID]
		if amount <= 0 {
			continue
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE players SET points = points + $1 WHERE tg_id = $2`, amount, tgID)
		if err != nil {
			return fmt.Errorf("failed to credit player %d: %w", tgID, err)
		}
		// A missing players row must abort the whole settlement. Ignoring the
		// row count credited nobody and still let the caller mark the bet
		// settled, which turns "this account is gone" into points that quietly
		// evaporate with no record that they were owed.
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to confirm credit for player %d: %w", tgID, err)
		}
		if affected == 0 {
			return fmt.Errorf("cannot credit %d points to telegram user %d: %w",
				amount, tgID, domain.ErrPlayerNotFound)
		}
	}
	return nil
}

// calculatePayoutShares distributes the whole pool across the winning bets in
// proportion to their stake. The integer remainder left over by the division is
// handed out one point at a time (largest remainder first, bet ID as tie-break)
// so that the sum of payouts equals the pool exactly and the result is
// deterministic. If nobody backed the winning team, the pool is consumed.
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

// lockUnsettledBets reads and locks every live bet of a match. Rows are fully
// consumed before the caller issues any write: lib/pq cannot interleave a query
// and an exec on the same connection.
func (r *BetPostgres) lockUnsettledBets(ctx context.Context, tx *sql.Tx, matchID int) ([]models.Bet, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, match_id, tg_user_id, team_chosen, amount, created_at, settled_at, payout
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
	if err := rows.Scan(&b.ID, &b.MatchID, &b.TgUserID, &b.TeamChosen,
		&b.Amount, &b.CreatedAt, &settledAt, &b.Payout); err != nil {
		return models.Bet{}, err
	}
	if settledAt.Valid {
		t := settledAt.Time
		b.SettledAt = &t
	}
	return b, nil
}

// GetPlayerPoints returns the current points for a player by Telegram ID.
func (r *BetPostgres) GetPlayerPoints(ctx context.Context, tgUserID int64) (int, error) {
	var points int
	err := r.db.QueryRowContext(ctx,
		`SELECT points FROM players WHERE tg_id = $1`,
		tgUserID,
	).Scan(&points)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("telegram user %d: %w", tgUserID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get player points: %w", err)
	}
	return points, nil
}

// RefundAllBets returns every live bet of a match to its owner and marks the
// bets settled. Already settled bets are left alone, so a repeated call is a
// no-op. Returns the number of refunded bets.
//
// Rows are locked match_bets -> players, the same order PayoutWinners uses, and
// the credits themselves go out in ascending tg_id order — see creditPlayers.
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

	refunds := make(map[int64]int, len(bets))
	for _, b := range bets {
		refunds[b.TgUserID] += b.Amount
	}
	if err := creditPlayers(ctx, tx, refunds); err != nil {
		return 0, err
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
