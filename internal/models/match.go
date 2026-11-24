package models

import "time"

type Match struct {
	ID              int            `json:"id"`
	LobbyMatchID    *int           `json:"lobby_match_id,omitempty"`
	FileHash        string         `json:"file_hash"`
	MatchSignature  string         `json:"match_signature"`
	CreatedAt       time.Time      `json:"created_at"`
	Players         []PlayerResult `json:"players"`
	MVP             string         `json:"mvp,omitempty"` // MVP player name
	SVP             string         `json:"svp,omitempty"` // SVPG/SVP player name
	HeaderWinKills  *int           `json:"header_win_kills,omitempty"`
	HeaderLoseKills *int           `json:"header_lose_kills,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
}

type PlayerResult struct {
	ID         int     `json:"id"`
	MatchID    int     `json:"match_id"`
	PlayerID   int     `json:"player_id"`
	PlayerName string  `json:"player_name"`
	Result     string  `json:"result"`
	Kills      int     `json:"kills"`
	Deaths     int     `json:"deaths"`
	Assists    int     `json:"assists"`
	Champion   string  `json:"champion"`
	Rating     float64 `json:"rating,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// AIImageResponse is the expected JSON structure from AI/OCR scoreboard parsers.
type AIImageResponse struct {
	Players         []PlayerResult `json:"players"`
	MVP             *string        `json:"mvp"`
	SVP             *string        `json:"svp"`
	HeaderWinKills  *int           `json:"header_win_kills,omitempty"`
	HeaderLoseKills *int           `json:"header_lose_kills,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
}

type Player struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Medals counts how many MVP and SVPG awards a player earned.
type Medals struct {
	MVP int `json:"mvp"`
	SVP int `json:"svp"`
}

// MatchResult describes a screenshot that was successfully turned into a match.
type MatchResult struct {
	MatchID int    `json:"match_id"`
	MVP     string `json:"mvp,omitempty"` // player name, empty when no medal was read
	SVP     string `json:"svp,omitempty"`
}
