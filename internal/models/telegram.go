package models

import "time"

const (
	StateIdle              = ""
	StateWaitingTeamName   = "waiting_team_name"
	StateWaitingReport     = "waiting_report" // legacy report state
	StateReportOpponent    = "report_opponent"
	StateReportScore       = "report_score"
	StateReportScreenshots = "report_screenshots"

	StateSoloLine    = "solo_line"
	StateSoloRole    = "solo_role"
	StateSoloConfirm = "solo_confirm"

	StateProfileLine = "profile_line"
	StateProfileRole = "profile_role"

	StateTeamConfirm = "team_confirm"
	// Slot-bearing team states are "<prefix><slot>", e.g. "team_line_3".
	StateTeamLinePrefix     = "team_line_"
	StateTeamRolePrefix     = "team_role_"
	StateTeamFixPrefix      = "team_fix_"
	StateTeamFixRolePrefix  = "team_fixrole_"
	StateTeamEditPrefix     = "team_edit_"
	StateTeamEditRolePrefix = "team_editrole_"
)

// Team statuses. A disqualified team stays in the database so the list of
// who is out is a query, not a chat message, and an admin can put it back.
const (
	TeamStatusActive       = "active"
	TeamStatusDisqualified = "disqualified"
)

type TelegramTeam struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	IsCheckedIn bool   `json:"is_checked_in"`
	Status      string `json:"status"`
	// ChallongeParticipantID is the team's id in the Challonge bracket, nil
	// until a bracket is built.
	ChallongeParticipantID *int64           `json:"challonge_participant_id"`
	Players                []TelegramPlayer `json:"players"`
}

type TelegramPlayer struct {
	ID               int    `json:"id"`
	TelegramID       *int64 `json:"telegram_id"`
	TelegramUsername string `json:"telegram_username"`
	FirstName        string `json:"first_name"`
	GameNickname     string `json:"game_nickname"`
	GameID           string `json:"game_id"`
	ZoneID           string `json:"zone_id"`
	Stars            int    `json:"stars"`
	MainRole         string `json:"main_role"`
	IsCaptain        bool   `json:"is_captain"`
	IsSubstitute     bool   `json:"is_substitute"`
	FSMState         string `json:"fsm_state"`
	TeamID           *int   `json:"team_id"`
}

type TelegramMatchReport struct {
	ID                 int      `json:"id"`
	ReporterTelegramID int64    `json:"reporter_telegram_id"`
	WinnerTeamID       int      `json:"winner_team_id"`
	WinnerTeamName     string   `json:"winner_team_name,omitempty"`
	LoserTeamID        int      `json:"loser_team_id"`
	LoserTeamName      string   `json:"loser_team_name,omitempty"`
	Score              string   `json:"score"`
	PhotoFileIDs       []string `json:"photo_file_ids"`
	// BracketMatchID links the report to the cached bracket match; nil for
	// reports made without a bracket.
	BracketMatchID *int `json:"bracket_match_id"`
	// SyncedAt is when the result reached Challonge; nil means still queued.
	SyncedAt  *time.Time `json:"synced_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Bracket match states, as Challonge reports them.
const (
	BracketPending  = "pending" // waiting for an earlier match
	BracketOpen     = "open"    // both sides known, not played
	BracketComplete = "complete"
)

// BracketMatch is one cached Challonge match. Team ids are nil while the
// slot is not decided yet.
type BracketMatch struct {
	ID               int    `json:"id"`
	ChallongeMatchID int64  `json:"challonge_match_id"`
	Round            int    `json:"round"`
	PlayOrder        int    `json:"play_order"`
	Team1ID          *int   `json:"team1_id"`
	Team2ID          *int   `json:"team2_id"`
	WinnerID         *int   `json:"winner_id"`
	Team1Name        string `json:"team1_name,omitempty"`
	Team2Name        string `json:"team2_name,omitempty"`
	State            string `json:"state"`
	ScoresCSV        string `json:"scores_csv"`
	BothNotified     bool   `json:"both_notified"`
}

// Has reports whether the team plays in this match.
func (m BracketMatch) Has(teamID int) bool {
	return (m.Team1ID != nil && *m.Team1ID == teamID) || (m.Team2ID != nil && *m.Team2ID == teamID)
}

// Opponent returns the other side for teamID, or nil if unknown.
func (m BracketMatch) Opponent(teamID int) *int {
	if m.Team1ID != nil && *m.Team1ID == teamID {
		return m.Team2ID
	}
	if m.Team2ID != nil && *m.Team2ID == teamID {
		return m.Team1ID
	}
	return nil
}

// Ready reports whether both sides are known and the match is unplayed.
func (m BracketMatch) Ready() bool {
	return m.State == BracketOpen && m.Team1ID != nil && m.Team2ID != nil
}
