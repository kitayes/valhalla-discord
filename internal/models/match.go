package models

import "time"

type Match struct {
	ID             int            `json:"id"`
	FileHash       string         `json:"file_hash"`
	MatchSignature string         `json:"match_signature"`
	CreatedAt      time.Time      `json:"created_at"`
	Players        []PlayerResult `json:"players"`
	MVP            string         `json:"mvp,omitempty"` // MVP player name
	SVP            string         `json:"svp,omitempty"` // SVPG/SVP player name
}

type PlayerResult struct {
	ID         int    `json:"id"`
	MatchID    int    `json:"match_id"`
	PlayerID   int    `json:"player_id"`
	PlayerName string `json:"player_name"`
	Result     string `json:"result"`
	Kills      int    `json:"kills"`
	Deaths     int    `json:"deaths"`
	Assists    int    `json:"assists"`
	Champion   string `json:"champion"`
}

// AIImageResponse is the expected JSON structure from Gemini AI for the new prompt format.
type AIImageResponse struct {
	Players []PlayerResult `json:"players"`
	MVP     *string        `json:"mvp"`
	SVP     *string        `json:"svp"`
}

type Player struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
