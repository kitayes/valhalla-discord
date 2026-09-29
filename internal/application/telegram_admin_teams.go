package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"blackwatch/internal/models"
)

// Admin-side team management for the referee panel. Unlike the captain flows
// these address a team or player by id and are not blocked by check-in: the
// referee is the one who fixes a roster after the captains are locked out.

// Actions accepted by AdminTeamAction.
const (
	TeamActionDisqualify = "disqualify"
	TeamActionReinstate  = "reinstate"
	TeamActionCheckIn    = "checkin"
	TeamActionUncheckIn  = "uncheckin"
)

// AdminListTeams returns the teams with their rosters. tournamentID <= 0 means
// the active tournament; with no active tournament (or nobody entered) every
// team is listed, matching what the check-in summary counts.
func (s *TelegramServiceImpl) AdminListTeams(ctx context.Context, tournamentID int) ([]models.TelegramTeam, error) {
	if tournamentID <= 0 {
		if active, _ := s.repo.GetActiveTournament(ctx); active != nil {
			tournamentID = active.ID
		}
	}
	if tournamentID > 0 {
		teams, err := s.repo.GetTournamentTeams(ctx, tournamentID)
		if err != nil {
			return nil, err
		}
		if len(teams) > 0 {
			return teams, nil
		}
	}
	return s.repo.GetAllTeams(ctx)
}

// AdminUpdatePlayer edits a roster row regardless of check-in state.
func (s *TelegramServiceImpl) AdminUpdatePlayer(ctx context.Context, playerID int, nick, gameID, zoneID, role string) error {
	target, err := s.repo.GetPlayerByID(ctx, playerID)
	if err != nil || target == nil {
		return errors.New("игрок не найден")
	}
	nick = strings.TrimSpace(nick)
	gameID = strings.TrimSpace(gameID)
	zoneID = strings.TrimSpace(zoneID)
	role = strings.TrimSpace(role)
	if nick == "" {
		return errors.New("никнейм игрока не может быть пустым")
	}
	if gameID == "" {
		return errors.New("game ID игрока не может быть пустым")
	}
	if dup := s.duplicateGameID(ctx, gameID, playerID); dup != "" {
		return errors.New(dup)
	}
	for _, f := range []struct {
		col string
		val string
	}{{"game_nickname", nick}, {"game_id", gameID}, {"zone_id", zoneID}} {
		if err := s.repo.UpdatePlayerFieldByID(ctx, playerID, f.col, f.val); err != nil {
			return fmt.Errorf("не удалось сохранить игрока: %w", err)
		}
	}
	if role != "" {
		if err := s.repo.UpdatePlayerFieldByID(ctx, playerID, "main_role", role); err != nil {
			return fmt.Errorf("не удалось сохранить игрока: %w", err)
		}
	}
	return nil
}

// AdminKickPlayer removes a non-captain from their team. Rows with a Telegram
// account are detached, roster-only rows are deleted.
func (s *TelegramServiceImpl) AdminKickPlayer(ctx context.Context, playerID int) error {
	target, err := s.repo.GetPlayerByID(ctx, playerID)
	if err != nil || target == nil {
		return errors.New("игрок не найден")
	}
	if target.TeamID == nil {
		return errors.New("игрок не состоит в команде")
	}
	if target.IsCaptain {
		return errors.New("нельзя исключить капитана: сначала назначьте другого капитана")
	}
	if target.TelegramID != nil {
		if err := s.repo.UpdatePlayerFieldByID(ctx, playerID, "team_id", nil); err != nil {
			return fmt.Errorf("не удалось исключить игрока: %w", err)
		}
		return nil
	}
	if err := s.repo.DeleteTeammate(ctx, playerID); err != nil {
		return fmt.Errorf("не удалось исключить игрока: %w", err)
	}
	return nil
}

