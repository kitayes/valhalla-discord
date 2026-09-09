package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"blackwatch/internal/domain"
)

type QueueBanPostgres struct {
	db *sql.DB
}

func NewQueueBanPostgres(db *sql.DB) *QueueBanPostgres {
	return &QueueBanPostgres{db: db}
}

// BanPlayer inserts or updates a queue ban for a Discord user until bannedUntil.
func (r *QueueBanPostgres) BanPlayer(ctx context.Context, discordID, reason string, bannedUntil time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO queue_bans (discord_id, reason, banned_until)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (discord_id) DO UPDATE
		 SET reason = $2, banned_until = $3`,
		discordID, reason, bannedUntil,
	)
	if err != nil {
		return fmt.Errorf("failed to ban player %s: %w", discordID, err)
	}
	return nil
}

// IsBanned checks if a player is currently queue-banned.
func (r *QueueBanPostgres) IsBanned(ctx context.Context, discordID string) (bool, error) {
	var bannedUntil time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT banned_until FROM queue_bans WHERE discord_id = $1`, discordID,
	).Scan(&bannedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check ban for %s: %w", discordID, err)
	}
	return time.Now().Before(bannedUntil), nil
}

// GetBanInfo returns the reason and expiry for an active ban.
//
// A missing row is reported as domain.ErrPlayerNotFound rather than a zero
// value: the caller renders the expiry to the user, and a zero time.Time turned
// "we could not read the ban" into "banned until 01.01.0001".
func (r *QueueBanPostgres) GetBanInfo(ctx context.Context, discordID string) (string, time.Time, error) {
	var reason string
	var until time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT reason, banned_until FROM queue_bans WHERE discord_id = $1`, discordID,
	).Scan(&reason, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, fmt.Errorf("queue ban for %s: %w", discordID, domain.ErrPlayerNotFound)
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to get ban info for %s: %w", discordID, err)
	}
	return reason, until, nil
}

// PurgeExpired drops bans that have already run out. Nothing else removes them,
// so without this the table only ever grows.
func (r *QueueBanPostgres) PurgeExpired(ctx context.Context) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM queue_bans WHERE banned_until <= NOW()`)
	if err != nil {
		return 0, fmt.Errorf("failed to purge expired queue bans: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count purged queue bans: %w", err)
	}
	return int(n), nil
}
