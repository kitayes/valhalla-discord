package repository

import (
	"context"
	"database/sql"
	"time"

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
	WipePlayerByID(ctx context.Context, id int) error
	RestorePlayer(ctx context.Context, id int) error
	RenamePlayer(ctx context.Context, id int, newName string) error

	GetAliasesForPlayers(ctx context.Context, playerIDs []int) (map[int][]string, error)
	ListAliases(ctx context.Context, playerID int) ([]models.PlayerAlias, error)
	LearnAlias(ctx context.Context, playerID int, alias, source string) error
	AddManualAlias(ctx context.Context, playerID int, alias string) error
	DeleteAlias(ctx context.Context, playerID int, alias string) (bool, error)
	MergePlayers(ctx context.Context, sourceID, targetID int) (*models.PlayerMergeSummary, error)
}

type LobbyMatch interface {
	Create(ctx context.Context, req models.CreateLobbyMatchRequest) (int, error)
	GetByID(ctx context.Context, id int) (*models.LobbyMatch, error)
	AtomicSetWinner(ctx context.Context, matchID int, winner string) (bool, error)
	GetAllByGuild(ctx context.Context, guildID string, limit int) ([]models.LobbyMatch, error)
	GetActiveByGuild(ctx context.Context, guildID string) (*models.LobbyMatch, error)
	GetPlayerMMRsBatch(ctx context.Context, playerIDs []int) (map[int]int, error)
	UpdatePlayerMMR(ctx context.Context, playerID, newMMR, matchID int) error
	UpdatePlayerMMRWithBreakdown(ctx context.Context, playerID, newMMR, matchID, baseDelta, kdaMod, medalBonus int) error
	GetMMRHistory(ctx context.Context, playerID, limit int) ([]models.MMRChange, error)
	SaveThreadID(ctx context.Context, matchID int, threadID string) error
	SaveDiscordBetCard(ctx context.Context, matchID int, channelID, messageID string) error
	GetDiscordBetCard(ctx context.Context, matchID int) (channelID, messageID string, ok bool, err error)
	GetByThreadID(ctx context.Context, threadID string) (*models.LobbyMatch, error)
	GetPlayerNamesByMatchID(ctx context.Context, matchID int) ([]string, error)
	IsInActiveMatch(ctx context.Context, playerID int) (bool, error)
	OpenBetting(ctx context.Context, matchID int, window time.Duration) error
	CloseBetting(ctx context.Context, matchID int) error
	CloseExpiredBetting(ctx context.Context) (int, error)
	CancelMatch(ctx context.Context, matchID int) ([]int, error)
	SaveMedals(ctx context.Context, matchID int, mvp, svp string) error
}

type Bet interface {
	PlaceDiscordBet(ctx context.Context, req models.PlaceDiscordBetRequest) (int, int, error)
	CancelDiscordBet(ctx context.Context, matchID, playerID int) (int, bool, int, error)
	GetOpenBetForPlayer(ctx context.Context, matchID, playerID int) (*models.Bet, error)
	GetBetsByMatch(ctx context.Context, matchID int) ([]models.Bet, error)
	BetPool(ctx context.Context, matchID int) (models.BetPool, error)
	PayoutWinnersDetailed(ctx context.Context, matchID int, winningTeam string) (*models.PayoutSummary, error)
	GetPlayerPointsByID(ctx context.Context, playerID int) (int, error)
	RefundAllBets(ctx context.Context, matchID int) (int, error)
	PendingSettlements(ctx context.Context) ([]models.PendingSettlement, error)
}

