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

// EconomyPostgres manages ValCoins, activity XP/levels, daily streaks, tips,
// ledger audit logs, and the server shop.
type EconomyPostgres struct {
	db *sql.DB
}

func NewEconomyPostgres(db *sql.DB) *EconomyPostgres {
	return &EconomyPostgres{db: db}
}

func insertLedgerTx(ctx context.Context, tx *sql.Tx, playerID, amount, balance int, reason, refKey, note string) (bool, error) {
	var ref any
	if refKey != "" {
		ref = refKey
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO currency_ledger (player_id, amount, balance, reason, ref_key, note)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (player_id, reason, ref_key) WHERE ref_key IS NOT NULL DO NOTHING`,
		playerID, amount, balance, reason, ref, note,
	)
	if err != nil {
		return false, fmt.Errorf("failed to insert currency ledger entry: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check ledger insert rows affected: %w", err)
	}
	return affected > 0, nil
}

// GetPlayerEconomy reads a player's full economy and activity state.
func (r *EconomyPostgres) GetPlayerEconomy(ctx context.Context, playerID int) (*models.PlayerEconomy, error) {
	var e models.PlayerEconomy
	var discordID sql.NullString
	var lastDaily sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, discord_id, points, xp, level, voice_seconds, messages_count,
		        last_daily_at, daily_streak, profile_title, profile_color, bet_insurance_count
		   FROM players
		  WHERE id = $1 AND is_deleted = FALSE`,
		playerID,
	).Scan(
		&e.PlayerID, &e.Name, &discordID, &e.Points, &e.XP, &e.Level,
		&e.VoiceSeconds, &e.MessagesCount, &lastDaily, &e.DailyStreak,
		&e.ProfileTitle, &e.ProfileColor, &e.BetInsuranceCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get player economy: %w", err)
	}
	if discordID.Valid {
		e.DiscordID = discordID.String
	}
	if lastDaily.Valid {
		t := lastDaily.Time
		e.LastDailyAt = &t
	}
	return &e, nil
}

// GetLedger returns the most recent currency movements for a player.
func (r *EconomyPostgres) GetLedger(ctx context.Context, playerID, limit int) ([]models.LedgerEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, player_id, amount, balance, reason, COALESCE(ref_key, ''), note, created_at
		   FROM currency_ledger
		  WHERE player_id = $1
		  ORDER BY created_at DESC, id DESC
		  LIMIT $2`,
		playerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query currency ledger: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var entries []models.LedgerEntry
	for rows.Next() {
		var e models.LedgerEntry
		if err := rows.Scan(&e.ID, &e.PlayerID, &e.Amount, &e.Balance, &e.Reason, &e.RefKey, &e.Note, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan ledger entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// GrantXP atomically adds activity XP (and voice/message counters), recomputes
// the player's level, and awards ValCoins for every newly reached level.
func (r *EconomyPostgres) GrantXP(ctx context.Context, playerID int, xpDelta, voiceSecondsDelta, messagesDelta int64) (*models.XPGrantResult, error) {
	if xpDelta < 0 || voiceSecondsDelta < 0 || messagesDelta < 0 {
		return nil, fmt.Errorf("%w: negative XP or counter delta", domain.ErrInvalidAmount)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin xp transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var curPoints, curLevel int
	var curXP int64
	err = tx.QueryRowContext(ctx,
		`SELECT points, xp, level FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		playerID,
	).Scan(&curPoints, &curXP, &curLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock player for xp: %w", err)
	}

	newXP := curXP + xpDelta
	newLevel, _, _ := domain.LevelFromXP(newXP)
	if newLevel < curLevel {
		newLevel = curLevel
	}

	totalCoins := 0
	runningBalance := curPoints
	for lvl := curLevel + 1; lvl <= newLevel; lvl++ {
		reward := domain.LevelUpCoinReward(lvl)
		if reward <= 0 {
			continue
		}
		refKey := fmt.Sprintf("level:%d", lvl)
		inserted, err := insertLedgerTx(
			ctx, tx, playerID, reward, runningBalance+reward,
			domain.LedgerReasonLevelUp, refKey,
			fmt.Sprintf("Уровень %d", lvl),
		)
		if err != nil {
			return nil, err
		}
		if inserted {
			runningBalance += reward
			totalCoins += reward
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE players
		    SET xp = $1,
		        level = $2,
		        points = $3,
		        voice_seconds = voice_seconds + $4,
		        messages_count = messages_count + $5
		  WHERE id = $6`,
		newXP, newLevel, runningBalance, voiceSecondsDelta, messagesDelta, playerID,
	); err != nil {
		return nil, fmt.Errorf("failed to update player xp and level: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit xp grant: %w", err)
	}

	return &models.XPGrantResult{
		PlayerID:     playerID,
		XPGained:     xpDelta,
		NewXP:        newXP,
		OldLevel:     curLevel,
		NewLevel:     newLevel,
		CoinsAwarded: totalCoins,
		NewBalance:   runningBalance,
	}, nil
}

// GrantCoins credits positive ValCoins to a player with optional idempotency via refKey.
// Returns (creditedAmount, newBalance, applied, err). When refKey was already recorded
// for (playerID, reason, refKey), applied is false and balance is unchanged.
func (r *EconomyPostgres) GrantCoins(ctx context.Context, playerID, amount int, reason, refKey, note string) (int, int, bool, error) {
	if amount <= 0 {
		return 0, 0, false, fmt.Errorf("%w: grant amount must be positive", domain.ErrInvalidAmount)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to begin grant transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var curPoints int
	err = tx.QueryRowContext(ctx,
		`SELECT points FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		playerID,
	).Scan(&curPoints)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to lock player for coin grant: %w", err)
	}

	newBalance := curPoints + amount
	inserted, err := insertLedgerTx(ctx, tx, playerID, amount, newBalance, reason, refKey, note)
	if err != nil {
		return 0, 0, false, err
	}
	if !inserted {
		if err := tx.Commit(); err != nil {
			return 0, 0, false, err
		}
		return 0, curPoints, false, nil
	}

	if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, newBalance, playerID); err != nil {
		return 0, 0, false, fmt.Errorf("failed to credit player coins: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, false, fmt.Errorf("failed to commit coin grant: %w", err)
	}
	return amount, newBalance, true, nil
}

