package domain

import (
	"fmt"
	"time"
)

// QuestMetric identifies the player action that advances a quest.
type QuestMetric string

const (
	QuestMetricPlayMatches  QuestMetric = "play_matches"
	QuestMetricWinMatches   QuestMetric = "win_matches"
	QuestMetricEarnMedals   QuestMetric = "earn_medals"
	QuestMetricPlaceBets    QuestMetric = "place_bets"
	QuestMetricWinBets      QuestMetric = "win_bets"
	QuestMetricClaimDaily   QuestMetric = "claim_daily"
	QuestMetricChatMessages QuestMetric = "chat_messages"
	QuestMetricVoiceMinutes QuestMetric = "voice_minutes"
)

// QuestPeriod identifies whether a quest resets daily or weekly.
type QuestPeriod string

const (
	QuestPeriodDaily  QuestPeriod = "daily"
	QuestPeriodWeekly QuestPeriod = "weekly"
)

// Special quest codes for completing all 3 quests in a period.
const (
	QuestBonusDailyAllCode  = "daily_all_clear"
	QuestBonusWeeklyAllCode = "weekly_all_clear"

	QuestBonusDailyAllCoins  = 35
	QuestBonusDailyAllXP     = 50
	QuestBonusWeeklyAllCoins = 120
	QuestBonusWeeklyAllXP    = 200
)

// QuestDef describes one static quest template.
type QuestDef struct {
	Code        string
	Period      QuestPeriod
	Metric      QuestMetric
	Title       string
	Description string
	Target      int
	RewardCoins int
	RewardXP    int64
}

// Daily quest pools grouped into 3 complementary slots (matches, community/bets, activity).
var (
	dailySlotMatches = []QuestDef{
		{
			Code:        "daily_play_2",
			Period:      QuestPeriodDaily,
			Metric:      QuestMetricPlayMatches,
			Title:       "Разминка на арене",
			Description: "Сыграй 2 рейтинговых матча",
			Target:      2,
			RewardCoins: 40,
			RewardXP:    60,
		},
		{
			Code:        "daily_win_1",
			Period:      QuestPeriodDaily,
			Metric:      QuestMetricWinMatches,
			Title:       "Первая победа дня",
			Description: "Одержи 1 победу в матче",
			Target:      1,
			RewardCoins: 45,
			RewardXP:    75,
		},
	}

	dailySlotCommunity = []QuestDef{
		{
			Code:        "daily_bet_1",
			Period:      QuestPeriodDaily,
			Metric:      QuestMetricPlaceBets,
			Title:       "Интуиция зрителя",
			Description: "Сделай 1 ставку на матч",
			Target:      1,
			RewardCoins: 25,
			RewardXP:    35,
		},
		{
			Code:        "daily_bet_win_1",
			Period:      QuestPeriodDaily,
			Metric:      QuestMetricWinBets,
			Title:       "Точный прогноз",
			Description: "Выиграй 1 ставку на матч",
			Target:      1,
			RewardCoins: 50,
			RewardXP:    70,
		},
		{
			Code:        "daily_checkin",
			Period:      QuestPeriodDaily,
			Metric:      QuestMetricClaimDaily,
			Title:       "Ежедневный визит",
			Description: "Забери награду командой /daily",
			Target:      1,
			RewardCoins: 20,
			RewardXP:    30,
		},
	}

	dailySlotActivity = []QuestDef{
		{
			Code:        "daily_chat_30",
			Period:      QuestPeriodDaily,
			Metric:      QuestMetricChatMessages,
			Title:       "Душа компании",
			Description: "Напиши 30 сообщений в чате",
			Target:      30,
			RewardCoins: 30,
			RewardXP:    45,
		},
		{
			Code:        "daily_voice_30",
			Period:      QuestPeriodDaily,
			Metric:      QuestMetricVoiceMinutes,
			Title:       "На связи в войсе",
			Description: "Проведи 30 минут в голосовом канале",
			Target:      30,
			RewardCoins: 35,
			RewardXP:    55,
		},
	}
)

