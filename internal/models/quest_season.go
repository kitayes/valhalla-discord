package models

import (
	"time"

	"blackwatch/internal/domain"
)

// QuestProgressRow is a persisted row from player_quest_progress.
type QuestProgressRow struct {
	PlayerID  int        `json:"player_id"`
	PeriodKey string     `json:"period_key"`
	QuestCode string     `json:"quest_code"`
	Progress  int        `json:"progress"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
}

// QuestStatus combines a static QuestDef with one player's live progress.
type QuestStatus struct {
	Def       domain.QuestDef
	PeriodKey string
	Progress  int
	Completed bool
	Claimed   bool
}

// QuestBonusStatus tracks the all-3-clear bonus for a period (daily or weekly).
type QuestBonusStatus struct {
	Code           string
	PeriodKey      string
	CompletedCount int
	TotalCount     int
	RewardCoins    int
	RewardXP       int64
	Eligible       bool
	Claimed        bool
}

// QuestBoard is the full view rendered by /quests.
type QuestBoard struct {
	DailyPeriodKey  string
	DailyResetsAt   time.Time
	DailyQuests     []QuestStatus
	DailyBonus      QuestBonusStatus
	WeeklyPeriodKey string
	WeeklyResetsAt  time.Time
	WeeklyQuests    []QuestStatus
	WeeklyBonus     QuestBonusStatus
	ClaimableCount  int
	ClaimableCoins  int
	ClaimableXP     int64
}

// QuestClaimSpec describes one completed quest or all-clear bonus to claim atomically.
type QuestClaimSpec struct {
	PeriodKey   string
	QuestCode   string
	Title       string
	Target      int  // required progress for regular quests; 0 for bonus specs
	IsBonus     bool // true for daily_all_clear / weekly_all_clear
	RewardCoins int
	RewardXP    int64
}

// QuestClaimResult summarizes the rewards granted by ClaimAll.
type QuestClaimResult struct {
	ClaimedTitles []string
	TotalCoins    int
	TotalXP       int64
	OldLevel      int
	NewLevel      int
	LevelUpCoins  int
	NewBalance    int
}

// Season represents a ranked season in the seasons table.
type Season struct {
	ID            int        `json:"id"`
	Number        int        `json:"number"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	MatchesPlayed int        `json:"matches_played"`
	MMRResetMode  string     `json:"mmr_reset_mode"`
	ClosedBy      string     `json:"closed_by,omitempty"`
}

// SeasonStanding is a frozen Hall of Fame row archived when a season closes.
type SeasonStanding struct {
	ID          int     `json:"id"`
	SeasonID    int     `json:"season_id"`
	Rank        int     `json:"rank"`
	PlayerID    int     `json:"player_id"`
	PlayerName  string  `json:"player_name"`
	FinalMMR    int     `json:"final_mmr"`
	Tier        string  `json:"tier"`
	Matches     int     `json:"matches"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	MVPCount    int     `json:"mvp_count"`
	SVPCount    int     `json:"svp_count"`
	KDA         float64 `json:"kda"`
	RewardCoins int     `json:"reward_coins"`
}

// PlayerTrophy is a podium finish (Top-3) displayed on a player's /profile.
type PlayerTrophy struct {
	SeasonNumber int    `json:"season_number"`
	SeasonName   string `json:"season_name"`
	Rank         int    `json:"rank"`
	FinalMMR     int    `json:"final_mmr"`
	Tier         string `json:"tier"`
}

// CloseSeasonResult summarizes an atomic season close + MMR reset.
type CloseSeasonResult struct {
	ClosedSeason     Season
	NextSeason       Season
	Standings        []SeasonStanding
	MMRResetCount    int
	NewMMRs          map[int]int
	TotalRewardCoins int
}