// AdminAdjustCoins adds or subtracts coins for a player, clamping balance at 0.
func (r *EconomyPostgres) AdminAdjustCoins(ctx context.Context, playerID, delta int, note string) (int, int, error) {
	if delta == 0 {
		return 0, 0, fmt.Errorf("%w: delta cannot be zero", domain.ErrInvalidAmount)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin admin coin adjustment: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var curPoints int
	err = tx.QueryRowContext(ctx,
		`SELECT points FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		playerID,
	).Scan(&curPoints)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("failed to lock player for admin adjust: %w", err)
	}

	newBalance := curPoints + delta
	if newBalance < 0 {
		newBalance = 0
	}
	actualDelta := newBalance - curPoints
	if actualDelta == 0 {
		if err := tx.Commit(); err != nil {
			return 0, 0, err
		}
		return 0, curPoints, nil
	}

	if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, newBalance, playerID); err != nil {
		return 0, 0, fmt.Errorf("failed to update player points: %w", err)
	}
	if _, err := insertLedgerTx(ctx, tx, playerID, actualDelta, newBalance, domain.LedgerReasonAdminGrant, "", note); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("failed to commit admin adjust: %w", err)
	}
	return actualDelta, newBalance, nil
}

// ClaimDaily grants the player's daily ValCoins reward if the cooldown has elapsed.
func (r *EconomyPostgres) ClaimDaily(ctx context.Context, playerID int, now time.Time) (*models.DailyClaimResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin daily claim: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var curPoints, streak int
	var lastDaily sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT points, last_daily_at, daily_streak
		   FROM players
		  WHERE id = $1 AND is_deleted = FALSE
		    FOR UPDATE`,
		playerID,
	).Scan(&curPoints, &lastDaily, &streak)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock player for daily claim: %w", err)
	}

	if lastDaily.Valid {
		elapsed := now.Sub(lastDaily.Time)
		if elapsed < domain.DailyCooldown {
			return &models.DailyClaimResult{
				NewBalance:  curPoints,
				Streak:      streak,
				NextClaimAt: lastDaily.Time.Add(domain.DailyCooldown),
			}, domain.ErrDailyAlreadyClaimed
		}
		if elapsed <= domain.DailyStreakWindow {
			streak++
		} else {
			streak = 1
		}
	} else {
		streak = 1
	}

	reward := domain.DailyRewardForStreak(streak)
	newBalance := curPoints + reward

	if _, err := tx.ExecContext(ctx,
		`UPDATE players SET points = $1, last_daily_at = $2, daily_streak = $3 WHERE id = $4`,
		newBalance, now, streak, playerID,
	); err != nil {
		return nil, fmt.Errorf("failed to update daily claim: %w", err)
	}

	note := fmt.Sprintf("Ежедневный бонус (серия %d дн.)", streak)
	if _, err := insertLedgerTx(ctx, tx, playerID, reward, newBalance, domain.LedgerReasonDaily, "", note); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit daily claim: %w", err)
	}

	return &models.DailyClaimResult{
		CoinsAwarded: reward,
		NewBalance:   newBalance,
		Streak:       streak,
		NextClaimAt:  now.Add(domain.DailyCooldown),
	}, nil
}

// TipCoins transfers ValCoins from sender to receiver with an optional anti-inflation burn.
func (r *EconomyPostgres) TipCoins(ctx context.Context, senderID, receiverID, amount, burnAmount int) (*models.TipResult, error) {
	if senderID == receiverID {
		return nil, domain.ErrSelfTip
	}
	if amount <= 0 || burnAmount < 0 || burnAmount >= amount {
		return nil, fmt.Errorf("%w: invalid tip amount %d (burn %d)", domain.ErrInvalidAmount, amount, burnAmount)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tip transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	// Lock both players in ascending ID order so concurrent tips A->B and B->A never deadlock.
	rows, err := tx.QueryContext(ctx,
		`SELECT id, name, points FROM players WHERE id IN ($1, $2) AND is_deleted = FALSE ORDER BY id FOR UPDATE`,
		senderID, receiverID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to lock players for tip: %w", err)
	}
	type locked struct {
		name   string
		points int
	}
	found := make(map[int]locked, 2)
	for rows.Next() {
		var id, pts int
		var name string
		if err := rows.Scan(&id, &name, &pts); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan tip player: %w", err)
		}
		found[id] = locked{name: name, points: pts}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sender, okSender := found[senderID]
	receiver, okReceiver := found[receiverID]
	if !okSender || !okReceiver {
		return nil, domain.ErrPlayerNotFound
	}
	if sender.points < amount {
		return nil, fmt.Errorf("have %d, need %d: %w", sender.points, amount, domain.ErrInsufficientPoints)
	}

	received := amount - burnAmount
	senderNew := sender.points - amount
	receiverNew := receiver.points + received

	if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, senderNew, senderID); err != nil {
		return nil, fmt.Errorf("failed to debit tip sender: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, receiverNew, receiverID); err != nil {
		return nil, fmt.Errorf("failed to credit tip receiver: %w", err)
	}

	outNote := fmt.Sprintf("Перевод для %s", receiver.name)
	inNote := fmt.Sprintf("Перевод от %s", sender.name)
	if _, err := insertLedgerTx(ctx, tx, senderID, -amount, senderNew, domain.LedgerReasonTipOut, "", outNote); err != nil {
		return nil, err
	}
	if _, err := insertLedgerTx(ctx, tx, receiverID, received, receiverNew, domain.LedgerReasonTipIn, "", inNote); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit tip: %w", err)
	}

	return &models.TipResult{
		SenderID:        senderID,
		ReceiverID:      receiverID,
		AmountSent:      amount,
		AmountReceived:  received,
		Burned:          burnAmount,
		SenderBalance:   senderNew,
		ReceiverBalance: receiverNew,
	}, nil
}

