package models

import (
	"testing"

	"blackwatch/internal/domain"
)

// Odds must be the multiplier settlement actually applies. calculatePayoutShares
// pays a winning bet stake * (whole pool / winning side pool); if this drifts
// from that, the channel advertises one price and the payout uses another.
func TestBetPoolOddsMatchPayoutFormula(t *testing.T) {
	pool := BetPool{MatchID: 1, AmountA: 340, CountA: 7, AmountB: 120, CountB: 3}

	if got, want := pool.Total(), 460; got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
	}

	// A 25-point bet on Team B is 25/120 of that side, so it takes 25/120 of the
	// 460-point pool.
	stake := 25
	gotPayout := int(float64(stake) * pool.Odds(domain.TeamB))
	wantPayout := stake * pool.Total() / pool.AmountB
	if gotPayout != wantPayout {
		t.Errorf("payout from Odds = %d, payout from the pool formula = %d", gotPayout, wantPayout)
	}
}

// An empty side has no coefficient. Zero is the signal for that, not a claim
// that the side pays nothing: a lone bettor on it would take the whole pool.
func TestBetPoolOddsEmptySide(t *testing.T) {
	pool := BetPool{MatchID: 1, AmountA: 100, CountA: 2}

	if got := pool.Odds(domain.TeamB); got != 0 {
		t.Errorf("Odds on an empty side = %v, want 0", got)
	}
	if got := pool.Odds(domain.TeamA); got != 1 {
		t.Errorf("Odds on the only backed side = %v, want 1 — the pool is all its own", got)
	}
}

func TestBetPoolEmpty(t *testing.T) {
	var pool BetPool

	if pool.Total() != 0 {
		t.Errorf("Total() = %d on an empty pool", pool.Total())
	}
	for _, team := range []string{domain.TeamA, domain.TeamB} {
		if pool.Odds(team) != 0 {
			t.Errorf("Odds(%s) = %v on an empty pool", team, pool.Odds(team))
		}
		if pool.Amount(team) != 0 || pool.Count(team) != 0 {
			t.Errorf("%s reports stakes on an empty pool", team)
		}
	}
}

// Amount and Count must not fold an unknown side into Team A — a caller passing
// a bad team would otherwise be handed Team A's money as if it were its own.
func TestBetPoolSidesAreDistinct(t *testing.T) {
	pool := BetPool{AmountA: 10, CountA: 1, AmountB: 90, CountB: 3}

	if pool.Amount(domain.TeamA) == pool.Amount(domain.TeamB) {
		t.Fatal("sides return the same amount")
	}
	if got, want := pool.Amount(domain.TeamB), 90; got != want {
		t.Errorf("Amount(TeamB) = %d, want %d", got, want)
	}
	if got, want := pool.Count(domain.TeamB), 3; got != want {
		t.Errorf("Count(TeamB) = %d, want %d", got, want)
	}
}
