package application

import (
	"valhalla/internal/integration"
	"valhalla/internal/models"
	"valhalla/internal/repository"
)

type AIProvider interface {
	AnalyzeScreenshot(data []byte) ([]models.PlayerResult, error)
}

type Logger interface {
	Error(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Info(format string, v ...interface{})
	Debug(format string, v ...interface{})
}

type MatchService interface {
	ProcessImage(data []byte) error
	GetExcelReport() ([]byte, error)
	SyncToGoogleSheet() (string, error)
	SetTimer(dateStr string) error
	ResetGlobal() error
	ResetPlayer(name, dateStr string) error
	DeleteMatch(id int) error
	GetLeaderboard() ([]*PlayerStats, error)
	GetPlayerStats(name string) (*PlayerStats, error)
	WipeAllData() error
}

type Service struct {
	MatchService MatchService
}

func NewService(repos *repository.Repository, ai AIProvider, sheets *integration.SheetService, ownerEmail string, logger Logger) *Service {
	return &Service{
		MatchService: NewMatchServiceImpl(repos.Match, ai, sheets, ownerEmail, logger),
	}
}