// GetRichLeaderboard returns the top players sorted by ValCoins balance.
func (r *EconomyPostgres) GetRichLeaderboard(ctx context.Context, limit int) ([]models.RichPlayer, error) {
	return r.queryLeaderboard(ctx,
		`SELECT id, name, COALESCE(discord_id, ''), points, level, xp, voice_seconds, profile_title
		   FROM players
		  WHERE is_deleted = FALSE
		  ORDER BY points DESC, xp DESC, id ASC
		  LIMIT $1`,
		limit,
	)
}

// GetXPLeaderboard returns the top players sorted by activity XP and level.
func (r *EconomyPostgres) GetXPLeaderboard(ctx context.Context, limit int) ([]models.RichPlayer, error) {
	return r.queryLeaderboard(ctx,
		`SELECT id, name, COALESCE(discord_id, ''), points, level, xp, voice_seconds, profile_title
		   FROM players
		  WHERE is_deleted = FALSE
		  ORDER BY xp DESC, level DESC, points DESC, id ASC
		  LIMIT $1`,
		limit,
	)
}

func (r *EconomyPostgres) queryLeaderboard(ctx context.Context, query string, limit int) ([]models.RichPlayer, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query economy leaderboard: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var out []models.RichPlayer
	for rows.Next() {
		var p models.RichPlayer
		if err := rows.Scan(&p.PlayerID, &p.Name, &p.DiscordID, &p.Points, &p.Level, &p.XP, &p.VoiceSeconds, &p.ProfileTitle); err != nil {
			return nil, fmt.Errorf("failed to scan leaderboard player: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlayerBetStats aggregates settled bets placed by playerID.
func (r *EconomyPostgres) GetPlayerBetStats(ctx context.Context, playerID int) (models.PlayerBetStats, error) {
	var stats models.PlayerBetStats
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COUNT(*) FILTER (WHERE payout > amount),
		        COALESCE(SUM(amount), 0),
		        COALESCE(SUM(payout), 0)
		   FROM match_bets
		  WHERE player_id = $1 AND settled_at IS NOT NULL`,
		playerID,
	).Scan(&stats.TotalBets, &stats.WonBets, &stats.TotalWagered, &stats.TotalPayout)
	if err != nil {
		return models.PlayerBetStats{}, fmt.Errorf("failed to query player bet stats: %w", err)
	}
	return stats, nil
}

// =====================================================================
// Shop catalog & orders
// =====================================================================

func scanShopItem(scanner interface{ Scan(dest ...any) error }) (models.ShopItem, error) {
	var item models.ShopItem
	var stock sql.NullInt64
	if err := scanner.Scan(
		&item.ID, &item.Code, &item.Name, &item.Description, &item.Kind,
		&item.Price, &item.Payload, &stock, &item.Enabled, &item.SortOrder, &item.CreatedAt,
	); err != nil {
		return models.ShopItem{}, err
	}
	if stock.Valid {
		v := int(stock.Int64)
		item.Stock = &v
	}
	return item, nil
}

func (r *EconomyPostgres) ListShopItems(ctx context.Context, includeDisabled bool) ([]models.ShopItem, error) {
	query := `SELECT id, code, name, description, kind, price, payload, stock, enabled, sort_order, created_at
	            FROM shop_items`
	if !includeDisabled {
		query += ` WHERE enabled = TRUE`
	}
	query += ` ORDER BY sort_order ASC, id ASC`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list shop items: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var items []models.ShopItem
	for rows.Next() {
		item, err := scanShopItem(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan shop item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *EconomyPostgres) GetShopItemByID(ctx context.Context, itemID int) (*models.ShopItem, error) {
	item, err := scanShopItem(r.db.QueryRowContext(ctx,
		`SELECT id, code, name, description, kind, price, payload, stock, enabled, sort_order, created_at
		   FROM shop_items WHERE id = $1`, itemID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrShopItemNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get shop item %d: %w", itemID, err)
	}
	return &item, nil
}

func (r *EconomyPostgres) UpsertShopItem(ctx context.Context, item *models.ShopItem) (*models.ShopItem, error) {
	var stock any
	if item.Stock != nil {
		stock = *item.Stock
	}
	out, err := scanShopItem(r.db.QueryRowContext(ctx,
		`INSERT INTO shop_items (code, name, description, kind, price, payload, stock, enabled, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (code) DO UPDATE
		    SET name = EXCLUDED.name,
		        description = EXCLUDED.description,
		        kind = EXCLUDED.kind,
		        price = EXCLUDED.price,
		        payload = EXCLUDED.payload,
		        stock = EXCLUDED.stock,
		        enabled = EXCLUDED.enabled,
		        sort_order = EXCLUDED.sort_order
		 RETURNING id, code, name, description, kind, price, payload, stock, enabled, sort_order, created_at`,
		item.Code, item.Name, item.Description, item.Kind, item.Price, item.Payload, stock, item.Enabled, item.SortOrder,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert shop item %q: %w", item.Code, err)
	}
	return &out, nil
}

func (r *EconomyPostgres) SetShopItemEnabled(ctx context.Context, code string, enabled bool) (*models.ShopItem, error) {
	out, err := scanShopItem(r.db.QueryRowContext(ctx,
		`UPDATE shop_items SET enabled = $2 WHERE code = $1
		 RETURNING id, code, name, description, kind, price, payload, stock, enabled, sort_order, created_at`,
		code, enabled,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrShopItemNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to toggle shop item %q: %w", code, err)
	}
	return &out, nil
}

// PurchaseShopItem atomically validates stock & balance, deducts coins, applies
// instant item effects (title, color, insurance) or creates a pending prize order.
func (r *EconomyPostgres) PurchaseShopItem(ctx context.Context, playerID, itemID int, customInput string) (*models.ShopOrder, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin shop purchase: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	// Lock shop item first, then player (consistent lock order with ResolveShopOrder).
	item, err := scanShopItem(tx.QueryRowContext(ctx,
		`SELECT id, code, name, description, kind, price, payload, stock, enabled, sort_order, created_at
		   FROM shop_items WHERE id = $1 FOR UPDATE`, itemID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrShopItemNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock shop item: %w", err)
	}
	if !item.Enabled {
		return nil, domain.ErrShopItemUnavailable
	}
	if item.Stock != nil && *item.Stock <= 0 {
		return nil, domain.ErrShopOutOfStock
	}

	// Validate item-specific input before debiting.
	var parsedColor int
	switch item.Kind {
	case domain.ShopKindTitle:
		clean, err := domain.SanitizeProfileTitle(customInput)
		if err != nil {
			return nil, err
		}
		customInput = clean
	case domain.ShopKindProfileColor:
		colorSrc := item.Payload
		if colorSrc == "" {
			colorSrc = customInput
		}
		c, err := domain.ParseHexColor(colorSrc)
		if err != nil {
			return nil, err
		}
		parsedColor = c
	}

	var curPoints int
	var playerName string
	var discordID sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT name, discord_id, points FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		playerID,
	).Scan(&playerName, &discordID, &curPoints)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrPlayerNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock player for purchase: %w", err)
	}
	if curPoints < item.Price {
		return nil, fmt.Errorf("have %d, need %d: %w", curPoints, item.Price, domain.ErrInsufficientPoints)
	}

	newBalance := curPoints - item.Price

	if item.Stock != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE shop_items SET stock = stock - 1 WHERE id = $1`, item.ID); err != nil {
			return nil, fmt.Errorf("failed to decrement stock: %w", err)
		}
	}

	status := domain.ShopOrderFulfilled
	var resolvedAt any = time.Now()
	switch item.Kind {
	case domain.ShopKindTitle:
		if _, err := tx.ExecContext(ctx,
			`UPDATE players SET points = $1, profile_title = $2 WHERE id = $3`,
			newBalance, customInput, playerID,
		); err != nil {
			return nil, fmt.Errorf("failed to apply custom title: %w", err)
		}
	case domain.ShopKindProfileColor:
		if _, err := tx.ExecContext(ctx,
			`UPDATE players SET points = $1, profile_color = $2 WHERE id = $3`,
			newBalance, parsedColor, playerID,
		); err != nil {
			return nil, fmt.Errorf("failed to apply profile color: %w", err)
		}
	case domain.ShopKindBetInsurance:
		if _, err := tx.ExecContext(ctx,
			`UPDATE players SET points = $1, bet_insurance_count = bet_insurance_count + 1 WHERE id = $2`,
			newBalance, playerID,
		); err != nil {
			return nil, fmt.Errorf("failed to grant bet insurance: %w", err)
		}
	case domain.ShopKindDiscordRole:
		if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, newBalance, playerID); err != nil {
			return nil, fmt.Errorf("failed to debit coins for role: %w", err)
		}
	case domain.ShopKindPrize:
		status = domain.ShopOrderPending
		resolvedAt = nil
		if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, newBalance, playerID); err != nil {
			return nil, fmt.Errorf("failed to debit coins for prize: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported shop item kind %q", item.Kind)
	}

	var order models.ShopOrder
	var resolved sql.NullTime
	err = tx.QueryRowContext(ctx,
		`INSERT INTO shop_orders (player_id, item_id, item_name, item_kind, price_paid, custom_input, status, resolved_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at, resolved_at`,
		playerID, item.ID, item.Name, item.Kind, item.Price, customInput, status, resolvedAt,
	).Scan(&order.ID, &order.CreatedAt, &resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to insert shop order: %w", err)
	}

	note := fmt.Sprintf("Покупка: %s", item.Name)
	if _, err := insertLedgerTx(ctx, tx, playerID, -item.Price, newBalance, domain.LedgerReasonShopBuy, fmt.Sprintf("order:%d", order.ID), note); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit shop purchase: %w", err)
	}

	order.PlayerID = playerID
	order.PlayerName = playerName
	if discordID.Valid {
		order.DiscordID = discordID.String
	}
	order.ItemID = item.ID
	order.ItemCode = item.Code
	order.ItemName = item.Name
	order.ItemKind = item.Kind
	order.ItemPayload = item.Payload
	order.PricePaid = item.Price
	order.CustomInput = customInput
	order.Status = status
	order.NewBalance = newBalance
	if resolved.Valid {
		t := resolved.Time
		order.ResolvedAt = &t
	}
	return &order, nil
}

// ResolveShopOrder approves or cancels a pending prize order (refunding coins & stock on cancel).
func (r *EconomyPostgres) ResolveShopOrder(ctx context.Context, orderID int, approve bool, adminDiscordID string) (*models.ShopOrder, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin order resolution: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var order models.ShopOrder
	var discordID sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT o.id, o.player_id, p.name, p.discord_id, o.item_id, o.item_name, o.item_kind,
		        o.price_paid, o.custom_input, o.status, o.created_at
		   FROM shop_orders o
		   JOIN players p ON p.id = o.player_id
		  WHERE o.id = $1
		    FOR UPDATE OF o`,
		orderID,
	).Scan(
		&order.ID, &order.PlayerID, &order.PlayerName, &discordID,
		&order.ItemID, &order.ItemName, &order.ItemKind,
		&order.PricePaid, &order.CustomInput, &order.Status, &order.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrShopOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock shop order: %w", err)
	}
	if discordID.Valid {
		order.DiscordID = discordID.String
	}
	if order.Status != domain.ShopOrderPending {
		return nil, domain.ErrShopOrderAlreadyResolved
	}

	now := time.Now()
	newStatus := domain.ShopOrderFulfilled
	if !approve {
		newStatus = domain.ShopOrderCancelled

		// Restore stock if item is stock-limited.
		if _, err := tx.ExecContext(ctx,
			`UPDATE shop_items SET stock = stock + 1 WHERE id = $1 AND stock IS NOT NULL`,
			order.ItemID,
		); err != nil {
			return nil, fmt.Errorf("failed to restore shop stock: %w", err)
		}

		// Refund coins to player.
		var newBalance int
		if err := tx.QueryRowContext(ctx,
			`UPDATE players SET points = points + $1 WHERE id = $2 RETURNING points`,
			order.PricePaid, order.PlayerID,
		).Scan(&newBalance); err != nil {
			return nil, fmt.Errorf("failed to refund cancelled order: %w", err)
		}
		order.NewBalance = newBalance

		if _, err := insertLedgerTx(
			ctx, tx, order.PlayerID, order.PricePaid, newBalance,
			domain.LedgerReasonOrderRefund, fmt.Sprintf("order:%d", order.ID),
			fmt.Sprintf("Возврат: %s", order.ItemName),
		); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_orders SET status = $1, handled_by = $2, resolved_at = $3 WHERE id = $4`,
		newStatus, adminDiscordID, now, order.ID,
	); err != nil {
		return nil, fmt.Errorf("failed to update shop order status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit order resolution: %w", err)
	}

	order.Status = newStatus
	order.HandledBy = adminDiscordID
	order.ResolvedAt = &now
	return &order, nil
}

