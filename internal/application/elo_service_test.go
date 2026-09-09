package application

import (
	"testing"

	"blackwatch/internal/domain"
)

func TestTeamDeltasAreOppositeAndSum(t *testing.T) {
	for _, delta := range []float64{-48, -12, 0, 7, 36} {
		a, b := teamDeltas(delta)
		if a != -b {
			t.Errorf("teamDeltas(%v) = (%d, %d): teams must move by opposite amounts", delta, a, b)
		}
		if a+b != 0 {
			t.Errorf("teamDeltas(%v) creates %d MMR out of nothing", delta, a+b)
		}
	}
}

func TestTeamDeltasRewardTheWinningSide(t *testing.T) {
	// Regression: the deltas used to be swapped when Team B won, which handed
	// the MMR gain to Team A — the team that had just lost.
	tests := []struct {
		name    string
		avgA    float64
		avgB    float64
		winner  string
		wantAUp bool
	}{
		{name: "Team A wins even match", avgA: 1500, avgB: 1500, winner: "Team A", wantAUp: true},
		{name: "Team B wins even match", avgA: 1500, avgB: 1500, winner: "Team B", wantAUp: false},
		{name: "favoured Team A loses", avgA: 1900, avgB: 1300, winner: "Team B", wantAUp: false},
		{name: "underdog Team A wins", avgA: 1300, avgB: 1900, winner: "Team A", wantAUp: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shift := domain.CalculateEloShift(tt.avgA, tt.avgB, tt.winner)
			deltaA, deltaB := teamDeltas(shift)

			if tt.wantAUp && (deltaA <= 0 || deltaB >= 0) {
				t.Errorf("Team A won but got %+d while Team B got %+d", deltaA, deltaB)
			}
			if !tt.wantAUp && (deltaA >= 0 || deltaB <= 0) {
				t.Errorf("Team B won but Team A got %+d while Team B got %+d", deltaA, deltaB)
			}
		})
	}
}
