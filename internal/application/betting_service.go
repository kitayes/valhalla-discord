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
	PlaceDiscordBet(ctx context.Context, req models.PlaceDiscordBetRequest) (int, int, error)
	CancelDiscordBet(ctx context.Context, matchID, playerID int) (int, bool, int, error)
	GetOpenBetForPlayer(ctx context.Context, matchID, playerID int) (*models.Bet, error)
	GetBetsByMatch(ctx context.Context, matchID int) ([]models.Bet, error)
	BetPool(ctx context.Context, matchID int) (models.BetPool, error)
	SaveBetPost(ctx context.Context, matchID int, chatID, messageID int64) error
	GetBetPost(ctx context.Context, matchID int) (chatID, messageID int64, ok bool, err error)
	SaveDiscordBetCard(ctx context.Context, matchID int, channelID, messageID string) error
	GetDiscordBetCard(ctx context.Context, matchID int) (channelID, messageID string, ok bool, err error)
	PayoutWinners(ctx context.Context, matchID int, winningTeam string) (map[int64]int, error)
	PayoutWinnersDetailed(ctx context.Context, matchID int, winningTeam string) (*models.PayoutSummary, error)
	GetPlayerPoints(ctx context.Context, tgUserID int64) (int, error)
	GetPlayerPointsByID(ctx context.Context, playerID int) (int, error)
	RefundAllBets(ctx context.Context, matchID int) (int, error)
	PendingSettlements(ctx context.Context) ([]models.PendingSettlement, error)
}

// PayoutResult is what settling one match produced. It is a struct rather than
// a widening list of callback arguments because Payouts alone cannot say why it
// is empty, and the announcement depends on exactly that.
type PayoutResult struct {
	MatchID     int
	WinningTeam string
	// Payouts maps tgUserID to the points credited (Telegram consumers).
	Payouts map[int64]int
	// PlayerPayouts maps internal playerID to the winning payout credited.
	PlayerPayouts map[int]int
	// InsuranceRefunds maps internal playerID to 50% insurance refund on losing insured bets.
	InsuranceRefunds map[int]int
	// TotalPool is the total staked amount across all settled bets.
	TotalPool int
	// BetCount is how many live bets were settled. Zero payouts with a non-zero
	// BetCount means everybody backed the losing side.
	BetCount int
}

// HadBets reports whether anyone had a stake on the match.
func (r PayoutResult) HadBets() bool { return r.BetCount > 0 }

