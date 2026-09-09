package repository

import (
	"testing"

	"blackwatch/internal/models"
)

func bet(id int, user int64, team string, amount int) models.Bet {
	return models.Bet{ID: id, TgUserID: user, TeamChosen: team, Amount: amount}
}

func sumShares(shares map[int]int) int {
	total := 0
	for _, v := range shares {
		total += v
	}
	return total
}

func TestCalculatePayoutSharesConservesPool(t *testing.T) {
	// 3:7 split of a 100 point pool does not divide evenly; the remainder must
	// still be handed out so that points are neither created nor destroyed.
	bets := []models.Bet{
		bet(1, 100, "Team A", 30),
		bet(2, 200, "Team A", 40),
		bet(3, 300, "Team B", 30),
	}

	shares := calculatePayoutShares(bets, "Team A")

	if got, want := sumShares(shares), 100; got != want {
		t.Errorf("payouts sum to %d, want the whole pool %d", got, want)
	}
	if shares[3] != 0 {
		t.Errorf("losing bet was paid %d", shares[3])
	}
	if shares[2] <= shares[1] {
		t.Errorf("bigger stake %d should not pay less than smaller stake %d", shares[2], shares[1])
	}
}

func TestCalculatePayoutSharesEveryoneOnWinner(t *testing.T) {
	// Nobody to take money from — each bettor gets exactly their stake back.
	bets := []models.Bet{
		bet(1, 100, "Team A", 25),
		bet(2, 200, "Team A", 75),
	}

	shares := calculatePayoutShares(bets, "Team A")

	if shares[1] != 25 || shares[2] != 75 {
		t.Errorf("expected stakes returned unchanged, got %v", shares)
	}
}

func TestCalculatePayoutSharesNoWinners(t *testing.T) {
	// Everyone backed the losing side: the pool is consumed, nothing is paid.
	bets := []models.Bet{
		bet(1, 100, "Team B", 10),
		bet(2, 200, "Team B", 20),
	}

	shares := calculatePayoutShares(bets, "Team A")

	if len(shares) != 0 {
		t.Errorf("expected no payouts, got %v", shares)
	}
}

func TestCalculatePayoutSharesNoBets(t *testing.T) {
	if shares := calculatePayoutShares(nil, "Team A"); len(shares) != 0 {
		t.Errorf("expected no payouts for an empty match, got %v", shares)
	}
}

func TestCalculatePayoutSharesIsDeterministic(t *testing.T) {
	// Equal stakes leave a remainder that must go to a stable, lowest-ID
	// bettor rather than to whoever the map iteration happens to visit first.
	bets := []models.Bet{
		bet(1, 100, "Team A", 10),
		bet(2, 200, "Team A", 10),
		bet(3, 300, "Team A", 10),
		bet(4, 400, "Team B", 1),
	}

	first := calculatePayoutShares(bets, "Team A")
	for i := 0; i < 20; i++ {
		next := calculatePayoutShares(bets, "Team A")
		for id, share := range first {
			if next[id] != share {
				t.Fatalf("payout for bet %d flipped between runs: %d vs %d", id, share, next[id])
			}
		}
	}
	if got, want := sumShares(first), 31; got != want {
		t.Errorf("payouts sum to %d, want %d", got, want)
	}
}

func TestCalculatePayoutSharesWinnerBeatsStake(t *testing.T) {
	// A winning bet against a larger losing pool must return more than it staked.
	bets := []models.Bet{
		bet(1, 100, "Team A", 10),
		bet(2, 200, "Team B", 90),
	}

	shares := calculatePayoutShares(bets, "Team A")

	if shares[1] != 100 {
		t.Errorf("lone winner should take the whole 100 point pool, got %d", shares[1])
	}
}
