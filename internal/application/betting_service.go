package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

// BettingRepository is the subset of repository needed by BettingService.
type BettingRepository interface {
	PlaceBet(ctx context.Context, req models.PlaceBetRequest) error
	GetBetsByMatch(ctx context.Context, matchID int) ([]models.Bet, error)
	PayoutWinners(ctx context.Context, matchID int, winningTeam string) (map[int64]int, error)
	GetPlayerPoints(ctx context.Context, tgUserID int64) (int, error)
	RefundAllBets(ctx context.Context, matchID int) (int, error)
	PendingSettlements(ctx context.Context) ([]models.PendingSettlement, error)
}

// PayoutResult is what settling one match produced. It is a struct rather than
// a widening list of callback arguments because Payouts alone cannot say why it
// is empty, and the announcement depends on exactly that.
type PayoutResult struct {
	MatchID     int
	WinningTeam string
	// Payouts maps tgUserID to the points credited.
	Payouts map[int64]int
	// BetCount is how many live bets were settled. Zero payouts with a non-zero
	// BetCount means everybody backed the losing side.
	BetCount int
}

// HadBets reports whether anyone had a stake on the match.
func (r PayoutResult) HadBets() bool { return r.BetCount > 0 }

// BettingService handles the Telegram betting economy.
type BettingService struct {
	logger Logger
	repo   BettingRepository

	// callbackMu guards the notification hooks: they are wired once the
	// Telegram bot exists, by which point the Discord handlers that fire them
	// are already running.
	callbackMu     sync.RWMutex
	payoutCallback func(PayoutResult)
	refundCallback func(matchID int, refundedCount int)
}

// NewBettingService creates a new BettingService.
func NewBettingService(logger Logger, repo BettingRepository) *BettingService {
	return &BettingService{
		logger: logger,
		repo:   repo,
	}
}

// SetPayoutCallback installs the hook fired after a successful payout.
func (s *BettingService) SetPayoutCallback(fn func(PayoutResult)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.payoutCallback = fn
}

// SetRefundCallback installs the hook fired after bets are refunded.
func (s *BettingService) SetRefundCallback(fn func(matchID int, refundedCount int)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.refundCallback = fn
}

// PlaceBet validates and places a bet for a Telegram user.
func (s *BettingService) PlaceBet(ctx context.Context, req models.PlaceBetRequest) error {
	if req.Amount <= 0 {
		return fmt.Errorf("bet amount must be positive, got %d", req.Amount)
	}

	if !domain.IsValidTeam(req.TeamChosen) {
		return fmt.Errorf("team must be %q or %q, got %q", domain.TeamA, domain.TeamB, req.TeamChosen)
	}

	s.logger.Info("bet: user %d places %d points on %s (match #%d)",
		req.TgUserID, req.Amount, req.TeamChosen, req.MatchID)

	return s.repo.PlaceBet(ctx, req)
}

// ProcessPayout calculates and distributes winnings after a match finishes.
// Returns the payout summary: map of tgUserID -> points received.
func (s *BettingService) ProcessPayout(ctx context.Context, matchID int, winningTeam string) (map[int64]int, error) {
	bets, err := s.repo.GetBetsByMatch(ctx, matchID)
	if err != nil {
		return nil, fmt.Errorf("betting: failed to get bets for match #%d: %w", matchID, err)
	}

	if len(bets) == 0 {
		s.logger.Info("betting: no bets for match #%d — skipping payout", matchID)
		return nil, nil
	}

	// Counters only: the actual distribution is computed inside the payout
	// transaction. Collecting winners and losers into slices copied every bet
	// twice just to take their lengths for one log line.
	//
	// Settled bets are skipped so this line describes the same pool the
	// transaction will actually distribute. Counting all of them meant a second
	// call logged the full pool next to a payout of zero.
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

	payouts, err := s.repo.PayoutWinners(ctx, matchID, winningTeam)
	if err != nil {
		return nil, fmt.Errorf("betting: payout failed: %w", err)
	}

	s.callbackMu.RLock()
	fn := s.payoutCallback
	s.callbackMu.RUnlock()
	if fn != nil {
		fn(PayoutResult{
			MatchID:     matchID,
			WinningTeam: winningTeam,
			Payouts:     payouts,
			BetCount:    liveCount,
		})
	}

	return payouts, nil
}

// SettlePending finishes settlements that were started and never completed:
// a decided match is paid out, a cancelled one refunded. Returns how many
// matches were settled.
//
// Settlement runs off a Discord interaction, and every step after the match row
// is committed can be lost — a failed rating update returns before scheduling
// the payout, a detached payout goroutine can lose its connection, a restart can
// land between cancelling a match and refunding it. By then the match is
// FINISHED, so neither the WIN button nor /cancel_match will run again: without
// this sweep the stakes stay deducted with nothing left to move them.
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

		payouts, err := s.ProcessPayout(ctx, p.MatchID, p.Winner)
		if err != nil {
			errs = append(errs, fmt.Errorf("match #%d payout: %w", p.MatchID, err))
			continue
		}
		s.logger.Warn("betting: swept match #%d (%s) — %d bettor(s) paid out after an incomplete settlement",
			p.MatchID, p.Winner, len(payouts))
		settled++
	}

	return settled, errors.Join(errs...)
}

// GetPlayerPoints returns the current points balance for a Telegram user.
func (s *BettingService) GetPlayerPoints(ctx context.Context, tgUserID int64) (int, error) {
	return s.repo.GetPlayerPoints(ctx, tgUserID)
}

// RefundAllBets returns all bets for a match and credits users back.
func (s *BettingService) RefundAllBets(ctx context.Context, matchID int) (int, error) {
	refunded, err := s.repo.RefundAllBets(ctx, matchID)
	if err != nil {
		return 0, err
	}

	s.callbackMu.RLock()
	fn := s.refundCallback
	s.callbackMu.RUnlock()
	if fn != nil {
		fn(matchID, refunded)
	}
	return refunded, nil
}

// FormatPayoutSummary returns a human-readable summary of payouts.
//
// PayoutResult.BetCount separates the two ways Payouts can come back empty.
// Folding them together announced "Ставок не было" to a channel full of people
// who had just lost their stakes backing the other team — the one case where the
// message has to be right.
func FormatPayoutSummary(res PayoutResult) string {
	payouts, winningTeam := res.Payouts, res.WinningTeam
	if len(payouts) == 0 {
		if !res.HadBets() {
			return fmt.Sprintf("🏁 Ставок не было. Команда %s победила!", winningTeam)
		}
		return fmt.Sprintf("🏁 Матч завершён! Победила **%s**\n\nНа неё никто не поставил — весь банк сгорел.", winningTeam)
	}

	// Sorted, so the same payout renders the same message every time — a map
	// range reordered the lines on each call and made two reports of one match
	// look like two different results.
	tgIDs := make([]int64, 0, len(payouts))
	for tgID := range payouts {
		tgIDs = append(tgIDs, tgID)
	}
	sort.Slice(tgIDs, func(i, j int) bool { return tgIDs[i] < tgIDs[j] })

	var sb strings.Builder
	fmt.Fprintf(&sb, "🏁 Матч завершён! Победила **%s**\n\nВыплаты победителям:\n", winningTeam)
	for _, tgID := range tgIDs {
		fmt.Fprintf(&sb, "• Пользователь `%d`: +%d 🔮 очков\n", tgID, payouts[tgID])
	}
	return sb.String()
}
