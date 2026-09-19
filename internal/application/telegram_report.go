package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	KbReportOpponent = "rep_opponent"
	KbReportScore    = "rep_score"
	KbReportPhotos   = "rep_photos"
)

type MatchReportDraft struct {
	ReporterTgID   int64
	WinnerTeamID   int
	WinnerTeamName string
	LoserTeamID    int
	LoserTeamName  string
	Score          string
	WinnerScore    int
	LoserScore     int
	BracketMatchID int
	PlayOrder      int
	PhotoFileIDs   []string
	UpdatedAt      time.Time
}

func (s *TelegramServiceImpl) GetReportDraft(tgID int64) *MatchReportDraft {
	s.reportMu.RLock()
	defer s.reportMu.RUnlock()
	return s.reportDrafts[tgID]
}

func (s *TelegramServiceImpl) setReportDraft(tgID int64, draft *MatchReportDraft) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	s.reportDrafts[tgID] = draft
}

func (s *TelegramServiceImpl) clearReportDraft(tgID int64) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	delete(s.reportDrafts, tgID)
}

func isReportState(st string) bool {
	return st == models.StateReportOpponent || st == models.StateReportScore || st == models.StateReportScreenshots
}

func parseScore(input string) (myScore, oppScore int, formatted string, ok bool) {
	s := strings.TrimSpace(input)
	var parts []string
	if strings.Contains(s, ":") {
		parts = strings.Split(s, ":")
	} else if strings.Contains(s, "-") {
		parts = strings.Split(s, "-")
	} else if strings.Contains(s, " ") {
		parts = strings.Fields(s)
	} else {
		return 0, 0, "", false
	}

	if len(parts) != 2 {
		return 0, 0, "", false
	}

	x, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	y, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || x < 0 || y < 0 {
		return 0, 0, "", false
	}

	return x, y, fmt.Sprintf("%d:%d", x, y), true
}

func (s *TelegramServiceImpl) StartReport(ctx context.Context, tgID int64) (string, string) {
	p, err := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if err != nil || p == nil || p.TeamID == nil {
		return "Вы не состоите в команде. Отчет о победе может отправить только капитан команды.", KbNone
	}
	if !p.IsCaptain {
		return "Только капитан команды может отправить отчет о результате матча.", KbNone
	}

	myTeam, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || myTeam == nil {
		return "Команда не найдена.", KbNone
	}
	if myTeam.Status == models.TeamStatusDisqualified {
		return "Ваша команда дисквалифицирована.", KbNone
	}

	draft := &MatchReportDraft{
		ReporterTgID:   tgID,
		WinnerTeamID:   myTeam.ID,
		WinnerTeamName: myTeam.Name,
		UpdatedAt:      time.Now(),
	}
	if s.bracket != nil {
		m, err := s.bracket.OpenMatchFor(ctx, myTeam.ID)
		if err != nil {
			s.logger.Error("telegram: OpenMatchFor %d: %v", myTeam.ID, err)
			return "Не удалось прочитать сетку. Попробуйте позже.", KbNone
		}
		if m == nil {
			return "У вашей команды сейчас нет открытого матча в сетке. Если это ошибка — напишите администратору.", KbNone
		}
		oppID := m.Opponent(myTeam.ID)
		if oppID == nil {
			return "У вашей команды сейчас нет определённого соперника в сетке.", KbNone
		}
		opp, err := s.repo.GetTeamByID(ctx, *oppID)
		if err != nil || opp == nil {
			return "Команда соперника не найдена.", KbNone
		}
		draft.LoserTeamID = opp.ID
		draft.LoserTeamName = opp.Name
		draft.BracketMatchID = m.ID
		draft.PlayOrder = m.PlayOrder
		s.setReportDraft(tgID, draft)
		s.setState(ctx, tgID, models.StateReportScore)
		return fmt.Sprintf("Отчет о результате матча\nМатч #%d (раунд %d): %s vs %s\n\nУкажите счет матча в пользу вашей команды:\n(Выберите кнопку или отправьте счет сообщением, например 2:0)",
			m.PlayOrder, m.Round, myTeam.Name, opp.Name), KbReportScore
	}

	opponents, err := s.GetEligibleOpponents(ctx, tgID)
	if err != nil || len(opponents) == 0 {
		return "Нет доступных команд-соперников для отправки отчета.", KbNone
	}
	s.setReportDraft(tgID, draft)
	s.setState(ctx, tgID, models.StateReportOpponent)

	msg := fmt.Sprintf("Отчет о результате матча\nВаша команда: %s (Победитель)\n\nВыберите команду соперника, против которой вы играли:", myTeam.Name)
	return msg, KbReportOpponent
}

