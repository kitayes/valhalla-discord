package models

const (
	StateIdle            = ""
	StateWaitingNickname = "waiting_nickname"
	StateWaitingGameID   = "waiting_game_id"
	StateWaitingZoneID   = "waiting_zone_id"
	StateWaitingStars    = "waiting_stars"
	StateWaitingRole     = "waiting_role"
	StateWaitingTeamName = "waiting_team_name"
	StateWaitingReport   = "waiting_report"
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
