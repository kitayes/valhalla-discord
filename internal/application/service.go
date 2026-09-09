package application

import (
	"context"
	"time"

	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"blackwatch/pkg/sheets"
)

type AIProvider interface {
	ParseImage(ctx context.Context, data []byte) (*models.Match, error)
	ParseImageWithPlayers(ctx context.Context, data []byte, expectedPlayers []string) (*models.Match, error)
}

type Logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
}

type MatchService interface {
	ProcessImage(ctx context.Context, data []byte) (*models.MatchResult, error)
	ProcessImageWithPlayers(ctx context.Context, data []byte, expectedPlayers []string) (*models.MatchResult, error)
	ProcessImageFromURL(ctx context.Context, url string) (*models.MatchResult, error)
	ProcessImageFromURLWithPlayers(ctx context.Context, url string, expectedPlayers []string) (*models.MatchResult, error)
	GetExcelReport(ctx context.Context) ([]byte, error)
	SyncToGoogleSheet(ctx context.Context) (string, error)
	SetTimer(ctx context.Context, dateStr string) error
	ResetGlobal(ctx context.Context) error
	ResetPlayer(ctx context.Context, name, dateStr string) error
	DeleteMatch(ctx context.Context, id int) error
	WipeAllData(ctx context.Context) error
	RenamePlayer(ctx context.Context, id int, newName string) error

	GetLeaderboard(ctx context.Context, sortBy string) ([]*PlayerStats, error)

	GetPlayerList(ctx context.Context) ([]models.Player, error)
	GetPlayerNameByID(ctx context.Context, id int) (string, error)
	GetPlayerNamesByIDs(ctx context.Context, ids []int) (map[int]string, error)
	GetDiscordIDByPlayerID(ctx context.Context, playerID int) (string, error)
	GetPlayerByDiscordID(ctx context.Context, discordID string) (int, string, error)
	BindDiscordID(ctx context.Context, playerID int, discordID string, force bool) (string, error)
	BindDiscordByName(ctx context.Context, nickname, discordID string) (int, bool, error)
	FindPlayerByName(ctx context.Context, nickname string) (int, string, error)
	SuggestPlayerNames(ctx context.Context, prefix string, limit int) ([]models.Player, error)
	UnbindDiscordID(ctx context.Context, playerID int) (string, error)
	GetHistoryByID(ctx context.Context, id int) ([]string, error)
	WipePlayerByID(ctx context.Context, id int) error
	GetPlayerStats(ctx context.Context, name string) (*PlayerStats, error)
	GetPlayerStatsByID(ctx context.Context, id int) (*PlayerStats, error)
	GetLifetimeMedals(ctx context.Context, playerID int) (models.Medals, error)

	// Shutdown drains in-flight background work (currently Google Sheets syncs)
	// before the process exits. It is part of the interface rather than
	// something main sniffs for with a type assertion on an anonymous
	// interface literal.
	Shutdown(ctx context.Context) error
}

type LicenseService interface {
	IsLicenseValid(ctx context.Context, guildID string) (bool, error)
	UpgradeLicense(ctx context.Context, guildID string, expiresAt time.Time) error
	ExpireLicense(ctx context.Context, guildID string) error
	GetLicenseInfo(ctx context.Context, guildID string) (status string, expiresAt time.Time, err error)
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
	lobby := NewLobbyService(logger, repos.LobbyMatch, repos.QueueBan)

	// Wire sheet syncer: when MMR updates happen, sync to Google Sheets asynchronously.
	// The callback runs detached from any request, so it gets its own bounded context.
	lobby.SetSheetSyncer(func(mmrUpdates map[int]int) {
		if sheetsClient == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), sheetSyncTimeout)
		defer cancel()

		if _, err := matchSvc.SyncToGoogleSheet(ctx); err != nil {
			logger.Error("lobby: failed to sync MMR to sheets: %v", err)
		} else {
			logger.Info("lobby: MMR synced to Google Sheets (%d players)", len(mmrUpdates))
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
