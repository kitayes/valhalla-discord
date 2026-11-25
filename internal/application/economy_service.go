package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
)

// EconomyService orchestrates ValCoins rewards, chat/voice XP & level-ups,
// daily claims, player tips, leaderboards, and the server shop.
type EconomyService struct {
	logger     Logger
	repo       repository.Economy
	lobbyMatch LobbyMatchRepository
	matchSvc   MatchService
}

func NewEconomyService(logger Logger, repo repository.Economy, lobbyMatch LobbyMatchRepository, matchSvc MatchService) *EconomyService {
	return &EconomyService{
		logger:     logger,
		repo:       repo,
		lobbyMatch: lobbyMatch,
		matchSvc:   matchSvc,
	}
}

func (s *EconomyService) GetPlayerEconomy(ctx context.Context, playerID int) (*models.PlayerEconomy, error) {
	return s.repo.GetPlayerEconomy(ctx, playerID)
}

func (s *EconomyService) GetLedger(ctx context.Context, playerID, limit int) ([]models.LedgerEntry, error) {
	return s.repo.GetLedger(ctx, playerID, limit)
}

func (s *EconomyService) GetPlayerBetStats(ctx context.Context, playerID int) (models.PlayerBetStats, error) {
	return s.repo.GetPlayerBetStats(ctx, playerID)
}

func (s *EconomyService) GetRichLeaderboard(ctx context.Context, limit int) ([]models.RichPlayer, error) {
	return s.repo.GetRichLeaderboard(ctx, limit)
}

func (s *EconomyService) GetXPLeaderboard(ctx context.Context, limit int) ([]models.RichPlayer, error) {
	return s.repo.GetXPLeaderboard(ctx, limit)
}

// GrantActivityXP records chat/voice XP and/or activity counters (messages & voice seconds).
// Either xpDelta or counter deltas may be zero (e.g. flushing message counts after daily XP cap).
func (s *EconomyService) GrantActivityXP(ctx context.Context, playerID int, xpDelta, voiceSeconds, messages int64) (*models.XPGrantResult, error) {
	if xpDelta <= 0 && voiceSeconds <= 0 && messages <= 0 {
		return nil, nil
	}
	return s.repo.GrantXP(ctx, playerID, xpDelta, voiceSeconds, messages)
}

// ClaimDaily grants the player's daily streak bonus.
func (s *EconomyService) ClaimDaily(ctx context.Context, playerID int) (*models.DailyClaimResult, error) {
	return s.repo.ClaimDaily(ctx, playerID, time.Now())
}

// TipCoins transfers coins from senderID to receiverID with a 5% anti-inflation burn.
func (s *EconomyService) TipCoins(ctx context.Context, senderID, receiverID, amount int) (*models.TipResult, error) {
	if senderID == receiverID {
		return nil, domain.ErrSelfTip
	}
	if amount < domain.TipMinAmount || amount > domain.TipMaxAmount {
		return nil, fmt.Errorf("%w: tip must be between %d and %d", domain.ErrInvalidAmount, domain.TipMinAmount, domain.TipMaxAmount)
	}
	burn := domain.CalculateTipBurn(amount)
	return s.repo.TipCoins(ctx, senderID, receiverID, amount, burn)
}

// AdminAdjustCoins manually adds or removes ValCoins with an audit log note.
func (s *EconomyService) AdminAdjustCoins(ctx context.Context, playerID, delta int, note string) (int, int, error) {
	if note == "" {
		if delta > 0 {
			note = "Начисление от администратора"
		} else {
			note = "Списание администратором"
		}
	}
	return s.repo.AdminAdjustCoins(ctx, playerID, delta, note)
}

