package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/lib/pq"
)

type QuestSeasonPostgres struct {
	db *sql.DB
}

func NewQuestSeasonPostgres(db *sql.DB) *QuestSeasonPostgres {
	return &QuestSeasonPostgres{db: db}
}

// IncrementQuestProgress atomically increments progress for one or more quest
// codes within a period key for a single player.
func (r *QuestSeasonPostgres) IncrementQuestProgress(ctx context.Context, playerID int, periodKey string, questCodes []string, delta int) error {
	if playerID <= 0 || periodKey == "" || len(questCodes) == 0 || delta <= 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("quest: begin increment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, code := range questCodes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO player_quest_progress (player_id, period_key, quest_code, progress, updated_at)
			 VALUES ($1, $2, $3, $4, NOW())
			 ON CONFLICT (player_id, period_key, quest_code)
			 DO UPDATE SET progress = player_quest_progress.progress + EXCLUDED.progress,
			               updated_at = NOW()`,
			playerID, periodKey, code, delta,
		); err != nil {
			return fmt.Errorf("quest: increment %s/%s for player %d: %w", periodKey, code, playerID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("quest: commit increment tx: %w", err)
	}
	return nil
}

// GetQuestProgress returns all progress rows for a player across the given period keys.
func (r *QuestSeasonPostgres) GetQuestProgress(ctx context.Context, playerID int, periodKeys []string) ([]models.QuestProgressRow, error) {
	if playerID <= 0 || len(periodKeys) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT player_id, period_key, quest_code, progress, claimed_at
		 FROM player_quest_progress
		 WHERE player_id = $1 AND period_key = ANY($2)`,
		playerID, pq.Array(periodKeys),
	)
	if err != nil {
		return nil, fmt.Errorf("quest: query progress: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.QuestProgressRow
	for rows.Next() {
		var row models.QuestProgressRow
		var claimedAt sql.NullTime
		if err := rows.Scan(&row.PlayerID, &row.PeriodKey, &row.QuestCode, &row.Progress, &claimedAt); err != nil {
			return nil, fmt.Errorf("quest: scan progress: %w", err)
		}
		if claimedAt.Valid {
			t := claimedAt.Time
			row.ClaimedAt = &t
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("quest: iterate progress: %w", err)
	}
	return out, nil
}

// ClaimQuestRewards atomically claims every eligible quest and all-clear bonus
// in claims, credits ValCoins and XP, applies any resulting level-up bonuses,
// and records idempotent currency_ledger rows.
func (r *QuestSeasonPostgres) ClaimQuestRewards(ctx context.Context, playerID int, claims []models.QuestClaimSpec) (*models.QuestClaimResult, error) {
	if len(claims) == 0 {
		return nil, domain.ErrNothingToClaim
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("quest: begin claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var balance int
	var xp int64
	var oldLevel int
	err = tx.QueryRowContext(ctx,
		`SELECT points, xp, level FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		playerID,
	).Scan(&balance, &xp, &oldLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrPlayerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("quest: lock player %d: %w", playerID, err)
	}

	var claimedTitles []string
	totalCoins := 0
	var totalXP int64

	for _, spec := range claims {
		var res sql.Result
		if spec.IsBonus {
			res, err = tx.ExecContext(ctx,
				`INSERT INTO player_quest_progress (player_id, period_key, quest_code, progress, claimed_at, updated_at)
				 VALUES ($1, $2, $3, 1, NOW(), NOW())
				 ON CONFLICT (player_id, period_key, quest_code) DO NOTHING`,
				playerID, spec.PeriodKey, spec.QuestCode,
			)
		} else {
			res, err = tx.ExecContext(ctx,
				`UPDATE player_quest_progress
				 SET claimed_at = NOW(), updated_at = NOW()
				 WHERE player_id = $1 AND period_key = $2 AND quest_code = $3
				   AND progress >= $4 AND claimed_at IS NULL`,
				playerID, spec.PeriodKey, spec.QuestCode, spec.Target,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("quest: mark claimed %s/%s: %w", spec.PeriodKey, spec.QuestCode, err)
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			continue
		}

		balance += spec.RewardCoins
		totalCoins += spec.RewardCoins
		totalXP += spec.RewardXP
		claimedTitles = append(claimedTitles, spec.Title)

		if spec.RewardCoins > 0 {
			refKey := fmt.Sprintf("quest:%s:%s", spec.PeriodKey, spec.QuestCode)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO currency_ledger (player_id, amount, balance, reason, ref_key, note)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (player_id, reason, ref_key) WHERE ref_key IS NOT NULL DO NOTHING`,
				playerID, spec.RewardCoins, balance, domain.LedgerReasonQuestReward, refKey, spec.Title,
			); err != nil {
				return nil, fmt.Errorf("quest: ledger insert %s: %w", refKey, err)
			}
		}
	}

	if len(claimedTitles) == 0 {
		return nil, domain.ErrNothingToClaim
	}

	newXP := xp + totalXP
	newLevel, _, _ := domain.LevelFromXP(newXP)
	levelUpCoins := 0
	for lvl := oldLevel + 1; lvl <= newLevel; lvl++ {
		bonus := domain.LevelUpCoinReward(lvl)
		if bonus <= 0 {
			continue
		}
		refKey := fmt.Sprintf("level:%d", lvl)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO currency_ledger (player_id, amount, balance, reason, ref_key, note)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (player_id, reason, ref_key) WHERE ref_key IS NOT NULL DO NOTHING`,
			playerID, bonus, balance+bonus, domain.LedgerReasonLevelUp, refKey,
			fmt.Sprintf("Награда за достижение %d уровня", lvl),
		)
		if err != nil {
			return nil, fmt.Errorf("quest: level-up ledger insert %d: %w", lvl, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			balance += bonus
			levelUpCoins += bonus
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE players SET points = $1, xp = $2, level = $3 WHERE id = $4`,
		balance, newXP, newLevel, playerID,
	); err != nil {
		return nil, fmt.Errorf("quest: update player %d balances: %w", playerID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("quest: commit claim tx: %w", err)
	}

	return &models.QuestClaimResult{
		ClaimedTitles: claimedTitles,
		TotalCoins:    totalCoins,
		TotalXP:       totalXP,
		OldLevel:      oldLevel,
		NewLevel:      newLevel,
		LevelUpCoins:  levelUpCoins,
		NewBalance:    balance,
	}, nil
}

// =====================================================================
// Seasons & Hall of Fame
// =====================================================================

func scanSeasonRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*models.Season, error) {
	var s models.Season
	var endedAt sql.NullTime
	var closedBy sql.NullString
	if err := scanner.Scan(&s.ID, &s.Number, &s.Name, &s.Status, &s.StartedAt, &endedAt, &s.MatchesPlayed, &s.MMRResetMode, &closedBy); err != nil {
		return nil, err
	}
	if endedAt.Valid {
		t := endedAt.Time
		s.EndedAt = &t
	}
	if closedBy.Valid {
		s.ClosedBy = closedBy.String
	}
	return &s, nil
}

// GetActiveSeason returns the currently active season, seeding Season 1 if none exists.
func (r *QuestSeasonPostgres) GetActiveSeason(ctx context.Context) (*models.Season, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by
		 FROM seasons WHERE status = 'ACTIVE' ORDER BY number DESC LIMIT 1`,
	)
	s, err := scanSeasonRow(row)
	if err == nil {
		return s, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("season: load active: %w", err)
	}

	// Self-heal: create Season 1 if table was empty.
	row = r.db.QueryRowContext(ctx,
		`INSERT INTO seasons (number, name, status, started_at)
		 VALUES (1, 'Сезон 1', 'ACTIVE', NOW())
		 ON CONFLICT (number) DO UPDATE SET name = seasons.name
		 RETURNING id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by`,
	)
	s, err = scanSeasonRow(row)
	if err != nil {
		return nil, fmt.Errorf("season: seed active: %w", err)
	}
	return s, nil
}

// GetSeasonByNumber loads a season (active or archived) by its 1-based number.
func (r *QuestSeasonPostgres) GetSeasonByNumber(ctx context.Context, number int) (*models.Season, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by
		 FROM seasons WHERE number = $1`,
		number,
	)
	s, err := scanSeasonRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrSeasonNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("season: get #%d: %w", number, err)
	}
	return s, nil
}

// ListArchivedSeasons returns archived seasons ordered newest first.
func (r *QuestSeasonPostgres) ListArchivedSeasons(ctx context.Context, limit int) ([]models.Season, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by
		 FROM seasons WHERE status = 'ARCHIVED' ORDER BY number DESC LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("season: list archived: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.Season
	for rows.Next() {
		s, err := scanSeasonRow(rows)
		if err != nil {
			return nil, fmt.Errorf("season: scan archived: %w", err)
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("season: iterate archived: %w", err)
	}
	return out, nil
}

// RenameActiveSeason updates the display name of the currently active season.
func (r *QuestSeasonPostgres) RenameActiveSeason(ctx context.Context, newName string) (*models.Season, error) {
	row := r.db.QueryRowContext(ctx,
		`UPDATE seasons SET name = $1 WHERE status = 'ACTIVE'
		 RETURNING id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by`,
		newName,
	)
	s, err := scanSeasonRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrSeasonNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("season: rename active: %w", err)
	}
	return s, nil
}

// GetSeasonStandings returns frozen standings for a season ordered by rank ASC.
func (r *QuestSeasonPostgres) GetSeasonStandings(ctx context.Context, seasonID, limit int) ([]models.SeasonStanding, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, season_id, rank, player_id, player_name, final_mmr, tier,
		        matches, wins, losses, mvp_count, svp_count, kda, reward_coins
		 FROM season_standings
		 WHERE season_id = $1
		 ORDER BY rank ASC
		 LIMIT $2`,
		seasonID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("season: query standings %d: %w", seasonID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.SeasonStanding
	for rows.Next() {
		var st models.SeasonStanding
		if err := rows.Scan(
			&st.ID, &st.SeasonID, &st.Rank, &st.PlayerID, &st.PlayerName, &st.FinalMMR, &st.Tier,
			&st.Matches, &st.Wins, &st.Losses, &st.MVPCount, &st.SVPCount, &st.KDA, &st.RewardCoins,
		); err != nil {
			return nil, fmt.Errorf("season: scan standing: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("season: iterate standings: %w", err)
	}
	return out, nil
}

// GetPlayerTrophies returns all Top-3 podium finishes for a player across archived seasons.
func (r *QuestSeasonPostgres) GetPlayerTrophies(ctx context.Context, playerID int) ([]models.PlayerTrophy, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.number, s.name, st.rank, st.final_mmr, st.tier
		 FROM season_standings st
		 JOIN seasons s ON s.id = st.season_id
		 WHERE st.player_id = $1 AND st.rank <= 3 AND s.status = 'ARCHIVED'
		 ORDER BY s.number DESC`,
		playerID,
	)
	if err != nil {
		return nil, fmt.Errorf("season: query trophies for player %d: %w", playerID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []models.PlayerTrophy
	for rows.Next() {
		var tr models.PlayerTrophy
		if err := rows.Scan(&tr.SeasonNumber, &tr.SeasonName, &tr.Rank, &tr.FinalMMR, &tr.Tier); err != nil {
			return nil, fmt.Errorf("season: scan trophy: %w", err)
		}
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("season: iterate trophies: %w", err)
	}
	return out, nil
}

// CountMatchesSince returns how many finished lobby matches (with a winner) or
// non-deleted OCR matches occurred since the given timestamp.
func (r *QuestSeasonPostgres) CountMatchesSince(ctx context.Context, since time.Time) (int, error) {
	var lobbyCount, ocrCount int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM lobby_matches WHERE status = 'FINISHED' AND winner IS NOT NULL AND created_at >= $1`,
		since,
	).Scan(&lobbyCount); err != nil {
		return 0, fmt.Errorf("season: count lobby matches: %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM matches WHERE is_deleted = FALSE AND created_at >= $1`,
		since,
	).Scan(&ocrCount); err != nil {
		return 0, fmt.Errorf("season: count ocr matches: %w", err)
	}
	if lobbyCount >= ocrCount {
		return lobbyCount, nil
	}
	return ocrCount, nil
}

// CloseSeasonAndReset atomically archives the active season, saves Hall of Fame
// standings, credits Top-3 ValCoins rewards, applies the requested MMR reset
// across all active players (logging mmr_history), opens Season N+1, and updates
// bot_settings.season_start_date.
func (r *QuestSeasonPostgres) CloseSeasonAndReset(
	ctx context.Context,
	standings []models.SeasonStanding,
	matchesPlayed int,
	nextName string,
	resetMode string,
	closedBy string,
	now time.Time,
) (*models.CloseSeasonResult, error) {
	if !domain.IsValidSeasonResetMode(resetMode) {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidSeasonResetMode, resetMode)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("season: begin close tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx,
		`SELECT id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by
		 FROM seasons WHERE status = 'ACTIVE' ORDER BY number DESC LIMIT 1 FOR UPDATE`,
	)
	active, err := scanSeasonRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrSeasonNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("season: lock active: %w", err)
	}

	savedStandings := make([]models.SeasonStanding, 0, len(standings))
	totalRewards := 0

	for _, st := range standings {
		st.SeasonID = active.ID
		var insertedID int
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO season_standings
			 (season_id, rank, player_id, player_name, final_mmr, tier, matches, wins, losses, mvp_count, svp_count, kda, reward_coins)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			 RETURNING id`,
			st.SeasonID, st.Rank, st.PlayerID, st.PlayerName, st.FinalMMR, st.Tier,
			st.Matches, st.Wins, st.Losses, st.MVPCount, st.SVPCount, st.KDA, st.RewardCoins,
		).Scan(&insertedID); err != nil {
			return nil, fmt.Errorf("season: insert standing rank %d (player %d): %w", st.Rank, st.PlayerID, err)
		}
		st.ID = insertedID
		savedStandings = append(savedStandings, st)

		if st.RewardCoins > 0 {
			var curPoints int
			if err := tx.QueryRowContext(ctx,
				`SELECT points FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
				st.PlayerID,
			).Scan(&curPoints); err != nil {
				return nil, fmt.Errorf("season: lock reward winner %d: %w", st.PlayerID, err)
			}
			newBal := curPoints + st.RewardCoins
			refKey := fmt.Sprintf("season:%d:rank:%d", active.Number, st.Rank)
			res, err := tx.ExecContext(ctx,
				`INSERT INTO currency_ledger (player_id, amount, balance, reason, ref_key, note)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (player_id, reason, ref_key) WHERE ref_key IS NOT NULL DO NOTHING`,
				st.PlayerID, st.RewardCoins, newBal, domain.LedgerReasonSeasonReward, refKey,
				fmt.Sprintf("Приз за #%d место в «%s»", st.Rank, active.Name),
			)
			if err != nil {
				return nil, fmt.Errorf("season: ledger reward %s: %w", refKey, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, newBal, st.PlayerID); err != nil {
					return nil, fmt.Errorf("season: credit reward player %d: %w", st.PlayerID, err)
				}
				totalRewards += st.RewardCoins
			}
		}
	}

	// Reset MMR across all non-deleted players if requested.
	newMMRs := make(map[int]int)
	if resetMode != domain.SeasonResetModeNone {
		type mmrRow struct {
			id  int
			mmr int
		}
		var playersToReset []mmrRow
		pRows, err := tx.QueryContext(ctx,
			`SELECT id, COALESCE(current_mmr, $1) FROM players WHERE is_deleted = FALSE ORDER BY id FOR UPDATE`,
			domain.BaseMMR,
		)
		if err != nil {
			return nil, fmt.Errorf("season: query players for MMR reset: %w", err)
		}
		for pRows.Next() {
			var pr mmrRow
			if err := pRows.Scan(&pr.id, &pr.mmr); err != nil {
				_ = pRows.Close()
				return nil, fmt.Errorf("season: scan player MMR: %w", err)
			}
			playersToReset = append(playersToReset, pr)
		}
		if err := pRows.Err(); err != nil {
			_ = pRows.Close()
			return nil, fmt.Errorf("season: iterate player MMRs: %w", err)
		}
		if err := pRows.Close(); err != nil {
			return nil, fmt.Errorf("season: close player MMRs: %w", err)
		}

		for _, pr := range playersToReset {
			nextMMR := domain.ApplySeasonMMRReset(pr.mmr, resetMode)
			if nextMMR == pr.mmr {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE players SET current_mmr = $1 WHERE id = $2`,
				nextMMR, pr.id,
			); err != nil {
				return nil, fmt.Errorf("season: reset MMR for player %d: %w", pr.id, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mmr_history (player_id, match_id, mmr_before, mmr_after, delta)
				 VALUES ($1, NULL, $2, $3, $4)`,
				pr.id, pr.mmr, nextMMR, nextMMR-pr.mmr,
			); err != nil {
				return nil, fmt.Errorf("season: record MMR reset history for player %d: %w", pr.id, err)
			}
			newMMRs[pr.id] = nextMMR
		}
	}

	// Archive the active season.
	closedRow := tx.QueryRowContext(ctx,
		`UPDATE seasons
		 SET status = 'ARCHIVED', ended_at = $1, matches_played = $2, mmr_reset_mode = $3, closed_by = $4
		 WHERE id = $5
		 RETURNING id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by`,
		now.UTC(), matchesPlayed, resetMode, closedBy, active.ID,
	)
	closedSeason, err := scanSeasonRow(closedRow)
	if err != nil {
		return nil, fmt.Errorf("season: archive #%d: %w", active.Number, err)
	}

	nextNum := active.Number + 1
	if nextName == "" {
		nextName = fmt.Sprintf("Сезон %d", nextNum)
	}
	nextRow := tx.QueryRowContext(ctx,
		`INSERT INTO seasons (number, name, status, started_at)
		 VALUES ($1, $2, 'ACTIVE', $3)
		 RETURNING id, number, name, status, started_at, ended_at, matches_played, mmr_reset_mode, closed_by`,
		nextNum, nextName, now.UTC(),
	)
	nextSeason, err := scanSeasonRow(nextRow)
	if err != nil {
		return nil, fmt.Errorf("season: create next #%d: %w", nextNum, err)
	}

	// Advance bot_settings.season_start_date so /top and /profile match stats begin fresh.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO bot_settings (key, value) VALUES ('season_start_date', $1)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		now.UTC().Format(time.RFC3339),
	); err != nil {
		return nil, fmt.Errorf("season: update season_start_date: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("season: commit close tx: %w", err)
	}

	return &models.CloseSeasonResult{
		ClosedSeason:     *closedSeason,
		NextSeason:       *nextSeason,
		Standings:        savedStandings,
		MMRResetCount:    len(newMMRs),
		NewMMRs:          newMMRs,
		TotalRewardCoins: totalRewards,
	}, nil
}
