package models

import "time"

type ProfileLink struct {
	ID               int       `json:"id"`
	DiscordPlayerID  int       `json:"discord_player_id"`
	TelegramID       *int64    `json:"telegram_id"`
	TelegramUsername string    `json:"telegram_username"`
	GameNickname     string    `json:"game_nickname"`
	GameID           string    `json:"game_id"`
	ZoneID           string    `json:"zone_id"`
	Stars            int       `json:"stars"`
	MainRole         string    `json:"main_role"`
	LinkedAt         time.Time `json:"linked_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type LinkCode struct {
	Code            string    `json:"code"`
	DiscordPlayerID int       `json:"discord_player_id"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}