// RefundFulfilledOrder cancels an immediately fulfilled order (e.g. when Discord
// refused to grant a role) and returns coins and stock cleanly.
func (r *EconomyPostgres) RefundFulfilledOrder(ctx context.Context, orderID int, reason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin order rollback: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var playerID, itemID, pricePaid int
	var itemName, status string
	err = tx.QueryRowContext(ctx,
		`SELECT player_id, item_id, item_name, price_paid, status FROM shop_orders WHERE id = $1 FOR UPDATE`,
		orderID,
	).Scan(&playerID, &itemID, &itemName, &pricePaid, &status)
	if err != nil {
		return fmt.Errorf("failed to lock order %d for rollback: %w", orderID, err)
	}
	if status == domain.ShopOrderCancelled {
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_items SET stock = stock + 1 WHERE id = $1 AND stock IS NOT NULL`, itemID,
	); err != nil {
		return fmt.Errorf("failed to restore stock: %w", err)
	}

	var newBalance int
	if err := tx.QueryRowContext(ctx,
		`UPDATE players SET points = points + $1 WHERE id = $2 RETURNING points`, pricePaid, playerID,
	).Scan(&newBalance); err != nil {
		return fmt.Errorf("failed to refund order coins: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_orders SET status = $1, handled_by = $2, resolved_at = NOW() WHERE id = $3`,
		domain.ShopOrderCancelled, "system", orderID,
	); err != nil {
		return fmt.Errorf("failed to mark order cancelled: %w", err)
	}

	note := fmt.Sprintf("Автовозврат (%s): %s", reason, itemName)
	if _, err := insertLedgerTx(ctx, tx, playerID, pricePaid, newBalance, domain.LedgerReasonOrderRefund, fmt.Sprintf("order:%d", orderID), note); err != nil {
		return err
	}
	return tx.Commit()
}

