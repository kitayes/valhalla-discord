package repository

import (
	"blackwatch/internal/models"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

type TelegramPostgres struct {
	db *sql.DB
}

func NewTelegramPostgres(db *sql.DB) *TelegramPostgres {
	return &TelegramPostgres{db: db}
}

func (r *TelegramPostgres) CreateOrUpdatePlayer(ctx context.Context, p *models.TelegramPlayer) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO telegram_players (telegram_id, telegram_username, first_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id) DO UPDATE SET
			telegram_username = $2,
			first_name = $3,
			updated_at = NOW()
	`, p.TelegramID, p.TelegramUsername, p.FirstName)
	return err
}

const telegramPlayerSelectCols = `
	id, telegram_id,
	COALESCE(telegram_username, ''),
	COALESCE(first_name, ''),
	COALESCE(game_nickname, ''),
	COALESCE(game_id, ''),
	COALESCE(zone_id, ''),
	COALESCE(stars, 0),
	COALESCE(main_role, ''),
	COALESCE(is_captain, FALSE),
	COALESCE(is_substitute, FALSE),
	COALESCE(fsm_state, ''),
	team_id
`

func (r *TelegramPostgres) GetPlayerByTelegramID(ctx context.Context, tgID int64) (*models.TelegramPlayer, error) {
	var p models.TelegramPlayer
	err := r.db.QueryRowContext(ctx, `
		SELECT `+telegramPlayerSelectCols+`
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

func (r *TelegramPostgres) GetPlayerByID(ctx context.Context, playerID int) (*models.TelegramPlayer, error) {
	var p models.TelegramPlayer
	err := r.db.QueryRowContext(ctx, `
		SELECT `+telegramPlayerSelectCols+`
		FROM telegram_players WHERE id = $1
	`, playerID).Scan(
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


func (r *TelegramPostgres) UpdatePlayerState(ctx context.Context, tgID int64, state string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_players SET fsm_state = $2, updated_at = NOW() WHERE telegram_id = $1`, tgID, state)
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

func (r *TelegramPostgres) UpdatePlayerField(ctx context.Context, tgID int64, column string, value interface{}) error {
	if !allowedPlayerColumns[column] {
		return fmt.Errorf("invalid column: %s", column)
	}
	query := fmt.Sprintf(`UPDATE telegram_players SET %s = $2, updated_at = NOW() WHERE telegram_id = $1`, column)
	_, err := r.db.ExecContext(ctx, query, tgID, value)
	return err
}

func (r *TelegramPostgres) UpdatePlayerFieldByID(ctx context.Context, playerID int, column string, value interface{}) error {
	if !allowedPlayerColumns[column] {
		return fmt.Errorf("invalid column: %s", column)
	}
	query := fmt.Sprintf(`UPDATE telegram_players SET %s = $2, updated_at = NOW() WHERE id = $1`, column)
	_, err := r.db.ExecContext(ctx, query, playerID, value)
	return err
}

func (r *TelegramPostgres) CreateTeam(ctx context.Context, name string) (*models.TelegramTeam, error) {
	var id int
	err := r.db.QueryRowContext(ctx, `INSERT INTO telegram_teams (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &models.TelegramTeam{ID: id, Name: name}, nil
}

func (r *TelegramPostgres) GetTeamByID(ctx context.Context, id int) (*models.TelegramTeam, error) {
	var t models.TelegramTeam
	err := r.db.QueryRowContext(ctx, `SELECT id, name, COALESCE(is_checked_in, FALSE), COALESCE(status, 'active'), challonge_participant_id FROM telegram_teams WHERE id = $1`, id).
		Scan(&t.ID, &t.Name, &t.IsCheckedIn, &t.Status, &t.ChallongeParticipantID)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TelegramPostgres) GetTeamByName(ctx context.Context, name string) (*models.TelegramTeam, error) {
	var t models.TelegramTeam
	err := r.db.QueryRowContext(ctx, `SELECT id, name, COALESCE(is_checked_in, FALSE), COALESCE(status, 'active'), challonge_participant_id FROM telegram_teams WHERE name = $1`, name).
		Scan(&t.ID, &t.Name, &t.IsCheckedIn, &t.Status, &t.ChallongeParticipantID)
	if err != nil {
		return nil, err
	}
	t.Players, _ = r.GetTeamMembers(ctx, t.ID)
	return &t, nil
}

func (r *TelegramPostgres) DeleteTeam(ctx context.Context, id int) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM telegram_teams WHERE id = $1`, id)
	return err
}

func (r *TelegramPostgres) GetAllTeams(ctx context.Context) ([]models.TelegramTeam, error) {
	query := `
		SELECT t.id, t.name, COALESCE(t.is_checked_in, FALSE), COALESCE(t.status, 'active'), t.challonge_participant_id,
		       p.id, p.telegram_id, p.telegram_username, p.first_name,
		       p.game_nickname, p.game_id, p.zone_id, p.stars, p.main_role,
		       p.is_captain, p.is_substitute, p.fsm_state, p.team_id
		FROM telegram_teams t
		LEFT JOIN telegram_players p ON p.team_id = t.id
		ORDER BY t.id, p.id
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	teamsMap := make(map[int]*models.TelegramTeam)
	var teamsOrder []int

	for rows.Next() {
		var t models.TelegramTeam
		var pID, pStars sql.NullInt64
		var pTelegramID sql.NullInt64
		var pTeamID sql.NullInt64
		var pParticipant sql.NullInt64
		var pUsername, pFirstName, pNickname, pGameID, pZoneID, pRole, pState sql.NullString
		var pIsCaptain, pIsSubstitute sql.NullBool

		if err := rows.Scan(
			&t.ID, &t.Name, &t.IsCheckedIn, &t.Status, &pParticipant,
			&pID, &pTelegramID, &pUsername, &pFirstName,
			&pNickname, &pGameID, &pZoneID, &pStars, &pRole,
			&pIsCaptain, &pIsSubstitute, &pState, &pTeamID,
		); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}

		if _, exists := teamsMap[t.ID]; !exists {
			team := &models.TelegramTeam{ID: t.ID, Name: t.Name, IsCheckedIn: t.IsCheckedIn, Status: t.Status, Players: []models.TelegramPlayer{}}
			if pParticipant.Valid {
				pid := pParticipant.Int64
				team.ChallongeParticipantID = &pid
			}
			teamsMap[t.ID] = team
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
	// Callers act on this list (check-in reminders, technical defeats), so a
	// silently truncated set would disqualify teams that simply were not read.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read teams: %w", err)
	}

	teams := make([]models.TelegramTeam, 0, len(teamsOrder))
	for _, id := range teamsOrder {
		teams = append(teams, *teamsMap[id])
	}
	return teams, nil
}

func (r *TelegramPostgres) GetTeamMembers(ctx context.Context, teamID int) ([]models.TelegramPlayer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+telegramPlayerSelectCols+`
		FROM telegram_players WHERE team_id = $1 ORDER BY id
	`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var players []models.TelegramPlayer
	for rows.Next() {
		var p models.TelegramPlayer
		if err := rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
			&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		players = append(players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read players: %w", err)
	}
	return players, nil
}

// CreateTeammate inserts a roster row entered by the captain. Such rows have
// no telegram_id of their own; every other field comes from the parsed line.
func (r *TelegramPostgres) CreateTeammate(ctx context.Context, p *models.TelegramPlayer) error {
	return r.db.QueryRowContext(ctx, `
		INSERT INTO telegram_players
			(team_id, game_nickname, game_id, zone_id, stars, main_role, telegram_username, is_substitute)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, p.TeamID, p.GameNickname, p.GameID, p.ZoneID, p.Stars, p.MainRole, p.TelegramUsername, p.IsSubstitute).Scan(&p.ID)
}

// DeleteTeammate deletes a teammate row by ID, ensuring captains cannot be accidentally deleted.
func (r *TelegramPostgres) DeleteTeammate(ctx context.Context, playerID int) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM telegram_players WHERE id = $1 AND is_captain = false", playerID)
	return err
}

// ReleaseTeamMembers detaches everyone from a team before it is deleted.
//
// Roster rows entered by the captain have no Telegram account of their own, so
// once detached they would only ever show up as phantom "solo players" — they
// are removed. The captain keeps their account row but loses the captain flag
// (otherwise they stay on the broadcast list) and any half-finished
// registration state.
func (r *TelegramPostgres) ReleaseTeamMembers(ctx context.Context, teamID int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_players WHERE team_id = $1 AND telegram_id IS NULL`, teamID); err != nil {
		return fmt.Errorf("delete roster rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE telegram_players
		SET team_id = NULL, is_captain = FALSE, fsm_state = '', updated_at = NOW()
		WHERE team_id = $1
	`, teamID); err != nil {
		return fmt.Errorf("detach captain: %w", err)
	}
	return tx.Commit()
}

func (r *TelegramPostgres) SetCheckIn(ctx context.Context, teamID int, status bool) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_teams SET is_checked_in = $2, updated_at = NOW() WHERE id = $1`, teamID, status)
	return err
}

func (r *TelegramPostgres) SetTeamStatus(ctx context.Context, teamID int, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_teams SET status = $2, updated_at = NOW() WHERE id = $1`, teamID, status)
	return err
}

func (r *TelegramPostgres) GetAllCaptains(ctx context.Context) ([]models.TelegramPlayer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+telegramPlayerSelectCols+`
		FROM telegram_players WHERE is_captain = TRUE AND telegram_id IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var players []models.TelegramPlayer
	for rows.Next() {
		var p models.TelegramPlayer
		if err := rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
			&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		players = append(players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read players: %w", err)
	}
	return players, nil
}

func (r *TelegramPostgres) GetSoloPlayers(ctx context.Context) ([]models.TelegramPlayer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+telegramPlayerSelectCols+`
		FROM telegram_players WHERE team_id IS NULL AND main_role != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var players []models.TelegramPlayer
	for rows.Next() {
		var p models.TelegramPlayer
		if err := rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
			&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		players = append(players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read players: %w", err)
	}
	return players, nil
}

func (r *TelegramPostgres) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM telegram_settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (r *TelegramPostgres) SetSetting(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO telegram_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = $2
	`, key, value)
	return err
}

func (r *TelegramPostgres) FindByGameID(ctx context.Context, gameID string) ([]models.TelegramPlayer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+telegramPlayerSelectCols+`
		FROM telegram_players WHERE game_id = $1 ORDER BY id
	`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var players []models.TelegramPlayer
	for rows.Next() {
		var p models.TelegramPlayer
		if err := rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.FirstName, &p.GameNickname, &p.GameID, &p.ZoneID,
			&p.Stars, &p.MainRole, &p.IsCaptain, &p.IsSubstitute, &p.FSMState, &p.TeamID); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		players = append(players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read players: %w", err)
	}
	return players, nil
}

func (r *TelegramPostgres) CreateMatchReport(ctx context.Context, report *models.TelegramMatchReport) error {
	var id int
	var createdAt time.Time
	photoIDs := report.PhotoFileIDs
	if photoIDs == nil {
		photoIDs = []string{}
	}
	status := report.Status
	if status == "" {
		status = models.ReportConfirmed
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO telegram_match_reports
			(reporter_telegram_id, winner_team_id, loser_team_id, score, photo_file_ids, bracket_match_id, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at
	`, report.ReporterTelegramID, report.WinnerTeamID, report.LoserTeamID, report.Score, pq.Array(photoIDs), report.BracketMatchID, status, report.ExpiresAt).Scan(&id, &createdAt)
	if err != nil {
		return err
	}
	report.ID = id
	report.CreatedAt = createdAt
	report.Status = status
	return nil
}

// reportColumns is the SELECT list scanReport expects, with team names joined.
const reportColumns = `
	mr.id, mr.reporter_telegram_id,
	mr.winner_team_id, COALESCE(wt.name, ''),
	mr.loser_team_id, COALESCE(lt.name, ''),
	mr.score, mr.photo_file_ids, mr.bracket_match_id, mr.synced_at, mr.created_at,
	mr.status, mr.expires_at, mr.resolved_at
	FROM telegram_match_reports mr
	LEFT JOIN telegram_teams wt ON mr.winner_team_id = wt.id
	LEFT JOIN telegram_teams lt ON mr.loser_team_id = lt.id`

func scanReport(row interface{ Scan(dest ...any) error }) (*models.TelegramMatchReport, error) {
	var rep models.TelegramMatchReport
	var winner, loser sql.NullInt64
	if err := row.Scan(&rep.ID, &rep.ReporterTelegramID,
		&winner, &rep.WinnerTeamName, &loser, &rep.LoserTeamName,
		&rep.Score, pq.Array(&rep.PhotoFileIDs), &rep.BracketMatchID, &rep.SyncedAt, &rep.CreatedAt,
		&rep.Status, &rep.ExpiresAt, &rep.ResolvedAt); err != nil {
		return nil, err
	}
	rep.WinnerTeamID = int(winner.Int64)
	rep.LoserTeamID = int(loser.Int64)
	return &rep, nil
}

func (r *TelegramPostgres) GetMatchReport(ctx context.Context, id int) (*models.TelegramMatchReport, error) {
	rep, err := scanReport(r.db.QueryRowContext(ctx, `SELECT `+reportColumns+` WHERE mr.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rep, err
}

func (r *TelegramPostgres) GetOpenReportForMatch(ctx context.Context, bracketMatchID int) (*models.TelegramMatchReport, error) {
	rep, err := scanReport(r.db.QueryRowContext(ctx, `SELECT `+reportColumns+`
		WHERE mr.bracket_match_id = $1 AND mr.status IN ($2, $3)
		ORDER BY mr.id DESC LIMIT 1`, bracketMatchID, models.ReportPending, models.ReportDisputed))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rep, err
}

func (r *TelegramPostgres) GetExpiredPendingReports(ctx context.Context, now time.Time) ([]models.TelegramMatchReport, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+reportColumns+`
		WHERE mr.status = $1 AND mr.expires_at IS NOT NULL AND mr.expires_at <= $2
		ORDER BY mr.id`, models.ReportPending, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup
	var out []models.TelegramMatchReport
	for rows.Next() {
		rep, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan expired report: %w", err)
		}
		out = append(out, *rep)
	}
	return out, rows.Err()
}

func (r *TelegramPostgres) SetReportStatus(ctx context.Context, id int, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE telegram_match_reports
		SET status = $2,
		    resolved_at = CASE WHEN $2 = $3 THEN NULL ELSE NOW() END
		WHERE id = $1`, id, status, models.ReportPending)
	return err
}

func (r *TelegramPostgres) GetRecentMatchReports(ctx context.Context, limit int) ([]models.TelegramMatchReport, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			mr.id,
			mr.reporter_telegram_id,
			mr.winner_team_id,
			COALESCE(wt.name, ''),
			mr.loser_team_id,
			COALESCE(lt.name, ''),
			mr.score,
			mr.photo_file_ids,
			mr.created_at
		FROM telegram_match_reports mr
		LEFT JOIN telegram_teams wt ON mr.winner_team_id = wt.id
		LEFT JOIN telegram_teams lt ON mr.loser_team_id = lt.id
		ORDER BY mr.id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var reports []models.TelegramMatchReport
	for rows.Next() {
		var rep models.TelegramMatchReport
		if err := rows.Scan(
			&rep.ID,
			&rep.ReporterTelegramID,
			&rep.WinnerTeamID,
			&rep.WinnerTeamName,
			&rep.LoserTeamID,
			&rep.LoserTeamName,
			&rep.Score,
			pq.Array(&rep.PhotoFileIDs),
			&rep.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan match report: %w", err)
		}
		reports = append(reports, rep)
	}
	return reports, rows.Err()
}

func (r *TelegramPostgres) SetTeamParticipantID(ctx context.Context, teamID int, pid int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_teams SET challonge_participant_id = $2 WHERE id = $1`, teamID, pid)
	return err
}

func (r *TelegramPostgres) ClearTeamParticipantIDs(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_teams SET challonge_participant_id = NULL`)
	return err
}

func (r *TelegramPostgres) ReplaceBracketMatches(ctx context.Context, matches []models.BracketMatch) error {
	active, err := r.GetActiveTournament(ctx)
	if err == nil && active != nil {
		return r.ReplaceBracketMatchesForTournament(ctx, active.ID, matches)
	}
	return r.replaceBracketMatchesInternal(ctx, 0, matches)
}

func (r *TelegramPostgres) ReplaceBracketMatchesForTournament(ctx context.Context, tournamentID int, matches []models.BracketMatch) error {
	return r.replaceBracketMatchesInternal(ctx, tournamentID, matches)
}

func (r *TelegramPostgres) replaceBracketMatchesInternal(ctx context.Context, tournamentID int, matches []models.BracketMatch) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	ids := make([]int64, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ChallongeMatchID)
		var tID *int
		if tournamentID > 0 {
			tID = &tournamentID
		} else if m.TournamentID != nil {
			tID = m.TournamentID
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO telegram_bracket_matches
				(tournament_id, challonge_match_id, round, play_order, team1_id, team2_id, winner_id, state, scores_csv, both_notified, synced_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, FALSE, NOW())
			ON CONFLICT (challonge_match_id) DO UPDATE SET
				tournament_id = COALESCE(EXCLUDED.tournament_id, telegram_bracket_matches.tournament_id),
				round = EXCLUDED.round, play_order = EXCLUDED.play_order,
				team1_id = EXCLUDED.team1_id, team2_id = EXCLUDED.team2_id, winner_id = EXCLUDED.winner_id,
				state = EXCLUDED.state, scores_csv = EXCLUDED.scores_csv, synced_at = NOW(),
				both_notified = CASE
					WHEN telegram_bracket_matches.team1_id IS NOT DISTINCT FROM EXCLUDED.team1_id
					 AND telegram_bracket_matches.team2_id IS NOT DISTINCT FROM EXCLUDED.team2_id
					THEN telegram_bracket_matches.both_notified ELSE FALSE END
		`, tID, m.ChallongeMatchID, m.Round, m.PlayOrder, m.Team1ID, m.Team2ID, m.WinnerID, m.State, m.ScoresCSV)
		if err != nil {
			return err
		}
	}
	if tournamentID > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_bracket_matches WHERE tournament_id = $1 AND NOT (challonge_match_id = ANY($2))`, tournamentID, pq.Array(ids)); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_bracket_matches WHERE NOT (challonge_match_id = ANY($1))`, pq.Array(ids)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *TelegramPostgres) GetBracketMatches(ctx context.Context) ([]models.BracketMatch, error) {
	active, err := r.GetActiveTournament(ctx)
	if err == nil && active != nil {
		return r.GetBracketMatchesForTournament(ctx, active.ID)
	}
	return r.getBracketMatchesInternal(ctx, 0)
}

func (r *TelegramPostgres) GetBracketMatchesForTournament(ctx context.Context, tournamentID int) ([]models.BracketMatch, error) {
	return r.getBracketMatchesInternal(ctx, tournamentID)
}

func (r *TelegramPostgres) getBracketMatchesInternal(ctx context.Context, tournamentID int) ([]models.BracketMatch, error) {
	query := `
		SELECT m.id, m.tournament_id, m.challonge_match_id, m.round, m.play_order,
		       m.team1_id, m.team2_id, m.winner_id,
		       COALESCE(t1.name, ''), COALESCE(t2.name, ''),
		       m.state, m.scores_csv, m.both_notified
		FROM telegram_bracket_matches m
		LEFT JOIN telegram_teams t1 ON t1.id = m.team1_id
		LEFT JOIN telegram_teams t2 ON t2.id = m.team2_id
	`
	var args []interface{}
	if tournamentID > 0 {
		query += ` WHERE m.tournament_id = $1 `
		args = append(args, tournamentID)
	}
	query += ` ORDER BY m.round, m.play_order `

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.BracketMatch
	for rows.Next() {
		var m models.BracketMatch
		if err := rows.Scan(&m.ID, &m.TournamentID, &m.ChallongeMatchID, &m.Round, &m.PlayOrder,
			&m.Team1ID, &m.Team2ID, &m.WinnerID, &m.Team1Name, &m.Team2Name,
			&m.State, &m.ScoresCSV, &m.BothNotified); err != nil {
			return nil, fmt.Errorf("scan bracket match: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *TelegramPostgres) MarkBracketNotified(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_bracket_matches SET both_notified = TRUE WHERE id = ANY($1)`, pq.Array(ids))
	return err
}

func (r *TelegramPostgres) SetReportSynced(ctx context.Context, reportID int) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_match_reports SET synced_at = NOW() WHERE id = $1`, reportID)
	return err
}

func (r *TelegramPostgres) GetUnsyncedReports(ctx context.Context) ([]models.TelegramMatchReport, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, reporter_telegram_id, winner_team_id, loser_team_id, score, photo_file_ids, created_at, bracket_match_id
		FROM telegram_match_reports
		WHERE bracket_match_id IS NOT NULL AND synced_at IS NULL
		  AND status IN ('confirmed', 'auto_confirmed')
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var out []models.TelegramMatchReport
	for rows.Next() {
		var rep models.TelegramMatchReport
		if err := rows.Scan(&rep.ID, &rep.ReporterTelegramID, &rep.WinnerTeamID, &rep.LoserTeamID,
			&rep.Score, pq.Array(&rep.PhotoFileIDs), &rep.CreatedAt, &rep.BracketMatchID); err != nil {
			return nil, fmt.Errorf("scan unsynced report: %w", err)
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

func (r *TelegramPostgres) CreateTournament(ctx context.Context, t *models.TelegramTournament) (*models.TelegramTournament, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	if t.IsActive {
		if _, err := tx.ExecContext(ctx, `UPDATE telegram_tournaments SET is_active = FALSE, status = 'completed' WHERE is_active = TRUE`); err != nil {
			return nil, err
		}
	}

	if t.TournamentType == "" {
		t.TournamentType = models.TournamentTypeSingleElimination
	}
	if t.SeedingType == "" {
		t.SeedingType = models.SeedingTypeStars
	}

	var out models.TelegramTournament
	err = tx.QueryRowContext(ctx, `
		INSERT INTO telegram_tournaments (name, slug, status, tournament_time, challonge_id, challonge_url, challonge_for, winner_team_id, winner_team_name, is_active, tournament_type, hold_third_place, seeding_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, name, slug, status, tournament_time, challonge_id, challonge_url, challonge_for, winner_team_id, winner_team_name, is_active, created_at, updated_at, tournament_type, hold_third_place, seeding_type
	`, t.Name, t.Slug, t.Status, t.TournamentTime, t.ChallongeID, t.ChallongeURL, t.ChallongeFor, t.WinnerTeamID, t.WinnerTeamName, t.IsActive, t.TournamentType, t.HoldThirdPlace, t.SeedingType).Scan(
		&out.ID, &out.Name, &out.Slug, &out.Status, &out.TournamentTime,
		&out.ChallongeID, &out.ChallongeURL, &out.ChallongeFor, &out.WinnerTeamID, &out.WinnerTeamName, &out.IsActive,
		&out.CreatedAt, &out.UpdatedAt, &out.TournamentType, &out.HoldThirdPlace, &out.SeedingType,
	)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *TelegramPostgres) GetActiveTournament(ctx context.Context) (*models.TelegramTournament, error) {
	var out models.TelegramTournament
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, slug, status, tournament_time, challonge_id, challonge_url, challonge_for, winner_team_id, winner_team_name, is_active, created_at, updated_at, COALESCE(tournament_type, 'single elimination'), COALESCE(hold_third_place, FALSE), COALESCE(seeding_type, 'stars')
		FROM telegram_tournaments
		WHERE is_active = TRUE
		LIMIT 1
	`).Scan(
		&out.ID, &out.Name, &out.Slug, &out.Status, &out.TournamentTime,
		&out.ChallongeID, &out.ChallongeURL, &out.ChallongeFor, &out.WinnerTeamID, &out.WinnerTeamName, &out.IsActive,
		&out.CreatedAt, &out.UpdatedAt, &out.TournamentType, &out.HoldThirdPlace, &out.SeedingType,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *TelegramPostgres) GetTournamentByID(ctx context.Context, id int) (*models.TelegramTournament, error) {
	var out models.TelegramTournament
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, slug, status, tournament_time, challonge_id, challonge_url, challonge_for, winner_team_id, winner_team_name, is_active, created_at, updated_at, COALESCE(tournament_type, 'single elimination'), COALESCE(hold_third_place, FALSE), COALESCE(seeding_type, 'stars')
		FROM telegram_tournaments
		WHERE id = $1
	`, id).Scan(
		&out.ID, &out.Name, &out.Slug, &out.Status, &out.TournamentTime,
		&out.ChallongeID, &out.ChallongeURL, &out.ChallongeFor, &out.WinnerTeamID, &out.WinnerTeamName, &out.IsActive,
		&out.CreatedAt, &out.UpdatedAt, &out.TournamentType, &out.HoldThirdPlace, &out.SeedingType,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *TelegramPostgres) GetAllTournaments(ctx context.Context) ([]models.TelegramTournament, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, slug, status, tournament_time, challonge_id, challonge_url, challonge_for, winner_team_id, winner_team_name, is_active, created_at, updated_at, COALESCE(tournament_type, 'single elimination'), COALESCE(hold_third_place, FALSE), COALESCE(seeding_type, 'stars')
		FROM telegram_tournaments
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []models.TelegramTournament
	for rows.Next() {
		var t models.TelegramTournament
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Status, &t.TournamentTime,
			&t.ChallongeID, &t.ChallongeURL, &t.ChallongeFor, &t.WinnerTeamID, &t.WinnerTeamName, &t.IsActive,
			&t.CreatedAt, &t.UpdatedAt, &t.TournamentType, &t.HoldThirdPlace, &t.SeedingType); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TelegramPostgres) SetActiveTournament(ctx context.Context, id int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `UPDATE telegram_tournaments SET is_active = FALSE WHERE is_active = TRUE`); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE telegram_tournaments SET is_active = TRUE WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("турнир не найден")
	}
	return tx.Commit()
}

func (r *TelegramPostgres) UpdateTournament(ctx context.Context, t *models.TelegramTournament) error {
	if t.TournamentType == "" {
		t.TournamentType = models.TournamentTypeSingleElimination
	}
	if t.SeedingType == "" {
		t.SeedingType = models.SeedingTypeStars
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE telegram_tournaments
		SET name = $2, slug = $3, status = $4, tournament_time = $5,
		    challonge_id = $6, challonge_url = $7, challonge_for = $8,
		    winner_team_id = $9, winner_team_name = $10, is_active = $11,
		    tournament_type = $12, hold_third_place = $13, seeding_type = $14,
		    updated_at = NOW()
		WHERE id = $1
	`, t.ID, t.Name, t.Slug, t.Status, t.TournamentTime, t.ChallongeID, t.ChallongeURL, t.ChallongeFor, t.WinnerTeamID, t.WinnerTeamName, t.IsActive, t.TournamentType, t.HoldThirdPlace, t.SeedingType)
	return err
}

func (r *TelegramPostgres) FinishTournamentRecord(ctx context.Context, tournamentID int, winnerTeamID *int, winnerTeamName string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE telegram_tournaments
		SET status = 'completed', is_active = FALSE, winner_team_id = $2, winner_team_name = $3, updated_at = NOW()
		WHERE id = $1
	`, tournamentID, winnerTeamID, winnerTeamName)
	return err
}

func (r *TelegramPostgres) UpdateTournamentStatus(ctx context.Context, id int, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_tournaments SET status = $2, updated_at = NOW() WHERE id = $1`, id, status)
	return err
}

func (r *TelegramPostgres) RegisterTeamForTournament(ctx context.Context, tournamentID, teamID int) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO telegram_tournament_teams (tournament_id, team_id, status, is_checked_in)
		VALUES ($1, $2, 'registered', FALSE)
		ON CONFLICT (tournament_id, team_id) DO UPDATE SET status = 'registered'
	`, tournamentID, teamID)
	return err
}

func (r *TelegramPostgres) UnregisterTeamFromTournament(ctx context.Context, tournamentID, teamID int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM telegram_tournament_teams
		WHERE tournament_id = $1 AND team_id = $2
	`, tournamentID, teamID)
	return err
}

func (r *TelegramPostgres) GetTournamentTeams(ctx context.Context, tournamentID int) ([]models.TelegramTeam, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.name, tt.is_checked_in, tt.status, tt.challonge_participant_id
		FROM telegram_tournament_teams tt
		JOIN telegram_teams t ON t.id = tt.team_id
		WHERE tt.tournament_id = $1
		ORDER BY t.id ASC
	`, tournamentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var teams []models.TelegramTeam
	for rows.Next() {
		var t models.TelegramTeam
		if err := rows.Scan(&t.ID, &t.Name, &t.IsCheckedIn, &t.Status, &t.ChallongeParticipantID); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range teams {
		members, err := r.GetTeamMembers(ctx, teams[i].ID)
		if err != nil {
			return nil, err
		}
		teams[i].Players = members
	}
	return teams, nil
}

func (r *TelegramPostgres) GetTournamentTeam(ctx context.Context, tournamentID, teamID int) (*models.TournamentTeam, error) {
	var tt models.TournamentTeam
	err := r.db.QueryRowContext(ctx, `
		SELECT tt.id, tt.tournament_id, tt.team_id, t.name, tt.is_checked_in, tt.status,
		       tt.challonge_participant_id, tt.seed, tt.placement, tt.points, tt.created_at
		FROM telegram_tournament_teams tt
		JOIN telegram_teams t ON t.id = tt.team_id
		WHERE tt.tournament_id = $1 AND tt.team_id = $2
	`, tournamentID, teamID).Scan(
		&tt.ID, &tt.TournamentID, &tt.TeamID, &tt.TeamName, &tt.IsCheckedIn, &tt.Status,
		&tt.ChallongeParticipantID, &tt.Seed, &tt.Placement, &tt.Points, &tt.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &tt, nil
}

func (r *TelegramPostgres) SetTournamentCheckIn(ctx context.Context, tournamentID, teamID int, status bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	newStatus := "registered"
	if status {
		newStatus = "checked_in"
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE telegram_tournament_teams
		SET is_checked_in = $3, status = $4
		WHERE tournament_id = $1 AND team_id = $2
	`, tournamentID, teamID, status, newStatus)
	if err != nil {
		return err
	}

	var isActive bool
	_ = tx.QueryRowContext(ctx, `SELECT is_active FROM telegram_tournaments WHERE id = $1`, tournamentID).Scan(&isActive)
	if isActive {
		_, _ = tx.ExecContext(ctx, `UPDATE telegram_teams SET is_checked_in = $2 WHERE id = $1`, teamID, status)
	}

	return tx.Commit()
}

func (r *TelegramPostgres) SetTournamentTeamStatus(ctx context.Context, tournamentID, teamID int, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE telegram_tournament_teams
		SET status = $3
		WHERE tournament_id = $1 AND team_id = $2
	`, tournamentID, teamID, status)
	return err
}

func (r *TelegramPostgres) SetTournamentTeamParticipantID(ctx context.Context, tournamentID, teamID int, participantID int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE telegram_tournament_teams
		SET challonge_participant_id = $3
		WHERE tournament_id = $1 AND team_id = $2
	`, tournamentID, teamID, participantID)
	return err
}

func (r *TelegramPostgres) ClearTournamentTeamParticipantIDs(ctx context.Context, tournamentID int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE telegram_tournament_teams
		SET challonge_participant_id = NULL
		WHERE tournament_id = $1
	`, tournamentID)
	return err
}

func (r *TelegramPostgres) UpdateTournamentPlacements(ctx context.Context, tournamentID int, placements map[int]int, points map[int]int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	for teamID, place := range placements {
		pts := points[teamID]
		_, err := tx.ExecContext(ctx, `
			UPDATE telegram_tournament_teams
			SET placement = $3, points = $4
			WHERE tournament_id = $1 AND team_id = $2
		`, tournamentID, teamID, place, pts)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

