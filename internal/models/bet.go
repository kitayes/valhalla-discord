package models

import (
	"time"

	"blackwatch/internal/domain"
)

// Bet represents a Telegram user's bet on a lobby match.
type Bet struct {
	ID         int        `json:"id"`
	MatchID    int        `json:"match_id"`
	TgUserID   int64      `json:"tg_user_id"`
	TeamChosen string     `json:"team_chosen"` // "Team A" or "Team B"
	Amount     int        `json:"amount"`
	CreatedAt  time.Time  `json:"created_at"`
	SettledAt  *time.Time `json:"settled_at,omitempty"` // nil while the bet is still live
	Payout     int        `json:"payout"`               // points credited back on settlement
}

// IsSettled reports whether the bet has already been paid out or refunded.
func (b Bet) IsSettled() bool { return b.SettledAt != nil }

// PendingSettlement is a finished match that still holds live bets, i.e. one
// whose settlement was started and never completed.
type PendingSettlement struct {
	MatchID int
	// Winner is the winning team, empty when the match was cancelled.
	Winner string
	// Cancelled distinguishes "no winner recorded" from "not looked up yet":
	// a cancelled match is refunded, a decided one is paid out.
	Cancelled bool
}

// PlaceBetRequest is the input for placing a bet.
type PlaceBetRequest struct {
	MatchID    int
	TgUserID   int64
	TeamChosen string
	Amount     int
}

// BetPool is the live money staked on one match, split by side.
//
// It exists so the coefficient shown to a bettor is the same number settlement
// will use: PayoutWinners pays a winning bet stake * (whole pool / winning side
// pool), which is exactly Odds. Computing the display from anything else would
// let the channel advertise one multiplier and pay another.
//
// Only unsettled bets count — a settled match's pool is history, not a market.
type BetPool struct {
	MatchID int
	AmountA int
	AmountB int
	CountA  int
	CountB  int
}

// Total is the whole pool, i.e. what gets distributed.
func (p BetPool) Total() int { return p.AmountA + p.AmountB }

// Count is how many live bets back the given side.
func (p BetPool) Count(team string) int {
	if team == domain.TeamB {
		return p.CountB
	}
	return p.CountA
}

// Amount is the points staked on the given side.
func (p BetPool) Amount(team string) int {
	if team == domain.TeamB {
		return p.AmountB
	}
	return p.AmountA
}

// Odds is the current multiplier for team: whole pool over that side's pool.
//
// Zero means "no coefficient", not "pays nothing". With an empty side there is
// no division to make, and a lone bettor on it would take the entire pool
// regardless of size. Callers render zero as a dash rather than as x0.00.
//
// The number moves with every bet placed afterwards and is only final when the
// window closes. Anything that renders it must say so.
func (p BetPool) Odds(team string) float64 {
	side := p.Amount(team)
	if side <= 0 {
		return 0
	}
	return float64(p.Total()) / float64(side)
}
