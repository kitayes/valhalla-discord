package application

import (
	"valhalla/internal/integration"
	"valhalla/internal/models"
	"valhalla/internal/repository"
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
	ProcessImage(data []byte) error
	GetExcelReport() ([]byte, error)
	SyncToGoogleSheet() (string, error)
	SetTimer(dateStr string) error
	ResetGlobal() error
	ResetPlayer(name, dateStr string) error
	DeleteMatch(id int) error
	WipeAllData() error

	GetLeaderboard(sortBy string) ([]*PlayerStats, error)

	GetPlayerList() ([]models.Player, error)
	GetPlayerNameByID(id int) (string, error)
	GetHistoryByID(id int) ([]string, error)
	WipePlayerByID(id int) error
	GetPlayerStats(name string) (*PlayerStats, error)
}

type Service struct {
	MatchService MatchService
}

func NewService(repos *repository.Repository, ai AIProvider, sheets *integration.SheetService, ownerEmail string, logger Logger) *Service {
	return &Service{
		MatchService: NewMatchServiceImpl(repos.Match, ai, sheets, ownerEmail, logger),
	}
}
