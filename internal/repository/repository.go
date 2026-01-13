package repository

import (
	"database/sql"
	"time"
	"valhalla/internal/models"
)

type Match interface {
	Create(match models.Match) (int, error)
	Exists(fileHash, matchSignature string) (bool, error)
	GetAllAfter(date time.Time) ([]models.Match, error)
	Delete(id int) error
	Restore(id int) error
	WipeAll() error

	SetSeasonStartDate(date time.Time) error
	GetSeasonStartDate() (time.Time, error)

	SetPlayerResetDate(playerName string, date time.Time) error
	GetPlayerResetDates() (map[string]time.Time, error)

	GetHistory(playerID int, limit int) ([]models.Match, error)
	EnsurePlayerExists(name string) (int, error)
	GetAllPlayers() ([]models.Player, error)
	GetPlayerNameByID(id int) (string, error)
	WipePlayerByID(id int) error
	RestorePlayer(id int) error
	RenamePlayer(id int, newName string) error
}

type ProfileLink interface {
	CreateLinkCode(playerID int) (string, error)
	ValidateLinkCode(code string) (int, error)
	CreateProfileLink(link *models.ProfileLink) error
	GetLinkByDiscordPlayer(playerID int) (*models.ProfileLink, error)
	GetLinkByTelegramID(telegramID int64) (*models.ProfileLink, error)
	UpdateTelegramProfile(telegramID int64, nickname, gameID, zoneID string, stars int, role string) error
	DeleteLinkByDiscordPlayer(playerID int) error
	DeleteLinkByTelegramID(telegramID int64) error
	GetPlayerIDByName(name string) (int, error)
	GetDiscordStatsByPlayerID(playerID int) (wins, losses, kills, deaths, assists int, err error)
}

type Telegram interface {
	CreateOrUpdatePlayer(p *models.TelegramPlayer) error
	GetPlayerByTelegramID(tgID int64) (*models.TelegramPlayer, error)
	UpdatePlayerState(tgID int64, state string) error
	UpdatePlayerField(tgID int64, column string, value interface{}) error
	UpdatePlayerFieldByID(playerID int, column string, value interface{}) error

	CreateTeam(name string) (*models.TelegramTeam, error)
	GetTeamByID(id int) (*models.TelegramTeam, error)
	GetTeamByName(name string) (*models.TelegramTeam, error)
	DeleteTeam(id int) error
	GetAllTeams() ([]models.TelegramTeam, error)

	GetTeamMembers(teamID int) ([]models.TelegramPlayer, error)
	CreateTeammate(p *models.TelegramPlayer) error
	UpdateLastTeammateData(teamID int, column string, value interface{}) error
	ResetTeamID(teamID int) error
	SetCheckIn(teamID int, status bool) error

	GetAllCaptains() ([]models.TelegramPlayer, error)
	GetSoloPlayers() ([]models.TelegramPlayer, error)

	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

type Repository struct {
	Match
	ProfileLink
	Telegram
	db *sql.DB
}

func NewRepository(cfg *Config, db *sql.DB) *Repository {
	return &Repository{
		Match:       NewMatchPostgres(db),
		ProfileLink: NewProfileLinkPostgres(db),
		Telegram:    NewTelegramPostgres(db),
		db:          db,
	}
}