// AwardMatchCoins credits win/participation + MVP/SVP ValCoins to every player
// in a finished lobby match. Each player's reward uses ref_key="match:<id>", so
// replays never double-pay.
func (s *EconomyService) AwardMatchCoins(
	ctx context.Context,
	matchID int,
	winner string,
	mvpPlayerID, svpPlayerID int,
	mvpName, svpName string,
) ([]models.MatchCoinReward, error) {
	match, err := s.lobbyMatch.GetByID(ctx, matchID)
	if err != nil {
		return nil, fmt.Errorf("economy: failed to load match %d: %w", matchID, err)
	}

	if mvpName == "" {
		mvpName = match.MVP
	}
	if svpName == "" {
		svpName = match.SVP
	}

	allIDs := make([]int, 0, len(match.TeamAIDs)+len(match.TeamBIDs))
	allIDs = append(allIDs, match.TeamAIDs...)
	allIDs = append(allIDs, match.TeamBIDs...)

	var names map[int]string
	if (mvpPlayerID <= 0 && mvpName != "") || (svpPlayerID <= 0 && svpName != "") {
		if loaded, nErr := s.matchSvc.GetPlayerNamesByIDs(ctx, allIDs); nErr == nil {
			names = loaded
		}
	}

	winningSet := make(map[int]bool, 5)
	if winner == domain.TeamA {
		for _, id := range match.TeamAIDs {
			winningSet[id] = true
		}
	} else if winner == domain.TeamB {
		for _, id := range match.TeamBIDs {
			winningSet[id] = true
		}
	}

	refKey := fmt.Sprintf("match:%d", matchID)
	rewards := make([]models.MatchCoinReward, 0, len(allIDs))

	for _, pid := range allIDs {
		won := winningSet[pid]
		coins := domain.MatchLossCoins
		matchXP := int64(domain.MatchLossXP)
		var parts []string
		if won {
			coins = domain.MatchWinCoins
			matchXP = int64(domain.MatchWinXP)
			parts = append(parts, fmt.Sprintf("Победа в матче #%d", matchID))
		} else {
			parts = append(parts, fmt.Sprintf("Участие в матче #%d", matchID))
		}

		isMVP := (mvpPlayerID > 0 && pid == mvpPlayerID) ||
			(mvpPlayerID <= 0 && won && mvpName != "" && strings.EqualFold(names[pid], mvpName))
		isSVP := (svpPlayerID > 0 && pid == svpPlayerID) ||
			(svpPlayerID <= 0 && !won && svpName != "" && strings.EqualFold(names[pid], svpName))

		if isMVP {
			coins += domain.MatchMVPCoins
			matchXP += int64(domain.MatchMVPXP)
			parts = append(parts, "MVP")
		} else if isSVP {
			coins += domain.MatchSVPCoins
			matchXP += int64(domain.MatchSVPXP)
			parts = append(parts, "SVPG")
		}

		note := strings.Join(parts, " + ")
		credited, bal, applied, grantErr := s.repo.GrantCoins(ctx, pid, coins, domain.LedgerReasonMatchReward, refKey, note)
		if grantErr != nil {
			s.logger.Error("economy: failed to award match #%d coins to player %d: %v", matchID, pid, grantErr)
			continue
		}

		var xpAwarded int64
		if applied && matchXP > 0 {
			if xpRes, xpErr := s.repo.GrantXP(ctx, pid, matchXP, 0, 0); xpErr != nil {
				s.logger.Warn("economy: failed to award match #%d XP to player %d: %v", matchID, pid, xpErr)
			} else if xpRes != nil {
				xpAwarded = xpRes.XPGained
				bal = xpRes.NewBalance
			}
		}

		rewards = append(rewards, models.MatchCoinReward{
			PlayerID: pid,
			Coins:    credited,
			XPGained: xpAwarded,
			Won:      won,
			IsMVP:    isMVP,
			IsSVP:    isSVP,
			Skipped:  !applied,
			Balance:  bal,
			Note:     note,
		})
	}
	return rewards, nil
}

// =====================================================================
// Shop operations
// =====================================================================

