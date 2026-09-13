package models

const (
	StateIdle            = ""
	StateWaitingTeamName = "waiting_team_name"
	StateWaitingReport   = "waiting_report"

	StateSoloLine    = "solo_line"
	StateSoloRole    = "solo_role"
	StateSoloConfirm = "solo_confirm"

	StateTeamConfirm = "team_confirm"
	// Slot-bearing team states are "<prefix><slot>", e.g. "team_line_3".
	StateTeamLinePrefix     = "team_line_"
	StateTeamRolePrefix     = "team_role_"
	StateTeamFixPrefix      = "team_fix_"
	StateTeamFixRolePrefix  = "team_fixrole_"
	StateTeamEditPrefix     = "team_edit_"
	StateTeamEditRolePrefix = "team_editrole_"
)

type TelegramTeam struct {
	ID          int              `json:"id"`
	Name        string           `json:"name"`
	IsCheckedIn bool             `json:"is_checked_in"`
	Players     []TelegramPlayer `json:"players"`
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
