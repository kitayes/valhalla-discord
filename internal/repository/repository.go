package repository

import (
	"context"
	"database/sql"
	"time"

	"blackwatch/internal/ai"
	"blackwatch/internal/models"
)

type Match interface {
	Create(ctx context.Context, match models.Match) (int, error)
	Exists(ctx context.Context, fileHash, matchSignature string) (bool, error)
	GetAllAfter(ctx context.Context, date time.Time) ([]models.Match, error)
	Delete(ctx context.Context, id int) error
	Restore(ctx context.Context, id int) error
	WipeAll(ctx context.Context) error

	SetSeasonStartDate(ctx context.Context, date time.Time) error
	GetSeasonStartDate(ctx context.Context) (time.Time, error)

	SetPlayerResetDate(ctx context.Context, playerName string, date time.Time) error
	GetPlayerResetDates(ctx context.Context) (map[string]time.Time, error)

	GetMedalCountsAfter(ctx context.Context, date time.Time) (map[int]models.Medals, error)
	GetLifetimeMedals(ctx context.Context, playerID int) (models.Medals, error)

	GetHistory(ctx context.Context, playerID int, limit int) ([]models.Match, error)
	EnsurePlayerExists(ctx context.Context, name string) (int, error)
	GetAllPlayers(ctx context.Context) ([]models.Player, error)
	GetPlayerNameByID(ctx context.Context, id int) (string, error)
	GetPlayerNamesByIDs(ctx context.Context, ids []int) (map[int]string, error)
	GetDiscordIDByPlayerID(ctx context.Context, playerID int) (string, error)
	GetPlayerByDiscordID(ctx context.Context, discordID string) (int, string, error)
	FindPlayerByExactName(ctx context.Context, name string) (int, string, error)
	SuggestPlayerNames(ctx context.Context, prefix string, limit int) ([]models.Player, error)
	CreatePlayerWithDiscord(ctx context.Context, name, discordID string) (int, error)
	SetDiscordID(ctx context.Context, playerID int, discordID string) error
	ClearDiscordID(ctx context.Context, playerID int) error
	SetTelegramID(ctx context.Context, playerID int, telegramID int64) error
	ClearTelegramID(ctx context.Context, playerID int) error
	GetPlayerByTelegramID(ctx context.Context, telegramID int64) (int, string, error)
	WipePlayerByID(ctx context.Context, id int) error
	RestorePlayer(ctx context.Context, id int) error
	RenamePlayer(ctx context.Context, id int, newName string) error
}

type ProfileLink interface {
	CreateLinkCode(ctx context.Context, playerID int) (string, error)
	ValidateLinkCode(ctx context.Context, code string) (int, error)
	CreateProfileLink(ctx context.Context, link *models.ProfileLink) error
	GetLinkByDiscordPlayer(ctx context.Context, playerID int) (*models.ProfileLink, error)
	GetLinkByTelegramID(ctx context.Context, telegramID int64) (*models.ProfileLink, error)
	UpdateTelegramProfile(ctx context.Context, telegramID int64, nickname, gameID, zoneID string, stars int, role string) error
	DeleteLinkByDiscordPlayer(ctx context.Context, playerID int) error
	DeleteLinkByTelegramID(ctx context.Context, telegramID int64) error
	GetPlayerIDByName(ctx context.Context, name string) (int, error)
	GetDiscordStatsByPlayerID(ctx context.Context, playerID int) (wins, losses, kills, deaths, assists int, err error)
}

