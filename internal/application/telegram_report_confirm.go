package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"time"
)

// ReportConfirmWindow is how long the opposing captain has to confirm or
// dispute a reported result before it is applied as filed. Short enough that
// an absent opponent cannot stall the bracket, long enough to open the app.
const ReportConfirmWindow = 5 * time.Minute

// GetOpenReportForMatch returns the report awaiting the other side on a
// bracket match, or nil.
func (s *TelegramServiceImpl) GetOpenReportForMatch(ctx context.Context, bracketMatchID int) (*models.TelegramMatchReport, error) {
	return s.repo.GetOpenReportForMatch(ctx, bracketMatchID)
}

// ConfirmReport is the opposing captain agreeing with a pending report; the
// bracket moves as filed.
func (s *TelegramServiceImpl) ConfirmReport(ctx context.Context, actorTgID int64, reportID int) error {
	rep, err := s.openReportForActor(ctx, actorTgID, reportID)
	if err != nil {
		return err
	}
	if rep.Status != models.ReportPending {
		return errors.New("отчёт уже оспорен — решение примет судья")
	}
	if err := s.applyReport(ctx, rep, models.ReportConfirmed); err != nil {
		return err
	}
	s.notifyTeamOfReporter(ctx, rep.ReporterTelegramID, rep,
		fmt.Sprintf("Соперник подтвердил результат матча: %s %s %s. Сетка обновлена.", rep.WinnerTeamName, rep.Score, rep.LoserTeamName))
	return nil
}

// DisputeReport is the opposing captain objecting. The report stays open, the
// bracket stays put, and the match is left for a referee to settle.
func (s *TelegramServiceImpl) DisputeReport(ctx context.Context, actorTgID int64, reportID int) error {
	rep, err := s.openReportForActor(ctx, actorTgID, reportID)
	if err != nil {
		return err
	}
	if rep.Status != models.ReportPending {
		return errors.New("отчёт уже оспорен")
	}
	if err := s.repo.SetReportStatus(ctx, rep.ID, models.ReportDisputed); err != nil {
		return fmt.Errorf("не удалось сохранить возражение: %w", err)
	}
	s.notifyTeamOfReporter(ctx, rep.ReporterTelegramID, rep,
		fmt.Sprintf("Соперник оспорил результат %s %s %s. Матч передан судье — приложите скриншоты, если ещё не сделали.", rep.WinnerTeamName, rep.Score, rep.LoserTeamName))
	return nil
}

// AutoConfirmExpiredReports applies pending reports whose confirmation window
// has passed, and returns them. Meant for a periodic sweep.
func (s *TelegramServiceImpl) AutoConfirmExpiredReports(ctx context.Context) ([]models.TelegramMatchReport, error) {
	expired, err := s.repo.GetExpiredPendingReports(ctx, s.now())
	if err != nil {
		return nil, err
	}
	var done []models.TelegramMatchReport
	for i := range expired {
		rep := &expired[i]
		if err := s.applyReport(ctx, rep, models.ReportAutoConfirmed); err != nil {
			s.logger.Error("telegram: auto-confirm report %d: %v", rep.ID, err)
			continue
		}
		done = append(done, *rep)
		s.notifyTeamOfReporter(ctx, rep.ReporterTelegramID, rep,
			fmt.Sprintf("Результат %s %s %s принят: соперник не возразил за %d минут. Сетка обновлена.",
				rep.WinnerTeamName, rep.Score, rep.LoserTeamName, int(ReportConfirmWindow.Minutes())))
	}
	return done, nil
}

// openReportForActor loads an open report and checks that the actor is the
// captain of the team that did not file it.
func (s *TelegramServiceImpl) openReportForActor(ctx context.Context, actorTgID int64, reportID int) (*models.TelegramMatchReport, error) {
	rep, err := s.repo.GetMatchReport(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения отчёта: %w", err)
	}
	if rep == nil {
		return nil, errors.New("отчёт не найден")
	}
	if !rep.Open() {
		return nil, errors.New("отчёт уже закрыт")
	}
	actor, err := s.repo.GetPlayerByTelegramID(ctx, actorTgID)
	if err != nil || actor == nil || actor.TeamID == nil {
		return nil, errors.New("вы не состоите в команде")
	}
	if !actor.IsCaptain {
		return nil, errors.New("подтвердить или оспорить результат может только капитан")
	}
	if actorTgID == rep.ReporterTelegramID {
		return nil, errors.New("свой отчёт подтверждает соперник")
	}
	if *actor.TeamID != rep.WinnerTeamID && *actor.TeamID != rep.LoserTeamID {
		return nil, errors.New("это не матч вашей команды")
	}
	if reporter, _ := s.repo.GetPlayerByTelegramID(ctx, rep.ReporterTelegramID); reporter != nil && reporter.TeamID != nil && *reporter.TeamID == *actor.TeamID {
		return nil, errors.New("свой отчёт подтверждает соперник")
	}
	return rep, nil
}

