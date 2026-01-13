package application

import (
	"valhalla/internal/models"
	"valhalla/internal/repository"
	"valhalla/pkg/sheets"
)

type AIProvider interface {
	ParseImage(data []byte) (*models.Match, error)
}

type Logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
}

type MatchService interface {
	ProcessImage(data []byte) (int, error)
	ProcessImageFromURL(url string) (int, error)
	GetExcelReport() ([]byte, error)
	SyncToGoogleSheet() (string, error)
	SetTimer(dateStr string) error
	ResetGlobal() error
	ResetPlayer(name, dateStr string) error
	DeleteMatch(id int) error
	WipeAllData() error
	RenamePlayer(id int, newName string) error

	GetLeaderboard(sortBy string) ([]*PlayerStats, error)

	GetPlayerList() ([]models.Player, error)
	GetPlayerNameByID(id int) (string, error)
	GetHistoryByID(id int) ([]string, error)
	WipePlayerByID(id int) error
	GetPlayerStats(name string) (*PlayerStats, error)
	GetPlayerStatsByID(id int) (*PlayerStats, error)
}

type Service struct {
	MatchService       MatchService
	ProfileLinkService ProfileLinkService
	TelegramService    TelegramService
}

func NewService(repos *repository.Repository, ai AIProvider, sheetsClient sheets.Client, ownerEmail string, logger Logger) *Service {
	return &Service{
		MatchService:       NewMatchServiceImpl(repos.Match, ai, sheetsClient, ownerEmail, logger),
		ProfileLinkService: NewProfileLinkServiceImpl(repos.ProfileLink, repos.Match, logger),
		TelegramService:    NewTelegramServiceImpl(repos.Telegram, logger),
	}
}
