package models

import "time"

// LobbyMatchStatus represents the state of a lobby match.
type LobbyMatchStatus string

const (
	LobbyMatchStatusActive     LobbyMatchStatus = "ACTIVE"
	LobbyMatchStatusProcessing LobbyMatchStatus = "PROCESSING"
	LobbyMatchStatusFinished   LobbyMatchStatus = "FINISHED"
)

// LobbyMatch represents a match created via the lobby system with captains and teams.
type LobbyMatch struct {
	ID          int              `json:"id"`
	GuildID     string           `json:"guild_id"`
	CaptainAID  int              `json:"captain_a_id"`
	CaptainBID  int              `json:"captain_b_id"`
	TeamAIDs    []int            `json:"team_a_ids"`
	TeamBIDs    []int            `json:"team_b_ids"`
	Winner      string           `json:"winner"` // "Team A" or "Team B"
	Status      LobbyMatchStatus `json:"status"`
	ThreadID    string           `json:"thread_id,omitempty"` // Discord thread ID
	BettingOpen bool             `json:"betting_open"`        // Whether betting is still open
	MVP         string           `json:"mvp,omitempty"`       // read off the scoreboard screenshot
	SVP         string           `json:"svp,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
}

// MMRChange is one recorded rating change. Delta is stored rather than derived
// so a later correction to either endpoint cannot rewrite what a past match was
// worth.
type MMRChange struct {
	// MatchID is 0 for a change with no match behind it.
	MatchID   int       `json:"match_id,omitempty"`
	Before    int       `json:"mmr_before"`
	After     int       `json:"mmr_after"`
	Delta     int       `json:"delta"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateLobbyMatchRequest is the input for creating a new lobby match.
type CreateLobbyMatchRequest struct {
	GuildID    string
	CaptainAID int
	CaptainBID int
	TeamAIDs   []int
	TeamBIDs   []int
}
