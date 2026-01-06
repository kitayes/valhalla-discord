package models

import "time"

type Match struct {
	ID             int            `json:"id" db:"id"`
	FileHash       string         `json:"file_hash" db:"file_hash"`
	MatchSignature string         `json:"match_signature" db:"match_signature"`
	CreatedAt      time.Time      `json:"created_at" db:"created_at"`
	Players        []PlayerResult `json:"players"`
}

type PlayerResult struct {
	ID         int    `json:"id" db:"id"`
	MatchID    int    `json:"match_id" db:"match_id"`
	PlayerName string `json:"player_name" db:"player_name"`
	Result     string `json:"result" db:"result"`
	Kills      int    `json:"kills" db:"kills"`
	Deaths     int    `json:"deaths" db:"deaths"`
	Assists    int    `json:"assists" db:"assists"`
	Champion   string `json:"champion" db:"champion"`
}
