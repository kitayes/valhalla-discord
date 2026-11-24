package application

import (
	"testing"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
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

func TestComputeMatchBreakdownsWithKDAAndMedals(t *testing.T) {
	match := &models.LobbyMatch{
		ID:       42,
		TeamAIDs: []int{1, 2, 3, 4, 5},
		TeamBIDs: []int{6, 7, 8, 9, 10},
	}
	mmrs := map[int]int{
		1: 1000, 2: 1000, 3: 1000, 4: 1000, 5: 1000,
		6: 1000, 7: 1000, 8: 1000, 9: 1000, 10: 1000,
	}
	names := map[int]string{
		1: "CarryA", 2: "MidA", 3: "TankA", 4: "SaverA", 5: "ExpA",
		6: "MidB", 7: "TankB", 8: "JungB", 9: "ExpB", 10: "FeedB",
	}
	kda := map[int]domain.PlayerKDA{
		1:  {PlayerID: 1, Kills: 11, Deaths: 2, Assists: 7},
		2:  {PlayerID: 2, Kills: 4, Deaths: 2, Assists: 10},
		3:  {PlayerID: 3, Kills: 1, Deaths: 5, Assists: 17},
		4:  {PlayerID: 4, Kills: 2, Deaths: 0, Assists: 3},
		5:  {PlayerID: 5, Kills: 2, Deaths: 6, Assists: 3},
		6:  {PlayerID: 6, Kills: 7, Deaths: 2, Assists: 5},
		7:  {PlayerID: 7, Kills: 1, Deaths: 5, Assists: 9},
		8:  {PlayerID: 8, Kills: 4, Deaths: 6, Assists: 4},
		9:  {PlayerID: 9, Kills: 2, Deaths: 5, Assists: 4},
		10: {PlayerID: 10, Kills: 1, Deaths: 7, Assists: 1},
	}

	bds, _, _, _ := computeMatchBreakdowns(match, mmrs, names, domain.TeamA, 1, 6, "", "", kda, nil)

	if bds[1].MedalBonus != domain.MVPBonusMMR {
		t.Errorf("MVP bonus = %d, want %d", bds[1].MedalBonus, domain.MVPBonusMMR)
	}
	if bds[6].MedalBonus != domain.SVPMitigationMMR {
		t.Errorf("SVP bonus = %d, want %d", bds[6].MedalBonus, domain.SVPMitigationMMR)
	}
	if bds[1].TotalDelta <= bds[5].TotalDelta {
		t.Errorf("MVP carry (%+d) should out-earn feeder (%+d)", bds[1].TotalDelta, bds[5].TotalDelta)
	}
	if bds[6].TotalDelta <= bds[10].TotalDelta {
		t.Errorf("SVP loser (%+d) should lose less than feeder (%+d)", bds[6].TotalDelta, bds[10].TotalDelta)
	}
	for _, pid := range match.TeamAIDs {
		if bds[pid].TotalDelta < domain.MinWinMMRDelta {
			t.Errorf("winner %d delta = %d < %d", pid, bds[pid].TotalDelta, domain.MinWinMMRDelta)
		}
	}
	for _, pid := range match.TeamBIDs {
		if bds[pid].TotalDelta > domain.MaxLossMMRDelta {
			t.Errorf("loser %d delta = %d > %d", pid, bds[pid].TotalDelta, domain.MaxLossMMRDelta)
		}
	}
}

func TestComputeMatchBreakdownsWithoutKDAAndNameMedals(t *testing.T) {
	match := &models.LobbyMatch{
		ID:       99,
		TeamAIDs: []int{1, 2},
		TeamBIDs: []int{3, 4},
	}
	mmrs := map[int]int{1: 1000, 2: 1000, 3: 1000, 4: 1000}
	names := map[int]string{1: "Alpha", 2: "Bravo", 3: "Charlie", 4: "Delta"}

	bds, _, _, _ := computeMatchBreakdowns(match, mmrs, names, domain.TeamB, 0, 0, "Charlie", "Alpha", nil, nil)
	if bds[3].MedalBonus != domain.MVPBonusMMR {
		t.Errorf("Team B MVP by name got %d, want %d", bds[3].MedalBonus, domain.MVPBonusMMR)
	}
	if bds[1].MedalBonus != domain.SVPMitigationMMR {
		t.Errorf("Team A SVP by name got %d, want %d", bds[1].MedalBonus, domain.SVPMitigationMMR)
	}
	if bds[2].KDAMod != 0 || bds[4].KDAMod != 0 {
		t.Errorf("without KDA input, KDAMod should be 0, got %+v", bds)
	}
}

func TestComputeMatchBreakdownsSignGuardOnUnderdogLoss(t *testing.T) {
	// High-rated underdog Team B loses a lopsided match in Obsidian (base -1) with SVP (+4) and +4 KDA mod.
	match := &models.LobbyMatch{
		ID:       101,
		TeamAIDs: []int{1, 2},
		TeamBIDs: []int{3, 4},
	}
	mmrs := map[int]int{1: 2800, 2: 2800, 3: 2200, 4: 2200}
	names := map[int]string{1: "A1", 2: "A2", 3: "B1", 4: "B2"}
	kda := map[int]domain.PlayerKDA{
		1: {PlayerID: 1, Kills: 10, Deaths: 1, Assists: 5},
		2: {PlayerID: 2, Kills: 1, Deaths: 8, Assists: 2},
		3: {PlayerID: 3, Kills: 9, Deaths: 1, Assists: 4},
		4: {PlayerID: 4, Kills: 0, Deaths: 10, Assists: 0},
	}

	bds, _, _, _ := computeMatchBreakdowns(match, mmrs, names, domain.TeamA, 1, 3, "", "", kda, nil)
	if bds[2].TotalDelta < domain.MinWinMMRDelta {
		t.Errorf("Team A feeder win delta = %d, want >= %d", bds[2].TotalDelta, domain.MinWinMMRDelta)
	}
	if bds[3].TotalDelta > domain.MaxLossMMRDelta {
		t.Errorf("Team B SVP underdog loss delta = %d, want <= %d", bds[3].TotalDelta, domain.MaxLossMMRDelta)
	}
}
