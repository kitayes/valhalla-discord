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

func TestCalculateTeamKDAModifiersWinningRosterOrderingAndZeroSum(t *testing.T) {
	// Winning team with 25 total kills (11 + 4 + 1 + 2 + 2 = 20 -> wait, let's check:
	// 11+4+1+2+7 = 25 kills)
	team := []PlayerKDA{
		{PlayerID: 1, Kills: 11, Deaths: 2, Assists: 7}, // carry (MVP)
		{PlayerID: 2, Kills: 4, Deaths: 2, Assists: 10}, // mid
		{PlayerID: 3, Kills: 1, Deaths: 5, Assists: 17}, // roam tank: high KP
		{PlayerID: 4, Kills: 2, Deaths: 0, Assists: 3},  // passive KDA saver
		{PlayerID: 5, Kills: 7, Deaths: 9, Assists: 2},  // lower efficiency / higher deaths
	}
	mods := CalculateTeamKDAModifiers(team, 24)

	sum := 0
	for _, p := range team {
		sum += mods[p.PlayerID]
	}
	if sum != 0 {
		t.Fatalf("sum of KDA modifiers = %d, want 0 (zero-sum): %v", sum, mods)
	}
	if mods[1] <= mods[2] {
		t.Errorf("carry (%d) should outscore mid (%d)", mods[1], mods[2])
	}
	if mods[3] <= mods[4] {
		t.Errorf("roam tank 1/5/17 (%d) should outscore passive 2/0/3 (%d)", mods[3], mods[4])
	}

	// Specifically check passive KDA saver 2/0/3 vs roam tank 1/5/17 with exact team kills = 25
	teamSaver := []PlayerKDA{
		{PlayerID: 10, Kills: 11, Deaths: 2, Assists: 7},
		{PlayerID: 11, Kills: 4, Deaths: 2, Assists: 10},
		{PlayerID: 12, Kills: 1, Deaths: 5, Assists: 17},
		{PlayerID: 13, Kills: 2, Deaths: 0, Assists: 3},
		{PlayerID: 14, Kills: 7, Deaths: 6, Assists: 3},
	}
	modsSaver := CalculateTeamKDAModifiers(teamSaver, 24)
	if modsSaver[12] < modsSaver[13] {
		t.Errorf("roam tank 1/5/17 (%d) should not fall behind passive 2/0/3 (%d)", modsSaver[12], modsSaver[13])
	}
}

func TestCalculateTeamKDAModifiersLosingRosterAndZeroSum(t *testing.T) {
	team := []PlayerKDA{
		{PlayerID: 1, Kills: 7, Deaths: 2, Assists: 5},
		{PlayerID: 2, Kills: 1, Deaths: 5, Assists: 9},
		{PlayerID: 3, Kills: 4, Deaths: 6, Assists: 4},
		{PlayerID: 4, Kills: 2, Deaths: 5, Assists: 4},
		{PlayerID: 5, Kills: 1, Deaths: 7, Assists: 1},
	}
	mods := CalculateTeamKDAModifiers(team, -24)

	sum := 0
	for _, p := range team {
		sum += mods[p.PlayerID]
	}
	if sum != 0 {
		t.Fatalf("sum of losing KDA modifiers = %d, want 0: %v", sum, mods)
	}
	if !(mods[1] > mods[2] && mods[2] >= mods[3] && mods[3] >= mods[4] && mods[4] > mods[5]) {
		t.Errorf("unexpected performance ordering on losing team: %v", mods)
	}
}

func TestCalculateTeamKDAModifiersIdenticalStatsYieldZero(t *testing.T) {
	team := []PlayerKDA{
		{PlayerID: 1, Kills: 3, Deaths: 3, Assists: 5},
		{PlayerID: 2, Kills: 3, Deaths: 3, Assists: 5},
		{PlayerID: 3, Kills: 3, Deaths: 3, Assists: 5},
		{PlayerID: 4, Kills: 3, Deaths: 3, Assists: 5},
		{PlayerID: 5, Kills: 3, Deaths: 3, Assists: 5},
	}
	mods := CalculateTeamKDAModifiers(team, 24)
	for _, p := range team {
		if mods[p.PlayerID] != 0 {
			t.Errorf("identical stats should give 0 modifier, player %d got %d", p.PlayerID, mods[p.PlayerID])
		}
	}
}

