package models

import "time"

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
