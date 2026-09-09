package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type LicensePostgres struct {
	db *sql.DB
}

func NewLicensePostgres(db *sql.DB) *LicensePostgres {
	return &LicensePostgres{db: db}
}

func (r *LicensePostgres) IsLicenseValid(ctx context.Context, guildID string) (bool, error) {
	var status string
	var expiresAt time.Time

	err := r.db.QueryRowContext(ctx,
		`SELECT license_status, license_expires_at
		 FROM discord_servers WHERE guild_id = $1`,
		guildID,
	).Scan(&status, &expiresAt)

	if err == sql.ErrNoRows {
		// Guild not registered — auto-register with TRIAL
		return r.autoRegisterTrial(ctx, guildID)
	}
	if err != nil {
		return false, fmt.Errorf("failed to check license: %w", err)
	}

	if status == "EXPIRED" || time.Now().After(expiresAt) {
		return false, nil
	}

	return true, nil
}

// autoRegisterTrial creates a new guild entry with TRIAL status (30 days).
func (r *LicensePostgres) autoRegisterTrial(ctx context.Context, guildID string) (bool, error) {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO discord_servers (guild_id, license_status, license_expires_at)
		 VALUES ($1, 'TRIAL', $2)
		 ON CONFLICT (guild_id) DO NOTHING`,
		guildID, time.Now().AddDate(0, 0, 30),
	)
	if err != nil {
		return false, fmt.Errorf("failed to register trial: %w", err)
	}
	return true, nil
}

// ExpireLicense sets a guild's license to EXPIRED.
func (r *LicensePostgres) ExpireLicense(ctx context.Context, guildID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE discord_servers
		 SET license_status = 'EXPIRED', license_expires_at = $1
		 WHERE guild_id = $2`,
		time.Now(), guildID,
	)
	if err != nil {
		return fmt.Errorf("failed to expire license: %w", err)
	}
	return nil
}

// UpgradeLicense sets a guild's license to PRO with an expiration date.
func (r *LicensePostgres) UpgradeLicense(ctx context.Context, guildID string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO discord_servers (guild_id, license_status, license_expires_at)
		 VALUES ($1, 'PRO', $2)
		 ON CONFLICT (guild_id) DO UPDATE
		 SET license_status = 'PRO', license_expires_at = $2`,
		guildID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upgrade license: %w", err)
	}
	return nil
}

// GetLicenseInfo returns the full license info for a guild.
func (r *LicensePostgres) GetLicenseInfo(ctx context.Context, guildID string) (status string, expiresAt time.Time, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT license_status, license_expires_at
		 FROM discord_servers WHERE guild_id = $1`,
		guildID,
	).Scan(&status, &expiresAt)
	return
}
