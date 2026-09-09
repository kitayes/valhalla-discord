package domain

import (
	"math"
	"testing"
)

func TestCalculateEloShift(t *testing.T) {
	tests := []struct {
		name    string
		avgA    float64
		avgB    float64
		winner  string
		want    float64
		wantAbs bool // compare exactly
	}{
		{name: "even teams, A wins", avgA: 1000, avgB: 1000, winner: "Team A", want: 24, wantAbs: true},
		{name: "even teams, B wins", avgA: 1000, avgB: 1000, winner: "Team B", want: -24, wantAbs: true},
		{name: "unknown winner yields no shift", avgA: 1000, avgB: 1000, winner: "", want: 0, wantAbs: true},
		{name: "draw string is not a winner", avgA: 1500, avgB: 1200, winner: "Draw", want: 0, wantAbs: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateEloShift(tt.avgA, tt.avgB, tt.winner)
			if got != tt.want {
				t.Errorf("CalculateEloShift(%v, %v, %q) = %v, want %v", tt.avgA, tt.avgB, tt.winner, got, tt.want)
			}
		})
	}
}

func TestCalculateEloShiftFavouriteGainsLess(t *testing.T) {
	// A heavy favourite that wins should gain less than an underdog that wins.
	favourite := CalculateEloShift(1900, 1500, "Team A")
	underdog := CalculateEloShift(1500, 1900, "Team A")

	if favourite <= 0 {
		t.Fatalf("winning favourite should gain points, got %v", favourite)
	}
	if underdog <= favourite {
		t.Errorf("underdog win (%v) should outweigh favourite win (%v)", underdog, favourite)
	}
}

func TestCalculateEloShiftSignsFollowTeamA(t *testing.T) {
	// The shift is always expressed from Team A's point of view: positive when
	// Team A won, negative when it lost — regardless of who was favoured.
	// Callers rely on this to derive Team B's delta by negation.
	cases := []struct{ avgA, avgB float64 }{
		{avgA: 1000, avgB: 1000},
		{avgA: 1900, avgB: 1400}, // A heavily favoured
		{avgA: 1400, avgB: 1900}, // A heavy underdog
	}
	for _, c := range cases {
		if got := CalculateEloShift(c.avgA, c.avgB, "Team A"); got <= 0 {
			t.Errorf("Team A win at (%v, %v) gave %v, want a gain", c.avgA, c.avgB, got)
		}
		if got := CalculateEloShift(c.avgA, c.avgB, "Team B"); got >= 0 {
			t.Errorf("Team A loss at (%v, %v) gave %v, want a loss", c.avgA, c.avgB, got)
		}
	}
}

func TestCalculateEloShiftMirrorsAcrossTeams(t *testing.T) {
	// The same match seen from either side must move the same number of points:
	// A beating B has to cost B exactly what it earns A.
	aBeatsB := CalculateEloShift(1600, 1400, "Team A")
	bLosesToA := CalculateEloShift(1400, 1600, "Team B")

	if math.Abs(aBeatsB+bLosesToA) > 1 {
		t.Errorf("MMR is not conserved: winner +%v, loser %v", aBeatsB, bLosesToA)
	}
}

func TestSelectKFactor(t *testing.T) {
	tests := []struct {
		mmr  float64
		want float64
	}{
		{mmr: 0, want: KFactorHigh},
		{mmr: 1799, want: KFactorHigh},
		{mmr: 1800, want: KFactorBase},
		{mmr: 2199, want: KFactorBase},
		{mmr: 2200, want: KFactorLow},
		{mmr: 5000, want: KFactorLow},
	}
	for _, tt := range tests {
		if got := selectKFactor(tt.mmr); got != tt.want {
			t.Errorf("selectKFactor(%v) = %v, want %v", tt.mmr, got, tt.want)
		}
	}
}

func TestCalculateAverageMMR(t *testing.T) {
	if got := CalculateAverageMMR(nil); got != BaseMMR {
		t.Errorf("empty slice should fall back to BaseMMR, got %v", got)
	}
	if got := CalculateAverageMMR([]int{1000, 2000}); got != 1500 {
		t.Errorf("CalculateAverageMMR([1000 2000]) = %v, want 1500", got)
	}
	if got := CalculateAverageMMR([]int{1000, 1001}); got != 1000.5 {
		t.Errorf("average must not truncate, got %v", got)
	}
}

func TestClampMMR(t *testing.T) {
	tests := []struct{ in, want int }{
		{in: -100, want: 0},
		{in: 0, want: 0},
		{in: 1234, want: 1234},
		{in: 5000, want: 5000},
		{in: 9999, want: 5000},
	}
	for _, tt := range tests {
		if got := ClampMMR(tt.in); got != tt.want {
			t.Errorf("ClampMMR(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