func (s *EconomyService) ListShopItems(ctx context.Context, includeDisabled bool) ([]models.ShopItem, error) {
	return s.repo.ListShopItems(ctx, includeDisabled)
}

func (s *EconomyService) GetShopItemByID(ctx context.Context, itemID int) (*models.ShopItem, error) {
	return s.repo.GetShopItemByID(ctx, itemID)
}

func (s *EconomyService) UpsertShopItem(ctx context.Context, item *models.ShopItem) (*models.ShopItem, error) {
	if !domain.IsValidShopKind(item.Kind) {
		return nil, fmt.Errorf("invalid shop item kind %q", item.Kind)
	}
	if item.Price <= 0 {
		return nil, fmt.Errorf("%w: price must be positive", domain.ErrInvalidAmount)
	}
	item.Code = strings.ToLower(strings.TrimSpace(item.Code))
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	item.Payload = strings.TrimSpace(item.Payload)
	if item.Code == "" || item.Name == "" {
		return nil, fmt.Errorf("shop item code and name are required")
	}
	if item.Kind == domain.ShopKindProfileColor && item.Payload != "" {
		if _, err := domain.ParseHexColor(item.Payload); err != nil {
			return nil, err
		}
	}
	if item.SortOrder == 0 {
		item.SortOrder = 100
	}
	return s.repo.UpsertShopItem(ctx, item)
}

func (s *EconomyService) SetShopItemEnabled(ctx context.Context, code string, enabled bool) (*models.ShopItem, error) {
	return s.repo.SetShopItemEnabled(ctx, strings.ToLower(strings.TrimSpace(code)), enabled)
}

func (s *EconomyService) PurchaseItem(ctx context.Context, playerID, itemID int, customInput string) (*models.ShopOrder, error) {
	return s.repo.PurchaseShopItem(ctx, playerID, itemID, customInput)
}

func (s *EconomyService) ResolveOrder(ctx context.Context, orderID int, approve bool, adminDiscordID string) (*models.ShopOrder, error) {
	return s.repo.ResolveShopOrder(ctx, orderID, approve, adminDiscordID)
}

func (s *EconomyService) RefundRoleOrder(ctx context.Context, orderID int, reason string) error {
	return s.repo.RefundFulfilledOrder(ctx, orderID, reason)
}

func (s *EconomyService) GetPlayerOrders(ctx context.Context, playerID, limit int) ([]models.ShopOrder, error) {
	return s.repo.GetPlayerOrders(ctx, playerID, limit)
}

func (s *EconomyService) AdminAdjustXP(ctx context.Context, playerID int, xpDelta int64) (*models.XPGrantResult, error) {
	return s.repo.AdminAdjustXP(ctx, playerID, xpDelta)
}

func (s *EconomyService) AdminSetLevel(ctx context.Context, playerID, targetLevel int) (*models.XPGrantResult, error) {
	return s.repo.AdminSetLevel(ctx, playerID, targetLevel)
}

func (s *EconomyService) AdminSetProfileTitle(ctx context.Context, playerID int, title string) error {
	return s.repo.AdminSetProfileTitle(ctx, playerID, title)
}

func (s *EconomyService) AdminClearProfileColor(ctx context.Context, playerID int) error {
	return s.repo.AdminClearProfileColor(ctx, playerID)
}

func (s *EconomyService) DeleteShopItem(ctx context.Context, code string) error {
	return s.repo.DeleteShopItem(ctx, strings.ToLower(strings.TrimSpace(code)))
}

func (s *EconomyService) AdminRefundShopOrder(ctx context.Context, orderID int, adminDiscordID string) (*models.ShopOrder, error) {
	return s.repo.AdminRefundShopOrder(ctx, orderID, adminDiscordID)
}

func (s *EconomyService) RevertFinishedMatch(ctx context.Context, matchID int) (*models.MatchRevertSummary, error) {
	s.logger.Warn("economy: reverting finished match #%d", matchID)
	return s.repo.RevertFinishedMatch(ctx, matchID)
}
