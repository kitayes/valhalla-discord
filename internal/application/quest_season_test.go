package application

import (
	"context"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

type fakeQuestSeasonRepo struct {
	progress map[string]map[string]*models.QuestProgressRow // periodKey -> code -> row
	claimed  []models.QuestClaimSpec
}

func newFakeQuestSeasonRepo() *fakeQuestSeasonRepo {
	return &fakeQuestSeasonRepo{
		progress: make(map[string]map[string]*models.QuestProgressRow),
	}
}

func (f *fakeQuestSeasonRepo) IncrementQuestProgress(_ context.Context, playerID int, periodKey string, questCodes []string, delta int) error {
	if f.progress[periodKey] == nil {
		f.progress[periodKey] = make(map[string]*models.QuestProgressRow)
	}
	for _, code := range questCodes {
		row := f.progress[periodKey][code]
		if row == nil {
			row = &models.QuestProgressRow{PlayerID: playerID, PeriodKey: periodKey, QuestCode: code}
			f.progress[periodKey][code] = row
		}
		row.Progress += delta
	}
	return nil
}

func (f *fakeQuestSeasonRepo) GetQuestProgress(_ context.Context, _ int, periodKeys []string) ([]models.QuestProgressRow, error) {
	var out []models.QuestProgressRow
	for _, pk := range periodKeys {
		for _, r := range f.progress[pk] {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeQuestSeasonRepo) ClaimQuestRewards(_ context.Context, _ int, claims []models.QuestClaimSpec) (*models.QuestClaimResult, error) {
	now := time.Now()
	res := &models.QuestClaimResult{OldLevel: 1, NewLevel: 1}
	for _, c := range claims {
		if f.progress[c.PeriodKey] == nil {
			f.progress[c.PeriodKey] = make(map[string]*models.QuestProgressRow)
		}
		r := f.progress[c.PeriodKey][c.QuestCode]
		if r == nil {
			r = &models.QuestProgressRow{PeriodKey: c.PeriodKey, QuestCode: c.QuestCode, Progress: 1}
			f.progress[c.PeriodKey][c.QuestCode] = r
		}
		if r.ClaimedAt != nil {
			continue
		}
		r.ClaimedAt = &now
		f.claimed = append(f.claimed, c)
		res.ClaimedTitles = append(res.ClaimedTitles, c.Title)
		res.TotalCoins += c.RewardCoins
		res.TotalXP += c.RewardXP
	}
	return res, nil
}

func (f *fakeQuestSeasonRepo) GetActiveSeason(context.Context) (*models.Season, error) {
	return nil, nil
}
func (f *fakeQuestSeasonRepo) GetSeasonByNumber(context.Context, int) (*models.Season, error) {
	return nil, nil
}
func (f *fakeQuestSeasonRepo) ListArchivedSeasons(context.Context, int) ([]models.Season, error) {
	return nil, nil
}
func (f *fakeQuestSeasonRepo) RenameActiveSeason(context.Context, string) (*models.Season, error) {
	return nil, nil
}
func (f *fakeQuestSeasonRepo) GetSeasonStandings(context.Context, int, int) ([]models.SeasonStanding, error) {
	return nil, nil
}
func (f *fakeQuestSeasonRepo) GetPlayerTrophies(context.Context, int) ([]models.PlayerTrophy, error) {
	return nil, nil
}
func (f *fakeQuestSeasonRepo) CountMatchesSince(context.Context, time.Time) (int, error) {
	return 0, nil
}
func (f *fakeQuestSeasonRepo) CloseSeasonAndReset(context.Context, []models.SeasonStanding, int, string, string, string, time.Time) (*models.CloseSeasonResult, error) {
	return nil, nil
}

func TestQuestServiceBoardAndAllClearBonus(t *testing.T) {
	repo := newFakeQuestSeasonRepo()
	svc := NewQuestService(nopLogger{}, repo)
	fixedNow := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return fixedNow }

	ctx := context.Background()
	daily := domain.DailyQuestsForTime(fixedNow)
	for _, q := range daily {
		svc.RecordEvent(ctx, 7, q.Metric, q.Target)
	}

	board, err := svc.GetQuestBoard(ctx, 7)
	if err != nil {
		t.Fatalf("GetQuestBoard: %v", err)
	}
	if !board.DailyBonus.Eligible || board.DailyBonus.CompletedCount != 3 {
		t.Fatalf("daily bonus = %+v, want Eligible=true CompletedCount=3", board.DailyBonus)
	}
	// All 3 daily quests + 1 daily all-clear bonus must be claimable.
	if board.ClaimableCount < 4 {
		t.Errorf("ClaimableCount = %d, want at least 4", board.ClaimableCount)
	}

	res, err := svc.ClaimAll(ctx, 7)
	if err != nil {
		t.Fatalf("ClaimAll: %v", err)
	}
	if len(res.ClaimedTitles) < 4 {
		t.Errorf("claimed titles = %v, want at least 4", res.ClaimedTitles)
	}

	// Board after claim should report 0 claimable.
	after, err := svc.GetQuestBoard(ctx, 7)
	if err != nil {
		t.Fatalf("GetQuestBoard after claim: %v", err)
	}
	if after.ClaimableCount != 0 || !after.DailyBonus.Claimed {
		t.Errorf("after claim: claimable=%d bonusClaimed=%v, want 0/true", after.ClaimableCount, after.DailyBonus.Claimed)
	}
}

func TestMergeAndSortSeasonEntries(t *testing.T) {
	players := []models.Player{
		{ID: 1, Name: "Active", CurrentMMR: 1200},
		{ID: 2, Name: "MovedMMR", CurrentMMR: 1450},
		{ID: 3, Name: "Idle", CurrentMMR: 1000},
		{ID: 4, Name: "ZeroMatchDefault", CurrentMMR: 0},
	}
	stats := []*PlayerStats{
		{ID: 1, Name: "Active", Matches: 3, Wins: 2, Losses: 1},
	}
	got := mergeAndSortSeasonEntries(players, stats)
	if len(got) != 2 || got[0].PlayerID != 2 || got[0].MMR != 1450 || got[1].PlayerID != 1 || got[1].Matches != 3 {
		t.Errorf("mergeAndSortSeasonEntries = %+v, want [player 2 (1450), player 1 (1200, 3m)]", got)
	}
}