// BettingService handles both Discord and Telegram betting economies.
type BettingService struct {
	logger Logger
	repo   BettingRepository

	// callbackMu guards the notification hooks: they are wired once the
	// Discord and Telegram bots exist.
	callbackMu            sync.RWMutex
	payoutCallback        func(PayoutResult)
	refundCallback        func(matchID int, refundedCount int)
	discordPayoutCallback func(PayoutResult)
	discordRefundCallback func(matchID int, refundedCount int)
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

// SetRefundCallback installs the Telegram hook fired after bets are refunded.
func (s *BettingService) SetRefundCallback(fn func(matchID int, refundedCount int)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.refundCallback = fn
}

// SetDiscordPayoutCallback installs the Discord hook fired after a payout settles.
func (s *BettingService) SetDiscordPayoutCallback(fn func(PayoutResult)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.discordPayoutCallback = fn
}

// SetDiscordRefundCallback installs the Discord hook fired after bets are refunded.
func (s *BettingService) SetDiscordRefundCallback(fn func(matchID int, refundedCount int)) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.discordRefundCallback = fn
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

// PlaceDiscordBet validates and places a bet directly for a Discord-bound player.
// Returns (betID, newBalance, err).
func (s *BettingService) PlaceDiscordBet(ctx context.Context, req models.PlaceDiscordBetRequest) (int, int, error) {
	if req.Amount <= 0 {
		return 0, 0, fmt.Errorf("bet amount must be positive, got %d: %w", req.Amount, domain.ErrInvalidAmount)
	}
	if !domain.IsValidTeam(req.TeamChosen) {
		return 0, 0, fmt.Errorf("team must be %q or %q, got %q", domain.TeamA, domain.TeamB, req.TeamChosen)
	}

	s.logger.Info("bet(discord): player %d places %d coins on %s (match #%d, insured=%v)",
		req.PlayerID, req.Amount, req.TeamChosen, req.MatchID, req.UseInsurance)

	return s.repo.PlaceDiscordBet(ctx, req)
}

// CancelDiscordBet cancels a player's live bet while the betting window is still open.
func (s *BettingService) CancelDiscordBet(ctx context.Context, matchID, playerID int) (int, bool, int, error) {
	s.logger.Info("bet(discord): player %d cancels bet on match #%d", playerID, matchID)
	return s.repo.CancelDiscordBet(ctx, matchID, playerID)
}

// GetOpenBetForPlayer returns the player's unsettled bet on matchID, or nil.
func (s *BettingService) GetOpenBetForPlayer(ctx context.Context, matchID, playerID int) (*models.Bet, error) {
	return s.repo.GetOpenBetForPlayer(ctx, matchID, playerID)
}

// SaveDiscordBetCard stores the Discord match card coordinates for pool updates & announcements.
func (s *BettingService) SaveDiscordBetCard(ctx context.Context, matchID int, channelID, messageID string) error {
	return s.repo.SaveDiscordBetCard(ctx, matchID, channelID, messageID)
}

// DiscordBetCard returns the saved Discord match card coordinates.
func (s *BettingService) DiscordBetCard(ctx context.Context, matchID int) (string, string, bool, error) {
	return s.repo.GetDiscordBetCard(ctx, matchID)
}

// GetPlayerPointsByID returns the current coin balance for an internal player ID.
func (s *BettingService) GetPlayerPointsByID(ctx context.Context, playerID int) (int, error) {
	return s.repo.GetPlayerPointsByID(ctx, playerID)
}

// ProcessPayout calculates and distributes winnings after a match finishes.
// Returns the Telegram payout summary: map of tgUserID -> points received.
func (s *BettingService) ProcessPayout(ctx context.Context, matchID int, winningTeam string) (map[int64]int, error) {
	result, err := s.settlePayout(ctx, matchID, winningTeam, true)
	if err != nil {
		return nil, err
	}
	if !result.HadBets() {
		return nil, nil
	}
	return result.Payouts, nil
}

// ProcessPayoutDetailed settles all bets for a finished match, notifies
// Telegram if bets existed, and returns the complete PayoutResult so the
// Discord betting channel can render a single combined match+betting summary.
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

	result := PayoutResult{
		MatchID:          matchID,
		WinningTeam:      winningTeam,
		Payouts:          summary.TgPayouts,
		PlayerPayouts:    summary.PlayerPayouts,
		InsuranceRefunds: summary.InsuranceRefunds,
		TotalPool:        summary.TotalPool,
		BetCount:         liveCount,
	}

	s.callbackMu.RLock()
	tgFn := s.payoutCallback
	dcFn := s.discordPayoutCallback
	s.callbackMu.RUnlock()
	if tgFn != nil {
		tgFn(result)
	}
	if fireDiscordCallback && dcFn != nil {
		dcFn(result)
	}

	return result, nil
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

// BetPool returns the live money staked on a match, split by side. It is what
// the coefficients shown to bettors are computed from.
func (s *BettingService) BetPool(ctx context.Context, matchID int) (models.BetPool, error) {
	return s.repo.BetPool(ctx, matchID)
}

// SaveBetPost records where a match's Telegram betting post lives so it can be
// edited as the pool moves and closed out when the match settles.
func (s *BettingService) SaveBetPost(ctx context.Context, matchID int, chatID, messageID int64) error {
	return s.repo.SaveBetPost(ctx, matchID, chatID, messageID)
}

// BetPost returns the chat and message of a match's betting post. ok is false
// when the match has no post — Telegram unconfigured, or the send failed.
func (s *BettingService) BetPost(ctx context.Context, matchID int) (chatID, messageID int64, ok bool, err error) {
	return s.repo.GetBetPost(ctx, matchID)
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
	tgFn := s.refundCallback
	dcFn := s.discordRefundCallback
	s.callbackMu.RUnlock()
	if tgFn != nil {
		tgFn(matchID, refunded)
	}
	if dcFn != nil {
		dcFn(matchID, refunded)
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
			return fmt.Sprintf("Ставок не было. Команда %s победила!", winningTeam)
		}
		return fmt.Sprintf("Матч завершён! Победила **%s**\n\nНа неё никто не поставил — весь банк сгорел.", winningTeam)
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
	fmt.Fprintf(&sb, "Матч завершён! Победила **%s**\n\nВыплаты победителям:\n", winningTeam)
	for _, tgID := range tgIDs {
		fmt.Fprintf(&sb, "• Пользователь `%d`: +%d очков\n", tgID, payouts[tgID])
	}
	return sb.String()
}
