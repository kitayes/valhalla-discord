package models

import (
	"time"

	"blackwatch/internal/domain"
)

// Bet represents a Discord player's ValCoins bet on a lobby match.
type Bet struct {
	ID         int        `json:"id"`
	MatchID    int        `json:"match_id"`
	PlayerID   int        `json:"player_id"`
	TeamChosen string     `json:"team_chosen"` // "Team A" or "Team B"
	Amount     int        `json:"amount"`
	Insured    bool       `json:"insured"`
	CreatedAt  time.Time  `json:"created_at"`
	SettledAt  *time.Time `json:"settled_at,omitempty"` // nil while the bet is still live
	Payout     int        `json:"payout"`               // ValCoins credited back on settlement
}

// IsSettled reports whether the bet has already been paid out or refunded.
func (b Bet) IsSettled() bool { return b.SettledAt != nil }

// PendingSettlement is a finished match that still holds live bets.
type PendingSettlement struct {
	MatchID   int
	Winner    string
	Cancelled bool
}

// PlaceDiscordBetRequest is the input for placing a bet from Discord.
type PlaceDiscordBetRequest struct {
	MatchID      int
	PlayerID     int
	TeamChosen   string
	Amount       int
	UseInsurance bool
}

// PayoutSummary carries the outcome of settling a match's bets, including
// any 50% insurance refunds on losing insured bets.
type PayoutSummary struct {
	PlayerPayouts    map[int]int
	InsuranceRefunds map[int]int
	TotalPool        int
	BetCount         int
}

// BetPool is the live ValCoins staked on one match, split by side.
type BetPool struct {
	MatchID int
	AmountA int
	AmountB int
	CountA  int
	CountB  int
}

func (p BetPool) Total() int { return p.AmountA + p.AmountB }

func (p BetPool) Count(team string) int {
	if team == domain.TeamB {
		return p.CountB
	}
	return p.CountA
}

func (p BetPool) Amount(team string) int {
	if team == domain.TeamB {
		return p.AmountB
	}
	return p.AmountA
}

func (p BetPool) Odds(team string) float64 {
	side := p.Amount(team)
	if side <= 0 {
		return 0
	}
	return float64(p.Total()) / float64(side)
}
