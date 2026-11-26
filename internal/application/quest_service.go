package application

import (
	"context"
	"fmt"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
)

// QuestService tracks daily/weekly quest progress and handles atomic reward claims.
type QuestService struct {
	logger Logger
	repo   repository.QuestSeason
	nowFn  func() time.Time
}

func NewQuestService(logger Logger, repo repository.QuestSeason) *QuestService {
	return &QuestService{
		logger: logger,
		repo:   repo,
		nowFn:  time.Now,
	}
}

func (s *QuestService) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// RecordEvent increments progress on every active daily and weekly quest whose
// metric matches the event. Errors are logged and swallowed so secondary quest
// tracking never breaks matches, bets, or activity XP.
func (s *QuestService) RecordEvent(ctx context.Context, playerID int, metric domain.QuestMetric, delta int) {
	if s == nil || s.repo == nil || playerID <= 0 || delta <= 0 {
		return
	}
	now := s.now()
	dayKey, _ := domain.DailyPeriodKey(now)
	weekKey, _ := domain.WeeklyPeriodKey(now)

	var dailyCodes []string
	for _, q := range domain.DailyQuestsForTime(now) {
		if q.Metric == metric {
			dailyCodes = append(dailyCodes, q.Code)
		}
	}
	if len(dailyCodes) > 0 {
		if err := s.repo.IncrementQuestProgress(ctx, playerID, dayKey, dailyCodes, delta); err != nil {
			s.logger.Warn("quest: failed to increment daily %s for player %d: %v", metric, playerID, err)
		}
	}

	var weeklyCodes []string
	for _, q := range domain.WeeklyQuestsForTime(now) {
		if q.Metric == metric {
			weeklyCodes = append(weeklyCodes, q.Code)
		}
	}
	if len(weeklyCodes) > 0 {
		if err := s.repo.IncrementQuestProgress(ctx, playerID, weekKey, weeklyCodes, delta); err != nil {
			s.logger.Warn("quest: failed to increment weekly %s for player %d: %v", metric, playerID, err)
		}
	}
}

// GetQuestBoard builds the current daily + weekly quest board for a player.
func (s *QuestService) GetQuestBoard(ctx context.Context, playerID int) (*models.QuestBoard, error) {
	now := s.now()
	dayKey, dayReset := domain.DailyPeriodKey(now)
	weekKey, weekReset := domain.WeeklyPeriodKey(now)

	rows, err := s.repo.GetQuestProgress(ctx, playerID, []string{dayKey, weekKey})
	if err != nil {
		return nil, err
	}

	type rowKey struct {
		period string
		code   string
	}
	byKey := make(map[rowKey]models.QuestProgressRow, len(rows))
	for _, r := range rows {
		byKey[rowKey{r.PeriodKey, r.QuestCode}] = r
	}

	buildSection := func(periodKey string, defs []domain.QuestDef, bonusCode string, bonusCoins int, bonusXP int64) ([]models.QuestStatus, models.QuestBonusStatus) {
		statuses := make([]models.QuestStatus, 0, len(defs))
		completedCount := 0
		for _, def := range defs {
			r := byKey[rowKey{periodKey, def.Code}]
			completed := r.Progress >= def.Target
			if completed {
				completedCount++
			}
			statuses = append(statuses, models.QuestStatus{
				Def:       def,
				PeriodKey: periodKey,
				Progress:  r.Progress,
				Completed: completed,
				Claimed:   r.ClaimedAt != nil,
			})
		}
		bonusRow := byKey[rowKey{periodKey, bonusCode}]
		bonus := models.QuestBonusStatus{
			Code:           bonusCode,
			PeriodKey:      periodKey,
			CompletedCount: completedCount,
			TotalCount:     len(defs),
			RewardCoins:    bonusCoins,
			RewardXP:       bonusXP,
			Eligible:       completedCount == len(defs) && len(defs) > 0,
			Claimed:        bonusRow.ClaimedAt != nil,
		}
		return statuses, bonus
	}

	dailyQuests, dailyBonus := buildSection(
		dayKey, domain.DailyQuestsForTime(now),
		domain.QuestBonusDailyAllCode, domain.QuestBonusDailyAllCoins, domain.QuestBonusDailyAllXP,
	)
	weeklyQuests, weeklyBonus := buildSection(
		weekKey, domain.WeeklyQuestsForTime(now),
		domain.QuestBonusWeeklyAllCode, domain.QuestBonusWeeklyAllCoins, domain.QuestBonusWeeklyAllXP,
	)

	board := &models.QuestBoard{
		DailyPeriodKey:  dayKey,
		DailyResetsAt:   dayReset,
		DailyQuests:     dailyQuests,
		DailyBonus:      dailyBonus,
		WeeklyPeriodKey: weekKey,
		WeeklyResetsAt:  weekReset,
		WeeklyQuests:    weeklyQuests,
		WeeklyBonus:     weeklyBonus,
	}

	addClaimable := func(qs []models.QuestStatus, bonus models.QuestBonusStatus) {
		for _, q := range qs {
			if q.Completed && !q.Claimed {
				board.ClaimableCount++
				board.ClaimableCoins += q.Def.RewardCoins
				board.ClaimableXP += q.Def.RewardXP
			}
		}
		if bonus.Eligible && !bonus.Claimed {
			board.ClaimableCount++
			board.ClaimableCoins += bonus.RewardCoins
			board.ClaimableXP += bonus.RewardXP
		}
	}
	addClaimable(dailyQuests, dailyBonus)
	addClaimable(weeklyQuests, weeklyBonus)

	return board, nil
}

// ClaimAll atomically claims every completed, unclaimed daily & weekly quest
// and all-clear bonus for playerID.
func (s *QuestService) ClaimAll(ctx context.Context, playerID int) (*models.QuestClaimResult, error) {
	board, err := s.GetQuestBoard(ctx, playerID)
	if err != nil {
		return nil, err
	}

	var specs []models.QuestClaimSpec
	collect := func(qs []models.QuestStatus, bonus models.QuestBonusStatus, bonusTitle string) {
		for _, q := range qs {
			if q.Completed && !q.Claimed {
				specs = append(specs, models.QuestClaimSpec{
					PeriodKey:   q.PeriodKey,
					QuestCode:   q.Def.Code,
					Title:       fmt.Sprintf("Квест: %s", q.Def.Title),
					Target:      q.Def.Target,
					RewardCoins: q.Def.RewardCoins,
					RewardXP:    q.Def.RewardXP,
				})
			}
		}
		if bonus.Eligible && !bonus.Claimed {
			specs = append(specs, models.QuestClaimSpec{
				PeriodKey:   bonus.PeriodKey,
				QuestCode:   bonus.Code,
				Title:       bonusTitle,
				IsBonus:     true,
				RewardCoins: bonus.RewardCoins,
				RewardXP:    bonus.RewardXP,
			})
		}
	}
	collect(board.DailyQuests, board.DailyBonus, "Бонус: все дейлики (3/3)")
	collect(board.WeeklyQuests, board.WeeklyBonus, "Бонус: все виклики (3/3)")

	if len(specs) == 0 {
		return nil, domain.ErrNothingToClaim
	}
	return s.repo.ClaimQuestRewards(ctx, playerID, specs)
}
