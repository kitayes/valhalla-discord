package discord

import (
	"strings"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

func TestQuestBoardEmbedAndComponents(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	dayKey, dayReset := domain.DailyPeriodKey(now)
	weekKey, weekReset := domain.WeeklyPeriodKey(now)
	dailyDefs := domain.DailyQuestsForTime(now)
	weeklyDefs := domain.WeeklyQuestsForTime(now)

	board := &models.QuestBoard{
		DailyPeriodKey: dayKey,
		DailyResetsAt:  dayReset,
		DailyQuests: []models.QuestStatus{
			{Def: dailyDefs[0], PeriodKey: dayKey, Progress: dailyDefs[0].Target, Completed: true, Claimed: false},
			{Def: dailyDefs[1], PeriodKey: dayKey, Progress: 0, Completed: false, Claimed: false},
			{Def: dailyDefs[2], PeriodKey: dayKey, Progress: dailyDefs[2].Target, Completed: true, Claimed: true},
		},
		DailyBonus: models.QuestBonusStatus{
			Code: domain.QuestBonusDailyAllCode, PeriodKey: dayKey,
			CompletedCount: 2, TotalCount: 3,
			RewardCoins: domain.QuestBonusDailyAllCoins, RewardXP: domain.QuestBonusDailyAllXP,
		},
		WeeklyPeriodKey: weekKey,
		WeeklyResetsAt:  weekReset,
		WeeklyQuests: []models.QuestStatus{
			{Def: weeklyDefs[0], PeriodKey: weekKey, Progress: 1},
			{Def: weeklyDefs[1], PeriodKey: weekKey, Progress: 0},
			{Def: weeklyDefs[2], PeriodKey: weekKey, Progress: 50},
		},
		WeeklyBonus: models.QuestBonusStatus{
			Code: domain.QuestBonusWeeklyAllCode, PeriodKey: weekKey,
			CompletedCount: 0, TotalCount: 3,
			RewardCoins: domain.QuestBonusWeeklyAllCoins, RewardXP: domain.QuestBonusWeeklyAllXP,
		},
		ClaimableCount: 1,
		ClaimableCoins: dailyDefs[0].RewardCoins,
		ClaimableXP:    dailyDefs[0].RewardXP,
	}

	embed := buildQuestBoardEmbed("TestPlayer", board, "")
	if !strings.Contains(embed.Title, "TestPlayer") {
		t.Errorf("embed title %q missing player name", embed.Title)
	}
	if len(embed.Fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(embed.Fields))
	}
	if !strings.Contains(embed.Fields[0].Value, "готово!") || !strings.Contains(embed.Fields[0].Value, "получено") {
		t.Errorf("daily section missing expected status markers:\n%s", embed.Fields[0].Value)
	}

	comps := buildQuestBoardComponents("12345", board)
	if len(comps) != 1 {
		t.Fatalf("got %d component rows, want 1", len(comps))
	}
}

func TestHallOfFameEmbedAndSelector(t *testing.T) {
	ended := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	season := models.Season{
		ID:            1,
		Number:        1,
		Name:          "Сезон 1: Начало",
		Status:        "ARCHIVED",
		StartedAt:     time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		EndedAt:       &ended,
		MatchesPlayed: 42,
		MMRResetMode:  domain.SeasonResetModeFull,
	}
	standings := []models.SeasonStanding{
		{Rank: 1, PlayerName: "Champ", FinalMMR: 2250, Tier: string(domain.TierObsidian), Matches: 20, Wins: 16, Losses: 4, MVPCount: 7, KDA: 4.5, RewardCoins: 500},
		{Rank: 2, PlayerName: "RunnerUp", FinalMMR: 1920, Tier: string(domain.TierOnyx), Matches: 18, Wins: 12, Losses: 6, KDA: 3.1, RewardCoins: 300},
	}

	embed := buildHallOfFameEmbed(season, standings)
	if !strings.Contains(embed.Title, "Зал Славы") || !strings.Contains(embed.Fields[0].Value, "Champ") {
		t.Errorf("unexpected Hall of Fame embed: %+v", embed)
	}
	if !strings.Contains(embed.Fields[0].Value, "🥇") || !strings.Contains(embed.Fields[0].Value, "🥈") {
		t.Errorf("podium medals missing:\n%s", embed.Fields[0].Value)
	}

	if sel := buildHallOfFameSelector([]models.Season{season}, 1); sel != nil {
		t.Errorf("single archived season should not render selector, got %v", sel)
	}
	s2 := season
	s2.Number = 2
	s2.Name = "Сезон 2"
	if sel := buildHallOfFameSelector([]models.Season{s2, season}, 2); len(sel) != 1 {
		t.Errorf("two archived seasons should render 1 selector row, got %d", len(sel))
	}
}

func TestQuestsAndSeasonCommandsRegistered(t *testing.T) {
	byName := commandsByName(t, initBot(t))
	for _, name := range []string{"quests", "season", "season_admin"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("/%s is not registered", name)
		}
	}
}

func TestLedgerReasonIncludesQuestAndSeason(t *testing.T) {
	if got := formatLedgerReason(domain.LedgerReasonQuestReward); got != "Награда за квест" {
		t.Errorf("quest reason = %q", got)
	}
	if got := formatLedgerReason(domain.LedgerReasonSeasonReward); got != "Приз за сезон" {
		t.Errorf("season reason = %q", got)
	}
}
