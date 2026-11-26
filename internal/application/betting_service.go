package application

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

// BettingRepository is the subset of repository needed by BettingService.
type BettingRepository interface {
	PlaceDiscordBet(ctx context.Context, req models.PlaceDiscordBetRequest) (int, int, error)
	CancelDiscordBet(ctx context.Context, matchID, playerID int) (int, bool, int, error)
	GetOpenBetForPlayer(ctx context.Context, matchID, playerID int) (*models.Bet, error)
	GetBetsByMatch(ctx context.Context, matchID int) ([]models.Bet, error)
	BetPool(ctx context.Context, matchID int) (models.BetPool, error)
	SaveDiscordBetCard(ctx context.Context, matchID int, channelID, messageID string) error
	GetDiscordBetCard(ctx context.Context, matchID int) (channelID, messageID string, ok bool, err error)
	PayoutWinnersDetailed(ctx context.Context, matchID int, winningTeam string) (*models.PayoutSummary, error)
	GetPlayerPointsByID(ctx context.Context, playerID int) (int, error)
	RefundAllBets(ctx context.Context, matchID int) (int, error)
	PendingSettlements(ctx context.Context) ([]models.PendingSettlement, error)
}

// PayoutResult is what settling one match produced.
type PayoutResult struct {
	MatchID          int
	WinningTeam      string
	PlayerPayouts    map[int]int
	InsuranceRefunds map[int]int
	TotalPool        int
	BetCount         int
}

// HadBets reports whether anyone had a stake on the match.
func (r PayoutResult) HadBets() bool { return r.BetCount > 0 }

// BettingService handles the Discord ValCoins betting economy.
type BettingService struct {
	logger Logger
	repo   BettingRepository
	quests *QuestService

	callbackMu            sync.RWMutex
	discordPayoutCallback func(PayoutResult)
	discordRefundCallback func(matchID int, refundedCount int)
}

func NewBettingService(logger Logger, repo BettingRepository) *BettingService {
	return &BettingService{
		logger: logger,
		repo:   repo,
	}
}

// SetQuestService connects automatic quest progress tracking to betting events.
func (s *BettingService) SetQuestService(q *QuestService) {
	s.quests = q
}

func (s *BettingService) SetDiscordPayoutCallback(fn func(PayoutResult)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.discordPayoutCallback = fn
}

func (s *BettingService) SetDiscordRefundCallback(fn func(matchID int, refundedCount int)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.discordRefundCallback = fn
}

func (s *BettingService) PlaceDiscordBet(ctx context.Context, req models.PlaceDiscordBetRequest) (int, int, error) {
	if req.Amount <= 0 {
		return 0, 0, fmt.Errorf("bet amount must be positive, got %d: %w", req.Amount, domain.ErrInvalidAmount)
	}
	if !domain.IsValidTeam(req.TeamChosen) {
		return 0, 0, fmt.Errorf("team must be %q or %q, got %q", domain.TeamA, domain.TeamB, req.TeamChosen)
	}

	s.logger.Info("bet: player %d places %d coins on %s (match #%d, insured=%v)",
		req.PlayerID, req.Amount, req.TeamChosen, req.MatchID, req.UseInsurance)

	betID, newBal, err := s.repo.PlaceDiscordBet(ctx, req)
	if err != nil {
		return 0, 0, err
	}
	if s.quests != nil {
		s.quests.RecordEvent(ctx, req.PlayerID, domain.QuestMetricPlaceBets, 1)
	}
	return betID, newBal, nil
}

func (s *BettingService) CancelDiscordBet(ctx context.Context, matchID, playerID int) (int, bool, int, error) {
	s.logger.Info("bet: player %d cancels bet on match #%d", playerID, matchID)
	return s.repo.CancelDiscordBet(ctx, matchID, playerID)
}

func (s *BettingService) GetOpenBetForPlayer(ctx context.Context, matchID, playerID int) (*models.Bet, error) {
	return s.repo.GetOpenBetForPlayer(ctx, matchID, playerID)
}

func (s *BettingService) SaveDiscordBetCard(ctx context.Context, matchID int, channelID, messageID string) error {
	return s.repo.SaveDiscordBetCard(ctx, matchID, channelID, messageID)
}

func (s *BettingService) DiscordBetCard(ctx context.Context, matchID int) (string, string, bool, error) {
	return s.repo.GetDiscordBetCard(ctx, matchID)
}

