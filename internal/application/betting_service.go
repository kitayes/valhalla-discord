package application

import (
	"fmt"

	"blackwatch/internal/models"
)

// BettingRepository is the subset of repository needed by BettingService.
type BettingRepository interface {
	PlaceBet(req models.PlaceBetRequest) error
	GetBetsByMatch(matchID int) ([]models.Bet, error)
	PayoutWinners(matchID int, winningTeam string) (map[int64]int, error)
	GetPlayerPoints(tgUserID int64) (int, error)
}

// BettingService handles the Telegram betting economy.
type BettingService struct {
	logger Logger
	repo   BettingRepository
}

// NewBettingService creates a new BettingService.
func NewBettingService(logger Logger, repo BettingRepository) *BettingService {
	return &BettingService{
		logger: logger,
		repo:   repo,
	}
}

// PlaceBet validates and places a bet for a Telegram user.
func (s *BettingService) PlaceBet(req models.PlaceBetRequest) error {
	if req.Amount <= 0 {
		return fmt.Errorf("bet amount must be positive, got %d", req.Amount)
	}

	if req.TeamChosen != "Team A" && req.TeamChosen != "Team B" {
		return fmt.Errorf("team must be 'Team A' or 'Team B', got '%s'", req.TeamChosen)
	}

	s.logger.Info("bet: user %d places %d points on %s (match #%d)",
		req.TgUserID, req.Amount, req.TeamChosen, req.MatchID)

	return s.repo.PlaceBet(req)
}

// ProcessPayout calculates and distributes winnings after a match finishes.
// Returns the payout summary: map of tgUserID -> points received.
func (s *BettingService) ProcessPayout(matchID int, winningTeam string) (map[int64]int, error) {
	bets, err := s.repo.GetBetsByMatch(matchID)
	if err != nil {
		return nil, fmt.Errorf("betting: failed to get bets for match #%d: %w", matchID, err)
	}

	if len(bets) == 0 {
		s.logger.Info("betting: no bets for match #%d — skipping payout", matchID)
		return nil, nil
	}

	totalPool := 0
	winningPool := 0
	var winners []models.Bet
	var losers []models.Bet

	for _, b := range bets {
		totalPool += b.Amount
		if b.TeamChosen == winningTeam {
			winningPool += b.Amount
			winners = append(winners, b)
		} else {
			losers = append(losers, b)
		}
	}

	s.logger.Info("betting: match #%d | %d bets | total pool: %d | winning pool: %d | %d winners, %d losers",
		matchID, len(bets), totalPool, winningPool, len(winners), len(losers))

	payouts, err := s.repo.PayoutWinners(matchID, winningTeam)
	if err != nil {
		return nil, fmt.Errorf("betting: payout failed: %w", err)
	}

	return payouts, nil
}

// GetPlayerPoints returns the current points balance for a Telegram user.
func (s *BettingService) GetPlayerPoints(tgUserID int64) (int, error) {
	return s.repo.GetPlayerPoints(tgUserID)
}

// FormatPayoutSummary returns a human-readable summary of payouts.
func FormatPayoutSummary(payouts map[int64]int, winningTeam string) string {
	if len(payouts) == 0 {
		return fmt.Sprintf("🏁 Ставок не было. Команда %s победила!", winningTeam)
	}

	summary := fmt.Sprintf("🏁 Матч завершён! Победила **%s**\n\nВыплаты победителям:\n", winningTeam)
	for tgID, amount := range payouts {
		summary += fmt.Sprintf("• Пользователь `%d`: +%d 🔮 очков\n", tgID, amount)
	}
	return summary
}