type Economy interface {
	GetPlayerEconomy(ctx context.Context, playerID int) (*models.PlayerEconomy, error)
	GetLedger(ctx context.Context, playerID, limit int) ([]models.LedgerEntry, error)
	GrantXP(ctx context.Context, playerID int, xpDelta, voiceSecondsDelta, messagesDelta int64) (*models.XPGrantResult, error)
	AdminAdjustXP(ctx context.Context, playerID int, xpDelta int64) (*models.XPGrantResult, error)
	AdminSetLevel(ctx context.Context, playerID, targetLevel int) (*models.XPGrantResult, error)
	AdminSetProfileTitle(ctx context.Context, playerID int, title string) error
	AdminClearProfileColor(ctx context.Context, playerID int) error
	GrantCoins(ctx context.Context, playerID, amount int, reason, refKey, note string) (int, int, bool, error)
	AdminAdjustCoins(ctx context.Context, playerID, delta int, note string) (int, int, error)
	ClaimDaily(ctx context.Context, playerID int, now time.Time) (*models.DailyClaimResult, error)
	TipCoins(ctx context.Context, senderID, receiverID, amount, burnAmount int) (*models.TipResult, error)
	GetRichLeaderboard(ctx context.Context, limit int) ([]models.RichPlayer, error)
	GetXPLeaderboard(ctx context.Context, limit int) ([]models.RichPlayer, error)
	GetPlayerBetStats(ctx context.Context, playerID int) (models.PlayerBetStats, error)

	ListShopItems(ctx context.Context, includeDisabled bool) ([]models.ShopItem, error)
	GetShopItemByID(ctx context.Context, itemID int) (*models.ShopItem, error)
	UpsertShopItem(ctx context.Context, item *models.ShopItem) (*models.ShopItem, error)
	SetShopItemEnabled(ctx context.Context, code string, enabled bool) (*models.ShopItem, error)
	DeleteShopItem(ctx context.Context, code string) error
	PurchaseShopItem(ctx context.Context, playerID, itemID int, customInput string) (*models.ShopOrder, error)
	ResolveShopOrder(ctx context.Context, orderID int, approve bool, adminDiscordID string) (*models.ShopOrder, error)
	AdminRefundShopOrder(ctx context.Context, orderID int, adminDiscordID string) (*models.ShopOrder, error)
	RefundFulfilledOrder(ctx context.Context, orderID int, reason string) error
	GetPlayerOrders(ctx context.Context, playerID, limit int) ([]models.ShopOrder, error)

	RevertFinishedMatch(ctx context.Context, matchID int) (*models.MatchRevertSummary, error)
}

type License interface {
	IsLicenseValid(ctx context.Context, guildID string) (bool, error)
	ExpireLicense(ctx context.Context, guildID string) error
	UpgradeLicense(ctx context.Context, guildID string, expiresAt time.Time) error
	GetLicenseInfo(ctx context.Context, guildID string) (status string, expiresAt time.Time, err error)
}

type QueueBan interface {
	BanPlayer(ctx context.Context, discordID, reason string, bannedUntil time.Time) error
	UnbanPlayer(ctx context.Context, discordID string) (bool, error)
	IsBanned(ctx context.Context, discordID string) (bool, error)
	GetBanInfo(ctx context.Context, discordID string) (reason string, until time.Time, err error)
	PurgeExpired(ctx context.Context) (int, error)
}

type QuestSeason interface {
	IncrementQuestProgress(ctx context.Context, playerID int, periodKey string, questCodes []string, delta int) error
	GetQuestProgress(ctx context.Context, playerID int, periodKeys []string) ([]models.QuestProgressRow, error)
	ClaimQuestRewards(ctx context.Context, playerID int, claims []models.QuestClaimSpec) (*models.QuestClaimResult, error)

	GetActiveSeason(ctx context.Context) (*models.Season, error)
	GetSeasonByNumber(ctx context.Context, number int) (*models.Season, error)
	ListArchivedSeasons(ctx context.Context, limit int) ([]models.Season, error)
	RenameActiveSeason(ctx context.Context, newName string) (*models.Season, error)
	GetSeasonStandings(ctx context.Context, seasonID, limit int) ([]models.SeasonStanding, error)
	GetPlayerTrophies(ctx context.Context, playerID int) ([]models.PlayerTrophy, error)
	CountMatchesSince(ctx context.Context, since time.Time) (int, error)
	CloseSeasonAndReset(ctx context.Context, standings []models.SeasonStanding, matchesPlayed int, nextName, resetMode, closedBy string, now time.Time) (*models.CloseSeasonResult, error)
}

type Repository struct {
	Match
	LobbyMatch
	Bet
	Economy
	License
	QueueBan
	QuestSeason
	db *sql.DB
}

func NewRepository(ctx context.Context, cfg *Config, db *sql.DB, cacheSize int) (*Repository, error) {
	matchRepo, err := NewMatchPostgres(ctx, db, cacheSize)
	if err != nil {
		return nil, err
	}
	return &Repository{
		Match:       matchRepo,
		LobbyMatch:  NewLobbyMatchPostgres(db),
		Bet:         NewBetPostgres(db),
		Economy:     NewEconomyPostgres(db),
		License:     NewLicensePostgres(db),
		QueueBan:    NewQueueBanPostgres(db),
		QuestSeason: NewQuestSeasonPostgres(db),
		db:          db,
	}, nil
}
