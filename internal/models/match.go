package models

import "time"

type Match struct {
	ID             int            `json:"id"`
	FileHash       string         `json:"file_hash"`
	MatchSignature string         `json:"match_signature"`
	CreatedAt      time.Time      `json:"created_at"`
	Players        []PlayerResult `json:"players"`
}

type PlayerResult struct {
	ID         int    `json:"id"`
	MatchID    int    `json:"match_id"`
	PlayerName string `json:"player_name"`
	Result     string `json:"result"`
	Kills      int    `json:"kills"`
	Deaths     int    `json:"deaths"`
	Assists    int    `json:"assists"`
	Champion   string `json:"champion"`
}

type Player struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
