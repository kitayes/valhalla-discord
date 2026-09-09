package domain

import (
	"strings"
	"testing"
)

func TestDetermineTier(t *testing.T) {
	tests := []struct {
		mmr  int
		want Tier
	}{
		{mmr: 0, want: TierUnranked},
		{mmr: 999, want: TierUnranked},
		{mmr: 1000, want: TierGraphite},
		{mmr: 1399, want: TierGraphite},
		{mmr: 1400, want: TierCarbon},
		{mmr: 1799, want: TierCarbon},
		{mmr: 1800, want: TierOnyx},
		{mmr: 2199, want: TierOnyx},
		{mmr: 2200, want: TierObsidian},
		{mmr: 9999, want: TierObsidian},
	}
	for _, tt := range tests {
		if got := DetermineTier(tt.mmr); got != tt.want {
			t.Errorf("DetermineTier(%d) = %q, want %q", tt.mmr, got, tt.want)
		}
	}
}

func TestDetermineTierBoundariesMatchKFactor(t *testing.T) {
	// The tier ladder and the Elo K-factor ladder share their 1800/2200 cuts.
	// If one moves without the other, ranks and rating volatility drift apart.
	if DetermineTier(1800) != TierOnyx || selectKFactor(1800) != KFactorBase {
		t.Error("1800 boundary diverged between tiers and K-factor")
	}
	if DetermineTier(2200) != TierObsidian || selectKFactor(2200) != KFactorLow {
		t.Error("2200 boundary diverged between tiers and K-factor")
	}
}

func TestFormatTierDisplayCoversEveryTier(t *testing.T) {
	for _, tier := range []Tier{TierObsidian, TierOnyx, TierCarbon, TierGraphite, TierUnranked} {
		got := FormatTierDisplay(tier)
		if got == "" {
			t.Errorf("FormatTierDisplay(%q) returned empty string", tier)
		}
		if tier != TierUnranked && !strings.Contains(got, string(tier)) {
			t.Errorf("FormatTierDisplay(%q) = %q, expected it to name the tier", tier, got)
		}
	}
}

func TestFormatTierWithMMR(t *testing.T) {
	got := FormatTierWithMMR(2500)
	if !strings.Contains(got, "OBSIDIAN") || !strings.Contains(got, "2500") {
		t.Errorf("FormatTierWithMMR(2500) = %q, want tier name and MMR", got)
	}
}
