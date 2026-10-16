package repository

import (
	"blackwatch/internal/models"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// UpdateMatchDesk commits the state machine and its outbox together. The row
// lock serializes callbacks and worker ticks across processes. Pairing locks
// prevent a bracket refresh from changing opponents during a captain action.
func (r *TelegramPostgres) UpdateMatchDesk(ctx context.Context, change func(*models.MatchDesk, models.DeskContext) error) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	// The desk serves the active tournament only. It used to read every
	// tournament's bracket rows, so a finished qualifier's unplayed rounds
	// stayed on the desk next to the next tournament's matches.
	active, err := scanTournament(tx.QueryRowContext(ctx, `SELECT `+tournamentColumns+` FROM telegram_tournaments WHERE is_active = TRUE LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("сетка ещё не построена")
	}
	if err != nil {
		return err
	}
	var tournament, starts string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM telegram_settings WHERE key='challonge_tournament_id'),''), COALESCE((SELECT value FROM telegram_settings WHERE key='challonge_tournament_for'),'')`).Scan(&tournament, &starts)
	if err != nil {
		return err
	}
	if tournament == "" {
		var cID sql.NullInt64
		var cFor sql.NullTime
		_ = tx.QueryRowContext(ctx, `SELECT challonge_id, challonge_for FROM telegram_tournaments WHERE is_active = TRUE LIMIT 1`).Scan(&cID, &cFor)
		if cID.Valid && cID.Int64 != 0 {
			tournament = strconv.FormatInt(cID.Int64, 10)
		}
		if cFor.Valid {
			starts = cFor.Time.UTC().Format(time.RFC3339)
		}
	}
	if tournament == "" {
		return errors.New("сетка ещё не построена")
	}
	startsAt, err := time.Parse(time.RFC3339, starts)
	if err != nil {
		return errors.New("сетка ещё не готова: время турнира не установлено")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_match_desks (tournament_id) VALUES ($1) ON CONFLICT DO NOTHING`, tournament); err != nil {
		return err
	}
	var data []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM telegram_match_desks WHERE tournament_id=$1 FOR UPDATE`, tournament).Scan(&data); err != nil {
		return err
	}
	var d models.MatchDesk
	if err = json.Unmarshal(data, &d); err != nil {
		return err
	}
	d.Tournament = tournament
	snapshot := models.DeskContext{Tournament: active, StartsAt: startsAt, Captains: map[int]models.TelegramPlayer{}, Revisions: map[int]int64{}}
	err = func() error {
		rows, err := tx.QueryContext(ctx, `SELECT m.id,m.challonge_match_id,m.round,m.play_order,m.team1_id,m.team2_id,m.winner_id,m.state,m.scores_csv,COALESCE(a.name,''),COALESCE(b.name,''),m.desk_revision
 FROM telegram_bracket_matches m LEFT JOIN telegram_teams a ON a.id=m.team1_id LEFT JOIN telegram_teams b ON b.id=m.team2_id WHERE m.tournament_id=$1 ORDER BY m.id FOR SHARE OF m`, active.ID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var m models.BracketMatch
			var revision int64
			if err = rows.Scan(&m.ID, &m.ChallongeMatchID, &m.Round, &m.PlayOrder, &m.Team1ID, &m.Team2ID, &m.WinnerID, &m.State, &m.ScoresCSV, &m.Team1Name, &m.Team2Name, &revision); err != nil {
				return err
			}
			snapshot.Matches = append(snapshot.Matches, m)
			snapshot.Revisions[m.ID] = revision
		}
		return rows.Err()
	}()
	if err != nil {
		return err
	}

	err = func() error {
		rows, err := tx.QueryContext(ctx, `SELECT p.id,p.telegram_id,COALESCE(p.telegram_username,''),COALESCE(p.game_nickname,''),COALESCE(p.game_id,''),COALESCE(p.zone_id,''),p.team_id
 FROM telegram_players p JOIN telegram_teams t ON t.id=p.team_id WHERE p.is_captain AND p.telegram_id IS NOT NULL AND t.status='active' FOR SHARE OF p,t`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var p models.TelegramPlayer
			if err = rows.Scan(&p.ID, &p.TelegramID, &p.TelegramUsername, &p.GameNickname, &p.GameID, &p.ZoneID, &p.TeamID); err != nil {
				return err
			}
			p.IsCaptain = true
			snapshot.Captains[*p.TeamID] = p
		}
		return rows.Err()
	}()
	if err != nil {
		return err
	}

	// Every player's chat, for reminders that must reach the whole team.
	snapshot.Members = map[int][]int64{}
	err = func() error {
		rows, err := tx.QueryContext(ctx, `SELECT p.team_id, p.telegram_id FROM telegram_players p JOIN telegram_teams t ON t.id = p.team_id
 WHERE p.telegram_id IS NOT NULL AND p.telegram_id > 0 AND t.status = 'active' ORDER BY p.team_id, p.id`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var team int
			var chat int64
			if err = rows.Scan(&team, &chat); err != nil {
				return err
			}
			snapshot.Members[team] = append(snapshot.Members[team], chat)
		}
		return rows.Err()
	}()
	if err != nil {
		return err
	}

	// The games of each open match at its current revision: the desk flags a
	// dispute nobody decided and a series with no news.
	snapshot.Series = map[int]models.DeskSeries{}
	err = func() error {
		rows, err := tx.QueryContext(ctx, `SELECT g.bracket_match_id, g.game_no, g.status, g.created_at, g.resolved_at
 FROM telegram_match_games g JOIN telegram_bracket_matches m ON m.id = g.bracket_match_id AND g.match_revision = m.desk_revision
 WHERE m.tournament_id = $1 AND m.state = 'open'`, active.ID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var matchID, gameNo int
			var status string
			var created time.Time
			var resolved sql.NullTime
			if err = rows.Scan(&matchID, &gameNo, &status, &created, &resolved); err != nil {
				return err
			}
			st := snapshot.Series[matchID]
			at := created
			if resolved.Valid && resolved.Time.After(at) {
				at = resolved.Time
			}
			if at.After(st.LastActivity) {
				st.LastActivity = at
			}
			if status == models.GameDisputed {
				st.DisputedGame, st.DisputedAt = gameNo, at
			}
			snapshot.Series[matchID] = st
		}
		return rows.Err()
	}()
	if err != nil {
		return err
	}

	if err = change(&d, snapshot); err != nil {
		return err
	}
	data, err = json.Marshal(d)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE telegram_match_desks SET data=$2,updated_at=NOW() WHERE tournament_id=$1`, tournament, string(data)); err != nil {
		return err
	}
	return tx.Commit()
}