func (s *TelegramServiceImpl) GetEligibleOpponents(ctx context.Context, tgID int64) ([]models.TelegramTeam, error) {
	p, err := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if err != nil || p == nil || p.TeamID == nil {
		return nil, errors.New("not in a team")
	}

	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}

	var eligible []models.TelegramTeam
	for _, t := range teams {
		if t.ID != *p.TeamID && t.Status != models.TeamStatusDisqualified {
			eligible = append(eligible, t)
		}
	}
	return eligible, nil
}

func (s *TelegramServiceImpl) SelectReportOpponent(ctx context.Context, tgID int64, opponentTeamID int) (string, string) {
	draft := s.GetReportDraft(tgID)
	if draft == nil {
		return "Сессия отчета истекла. Начните заново: /report", KbNone
	}

	if opponentTeamID == draft.WinnerTeamID {
		return "Нельзя выбрать свою команду в качестве соперника.", KbReportOpponent
	}

	opp, err := s.repo.GetTeamByID(ctx, opponentTeamID)
	if err != nil || opp == nil || opp.Status == models.TeamStatusDisqualified {
		return "Команда соперника не найдена или снята с турнира.", KbReportOpponent
	}

	draft.LoserTeamID = opp.ID
	draft.LoserTeamName = opp.Name
	draft.UpdatedAt = time.Now()
	s.setReportDraft(tgID, draft)
	s.setState(ctx, tgID, models.StateReportScore)

	msg := fmt.Sprintf("Матч: %s vs %s\n\nУкажите счет матча в пользу вашей команды:\n(Выберите кнопку или отправьте счет сообщением, например 2:0)",
		draft.WinnerTeamName, opp.Name)
	return msg, KbReportScore
}

func (s *TelegramServiceImpl) SelectReportOpponentByName(ctx context.Context, tgID int64, name string) (string, string) {
	draft := s.GetReportDraft(tgID)
	if draft == nil {
		return "Сессия отчета истекла. Начните заново: /report", KbNone
	}

	trimmed := strings.TrimSpace(name)
	opp, err := s.repo.GetTeamByName(ctx, trimmed)
	if err != nil || opp == nil {
		return fmt.Sprintf("Команда '%s' не найдена. Выберите команду из кнопок ниже:", trimmed), KbReportOpponent
	}
	return s.SelectReportOpponent(ctx, tgID, opp.ID)
}

func (s *TelegramServiceImpl) SetReportScore(ctx context.Context, tgID int64, scoreStr string) (string, string) {
	draft := s.GetReportDraft(tgID)
	if draft == nil {
		return "Сессия отчета истекла. Начните заново: /report", KbNone
	}

	x, y, formatted, ok := parseScore(scoreStr)
	if !ok {
		return "Некорректный формат счета. Введите счет в формате 2:0 или 2:1 (или выберите кнопку ниже):", KbReportScore
	}

	if x <= y {
		return fmt.Sprintf("По правилам турнира отчет отправляет команда-победитель.\nВ счете победа должна быть за вашей командой (например, 2:0 или 2:1).\n\nЕсли вы проиграли матч со счетом %s, отчет должен отправить капитан команды соперника.", formatted), KbReportScore
	}

	draft.Score = formatted
	draft.WinnerScore, draft.LoserScore = x, y
	draft.UpdatedAt = time.Now()
	s.setReportDraft(tgID, draft)
	s.setState(ctx, tgID, models.StateReportScreenshots)

	msg := fmt.Sprintf("Матч: %s %s %s\n\nОтправьте скриншоты победы в матче.\nВы можете отправить один или несколько скриншотов (альбомом или по очереди).\n\nЗагружено: 0 скриншотов.",
		draft.WinnerTeamName, draft.Score, draft.LoserTeamName)
	return msg, KbReportPhotos + ":0"
}

func (s *TelegramServiceImpl) AddReportPhoto(ctx context.Context, tgID int64, photoFileID string) (string, string, int) {
	s.reportMu.Lock()
	draft := s.reportDrafts[tgID]
	if draft == nil {
		s.reportMu.Unlock()
		return "Сессия отчета не найдена. Начните заново: /report", KbNone, 0
	}

	draft.PhotoFileIDs = append(draft.PhotoFileIDs, photoFileID)
	draft.UpdatedAt = time.Now()
	n := len(draft.PhotoFileIDs)
	s.reportMu.Unlock()

	msg := fmt.Sprintf("Скриншот добавлен! Всего загружено: %d.\n\nОтправьте ещё скриншот или нажмите «Отправить отчет» ниже:", n)
	return msg, KbReportPhotos + ":" + strconv.Itoa(n), n
}

func (s *TelegramServiceImpl) ResetReportPhotos(ctx context.Context, tgID int64) (string, string) {
	s.reportMu.Lock()
	draft := s.reportDrafts[tgID]
	if draft == nil {
		s.reportMu.Unlock()
		return "Сессия отчета не найдена. Начните заново: /report", KbNone
	}

	draft.PhotoFileIDs = nil
	draft.UpdatedAt = time.Now()
	winnerName := draft.WinnerTeamName
	score := draft.Score
	loserName := draft.LoserTeamName
	s.reportMu.Unlock()

	msg := fmt.Sprintf("Скриншоты сброшены.\nМатч: %s %s %s\n\nОтправьте новые скриншоты победы:",
		winnerName, score, loserName)
	return msg, KbReportPhotos + ":0"
}