func (s *BettingService) GetPlayerPointsByID(ctx context.Context, playerID int) (int, error) {
	return s.repo.GetPlayerPointsByID(ctx, playerID)
}

// ProcessPayoutDetailed settles all bets for a finished match and returns the
// full PayoutResult so the Discord betting channel can render a single combined
// match+betting summary.
func (s *BettingService) ProcessPayoutDetailed(ctx context.Context, matchID int, winningTeam string) (PayoutResult, error) {
	return s.settlePayout(ctx, matchID, winningTeam, false)
}

func (s *BettingService) settlePayout(ctx context.Context, matchID int, winningTeam string, fireDiscordCallback bool) (PayoutResult, error) {
	bets, err := s.repo.GetBetsByMatch(ctx, matchID)
	if err != nil {
		return PayoutResult{}, fmt.Errorf("betting: failed to get bets for match #%d: %w", matchID, err)
	}

	if len(bets) == 0 {
		s.logger.Info("betting: no bets for match #%d — skipping payout", matchID)
		return PayoutResult{MatchID: matchID, WinningTeam: winningTeam}, nil
	}

	totalPool, winningPool, liveCount, winnerCount := 0, 0, 0, 0
	for _, b := range bets {
		if b.IsSettled() {
			continue
		}
		liveCount++
		totalPool += b.Amount
		if b.TeamChosen == winningTeam {
			winningPool += b.Amount
			winnerCount++
		}
	}

	s.logger.Info("betting: match #%d | %d live bets | total pool: %d | winning pool: %d | %d winners, %d losers",
		matchID, liveCount, totalPool, winningPool, winnerCount, liveCount-winnerCount)

	summary, err := s.repo.PayoutWinnersDetailed(ctx, matchID, winningTeam)
	if err != nil {
		return PayoutResult{}, fmt.Errorf("betting: payout failed: %w", err)
	}

	if s.quests != nil {
		for pid, payout := range summary.PlayerPayouts {
			if payout > 0 {
				s.quests.RecordEvent(ctx, pid, domain.QuestMetricWinBets, 1)
			}
		}
	}

	result := PayoutResult{
		MatchID:          matchID,
		WinningTeam:      winningTeam,
		PlayerPayouts:    summary.PlayerPayouts,
		InsuranceRefunds: summary.InsuranceRefunds,
		TotalPool:        summary.TotalPool,
		BetCount:         liveCount,
	}

	if fireDiscordCallback {
		s.callbackMu.RLock()
		dcFn := s.discordPayoutCallback
		s.callbackMu.RUnlock()
		if dcFn != nil {
			dcFn(result)
		}
	}

	return result, nil
}

// SettlePending finishes settlements that were started and never completed:
// a decided match is paid out, a cancelled one refunded.
func (s *BettingService) SettlePending(ctx context.Context) (int, error) {
	pending, err := s.repo.PendingSettlements(ctx)
	if err != nil {
		return 0, fmt.Errorf("betting: failed to list pending settlements: %w", err)
	}

	settled := 0
	var errs []error
	for _, p := range pending {
		if p.Cancelled {
			refunded, err := s.RefundAllBets(ctx, p.MatchID)
			if err != nil {
				errs = append(errs, fmt.Errorf("match #%d refund: %w", p.MatchID, err))
				continue
			}
			s.logger.Warn("betting: swept match #%d — %d bet(s) refunded after an incomplete cancellation",
				p.MatchID, refunded)
			settled++
			continue
		}

		res, err := s.settlePayout(ctx, p.MatchID, p.Winner, true)
		if err != nil {
			errs = append(errs, fmt.Errorf("match #%d payout: %w", p.MatchID, err))
			continue
		}
		s.logger.Warn("betting: swept match #%d (%s) — %d bettor(s) paid out after an incomplete settlement",
			p.MatchID, p.Winner, len(res.PlayerPayouts))
		settled++
	}

	return settled, errors.Join(errs...)
}

func (s *BettingService) BetPool(ctx context.Context, matchID int) (models.BetPool, error) {
	return s.repo.BetPool(ctx, matchID)
}

func (s *BettingService) RefundAllBets(ctx context.Context, matchID int) (int, error) {
	refunded, err := s.repo.RefundAllBets(ctx, matchID)
	if err != nil {
		return 0, err
	}

	s.callbackMu.RLock()
	dcFn := s.discordRefundCallback
	s.callbackMu.RUnlock()
	if dcFn != nil {
		dcFn(matchID, refunded)
	}
	return refunded, nil
}