// AdminSetCaptain makes the player the captain of their team, demoting the
// current one.
func (s *TelegramServiceImpl) AdminSetCaptain(ctx context.Context, playerID int) error {
	target, err := s.repo.GetPlayerByID(ctx, playerID)
	if err != nil || target == nil {
		return errors.New("игрок не найден")
	}
	if target.TeamID == nil {
		return errors.New("игрок не состоит в команде")
	}
	if target.IsCaptain {
		return errors.New("игрок уже капитан")
	}
	if target.TelegramID == nil {
		return errors.New("капитаном можно назначить только игрока с аккаунтом Telegram")
	}
	members, err := s.repo.GetTeamMembers(ctx, *target.TeamID)
	if err != nil {
		return errors.New("не удалось получить состав команды")
	}
	var oldCaptainID int
	for _, m := range members {
		if m.IsCaptain {
			oldCaptainID = m.ID
			break
		}
	}
	if oldCaptainID != 0 {
		if err := s.repo.UpdatePlayerFieldByID(ctx, oldCaptainID, "is_captain", false); err != nil {
			return fmt.Errorf("не удалось снять флаг капитана: %w", err)
		}
	}
	if err := s.repo.UpdatePlayerFieldByID(ctx, playerID, "is_captain", true); err != nil {
		if oldCaptainID != 0 {
			_ = s.repo.UpdatePlayerFieldByID(ctx, oldCaptainID, "is_captain", true)
		}
		return fmt.Errorf("не удалось назначить капитана: %w", err)
	}
	return nil
}

// AdminTeamAction applies a referee decision to a team and returns the message
// to show. Disqualify and reinstate touch the tournament entry too: that is
// what the bracket and the sweeps read.
func (s *TelegramServiceImpl) AdminTeamAction(ctx context.Context, teamID int, action string) (string, error) {
	team, err := s.repo.GetTeamByID(ctx, teamID)
	if err != nil || team == nil {
		return "", errors.New("команда не найдена")
	}
	switch action {
	case TeamActionDisqualify:
		s.logWrite("SetTeamStatus", s.repo.SetTeamStatus(ctx, team.ID, models.TeamStatusDisqualified))
		if active, _ := s.repo.GetActiveTournament(ctx); active != nil {
			s.logWrite("SetTournamentTeamStatus", s.repo.SetTournamentTeamStatus(ctx, active.ID, team.ID, models.TeamStatusDisqualified))
		}
		return fmt.Sprintf("Команда '%s' снята с турнира (тех. поражение).", team.Name), nil
	case TeamActionReinstate:
		return s.reinstateTeam(ctx, team), nil
	case TeamActionCheckIn, TeamActionUncheckIn:
		st, err := s.checkInState(ctx, team)
		if err != nil {
			return "", errors.New("не удалось изменить check-in, попробуйте ещё раз")
		}
		if st.disqualified {
			return "", fmt.Errorf("команда '%s' снята с турнира: сначала верните её", team.Name)
		}
		checkedIn := action == TeamActionCheckIn
		if err := s.recordCheckIn(ctx, st, team.ID, checkedIn); err != nil {
			if errors.Is(err, errNotEntered) && st.tournament != nil {
				return "", errors.New(notEnteredMessage(team.Name, st.tournament))
			}
			return "", fmt.Errorf("не удалось изменить check-in: %w", err)
		}
		if checkedIn {
			return fmt.Sprintf("Команда '%s' отмечена как прошедшая check-in.", team.Name), nil
		}
		return fmt.Sprintf("Check-in команды '%s' снят.", team.Name), nil
	default:
		return "", errors.New("неизвестное действие")
	}
}

// AdminDeleteTeamByID deletes a team and detaches its members. During a running
// tournament the bracket depends on the team, so disqualify it instead.
func (s *TelegramServiceImpl) AdminDeleteTeamByID(ctx context.Context, teamID int) error {
	team, err := s.repo.GetTeamByID(ctx, teamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if active, _ := s.repo.GetActiveTournament(ctx); active != nil {
		if active.Status == models.TournamentStatusActive {
			return errors.New("турнир уже идёт: вместо удаления снимите команду с турнира (тех. поражение)")
		}
		_ = s.repo.UnregisterTeamFromTournament(ctx, active.ID, team.ID)
	}
	s.logWrite("ReleaseTeamMembers", s.repo.ReleaseTeamMembers(ctx, team.ID))
	if err := s.repo.DeleteTeam(ctx, team.ID); err != nil {
		return fmt.Errorf("не удалось удалить команду: %w", err)
	}
	return nil
}
