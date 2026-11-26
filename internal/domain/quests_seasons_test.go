package domain

import (
	"errors"
	"testing"
	"time"
)

func TestQuestPeriodKeysAndResets(t *testing.T) {
	// 2026-11-26 is Thursday in ISO week 2026-W48.
	now := time.Date(2026, 11, 26, 14, 30, 0, 0, time.UTC)

	dayKey, dayReset := DailyPeriodKey(now)
	if dayKey != "daily:2026-11-26" {
		t.Errorf("DailyPeriodKey = %q, want daily:2026-11-26", dayKey)
	}
	wantDayReset := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	if !dayReset.Equal(wantDayReset) {
		t.Errorf("dayReset = %v, want %v", dayReset, wantDayReset)
	}

	weekKey, weekReset := WeeklyPeriodKey(now)
	if weekKey != "weekly:2026-W48" {
		t.Errorf("WeeklyPeriodKey = %q, want weekly:2026-W48", weekKey)
	}
	wantWeekReset := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC) // Monday
	if !weekReset.Equal(wantWeekReset) {
		t.Errorf("weekReset = %v, want %v", weekReset, wantWeekReset)
	}
}

func TestDailyAndWeeklyQuestRotationIsDeterministicAndUnique(t *testing.T) {
	base := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for d := 0; d < 14; d++ {
		day := base.AddDate(0, 0, d)
		daily := DailyQuestsForTime(day)
		if len(daily) != 3 {
			t.Fatalf("day %d: got %d daily quests, want 3", d, len(daily))
		}
		seen := make(map[string]bool)
		for _, q := range daily {
			if seen[q.Code] {
				t.Errorf("day %d: duplicate daily quest %q", d, q.Code)
			}
			seen[q.Code] = true
			if q.Target <= 0 || q.RewardCoins <= 0 || q.RewardXP <= 0 {
				t.Errorf("invalid daily quest definition: %+v", q)
			}
		}

		weekly := WeeklyQuestsForTime(day)
		if len(weekly) != 3 {
			t.Fatalf("day %d: got %d weekly quests, want 3", d, len(weekly))
		}
		seenW := make(map[string]bool)
		for _, q := range weekly {
			if seenW[q.Code] {
				t.Errorf("day %d: duplicate weekly quest %q", d, q.Code)
			}
			seenW[q.Code] = true
			if q.Target <= 0 || q.RewardCoins <= 0 || q.RewardXP <= 0 {
				t.Errorf("invalid weekly quest definition: %+v", q)
			}
		}
	}

	// Two timestamps in the same UTC day must yield identical daily quests.
	a := DailyQuestsForTime(time.Date(2026, 11, 26, 0, 1, 0, 0, time.UTC))
	b := DailyQuestsForTime(time.Date(2026, 11, 26, 23, 59, 0, 0, time.UTC))
	for i := range a {
		if a[i].Code != b[i].Code {
			t.Errorf("same day produced different quest[%d]: %q vs %q", i, a[i].Code, b[i].Code)
		}
	}
}

func TestApplySeasonMMRReset(t *testing.T) {
	tests := []struct {
		mmr  int
		mode string
		want int
	}{
		{2400, SeasonResetModeFull, BaseMMR},
		{800, SeasonResetModeFull, BaseMMR},
		{2400, SeasonResetModeSoft, 1700},
		{800, SeasonResetModeSoft, 900},
		{1000, SeasonResetModeSoft, 1000},
		{2400, SeasonResetModeNone, 2400},
		{150, SeasonResetModeSoft, 575},
	}
	for _, tc := range tests {
		if got := ApplySeasonMMRReset(tc.mmr, tc.mode); got != tc.want {
			t.Errorf("ApplySeasonMMRReset(%d, %q) = %d, want %d", tc.mmr, tc.mode, got, tc.want)
		}
	}
}

func TestSeasonRewardsAndValidation(t *testing.T) {
	if SeasonPodiumReward(1) != SeasonRewardRank1 || SeasonPodiumReward(2) != SeasonRewardRank2 ||
		SeasonPodiumReward(3) != SeasonRewardRank3 || SeasonPodiumReward(4) != 0 {
		t.Error("unexpected podium reward schedule")
	}

	if _, err := ValidateSeasonName("   "); !errors.Is(err, ErrInvalidSeasonName) {
		t.Errorf("empty name err = %v, want ErrInvalidSeasonName", err)
	}
	got, err := ValidateSeasonName("  Сезон 2: Вальгалла  ")
	if err != nil || got != "Сезон 2: Вальгалла" {
		t.Errorf("ValidateSeasonName = (%q, %v)", got, err)
	}
}
