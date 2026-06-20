package application

import (
	"time"

	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"blackwatch/pkg/sheets"
)

type AIProvider interface {
	ParseImage(data []byte) (*models.Match, error)
	ParseImageWithPlayers(data []byte, expectedPlayers []string) (*models.Match, error)
}

type Logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
}

type MatchService interface {
	ProcessImage(data []byte) (int, error)
	ProcessImageWithPlayers(data []byte, expectedPlayers []string) (int, error)
	ProcessImageFromURL(url string) (int, error)
	ProcessImageFromURLWithPlayers(url string, expectedPlayers []string) (int, error)
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
	GetDiscordIDByPlayerID(playerID int) (string, error)
	GetPlayerByDiscordID(discordID string) (int, string, error)
	GetHistoryByID(id int) ([]string, error)
	WipePlayerByID(id int) error
	GetPlayerStats(name string) (*PlayerStats, error)
	GetPlayerStatsByID(id int) (*PlayerStats, error)
}

type LicenseService interface {
	IsLicenseValid(guildID string) (bool, error)
	UpgradeLicense(guildID string, expiresAt time.Time) error
	ExpireLicense(guildID string) error
	GetLicenseInfo(guildID string) (status string, expiresAt time.Time, err error)
}

type Service struct {
	MatchService       MatchService
	ProfileLinkService ProfileLinkService
	TelegramService    TelegramService
	Lobby              *LobbyService
	EloService         *EloService
	BettingService     *BettingService
	LicenseService     LicenseService
	FAQService         *FAQService
}

func NewService(repos *repository.Repository, ai AIProvider, sheetsClient sheets.Client, ownerEmail, spreadsheetID string, httpTimeoutSec int, logger Logger) *Service {
	matchSvc := NewMatchServiceImpl(repos.Match, ai, sheetsClient, ownerEmail, spreadsheetID, httpTimeoutSec, logger)
	lobby := NewLobbyService(logger)

	// Wire the lobby with the match repository for DB-backed match lifecycle
	lobby.SetMatchRepository(repos.LobbyMatch)

	// Wire sheet syncer: when MMR updates happen, sync to Google Sheets asynchronously
	lobby.SetSheetSyncer(func(mmrUpdates map[int]int) {
		if sheetsClient != nil {
			_, err := matchSvc.SyncToGoogleSheet()
			if err != nil {
				logger.Error("lobby: failed to sync MMR to sheets: %v", err)
			} else {
				logger.Info("lobby: MMR synced to Google Sheets (%d players)", len(mmrUpdates))
			}
		}
	})

	// FAQService will be set via SetFAQService after construction if DeepSeek is configured
	return &Service{
		MatchService:       matchSvc,
		ProfileLinkService: NewProfileLinkServiceImpl(repos.ProfileLink, repos.Match, logger),
		TelegramService:    NewTelegramServiceImpl(repos.Telegram, logger),
		Lobby:              lobby,
		EloService:         NewEloService(logger, matchSvc, repos.LobbyMatch),
		BettingService:     NewBettingService(logger, repos.Bet),
		LicenseService:     repos.License,
	}
}

// SetFAQService sets the FAQ service after construction.
func (s *Service) SetFAQService(faq *FAQService) {
	s.FAQService = faq
}