// applyReport closes the report with status and moves the bracket as filed.
// A bracket already settled by a referee is not an error here: the status
// still records how the report ended.
func (s *TelegramServiceImpl) applyReport(ctx context.Context, rep *models.TelegramMatchReport, status string) error {
	if err := s.repo.SetReportStatus(ctx, rep.ID, status); err != nil {
		return fmt.Errorf("не удалось закрыть отчёт: %w", err)
	}
	rep.Status = status
	if rep.BracketMatchID == nil {
		return nil
	}
	win, lose, _, ok := parseScore(rep.Score)
	if !ok {
		return fmt.Errorf("некорректный счёт в отчёте: %q", rep.Score)
	}
	if err := s.propagateBracketResult(ctx, *rep.BracketMatchID, rep.WinnerTeamID, win, lose); err != nil {
		s.logger.Error("telegram: applyReport %d propagateBracketResult: %v", rep.ID, err)
	}
	if s.bracket != nil {
		if _, err := s.bracket.ReportResult(ctx, *rep.BracketMatchID, rep.WinnerTeamID, win, lose); err != nil {
			s.logger.Error("telegram: applyReport %d ReportResult: %v", rep.ID, err)
		} else if err := s.repo.SetReportSynced(ctx, rep.ID); err == nil {
			now := s.now()
			rep.SyncedAt = &now
		}
	}
	return nil
}

// closeOpenReport marks whatever report is open on a match as overridden; a
// referee's decision supersedes the captains' exchange. matchID may be the
// bracket id or the play order, as elsewhere in the admin commands.
func (s *TelegramServiceImpl) closeOpenReport(ctx context.Context, matchID int) {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return
	}
	id := matchID
	for _, m := range ms {
		if m.ID == matchID || m.PlayOrder == matchID {
			id = m.ID
			break
		}
	}
	open, err := s.repo.GetOpenReportForMatch(ctx, id)
	if err != nil || open == nil {
		return
	}
	s.logWrite("SetReportStatus", s.repo.SetReportStatus(ctx, open.ID, models.ReportOverridden))
}

// notifyPendingReport tells the opposing team a result is waiting on them.
func (s *TelegramServiceImpl) notifyPendingReport(ctx context.Context, rep *models.TelegramMatchReport, m *models.BracketMatch, reporterTeamID int) {
	oppID := rep.LoserTeamID
	if reporterTeamID == rep.LoserTeamID {
		oppID = rep.WinnerTeamID
	}
	text := fmt.Sprintf("Матч #%d: соперник внёс результат %s %s %s.\n\nПодтвердите или оспорьте его в приложении. Без ответа результат будет принят через %d минут.",
		m.PlayOrder, rep.WinnerTeamName, rep.Score, rep.LoserTeamName, int(ReportConfirmWindow.Minutes()))
	members, err := s.repo.GetTeamMembers(ctx, oppID)
	if err != nil {
		return
	}
	seen := make(map[int64]bool)
	for _, m := range members {
		if m.TelegramID != nil && *m.TelegramID > 0 && !seen[*m.TelegramID] {
			seen[*m.TelegramID] = true
			s.notifyCaptain(ctx, *m.TelegramID, text, true)
		}
	}
}

func (s *TelegramServiceImpl) notifyTeamOfReporter(ctx context.Context, reporterTgID int64, rep *models.TelegramMatchReport, text string) {
	var teamID int
	if reporter, _ := s.repo.GetPlayerByTelegramID(ctx, reporterTgID); reporter != nil && reporter.TeamID != nil {
		teamID = *reporter.TeamID
	} else if rep != nil {
		teamID = rep.WinnerTeamID
	}
	if teamID > 0 {
		members, err := s.repo.GetTeamMembers(ctx, teamID)
		if err == nil {
			seen := make(map[int64]bool)
			for _, m := range members {
				if m.TelegramID != nil && *m.TelegramID > 0 && !seen[*m.TelegramID] {
					seen[*m.TelegramID] = true
					s.notifyCaptain(ctx, *m.TelegramID, text, true)
				}
			}
			return
		}
	}
	s.notifyCaptain(ctx, reporterTgID, text, true)
}

func (s *TelegramServiceImpl) notifyCaptain(ctx context.Context, chatID int64, text string, webApp bool) {
	if s.notifyMatchFunc == nil || chatID <= 0 {
		return
	}
	s.notifyMatchFunc(ctx, chatID, text, webApp)
}
