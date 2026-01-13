package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"valhalla/internal/models"
)

type ProfileLinkPostgres struct {
	db *sql.DB
}

func NewProfileLinkPostgres(db *sql.DB) *ProfileLinkPostgres {
	return &ProfileLinkPostgres{db: db}
}

func (r *ProfileLinkPostgres) CreateLinkCode(playerID int) (string, error) {
	code := generateCode(6)

	_, err := r.db.Exec(`DELETE FROM link_codes WHERE discord_player_id = $1`, playerID)
	if err != nil {
		return "", fmt.Errorf("failed to cleanup old codes: %w", err)
	}

	_, err = r.db.Exec(`
		INSERT INTO link_codes (code, discord_player_id, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '10 minutes')
	`, code, playerID)
	if err != nil {
		return "", fmt.Errorf("failed to create link code: %w", err)
	}

	return code, nil
}

func (r *ProfileLinkPostgres) ValidateLinkCode(code string) (int, error) {
	r.cleanupExpiredCodes()

	var playerID int
	err := r.db.QueryRow(`
		SELECT discord_player_id FROM link_codes 
		WHERE code = $1 AND expires_at > NOW()
	`, code).Scan(&playerID)

	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("код недействителен или истёк")
	}
	if err != nil {
		return 0, fmt.Errorf("failed to validate code: %w", err)
	}

	_, err = r.db.Exec(`DELETE FROM link_codes WHERE code = $1`, code)
	if err != nil {
		return 0, fmt.Errorf("failed to delete used code: %w", err)
	}

	return playerID, nil
}

func (r *ProfileLinkPostgres) CreateProfileLink(link *models.ProfileLink) error {
	_, err := r.db.Exec(`
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

func (r *ProfileLinkPostgres) GetLinkByDiscordPlayer(playerID int) (*models.ProfileLink, error) {
	var link models.ProfileLink
	err := r.db.QueryRow(`
		SELECT id, discord_player_id, telegram_id, telegram_username, 
			   game_nickname, game_id, zone_id, stars, main_role, linked_at, updated_at
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

func (r *ProfileLinkPostgres) GetLinkByTelegramID(telegramID int64) (*models.ProfileLink, error) {
	var link models.ProfileLink
	err := r.db.QueryRow(`
		SELECT id, discord_player_id, telegram_id, telegram_username,
			   game_nickname, game_id, zone_id, stars, main_role, linked_at, updated_at
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

func (r *ProfileLinkPostgres) UpdateTelegramProfile(telegramID int64, nickname, gameID, zoneID string, stars int, role string) error {
	result, err := r.db.Exec(`
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

func (r *ProfileLinkPostgres) DeleteLinkByDiscordPlayer(playerID int) error {
	result, err := r.db.Exec(`DELETE FROM profile_links WHERE discord_player_id = $1`, playerID)
	if err != nil {
		return fmt.Errorf("failed to delete profile link: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("связь не найдена")
	}
	return nil
}

func (r *ProfileLinkPostgres) DeleteLinkByTelegramID(telegramID int64) error {
	result, err := r.db.Exec(`DELETE FROM profile_links WHERE telegram_id = $1`, telegramID)
	if err != nil {
		return fmt.Errorf("failed to delete profile link: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("связь не найдена")
	}
	return nil
}

func (r *ProfileLinkPostgres) cleanupExpiredCodes() {
	r.db.Exec(`DELETE FROM link_codes WHERE expires_at < NOW()`)
}

func generateCode(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}

func (r *ProfileLinkPostgres) GetPlayerIDByName(name string) (int, error) {
	var id int
	err := r.db.QueryRow(`SELECT id FROM players WHERE name = $1 AND is_deleted = FALSE`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("игрок не найден")
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get player: %w", err)
	}
	return id, nil
}

func (r *ProfileLinkPostgres) GetDiscordStatsByPlayerID(playerID int) (wins, losses, kills, deaths, assists int, err error) {
	var playerName string
	err = r.db.QueryRow(`SELECT name FROM players WHERE id = $1 AND is_deleted = FALSE`, playerID).Scan(&playerName)
	if err != nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("failed to get player name: %w", err)
	}

	err = r.db.QueryRow(`
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