func TestCalculateTeamKDAModifiersZeroKillSurrender(t *testing.T) {
	team := []PlayerKDA{
		{PlayerID: 1, Kills: 0, Deaths: 1, Assists: 0},
		{PlayerID: 2, Kills: 0, Deaths: 2, Assists: 0},
		{PlayerID: 3, Kills: 0, Deaths: 3, Assists: 0},
		{PlayerID: 4, Kills: 0, Deaths: 4, Assists: 0},
		{PlayerID: 5, Kills: 0, Deaths: 8, Assists: 0},
	}
	mods := CalculateTeamKDAModifiers(team, -24)
	sum := 0
	for _, p := range team {
		sum += mods[p.PlayerID]
	}
	if sum != 0 {
		t.Fatalf("zero-kill surrender sum = %d, want 0: %v", sum, mods)
	}
	if mods[1] <= mods[5] {
		t.Errorf("0/1/0 (%d) should outscore 0/8/0 (%d)", mods[1], mods[5])
	}
	if mods[1] > 3 || mods[5] < -3 {
		t.Errorf("zero-kill surrender diffs should stay modest due to scale floor: %v", mods)
	}
}

func TestCalculateTeamKDAModifiersTierScaling(t *testing.T) {
	team := []PlayerKDA{
		{PlayerID: 1, Kills: 15, Deaths: 0, Assists: 10},
		{PlayerID: 2, Kills: 5, Deaths: 3, Assists: 5},
		{PlayerID: 3, Kills: 3, Deaths: 4, Assists: 6},
		{PlayerID: 4, Kills: 2, Deaths: 5, Assists: 4},
		{PlayerID: 5, Kills: 0, Deaths: 10, Assists: 0},
	}

	cases := []struct {
		name      string
		baseDelta int
		wantCap   int
	}{
		{name: "graphite/carbon ±24", baseDelta: 24, wantCap: 8},
		{name: "onyx ±16", baseDelta: 16, wantCap: 6},
		{name: "obsidian ±12", baseDelta: 12, wantCap: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mods := CalculateTeamKDAModifiers(team, tc.baseDelta)
			if mods[1] != tc.wantCap {
				t.Errorf("carry mod = %d, want exact cap %d (mods=%v)", mods[1], tc.wantCap, mods)
			}
			for _, p := range team {
				if mods[p.PlayerID] > tc.wantCap || mods[p.PlayerID] < -tc.wantCap {
					t.Errorf("player %d mod %d exceeds cap ±%d", p.PlayerID, mods[p.PlayerID], tc.wantCap)
				}
			}
			if mods[5] >= 0 || mods[5] >= mods[4] {
				t.Errorf("feeder mod = %d should be strictly worst (mods=%v)", mods[5], mods)
			}
			sum := 0
			for _, p := range team {
				sum += mods[p.PlayerID]
			}
			if sum != 0 {
				t.Errorf("sum = %d, want 0", sum)
			}
		})
	}
}

func TestCalculateTeamKDAModifiersEdgeCases(t *testing.T) {
	if got := CalculateTeamKDAModifiers(nil, 24); len(got) != 0 {
		t.Errorf("empty team = %v, want empty map", got)
	}
	if got := CalculateTeamKDAModifiers([]PlayerKDA{{PlayerID: 1, Kills: 5}}, 0); len(got) != 0 {
		t.Errorf("zero baseDelta = %v, want empty map", got)
	}
}

func TestApplyMatchDeltaSignGuards(t *testing.T) {
	// Underdog Obsidian SVP loss: base -4, KDA +4, SVP +4 = +4 -> must clamp to -3.
	if got := ApplyMatchDelta(-4, 4, SVPMitigationMMR, false); got != MaxLossMMRDelta {
		t.Errorf("underdog SVP loss = %d, want %d", got, MaxLossMMRDelta)
	}
	// Favorite feeder win: base +6, KDA -4 = +2 -> must clamp to +3.
	if got := ApplyMatchDelta(6, -4, 0, true); got != MinWinMMRDelta {
		t.Errorf("favorite feeder win = %d, want %d", got, MinWinMMRDelta)
	}
	// Normal win & loss stay unclamped.
	if got := ApplyMatchDelta(24, 5, MVPBonusMMR, true); got != 32 {
		t.Errorf("MVP win = %d, want 32", got)
	}
	if got := ApplyMatchDelta(-24, 8, SVPMitigationMMR, false); got != -12 {
		t.Errorf("SVP loss = %d, want -12", got)
	}
}
