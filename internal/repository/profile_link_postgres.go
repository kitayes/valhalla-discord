package repository

import (
	"blackwatch/internal/models"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	linkCodeLength      = 6
	linkCodeExpireQuery = "NOW() + INTERVAL '10 minutes'"
)

type ProfileLinkPostgres struct {
	db *sql.DB
}

func NewProfileLinkPostgres(db *sql.DB) *ProfileLinkPostgres {
	return &ProfileLinkPostgres{db: db}
}

func (r *ProfileLinkPostgres) CreateLinkCode(ctx context.Context, playerID int) (string, error) {
	code, err := generateCode(linkCodeLength)
	if err != nil {
		return "", err
	}

	_, err = r.db.ExecContext(ctx, `DELETE FROM link_codes WHERE discord_player_id = $1`, playerID)
	if err != nil {
		return "", fmt.Errorf("failed to cleanup old codes: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO link_codes (code, discord_player_id, expires_at)
		VALUES ($1, $2, `+linkCodeExpireQuery+`)
	`, code, playerID)
	if err != nil {
		return "", fmt.Errorf("failed to create link code: %w", err)
	}

	return code, nil
}

func (r *ProfileLinkPostgres) ValidateLinkCode(ctx context.Context, code string) (int, error) {
	r.cleanupExpiredCodes(ctx)

	var playerID int
	err := r.db.QueryRowContext(ctx, `
		SELECT discord_player_id FROM link_codes 
		WHERE code = $1 AND expires_at > NOW()
	`, code).Scan(&playerID)

	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("код недействителен или истёк")
	}
	if err != nil {
		return 0, fmt.Errorf("failed to validate code: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `DELETE FROM link_codes WHERE code = $1`, code)
	if err != nil {
		return 0, fmt.Errorf("failed to delete used code: %w", err)
	}

	return playerID, nil
}

func (r *ProfileLinkPostgres) CreateProfileLink(ctx context.Context, link *models.ProfileLink) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO profile_links (discord_player_id, telegram_id, telegram_username)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id) DO UPDATE SET
			discord_player_id = $1,
			telegram_username = $3,
			updated_at = NOW()
	`, link.DiscordPlayerID, link.TelegramID, link.TelegramUsername)

	if err != nil {
		return fmt.Errorf("failed to create profile link: %w", err)
	}
	return nil
}

func (r *ProfileLinkPostgres) GetLinkByDiscordPlayer(ctx context.Context, playerID int) (*models.ProfileLink, error) {
	var link models.ProfileLink
	err := r.db.QueryRowContext(ctx, `
		SELECT id, discord_player_id, telegram_id, COALESCE(telegram_username, ''),
			   COALESCE(game_nickname, ''), COALESCE(game_id, ''), COALESCE(zone_id, ''), 
			   COALESCE(stars, 0), COALESCE(main_role, ''), linked_at, updated_at
		FROM profile_links 
		WHERE discord_player_id = $1
	`, playerID).Scan(
		&link.ID, &link.DiscordPlayerID, &link.TelegramID, &link.TelegramUsername,
		&link.GameNickname, &link.GameID, &link.ZoneID, &link.Stars, &link.MainRole,
		&link.LinkedAt, &link.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get profile link: %w", err)
	}
	return &link, nil
}

func (r *ProfileLinkPostgres) GetLinkByTelegramID(ctx context.Context, telegramID int64) (*models.ProfileLink, error) {
	var link models.ProfileLink
	err := r.db.QueryRowContext(ctx, `
		SELECT id, discord_player_id, telegram_id, COALESCE(telegram_username, ''),
			   COALESCE(game_nickname, ''), COALESCE(game_id, ''), COALESCE(zone_id, ''),
			   COALESCE(stars, 0), COALESCE(main_role, ''), linked_at, updated_at
		FROM profile_links 
		WHERE telegram_id = $1
	`, telegramID).Scan(
		&link.ID, &link.DiscordPlayerID, &link.TelegramID, &link.TelegramUsername,
		&link.GameNickname, &link.GameID, &link.ZoneID, &link.Stars, &link.MainRole,
		&link.LinkedAt, &link.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get profile link: %w", err)
	}
	return &link, nil
}

func (r *ProfileLinkPostgres) UpdateTelegramProfile(ctx context.Context, telegramID int64, nickname, gameID, zoneID string, stars int, role string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE profile_links SET
			game_nickname = $2,
			game_id = $3,
			zone_id = $4,
			stars = $5,
			main_role = $6,
			updated_at = NOW()
		WHERE telegram_id = $1
	`, telegramID, nickname, gameID, zoneID, stars, role)

	if err != nil {
		return fmt.Errorf("failed to update telegram profile: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("профиль не найден")
	}
	return nil
}

func (r *ProfileLinkPostgres) DeleteLinkByDiscordPlayer(ctx context.Context, playerID int) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM profile_links WHERE discord_player_id = $1`, playerID)
	if err != nil {
		return fmt.Errorf("failed to delete profile link: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("связь не найдена")
	}
	return nil
}

func (r *ProfileLinkPostgres) DeleteLinkByTelegramID(ctx context.Context, telegramID int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM profile_links WHERE telegram_id = $1`, telegramID)
	if err != nil {
		return fmt.Errorf("failed to delete profile link: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("связь не найдена")
	}
	return nil
}

// cleanupExpiredCodes prunes codes that have already timed out. Best effort:
// every lookup filters on expires_at anyway, so a failed sweep costs table size,
// not correctness.
func (r *ProfileLinkPostgres) cleanupExpiredCodes(ctx context.Context) {
	_, _ = r.db.ExecContext(ctx, `DELETE FROM link_codes WHERE expires_at < NOW()`)
}

// generateCode returns a random code used to link a Telegram account to a
// Discord profile.
//
// crypto/rand's error was previously discarded, which would have left the buffer
// zeroed — every code would be "000000", and anyone could claim anyone's profile
// by guessing it. A failure here has to be reported, not swallowed.
func generateCode(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate link code: %w", err)
	}
	return hex.EncodeToString(buf)[:length], nil
}

func (r *ProfileLinkPostgres) GetPlayerIDByName(ctx context.Context, name string) (int, error) {
	var id int
	err := r.db.QueryRowContext(ctx, `SELECT id FROM players WHERE name = $1 AND is_deleted = FALSE`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("игрок не найден")
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get player: %w", err)
	}
	return id, nil
}

func (r *ProfileLinkPostgres) GetDiscordStatsByPlayerID(ctx context.Context, playerID int) (wins, losses, kills, deaths, assists int, err error) {
	var playerName string
	err = r.db.QueryRowContext(ctx, `SELECT name FROM players WHERE id = $1 AND is_deleted = FALSE`, playerID).Scan(&playerName)
	if err != nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("failed to get player name: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		SELECT 
			COALESCE(SUM(CASE WHEN result = 'WIN' THEN 1 ELSE 0 END), 0) as wins,
			COALESCE(SUM(CASE WHEN result = 'LOSE' THEN 1 ELSE 0 END), 0) as losses,
			COALESCE(SUM(kills), 0) as kills,
			COALESCE(SUM(deaths), 0) as deaths,
			COALESCE(SUM(assists), 0) as assists
		FROM player_results WHERE player_name = $1 AND is_deleted = FALSE
	`, playerName).Scan(&wins, &losses, &kills, &deaths, &assists)

	if err != nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("failed to get stats: %w", err)
	}
	return wins, losses, kills, deaths, assists, nil
}
