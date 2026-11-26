//go:build integration

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

func TestIntegrationQuestsProgressClaimAndIdempotency(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	qs := NewQuestSeasonPostgres(db)

	playerID := seedPlayer(t, db, matchRepo, 100)
	periodKey := "daily:2099-01-01"
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM player_quest_progress WHERE player_id = $1`, playerID)
		_, _ = db.Exec(`DELETE FROM currency_ledger WHERE player_id = $1`, playerID)
	})

	// 1. Increment progress incrementally across two calls.
	if err := qs.IncrementQuestProgress(ctx, playerID, periodKey, []string{"daily_play_2"}, 1); err != nil {
		t.Fatalf("IncrementQuestProgress #1: %v", err)
	}
	if err := qs.IncrementQuestProgress(ctx, playerID, periodKey, []string{"daily_play_2", "daily_chat_30"}, 1); err != nil {
		t.Fatalf("IncrementQuestProgress #2: %v", err)
	}

	rows, err := qs.GetQuestProgress(ctx, playerID, []string{periodKey})
	if err != nil {
		t.Fatalf("GetQuestProgress: %v", err)
	}
	got := make(map[string]int, len(rows))
	for _, r := range rows {
		got[r.QuestCode] = r.Progress
	}
	if got["daily_play_2"] != 2 || got["daily_chat_30"] != 1 {
		t.Fatalf("progress = %+v, want daily_play_2=2 daily_chat_30=1", got)
	}

	// 2. Claim completed quest + all-clear bonus; incomplete quest must be skipped cleanly.
	claims := []models.QuestClaimSpec{
		{PeriodKey: periodKey, QuestCode: "daily_play_2", Title: "Квест: Разминка", Target: 2, RewardCoins: 40, RewardXP: 160},
		{PeriodKey: periodKey, QuestCode: "daily_chat_30", Title: "Квест: Чат", Target: 30, RewardCoins: 30, RewardXP: 45},
		{PeriodKey: periodKey, QuestCode: domain.QuestBonusDailyAllCode, Title: "Бонус: все дейлики (3/3)", IsBonus: true, RewardCoins: 35, RewardXP: 50},
	}
	res, err := qs.ClaimQuestRewards(ctx, playerID, claims)
	if err != nil {
		t.Fatalf("ClaimQuestRewards: %v", err)
	}
	if len(res.ClaimedTitles) != 2 {
		t.Fatalf("claimed %d items (%v), want 2 (daily_play_2 + bonus)", len(res.ClaimedTitles), res.ClaimedTitles)
	}
	if res.TotalCoins != 75 || res.TotalXP != 210 {
		t.Errorf("totals = (%d coins, %d XP), want (75, 210)", res.TotalCoins, res.TotalXP)
	}
	// 210 XP crosses level 1 -> 2 (150 XP threshold), so level-up bonus must fire!
	if res.OldLevel != 1 || res.NewLevel != 2 || res.LevelUpCoins <= 0 {
		t.Errorf("level-up = (%d -> %d, bonus=%d), want 1 -> 2 with positive bonus", res.OldLevel, res.NewLevel, res.LevelUpCoins)
	}
	wantBal := 100 + 75 + res.LevelUpCoins
	if res.NewBalance != wantBal {
		t.Errorf("NewBalance = %d, want %d", res.NewBalance, wantBal)
	}

	// 3. Replaying the exact same claim must return ErrNothingToClaim and not double-pay.
	if _, err := qs.ClaimQuestRewards(ctx, playerID, claims); !errors.Is(err, domain.ErrNothingToClaim) {
		t.Errorf("replay ClaimQuestRewards = %v, want ErrNothingToClaim", err)
	}
}

func TestIntegrationSeasonCloseStandingsTrophiesAndFullMMRReset(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobbyRepo := NewLobbyMatchPostgres(db)
	qs := NewQuestSeasonPostgres(db)

	// Isolate seasons state and restore afterward so repeated runs stay clean.
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM season_standings`)
		_, _ = db.Exec(`DELETE FROM seasons WHERE number > 1`)
		_, _ = db.Exec(`UPDATE seasons SET status = 'ACTIVE', ended_at = NULL, matches_played = 0, closed_by = NULL, name = 'Сезон 1' WHERE number = 1`)
	})

	p1 := seedPlayer(t, db, matchRepo, 100)
	p2 := seedPlayer(t, db, matchRepo, 100)
	p3 := seedPlayer(t, db, matchRepo, 100)
	p1Name, _ := matchRepo.GetPlayerNameByID(ctx, p1)
	p2Name, _ := matchRepo.GetPlayerNameByID(ctx, p2)
	p3Name, _ := matchRepo.GetPlayerNameByID(ctx, p3)

	// Give distinct MMRs above/below 1000.
	if _, err := db.Exec(`UPDATE players SET current_mmr = 2250 WHERE id = $1`, p1); err != nil {
		t.Fatalf("set mmr p1: %v", err)
	}
	if _, err := db.Exec(`UPDATE players SET current_mmr = 1850 WHERE id = $1`, p2); err != nil {
		t.Fatalf("set mmr p2: %v", err)
	}
	if _, err := db.Exec(`UPDATE players SET current_mmr = 900 WHERE id = $1`, p3); err != nil {
		t.Fatalf("set mmr p3: %v", err)
	}

	// Rename active season first.
	renamed, err := qs.RenameActiveSeason(ctx, "Сезон 1: Вальгалла")
	if err != nil {
		t.Fatalf("RenameActiveSeason: %v", err)
	}
	if renamed.Name != "Сезон 1: Вальгалла" {
		t.Fatalf("renamed = %q, want Сезон 1: Вальгалла", renamed.Name)
	}

	standings := []models.SeasonStanding{
		{Rank: 1, PlayerID: p1, PlayerName: p1Name, FinalMMR: 2250, Tier: string(domain.TierObsidian), Matches: 15, Wins: 12, Losses: 3, MVPCount: 5, KDA: 4.2, RewardCoins: domain.SeasonPodiumReward(1)},
		{Rank: 2, PlayerID: p2, PlayerName: p2Name, FinalMMR: 1850, Tier: string(domain.TierOnyx), Matches: 14, Wins: 9, Losses: 5, MVPCount: 2, KDA: 3.0, RewardCoins: domain.SeasonPodiumReward(2)},
		{Rank: 3, PlayerID: p3, PlayerName: p3Name, FinalMMR: 900, Tier: string(domain.TierUnranked), Matches: 10, Wins: 4, Losses: 6, SVPCount: 3, KDA: 1.8, RewardCoins: domain.SeasonPodiumReward(3)},
	}

	now := time.Now().UTC().Truncate(time.Second)
	res, err := qs.CloseSeasonAndReset(ctx, standings, 25, "Сезон 2: Рагнарёк", domain.SeasonResetModeFull, "admin-1", now)
	if err != nil {
		t.Fatalf("CloseSeasonAndReset: %v", err)
	}
	if res.ClosedSeason.Number != 1 || res.ClosedSeason.Status != "ARCHIVED" || res.ClosedSeason.MatchesPlayed != 25 {
		t.Errorf("unexpected closed season: %+v", res.ClosedSeason)
	}
	if res.NextSeason.Number != 2 || res.NextSeason.Name != "Сезон 2: Рагнарёк" || res.NextSeason.Status != "ACTIVE" {
		t.Errorf("unexpected next season: %+v", res.NextSeason)
	}
	if res.TotalRewardCoins != 950 {
		t.Errorf("TotalRewardCoins = %d, want 950 (500+300+150)", res.TotalRewardCoins)
	}

	// Verify all three players were reset to 1000 MMR and history recorded.
	mmrs, err := lobbyRepo.GetPlayerMMRsBatch(ctx, []int{p1, p2, p3})
	if err != nil {
		t.Fatalf("GetPlayerMMRsBatch: %v", err)
	}
	for _, pid := range []int{p1, p2, p3} {
		if mmrs[pid] != domain.BaseMMR {
			t.Errorf("player %d MMR after full reset = %d, want %d", pid, mmrs[pid], domain.BaseMMR)
		}
	}
	hist1, err := lobbyRepo.GetMMRHistory(ctx, p1, 5)
	if err != nil || len(hist1) == 0 || hist1[0].Before != 2250 || hist1[0].After != 1000 || hist1[0].Delta != -1250 {
		t.Errorf("p1 reset MMR history = %+v, err=%v", hist1, err)
	}

	// Verify podium ValCoins credited.
	betRepo := NewBetPostgres(db)
	for pid, wantPts := range map[int]int{p1: 600, p2: 400, p3: 250} {
		pts, err := betRepo.GetPlayerPointsByID(ctx, pid)
		if err != nil || pts != wantPts {
			t.Errorf("player %d points = (%d, %v), want (%d, nil)", pid, pts, err, wantPts)
		}
	}

	// Verify Hall of Fame standings & trophies read back.
	gotStandings, err := qs.GetSeasonStandings(ctx, res.ClosedSeason.ID, 10)
	if err != nil || len(gotStandings) != 3 || gotStandings[0].PlayerID != p1 || gotStandings[2].Rank != 3 {
		t.Errorf("GetSeasonStandings = (%+v, %v)", gotStandings, err)
	}
	trophies, err := qs.GetPlayerTrophies(ctx, p1)
	if err != nil || len(trophies) != 1 || trophies[0].Rank != 1 || trophies[0].SeasonNumber != 1 {
		t.Errorf("GetPlayerTrophies(p1) = (%+v, %v)", trophies, err)
	}

	// Verify active season is now #2 and season_start_date moved forward.
	active, err := qs.GetActiveSeason(ctx)
	if err != nil || active.Number != 2 {
		t.Errorf("GetActiveSeason = (%+v, %v), want #2", active, err)
	}
	startDate, err := matchRepo.GetSeasonStartDate(ctx)
	if err != nil || !startDate.Equal(now) {
		t.Errorf("GetSeasonStartDate = (%v, %v), want %v", startDate, err, now)
	}
}
