package repository

import (
	"database/sql"
	"fmt"
	"blackwatch/internal/models"
)

type TelegramPostgres struct {
	db *sql.DB
}

func NewTelegramPostgres(db *sql.DB) *TelegramPostgres {
	return &TelegramPostgres{db: db}
}

func (r *TelegramPostgres) CreateOrUpdatePlayer(p *models.TelegramPlayer) error {
	_, err := r.db.Exec(`
		INSERT INTO telegram_players (telegram_id, telegram_username, first_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id) DO UPDATE SET
			telegram_username = $2,
			first_name = $3,
			updated_at = NOW()
	`, p.TelegramID, p.TelegramUsername, p.FirstName)
	return err
}

func (r *TelegramPostgres) GetPlayerByTelegramID(tgID int64) (*models.TelegramPlayer, error) {
	var p models.TelegramPlayer
	err := r.db.QueryRow(`
		SELECT id, telegram_id, telegram_username, first_name, game_nickname, game_id, zone_id,
			   stars, main_role, is_captain, is_substitute, fsm_state, team_id
		FROM telegram_players WHERE telegram_id = $1
	`, tgID).Scan(
		&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
		&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *TelegramPostgres) UpdatePlayerState(tgID int64, state string) error {
	_, err := r.db.Exec(`UPDATE telegram_players SET fsm_state = $2, updated_at = NOW() WHERE telegram_id = $1`, tgID, state)
	return err
}

var allowedPlayerColumns = map[string]bool{
	"game_nickname":     true,
	"game_id":           true,
	"zone_id":           true,
	"stars":             true,
	"main_role":         true,
	"fsm_state":         true,
	"team_id":           true,
	"is_captain":        true,
	"is_substitute":     true,
	"telegram_username": true,
}

func (r *TelegramPostgres) UpdatePlayerField(tgID int64, column string, value interface{}) error {
	if !allowedPlayerColumns[column] {
		return fmt.Errorf("invalid column: %s", column)
	}
	query := fmt.Sprintf(`UPDATE telegram_players SET %s = $2, updated_at = NOW() WHERE telegram_id = $1`, column)
	_, err := r.db.Exec(query, tgID, value)
	return err
}

func (r *TelegramPostgres) UpdatePlayerFieldByID(playerID int, column string, value interface{}) error {
	if !allowedPlayerColumns[column] {
		return fmt.Errorf("invalid column: %s", column)
	}
	query := fmt.Sprintf(`UPDATE telegram_players SET %s = $2, updated_at = NOW() WHERE id = $1`, column)
	_, err := r.db.Exec(query, playerID, value)
	return err
}

func (r *TelegramPostgres) CreateTeam(name string) (*models.TelegramTeam, error) {
	var id int
	err := r.db.QueryRow(`INSERT INTO telegram_teams (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &models.TelegramTeam{ID: id, Name: name}, nil
}

func (r *TelegramPostgres) GetTeamByID(id int) (*models.TelegramTeam, error) {
	var t models.TelegramTeam
	err := r.db.QueryRow(`SELECT id, name, is_checked_in FROM telegram_teams WHERE id = $1`, id).Scan(&t.ID, &t.Name, &t.IsCheckedIn)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TelegramPostgres) GetTeamByName(name string) (*models.TelegramTeam, error) {
	var t models.TelegramTeam
	err := r.db.QueryRow(`SELECT id, name, is_checked_in FROM telegram_teams WHERE name = $1`, name).Scan(&t.ID, &t.Name, &t.IsCheckedIn)
	if err != nil {
		return nil, err
	}
	t.Players, _ = r.GetTeamMembers(t.ID)
	return &t, nil
}

func (r *TelegramPostgres) DeleteTeam(id int) error {
	_, err := r.db.Exec(`DELETE FROM telegram_teams WHERE id = $1`, id)
	return err
}

func (r *TelegramPostgres) GetAllTeams() ([]models.TelegramTeam, error) {
	query := `
		SELECT t.id, t.name, t.is_checked_in,
		       p.id, p.telegram_id, p.telegram_username, p.first_name, 
		       p.game_nickname, p.game_id, p.zone_id, p.stars, p.main_role,
		       p.is_captain, p.is_substitute, p.fsm_state, p.team_id
		FROM telegram_teams t
		LEFT JOIN telegram_players p ON p.team_id = t.id
		ORDER BY t.id, p.id
	`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teamsMap := make(map[int]*models.TelegramTeam)
	var teamsOrder []int

	for rows.Next() {
		var t models.TelegramTeam
		var pID, pStars sql.NullInt64
		var pTelegramID sql.NullInt64
		var pTeamID sql.NullInt64
		var pUsername, pFirstName, pNickname, pGameID, pZoneID, pRole, pState sql.NullString
		var pIsCaptain, pIsSubstitute sql.NullBool

		if err := rows.Scan(
			&t.ID, &t.Name, &t.IsCheckedIn,
			&pID, &pTelegramID, &pUsername, &pFirstName,
			&pNickname, &pGameID, &pZoneID, &pStars, &pRole,
			&pIsCaptain, &pIsSubstitute, &pState, &pTeamID,
		); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}

		if _, exists := teamsMap[t.ID]; !exists {
			teamsMap[t.ID] = &models.TelegramTeam{
				ID:          t.ID,
				Name:        t.Name,
				IsCheckedIn: t.IsCheckedIn,
				Players:     []models.TelegramPlayer{},
			}
			teamsOrder = append(teamsOrder, t.ID)
		}

		if pID.Valid {
			player := models.TelegramPlayer{
				ID:               int(pID.Int64),
				GameNickname:     pNickname.String,
				GameID:           pGameID.String,
				ZoneID:           pZoneID.String,
				Stars:            int(pStars.Int64),
				MainRole:         pRole.String,
				IsCaptain:        pIsCaptain.Bool,
				IsSubstitute:     pIsSubstitute.Bool,
				FSMState:         pState.String,
				TelegramUsername: pUsername.String,
				FirstName:        pFirstName.String,
			}
			if pTelegramID.Valid {
				tgID := pTelegramID.Int64
				player.TelegramID = &tgID
			}
			if pTeamID.Valid {
				teamID := int(pTeamID.Int64)
				player.TeamID = &teamID
			}
			teamsMap[t.ID].Players = append(teamsMap[t.ID].Players, player)
		}
	}

	teams := make([]models.TelegramTeam, 0, len(teamsOrder))
	for _, id := range teamsOrder {
		teams = append(teams, *teamsMap[id])
	}
	return teams, nil
}

func (r *TelegramPostgres) GetTeamMembers(teamID int) ([]models.TelegramPlayer, error) {
	rows, err := r.db.Query(`
		SELECT id, telegram_id, telegram_username, first_name, game_nickname, game_id, zone_id,
			   stars, main_role, is_captain, is_substitute, fsm_state, team_id
		FROM telegram_players WHERE team_id = $1 ORDER BY id
	`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var players []models.TelegramPlayer
	for rows.Next() {
		var p models.TelegramPlayer
		if err := rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
			&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		players = append(players, p)
	}
	return players, nil
}

func (r *TelegramPostgres) CreateTeammate(p *models.TelegramPlayer) error {
	_, err := r.db.Exec(`
		INSERT INTO telegram_players (team_id, game_nickname, is_substitute)
		VALUES ($1, $2, $3)
	`, p.TeamID, p.GameNickname, p.IsSubstitute)
	return err
}

func (r *TelegramPostgres) UpdateLastTeammateData(teamID int, column string, value interface{}) error {
	var playerID int
	err := r.db.QueryRow(`SELECT id FROM telegram_players WHERE team_id = $1 ORDER BY id DESC LIMIT 1`, teamID).Scan(&playerID)
	if err != nil {
		return err
	}
	return r.UpdatePlayerFieldByID(playerID, column, value)
}

func (r *TelegramPostgres) ResetTeamID(teamID int) error {
	_, err := r.db.Exec(`UPDATE telegram_players SET team_id = NULL WHERE team_id = $1`, teamID)
	return err
}

func (r *TelegramPostgres) SetCheckIn(teamID int, status bool) error {
	_, err := r.db.Exec(`UPDATE telegram_teams SET is_checked_in = $2, updated_at = NOW() WHERE id = $1`, teamID, status)
	return err
}

func (r *TelegramPostgres) GetAllCaptains() ([]models.TelegramPlayer, error) {
	rows, err := r.db.Query(`
		SELECT id, telegram_id, telegram_username, first_name, game_nickname, game_id, zone_id,
			   stars, main_role, is_captain, is_substitute, fsm_state, team_id
		FROM telegram_players WHERE is_captain = TRUE AND telegram_id IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var players []models.TelegramPlayer
	for rows.Next() {
		var p models.TelegramPlayer
		if err := rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
			&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		players = append(players, p)
	}
	return players, nil
}

func (r *TelegramPostgres) GetSoloPlayers() ([]models.TelegramPlayer, error) {
	rows, err := r.db.Query(`
		SELECT id, telegram_id, telegram_username, first_name, game_nickname, game_id, zone_id,
			   stars, main_role, is_captain, is_substitute, fsm_state, team_id
		FROM telegram_players WHERE team_id IS NULL AND main_role != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var players []models.TelegramPlayer
	for rows.Next() {
		var p models.TelegramPlayer
		if err := rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
			&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		players = append(players, p)
	}
	return players, nil
}

func (r *TelegramPostgres) GetSetting(key string) (string, error) {
	var value string
	err := r.db.QueryRow(`SELECT value FROM telegram_settings WHERE key = $1`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (r *TelegramPostgres) SetSetting(key, value string) error {
	_, err := r.db.Exec(`
		INSERT INTO telegram_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = $2
	`, key, value)
	return err
}