// GetPlayerOrders lists a player's recent shop orders.
func (r *EconomyPostgres) GetPlayerOrders(ctx context.Context, playerID, limit int) ([]models.ShopOrder, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, player_id, item_id, item_name, item_kind, price_paid, custom_input, status, COALESCE(handled_by, ''), created_at, resolved_at
		   FROM shop_orders
		  WHERE player_id = $1
		  ORDER BY created_at DESC, id DESC
		  LIMIT $2`,
		playerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query shop orders: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var orders []models.ShopOrder
	for rows.Next() {
		var o models.ShopOrder
		var resolved sql.NullTime
		if err := rows.Scan(&o.ID, &o.PlayerID, &o.ItemID, &o.ItemName, &o.ItemKind, &o.PricePaid, &o.CustomInput, &o.Status, &o.HandledBy, &o.CreatedAt, &resolved); err != nil {
			return nil, fmt.Errorf("failed to scan shop order: %w", err)
		}
		if resolved.Valid {
			t := resolved.Time
			o.ResolvedAt = &t
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// =====================================================================
// Admin moderation, XP/profile fixes, shop refunds/deletes, match revert
// =====================================================================

const pgForeignKeyViolation = "23503"

// AdminAdjustXP adds or subtracts XP for a player (clamped at 0), recalculating
// their level. Upward level crossings still award idempotent level-up coins.
func (r *EconomyPostgres) AdminAdjustXP(ctx context.Context, playerID int, xpDelta int64) (*models.XPGrantResult, error) {
	if xpDelta == 0 {
		return nil, fmt.Errorf("%w: xp delta cannot be zero", domain.ErrInvalidAmount)
	}
	if xpDelta > 0 {
		return r.GrantXP(ctx, playerID, xpDelta, 0, 0)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin admin xp adjust: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var curPoints, curLevel int
	var curXP int64
	err = tx.QueryRowContext(ctx,
		`SELECT points, xp, level FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		playerID,
	).Scan(&curPoints, &curXP, &curLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock player for admin xp adjust: %w", err)
	}

	newXP := curXP + xpDelta
	if newXP < 0 {
		newXP = 0
	}
	newLevel, _, _ := domain.LevelFromXP(newXP)

	if _, err := tx.ExecContext(ctx, `UPDATE players SET xp = $1, level = $2 WHERE id = $3`, newXP, newLevel, playerID); err != nil {
		return nil, fmt.Errorf("failed to update player xp: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit admin xp adjust: %w", err)
	}

	return &models.XPGrantResult{
		PlayerID:   playerID,
		XPGained:   newXP - curXP,
		NewXP:      newXP,
		OldLevel:   curLevel,
		NewLevel:   newLevel,
		NewBalance: curPoints,
	}, nil
}