func (s *TelegramServiceImpl) SubmitReport(ctx context.Context, tgID int64) (string, string, *models.TelegramMatchReport, []models.BracketMatch) {
	s.reportMu.Lock()
	draft := s.reportDrafts[tgID]
	if draft == nil {
		s.reportMu.Unlock()
		return "Сессия отчета истекла. Начните заново: /report", KbNone, nil, nil
	}

	if len(draft.PhotoFileIDs) == 0 {
		s.reportMu.Unlock()
		return "Сначала загрузите хотя бы один скриншот матча.", KbReportPhotos + ":0", nil, nil
	}

	d := *draft
	d.PhotoFileIDs = append([]string(nil), draft.PhotoFileIDs...)
	s.reportMu.Unlock()

	report := &models.TelegramMatchReport{
		ReporterTelegramID: tgID,
		WinnerTeamID:       d.WinnerTeamID,
		WinnerTeamName:     d.WinnerTeamName,
		LoserTeamID:        d.LoserTeamID,
		LoserTeamName:      d.LoserTeamName,
		Score:              d.Score,
		PhotoFileIDs:       d.PhotoFileIDs,
	}
	if d.BracketMatchID != 0 {
		id := d.BracketMatchID
		report.BracketMatchID = &id
	}

	if err := s.repo.CreateMatchReport(ctx, report); err != nil {
		s.logger.Error("telegram: failed to save match report: %v", err)
		return "Ошибка при сохранении отчета в базу данных: " + err.Error(), KbReportPhotos + ":" + strconv.Itoa(len(d.PhotoFileIDs)), nil, nil
	}

	s.setState(ctx, tgID, models.StateIdle)
	s.clearReportDraft(tgID)

	successMsg := fmt.Sprintf("Отчет о матче %s %s %s успешно отправлен судьям!",
		d.WinnerTeamName, d.Score, d.LoserTeamName)
	var ready []models.BracketMatch
	if s.bracket != nil && d.BracketMatchID != 0 {
		var err error
		ready, err = s.bracket.ReportResult(ctx, d.BracketMatchID, d.WinnerTeamID, d.WinnerScore, d.LoserScore)
		if err != nil {
			s.logger.Error("telegram: bracket report for match %d failed, queued: %v", d.BracketMatchID, err)
			successMsg += "\n\nСетка сейчас недоступна — результат принят, сетка обновится в течение нескольких минут."
		} else {
			if err := s.repo.SetReportSynced(ctx, report.ID); err != nil {
				s.logger.Error("telegram: SetReportSynced %d failed: %v", report.ID, err)
			} else {
				now := time.Now()
				report.SyncedAt = &now
			}
			successMsg += fmt.Sprintf("\nМатч #%d закрыт, победитель проходит дальше.", d.PlayOrder)
		}
	}
	return successMsg, "main_menu", report, ready
}

func (s *TelegramServiceImpl) CancelReport(ctx context.Context, tgID int64) (string, string) {
	s.clearReportDraft(tgID)
	s.setState(ctx, tgID, models.StateIdle)
	return "Отчет отменен. Возврат в меню.", "main_menu"
}

func (s *TelegramServiceImpl) GetRecentMatchReports(ctx context.Context, limit int) (string, error) {
	reports, err := s.repo.GetRecentMatchReports(ctx, limit)
	if err != nil {
		return "Ошибка получения отчетов: " + err.Error(), err
	}
	if len(reports) == 0 {
		return "Отчетов о матчах пока нет.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Последние отчеты о матчах (%d):\n\n", len(reports)))
	for i, r := range reports {
		sb.WriteString(fmt.Sprintf("%d. %s %s %s (%s) — скриншотов: %d\n",
			i+1, r.WinnerTeamName, r.Score, r.LoserTeamName,
			r.CreatedAt.Format("02.01 15:04"), len(r.PhotoFileIDs)))
	}
	return sb.String(), nil
}

func (s *TelegramServiceImpl) handleReportText(ctx context.Context, p *models.TelegramPlayer, input string) (string, string) {
	switch p.FSMState {
	case models.StateReportOpponent:
		return s.SelectReportOpponentByName(ctx, *p.TelegramID, input)
	case models.StateReportScore:
		return s.SetReportScore(ctx, *p.TelegramID, input)
	case models.StateReportScreenshots:
		return "Пожалуйста, отправьте файл скриншота (изображение) или нажмите «Отправить отчет» / «Отмена»:", KbReportPhotos + ":" + strconv.Itoa(len(s.GetReportDraft(*p.TelegramID).PhotoFileIDs))
	}
	return "Используйте меню для управления.", KbNone
}