// Weekly quest pools grouped into 3 complementary slots (grind, skill/bets, activity).
var (
	weeklySlotMatches = []QuestDef{
		{
			Code:        "weekly_play_10",
			Period:      QuestPeriodWeekly,
			Metric:      QuestMetricPlayMatches,
			Title:       "Марафонец недели",
			Description: "Сыграй 10 рейтинговых матчей",
			Target:      10,
			RewardCoins: 150,
			RewardXP:    250,
		},
		{
			Code:        "weekly_win_6",
			Period:      QuestPeriodWeekly,
			Metric:      QuestMetricWinMatches,
			Title:       "Доминация",
			Description: "Выиграй 6 рейтинговых матчей",
			Target:      6,
			RewardCoins: 180,
			RewardXP:    300,
		},
	}

	weeklySlotSkill = []QuestDef{
		{
			Code:        "weekly_medals_2",
			Period:      QuestPeriodWeekly,
			Metric:      QuestMetricEarnMedals,
			Title:       "Звезда матча",
			Description: "Получи 2 медали MVP или SVPG",
			Target:      2,
			RewardCoins: 175,
			RewardXP:    250,
		},
		{
			Code:        "weekly_bet_win_3",
			Period:      QuestPeriodWeekly,
			Metric:      QuestMetricWinBets,
			Title:       "Пророк тотализатора",
			Description: "Выиграй 3 ставки на матчи",
			Target:      3,
			RewardCoins: 140,
			RewardXP:    200,
		},
	}

	weeklySlotActivity = []QuestDef{
		{
			Code:        "weekly_chat_200",
			Period:      QuestPeriodWeekly,
			Metric:      QuestMetricChatMessages,
			Title:       "Активист сервера",
			Description: "Напиши 200 сообщений за неделю",
			Target:      200,
			RewardCoins: 120,
			RewardXP:    200,
		},
		{
			Code:        "weekly_voice_180",
			Period:      QuestPeriodWeekly,
			Metric:      QuestMetricVoiceMinutes,
			Title:       "Голос Валгаллы",
			Description: "Проведи 180 минут (3 ч) в войсе",
			Target:      180,
			RewardCoins: 140,
			RewardXP:    220,
		},
	}
)

// DailyPeriodKey returns the UTC day key ("daily:YYYY-MM-DD") and next reset time.
func DailyPeriodKey(now time.Time) (string, time.Time) {
	u := now.UTC()
	dayStart := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	return "daily:" + dayStart.Format("2006-01-02"), dayStart.Add(24 * time.Hour)
}

// WeeklyPeriodKey returns the UTC ISO-week key ("weekly:YYYY-Www") and next Monday 00:00 UTC.
func WeeklyPeriodKey(now time.Time) (string, time.Time) {
	u := now.UTC()
	year, week := u.ISOWeek()
	// Find Monday 00:00 UTC of the current ISO week.
	offset := (int(u.Weekday()) + 6) % 7 // Mon=0 .. Sun=6
	weekStart := time.Date(u.Year(), u.Month(), u.Day()-offset, 0, 0, 0, 0, time.UTC)
	return fmt.Sprintf("weekly:%04d-W%02d", year, week), weekStart.Add(7 * 24 * time.Hour)
}

// DailyQuestsForTime deterministically picks the 3 daily quests for the UTC day of now.
func DailyQuestsForTime(now time.Time) []QuestDef {
	dayIdx := now.UTC().Unix() / 86400
	if dayIdx < 0 {
		dayIdx = -dayIdx
	}
	return []QuestDef{
		dailySlotMatches[int(dayIdx)%len(dailySlotMatches)],
		dailySlotCommunity[int(dayIdx)%len(dailySlotCommunity)],
		dailySlotActivity[int(dayIdx)%len(dailySlotActivity)],
	}
}

// WeeklyQuestsForTime deterministically picks the 3 weekly quests for the ISO week of now.
func WeeklyQuestsForTime(now time.Time) []QuestDef {
	u := now.UTC()
	offset := (int(u.Weekday()) + 6) % 7
	monday := time.Date(u.Year(), u.Month(), u.Day()-offset, 0, 0, 0, 0, time.UTC)
	weekIdx := monday.Unix() / (7 * 86400)
	if weekIdx < 0 {
		weekIdx = -weekIdx
	}
	return []QuestDef{
		weeklySlotMatches[int(weekIdx)%len(weeklySlotMatches)],
		weeklySlotSkill[int(weekIdx+1)%len(weeklySlotSkill)],
		weeklySlotActivity[int(weekIdx)%len(weeklySlotActivity)],
	}
}
