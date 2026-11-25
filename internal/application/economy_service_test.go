package application

import (
	"context"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
)

type fakeEconomyRepo struct {
	repository.Economy
	grants   map[string]int
	balances map[int]int
	xp       map[int]int64
}

func (f *fakeEconomyRepo) GrantCoins(_ context.Context, playerID, amount int, reason, refKey, _ string) (int, int, bool, error) {
	if f.grants == nil {
		f.grants = make(map[string]int)
	}
	if f.balances == nil {
		f.balances = make(map[int]int)
	}
	key := reason + "|" + refKey + "|" + string(rune(playerID))
	if refKey != "" {
		if _, dup := f.grants[key]; dup {
			return 0, f.balances[playerID], false, nil
		}
		f.grants[key] = amount
	}
	f.balances[playerID] += amount
	return amount, f.balances[playerID], true, nil
}

func (f *fakeEconomyRepo) GrantXP(_ context.Context, playerID int, xpDelta, _, _ int64) (*models.XPGrantResult, error) {
	if f.xp == nil {
		f.xp = make(map[int]int64)
	}
	f.xp[playerID] += xpDelta
	return &models.XPGrantResult{
		PlayerID:   playerID,
		XPGained:   xpDelta,
		NewXP:      f.xp[playerID],
		NewBalance: f.balances[playerID],
	}, nil
}

type fakeEconomyLobbyRepo struct {
	LobbyMatchRepository
	match *models.LobbyMatch
}

func (f *fakeEconomyLobbyRepo) GetByID(_ context.Context, _ int) (*models.LobbyMatch, error) {
	return f.match, nil
}

type fakeEconomyMatchSvc struct {
	MatchService
	names       map[int]string
	invalidated bool
}

func (f *fakeEconomyMatchSvc) GetPlayerNamesByIDs(_ context.Context, _ []int) (map[int]string, error) {
	return f.names, nil
}

func (f *fakeEconomyMatchSvc) InvalidateStatsCache() {
	f.invalidated = true
}

func (f *fakeEconomyRepo) RevertFinishedMatch(_ context.Context, matchID int) (*models.MatchRevertSummary, error) {
	return &models.MatchRevertSummary{MatchID: matchID, OldWinner: domain.TeamA}, nil
}

func TestRevertFinishedMatchInvalidatesStatsCache(t *testing.T) {
	repo := &fakeEconomyRepo{}
	matchSvc := &fakeEconomyMatchSvc{}
	svc := NewEconomyService(nopLogger{}, repo, nil, matchSvc)

	if _, err := svc.RevertFinishedMatch(context.Background(), 7); err != nil {
		t.Fatalf("RevertFinishedMatch: %v", err)
	}
	if !matchSvc.invalidated {
		t.Fatal("expected RevertFinishedMatch to invalidate statsCache")
	}
}

func TestAwardMatchCoinsWinLossMVPSVPAndIdempotency(t *testing.T) {
	repo := &fakeEconomyRepo{}
	lobbyRepo := &fakeEconomyLobbyRepo{
		match: &models.LobbyMatch{
			ID:        42,
			TeamAIDs:  []int{1, 2},
			TeamBIDs:  []int{3, 4},
			CreatedAt: time.Now(),
		},
	}
	matchSvc := &fakeEconomyMatchSvc{
		names: map[int]string{1: "Alpha", 2: "Bravo", 3: "Charlie", 4: "Delta"},
	}

	svc := NewEconomyService(nopLogger{}, repo, lobbyRepo, matchSvc)

	rewards, err := svc.AwardMatchCoins(context.Background(), 42, domain.TeamA, 1, 3, "Alpha", "Charlie")
	if err != nil {
		t.Fatalf("AwardMatchCoins: %v", err)
	}
	if len(rewards) != 4 {
		t.Fatalf("len(rewards) = %d, want 4", len(rewards))
	}

	wantCoins := map[int]int{
		1: domain.MatchWinCoins + domain.MatchMVPCoins,  // 40 + 25 = 65
		2: domain.MatchWinCoins,                         // 40
		3: domain.MatchLossCoins + domain.MatchSVPCoins, // 15 + 15 = 30
		4: domain.MatchLossCoins,                        // 15
	}
	wantXP := map[int]int64{
		1: domain.MatchWinXP + domain.MatchMVPXP,  // 60 + 25 = 85
		2: domain.MatchWinXP,                      // 60
		3: domain.MatchLossXP + domain.MatchSVPXP, // 40 + 20 = 60
		4: domain.MatchLossXP,                     // 40
	}
	for pid, exp := range wantCoins {
		if repo.balances[pid] != exp {
			t.Errorf("player %d balance = %d, want %d", pid, repo.balances[pid], exp)
		}
		if repo.xp[pid] != wantXP[pid] {
			t.Errorf("player %d xp = %d, want %d", pid, repo.xp[pid], wantXP[pid])
		}
	}

	// Second call for same match must be a complete no-op for BOTH coins and XP.
	rewards2, err := svc.AwardMatchCoins(context.Background(), 42, domain.TeamA, 1, 3, "Alpha", "Charlie")
	if err != nil {
		t.Fatalf("AwardMatchCoins replay: %v", err)
	}
	for _, r := range rewards2 {
		if !r.Skipped || r.Coins != 0 || r.XPGained != 0 {
			t.Errorf("replay should be skipped with 0 coins/XP, got %+v", r)
		}
	}
	for pid, exp := range wantCoins {
		if repo.balances[pid] != exp {
			t.Errorf("after replay player %d balance = %d, want %d", pid, repo.balances[pid], exp)
		}
		if repo.xp[pid] != wantXP[pid] {
			t.Errorf("after replay player %d xp = %d, want %d", pid, repo.xp[pid], wantXP[pid])
		}
	}
}