// AdminSetLevel sets a player's exact level and baseline XP without granting coins.
func (r *EconomyPostgres) AdminSetLevel(ctx context.Context, playerID, targetLevel int) (*models.XPGrantResult, error) {
	if targetLevel < 1 || targetLevel > 200 {
		return nil, fmt.Errorf("%w: level must be between 1 and 200", domain.ErrInvalidAmount)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin admin set level: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var curPoints, curLevel int
	var curXP int64
	err = tx.QueryRowContext(ctx,
		`SELECT points, xp, level FROM players WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		playerID,
	).Scan(&curPoints, &curXP, &curLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock player for set level: %w", err)
	}

	newXP := domain.TotalXPForLevel(targetLevel)
	if _, err := tx.ExecContext(ctx, `UPDATE players SET xp = $1, level = $2 WHERE id = $3`, newXP, targetLevel, playerID); err != nil {
		return nil, fmt.Errorf("failed to update level: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit set level: %w", err)
	}

	return &models.XPGrantResult{
		PlayerID:   playerID,
		XPGained:   newXP - curXP,
		NewXP:      newXP,
		OldLevel:   curLevel,
		NewLevel:   targetLevel,
		NewBalance: curPoints,
	}, nil
}

// AdminSetProfileTitle sets or clears (when empty) a player's custom profile title.
func (r *EconomyPostgres) AdminSetProfileTitle(ctx context.Context, playerID int, title string) error {
	clean := ""
	if title != "" {
		var err error
		clean, err = domain.SanitizeProfileTitle(title)
		if err != nil {
			return err
		}
	}
	res, err := r.db.ExecContext(ctx, `UPDATE players SET profile_title = $1 WHERE id = $2 AND is_deleted = FALSE`, clean, playerID)
	if err != nil {
		return fmt.Errorf("failed to update profile title: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrPlayerNotFound
	}
	return nil
}

// AdminClearProfileColor resets a player's profile embed color back to default.
func (r *EconomyPostgres) AdminClearProfileColor(ctx context.Context, playerID int) error {
	res, err := r.db.ExecContext(ctx, `UPDATE players SET profile_color = 0 WHERE id = $1 AND is_deleted = FALSE`, playerID)
	if err != nil {
		return fmt.Errorf("failed to clear profile color: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrPlayerNotFound
	}
	return nil
}

// DeleteShopItem removes a shop item by code if no orders reference it.
func (r *EconomyPostgres) DeleteShopItem(ctx context.Context, code string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM shop_items WHERE code = $1`, code)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgForeignKeyViolation {
			return domain.ErrShopItemHasOrders
		}
		return fmt.Errorf("failed to delete shop item %q: %w", code, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrShopItemNotFound
	}
	return nil
}

// AdminRefundShopOrder refunds either a pending OR already fulfilled shop order,
// returning coins and stock and reversing any instant cosmetic/coupon effect.
func (r *EconomyPostgres) AdminRefundShopOrder(ctx context.Context, orderID int, adminDiscordID string) (*models.ShopOrder, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin admin order refund: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var order models.ShopOrder
	var discordID sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT o.id, o.player_id, p.name, p.discord_id, o.item_id, i.code, i.payload,
		        o.item_name, o.item_kind, o.price_paid, o.custom_input, o.status, o.created_at
		   FROM shop_orders o
		   JOIN players p ON p.id = o.player_id
		   JOIN shop_items i ON i.id = o.item_id
		  WHERE o.id = $1
		    FOR UPDATE OF o`,
		orderID,
	).Scan(
		&order.ID, &order.PlayerID, &order.PlayerName, &discordID,
		&order.ItemID, &order.ItemCode, &order.ItemPayload,
		&order.ItemName, &order.ItemKind, &order.PricePaid, &order.CustomInput,
		&order.Status, &order.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrShopOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock shop order %d: %w", orderID, err)
	}
	if discordID.Valid {
		order.DiscordID = discordID.String
	}
	if order.Status == domain.ShopOrderCancelled {
		return nil, domain.ErrShopOrderAlreadyResolved
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_items SET stock = stock + 1 WHERE id = $1 AND stock IS NOT NULL`, order.ItemID,
	); err != nil {
		return nil, fmt.Errorf("failed to restore stock: %w", err)
	}

	// Reverse instant side-effects when refunding an already fulfilled order.
	var newBalance int
	if order.Status == domain.ShopOrderFulfilled {
		switch order.ItemKind {
		case domain.ShopKindTitle:
			err = tx.QueryRowContext(ctx,
				`UPDATE players SET points = points + $1, profile_title = '' WHERE id = $2 RETURNING points`,
				order.PricePaid, order.PlayerID,
			).Scan(&newBalance)
		case domain.ShopKindProfileColor:
			err = tx.QueryRowContext(ctx,
				`UPDATE players SET points = points + $1, profile_color = 0 WHERE id = $2 RETURNING points`,
				order.PricePaid, order.PlayerID,
			).Scan(&newBalance)
		case domain.ShopKindBetInsurance:
			err = tx.QueryRowContext(ctx,
				`UPDATE players SET points = points + $1, bet_insurance_count = GREATEST(0, bet_insurance_count - 1) WHERE id = $2 RETURNING points`,
				order.PricePaid, order.PlayerID,
			).Scan(&newBalance)
		default:
			err = tx.QueryRowContext(ctx,
				`UPDATE players SET points = points + $1 WHERE id = $2 RETURNING points`,
				order.PricePaid, order.PlayerID,
			).Scan(&newBalance)
		}
	} else {
		err = tx.QueryRowContext(ctx,
			`UPDATE players SET points = points + $1 WHERE id = $2 RETURNING points`,
			order.PricePaid, order.PlayerID,
		).Scan(&newBalance)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to refund order %d coins: %w", orderID, err)
	}

	now := time.Now()
	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_orders SET status = $1, handled_by = $2, resolved_at = $3 WHERE id = $4`,
		domain.ShopOrderCancelled, adminDiscordID, now, order.ID,
	); err != nil {
		return nil, fmt.Errorf("failed to mark order cancelled: %w", err)
	}

	note := fmt.Sprintf("Возврат заказа #%d (%s)", order.ID, order.ItemName)
	if _, err := insertLedgerTx(ctx, tx, order.PlayerID, order.PricePaid, newBalance, domain.LedgerReasonOrderRefund, fmt.Sprintf("order:%d", order.ID), note); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit admin order refund: %w", err)
	}

	order.Status = domain.ShopOrderCancelled
	order.HandledBy = adminDiscordID
	order.NewBalance = newBalance
	order.ResolvedAt = &now
	return &order, nil
}

// RevertFinishedMatch atomically rolls back a mistakenly closed lobby match:
// restores MMR deltas from mmr_history, reverses match_reward coins, unwinds
// settled bets back to each bettor's stake (restoring insurance coupons),
// soft-deletes any linked OCR match scoreboard, and returns the lobby_match to
// ACTIVE so the referee can record the correct result.
func (r *EconomyPostgres) RevertFinishedMatch(ctx context.Context, matchID int) (*models.MatchRevertSummary, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin revert match tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort cleanup

	var status string
	var winner, threadID sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT status, winner, thread_id FROM lobby_matches WHERE id = $1 FOR UPDATE`, matchID,
	).Scan(&status, &winner, &threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrMatchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock lobby match %d: %w", matchID, err)
	}
	if status != string(models.LobbyMatchStatusFinished) || !winner.Valid || winner.String == "" {
		return nil, domain.ErrMatchNotRevertible
	}

	summary := &models.MatchRevertSummary{
		MatchID:      matchID,
		OldWinner:    winner.String,
		RestoredMMRs: make(map[int]int),
	}
	if threadID.Valid {
		summary.ThreadID = threadID.String
	}

	// 1. Revert MMR shifts recorded for this match (ordered by player_id).
	type mmrRow struct {
		playerID int
		delta    int
	}
	var mmrRows []mmrRow
	rows, err := tx.QueryContext(ctx,
		`SELECT player_id, delta FROM mmr_history WHERE match_id = $1 ORDER BY player_id FOR UPDATE`, matchID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query mmr_history for revert: %w", err)
	}
	for rows.Next() {
		var rRow mmrRow
		if err := rows.Scan(&rRow.playerID, &rRow.delta); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan mmr_history: %w", err)
		}
		mmrRows = append(mmrRows, rRow)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, mr := range mmrRows {
		var restored int
		if err := tx.QueryRowContext(ctx,
			`UPDATE players SET current_mmr = GREATEST(0, COALESCE(current_mmr, 1000) - $1) WHERE id = $2 RETURNING current_mmr`,
			mr.delta, mr.playerID,
		).Scan(&restored); err != nil {
			return nil, fmt.Errorf("failed to revert MMR for player %d: %w", mr.playerID, err)
		}
		summary.RestoredMMRs[mr.playerID] = restored
		summary.RevertedMMRCount++
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mmr_history WHERE match_id = $1`, matchID); err != nil {
		return nil, fmt.Errorf("failed to delete reverted mmr_history: %w", err)
	}

	// 2. Revert match_reward coins and release ref_key so re-closing can award fresh coins.
	refKey := fmt.Sprintf("match:%d", matchID)
	type coinRow struct {
		ledgerID int
		playerID int
		amount   int
	}
	var coinRows []coinRow
	cRows, err := tx.QueryContext(ctx,
		`SELECT id, player_id, amount
		   FROM currency_ledger
		  WHERE reason = $1 AND ref_key = $2
		  ORDER BY player_id, id
		    FOR UPDATE`,
		domain.LedgerReasonMatchReward, refKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query match coin rewards for revert: %w", err)
	}
	for cRows.Next() {
		var cr coinRow
		if err := cRows.Scan(&cr.ledgerID, &cr.playerID, &cr.amount); err != nil {
			_ = cRows.Close()
			return nil, err
		}
		coinRows = append(coinRows, cr)
	}
	if err := cRows.Close(); err != nil {
		return nil, err
	}
	if err := cRows.Err(); err != nil {
		return nil, err
	}

	for _, cr := range coinRows {
		if _, err := tx.ExecContext(ctx, `UPDATE currency_ledger SET ref_key = NULL WHERE id = $1`, cr.ledgerID); err != nil {
			return nil, fmt.Errorf("failed to release match reward ref_key: %w", err)
		}
		if cr.amount <= 0 {
			continue
		}
		var curBal int
		if err := tx.QueryRowContext(ctx, `SELECT points FROM players WHERE id = $1 FOR UPDATE`, cr.playerID).Scan(&curBal); err != nil {
			return nil, fmt.Errorf("failed to lock player %d for coin revert: %w", cr.playerID, err)
		}
		newBal := curBal - cr.amount
		if newBal < 0 {
			newBal = 0
		}
		deducted := curBal - newBal
		if deducted > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE players SET points = $1 WHERE id = $2`, newBal, cr.playerID); err != nil {
				return nil, fmt.Errorf("failed to deduct reverted match coins: %w", err)
			}
			note := fmt.Sprintf("Откат награды за матч #%d", matchID)
			if _, err := insertLedgerTx(ctx, tx, cr.playerID, -deducted, newBal, domain.LedgerReasonMatchRevert, "", note); err != nil {
				return nil, err
			}
			summary.RevertedMatchCoins += deducted
		}
	}

	// 3. Unwind settled bets so every bettor is refunded back to their original stake.
	type betRow struct {
		id       int
		playerID int
		amount   int
		payout   int
		insured  bool
	}
	var betRows []betRow
	bRows, err := tx.QueryContext(ctx,
		`SELECT id, player_id, amount, payout, insured
		   FROM match_bets
		  WHERE match_id = $1 AND settled_at IS NOT NULL
		  ORDER BY player_id, id
		    FOR UPDATE`,
		matchID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query settled bets for revert: %w", err)
	}
	for bRows.Next() {
		var br betRow
		if err := bRows.Scan(&br.id, &br.playerID, &br.amount, &br.payout, &br.insured); err != nil {
			_ = bRows.Close()
			return nil, err
		}
		betRows = append(betRows, br)
	}
	if err := bRows.Close(); err != nil {
		return nil, err
	}
	if err := bRows.Err(); err != nil {
		return nil, err
	}

	for _, br := range betRows {
		netDelta := br.amount - br.payout
		restoreIns := 0
		if br.insured {
			restoreIns = 1
		}

		pid := br.playerID
		if pid > 0 && (netDelta != 0 || restoreIns > 0) {
			var curBal int
			if err := tx.QueryRowContext(ctx, `SELECT points FROM players WHERE id = $1 FOR UPDATE`, pid).Scan(&curBal); err != nil {
				return nil, fmt.Errorf("failed to lock bettor %d for match revert: %w", pid, err)
			}
			newBal := curBal + netDelta
			if newBal < 0 {
				newBal = 0
			}
			applied := newBal - curBal
			if _, err := tx.ExecContext(ctx,
				`UPDATE players SET points = $1, bet_insurance_count = bet_insurance_count + $2 WHERE id = $3`,
				newBal, restoreIns, pid,
			); err != nil {
				return nil, fmt.Errorf("failed to adjust bettor %d balance on revert: %w", pid, err)
			}
			if applied != 0 {
				note := fmt.Sprintf("Откат ставки (матч #%d откачен)", matchID)
				if _, err := insertLedgerTx(ctx, tx, pid, applied, newBal, domain.LedgerReasonMatchRevert, "", note); err != nil {
					return nil, err
				}
			}
		}

		if _, err := tx.ExecContext(ctx, `UPDATE match_bets SET payout = amount WHERE id = $1`, br.id); err != nil {
			return nil, fmt.Errorf("failed to update reverted bet %d: %w", br.id, err)
		}
		summary.RevertedBetsCount++
	}

	// 4. Soft-delete any linked OCR scoreboard matches and undo their medal counters.
	type linkedMatch struct {
		id    int
		mvpID sql.NullInt64
		svpID sql.NullInt64
	}
	var linked []linkedMatch
	mRows, err := tx.QueryContext(ctx,
		`SELECT id, mvp_player_id, svp_player_id FROM matches WHERE lobby_match_id = $1 AND is_deleted = FALSE FOR UPDATE`,
		matchID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query linked matches: %w", err)
	}
	for mRows.Next() {
		var lm linkedMatch
		if err := mRows.Scan(&lm.id, &lm.mvpID, &lm.svpID); err != nil {
			_ = mRows.Close()
			return nil, err
		}
		linked = append(linked, lm)
	}
	if err := mRows.Close(); err != nil {
		return nil, err
	}
	if err := mRows.Err(); err != nil {
		return nil, err
	}

	for _, lm := range linked {
		if _, err := tx.ExecContext(ctx, `UPDATE matches SET is_deleted = TRUE, deleted_at = NOW() WHERE id = $1`, lm.id); err != nil {
			return nil, fmt.Errorf("failed to soft-delete linked match %d: %w", lm.id, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE player_results SET is_deleted = TRUE WHERE match_id = $1`, lm.id); err != nil {
			return nil, fmt.Errorf("failed to soft-delete linked player_results: %w", err)
		}
		if lm.mvpID.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE players SET mvp_count = GREATEST(0, COALESCE(mvp_count, 0) - 1) WHERE id = $1`, lm.mvpID.Int64); err != nil {
				return nil, fmt.Errorf("failed to decrement mvp_count: %w", err)
			}
		}
		if lm.svpID.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE players SET svp_count = GREATEST(0, COALESCE(svp_count, 0) - 1) WHERE id = $1`, lm.svpID.Int64); err != nil {
				return nil, fmt.Errorf("failed to decrement svp_count: %w", err)
			}
		}
		summary.DeletedScoreboards++
	}

	// 5. Return the lobby_match to ACTIVE (betting stays closed so outcome cannot be exploited).
	if _, err := tx.ExecContext(ctx,
		`UPDATE lobby_matches
		    SET status = 'ACTIVE',
		        winner = NULL,
		        mvp_name = NULL,
		        svp_name = NULL,
		        betting_open = FALSE,
		        betting_closes_at = NULL
		  WHERE id = $1`,
		matchID,
	); err != nil {
		return nil, fmt.Errorf("failed to reopen lobby match %d: %w", matchID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit match revert: %w", err)
	}
	return summary, nil
}

// ensure pq import stays referenced if extended with error code checks later.
var _ = pq.Array