type Telegram interface {
	CreateOrUpdatePlayer(ctx context.Context, p *models.TelegramPlayer) error
	GetPlayerByTelegramID(ctx context.Context, tgID int64) (*models.TelegramPlayer, error)
	UpdatePlayerState(ctx context.Context, tgID int64, state string) error
	UpdatePlayerField(ctx context.Context, tgID int64, column string, value interface{}) error
	UpdatePlayerFieldByID(ctx context.Context, playerID int, column string, value interface{}) error

	CreateTeam(ctx context.Context, name string) (*models.TelegramTeam, error)
	GetTeamByID(ctx context.Context, id int) (*models.TelegramTeam, error)
	GetTeamByName(ctx context.Context, name string) (*models.TelegramTeam, error)
	DeleteTeam(ctx context.Context, id int) error
	GetAllTeams(ctx context.Context) ([]models.TelegramTeam, error)

	GetTeamMembers(ctx context.Context, teamID int) ([]models.TelegramPlayer, error)
	CreateTeammate(ctx context.Context, p *models.TelegramPlayer) error
	ReleaseTeamMembers(ctx context.Context, teamID int) error
	SetCheckIn(ctx context.Context, teamID int, status bool) error

	GetAllCaptains(ctx context.Context) ([]models.TelegramPlayer, error)
	GetSoloPlayers(ctx context.Context) ([]models.TelegramPlayer, error)

	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

type LobbyMatch interface {
	Create(ctx context.Context, req models.CreateLobbyMatchRequest) (int, error)
	GetByID(ctx context.Context, id int) (*models.LobbyMatch, error)
	AtomicSetWinner(ctx context.Context, matchID int, winner string) (bool, error)
	GetAllByGuild(ctx context.Context, guildID string, limit int) ([]models.LobbyMatch, error)
	GetActiveByGuild(ctx context.Context, guildID string) (*models.LobbyMatch, error)
	GetPlayerMMRsBatch(ctx context.Context, playerIDs []int) (map[int]int, error)
	UpdatePlayerMMR(ctx context.Context, playerID, newMMR, matchID int) error
	GetMMRHistory(ctx context.Context, playerID, limit int) ([]models.MMRChange, error)
	SaveThreadID(ctx context.Context, matchID int, threadID string) error
	SaveBetPost(ctx context.Context, matchID int, chatID, messageID int64) error
	GetBetPost(ctx context.Context, matchID int) (chatID, messageID int64, ok bool, err error)
	GetByThreadID(ctx context.Context, threadID string) (*models.LobbyMatch, error)
	GetPlayerNamesByMatchID(ctx context.Context, matchID int) ([]string, error)
	OpenBetting(ctx context.Context, matchID int, window time.Duration) error
	CloseBetting(ctx context.Context, matchID int) error
	CloseExpiredBetting(ctx context.Context) (int, error)
	CancelMatch(ctx context.Context, matchID int) ([]int, error)
	SaveMedals(ctx context.Context, matchID int, mvp, svp string) error
}

type Bet interface {
	PlaceBet(ctx context.Context, req models.PlaceBetRequest) error
	GetBetsByMatch(ctx context.Context, matchID int) ([]models.Bet, error)
	BetPool(ctx context.Context, matchID int) (models.BetPool, error)
	PayoutWinners(ctx context.Context, matchID int, winningTeam string) (map[int64]int, error)
	GetPlayerPoints(ctx context.Context, tgUserID int64) (int, error)
	RefundAllBets(ctx context.Context, matchID int) (int, error)
	PendingSettlements(ctx context.Context) ([]models.PendingSettlement, error)
}

type License interface {
	IsLicenseValid(ctx context.Context, guildID string) (bool, error)
	ExpireLicense(ctx context.Context, guildID string) error
	UpgradeLicense(ctx context.Context, guildID string, expiresAt time.Time) error
	GetLicenseInfo(ctx context.Context, guildID string) (status string, expiresAt time.Time, err error)
}

type QueueBan interface {
	BanPlayer(ctx context.Context, discordID, reason string, bannedUntil time.Time) error
	IsBanned(ctx context.Context, discordID string) (bool, error)
	GetBanInfo(ctx context.Context, discordID string) (reason string, until time.Time, err error)
	PurgeExpired(ctx context.Context) (int, error)
}

type Repository struct {
	Match
	ProfileLink
	Telegram
	LobbyMatch
	Bet
	License
	QueueBan
	db *sql.DB
}

func NewRepository(ctx context.Context, cfg *Config, db *sql.DB, cacheSize int, embeddingClient *ai.EmbeddingClient) (*Repository, error) {
	matchRepo, err := NewMatchPostgres(ctx, db, cacheSize, embeddingClient)
	if err != nil {
		return nil, err
	}
	return &Repository{
		Match:       matchRepo,
		ProfileLink: NewProfileLinkPostgres(db),
		Telegram:    NewTelegramPostgres(db),
		LobbyMatch:  NewLobbyMatchPostgres(db),
		Bet:         NewBetPostgres(db),
		License:     NewLicensePostgres(db),
		QueueBan:    NewQueueBanPostgres(db),
		db:          db,
	}, nil
}
