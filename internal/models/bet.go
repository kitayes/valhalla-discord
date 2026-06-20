package models

import "time"

// Bet represents a Telegram user's bet on a lobby match.
type Bet struct {
	ID         int       `json:"id"`
	MatchID    int       `json:"match_id"`
	TgUserID   int64     `json:"tg_user_id"`
	TeamChosen string    `json:"team_chosen"` // "Team A" or "Team B"
	Amount     int       `json:"amount"`
	CreatedAt  time.Time `json:"created_at"`
}

// PlaceBetRequest is the input for placing a bet.
type PlaceBetRequest struct {
	MatchID    int
	TgUserID   int64
	TeamChosen string
	Amount     int
}
