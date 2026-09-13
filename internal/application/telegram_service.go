package application

import (
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const KbNone = "empty"

const (
	// maxTeamNameLen mirrors telegram_teams.name VARCHAR(64).
	maxTeamNameLen = 64
	// Roster: slot 1 is the captain, 2–5 the main five, 6–7 optional substitutes.
	firstSubstituteSlot = 6
	maxTeamSlots        = 7
)

type TelegramService interface {
	RegisterUser(ctx context.Context, tgID int64, username, firstName string) string
	HandleUserInput(ctx context.Context, tgID int64, input string) (string, string)
	HandleRegAction(ctx context.Context, tgID int64, action, arg string) (string, string)

	StartSoloRegistration(ctx context.Context, tgID int64) (string, string)
	StartTeamRegistration(ctx context.Context, tgID int64) (string, string)
	StartEditPlayer(ctx context.Context, tgID int64, slot int) (string, string)
	StartReport(ctx context.Context, tgID int64) (string, string)

	DeleteTeam(ctx context.Context, tgID int64) string
	GetTeamInfo(ctx context.Context, tgID int64) (string, string)
	ToggleCheckIn(ctx context.Context, tgID int64) string

	SetRegistrationOpen(ctx context.Context, isOpen bool)
	IsRegistrationOpen(ctx context.Context) bool
	GenerateTeamsCSV(ctx context.Context) ([]byte, error)
	GetBroadcastList(ctx context.Context) ([]int64, error)
	AdminDeleteTeam(ctx context.Context, teamName string) string
	AdminResetUser(ctx context.Context, tgID int64) string
	HandleReport(ctx context.Context, tgID int64, photoFileID, caption string) string

	SetTournamentTime(ctx context.Context, t time.Time)
	GetTournamentTime(ctx context.Context) time.Time
	GetUncheckedTeams(ctx context.Context) ([]models.TelegramTeam, error)

	GetTeamsList(ctx context.Context) string
	AdminGetTeamDetails(ctx context.Context, name string) string

	GenerateSoloPlayersCSV(ctx context.Context) ([]byte, error)
	GetSoloPlayersList(ctx context.Context) string
}

type TelegramServiceImpl struct {
	repo           repository.Telegram
	mu             sync.RWMutex
	tournamentTime time.Time
	logger         Logger
}

func NewTelegramServiceImpl(repo repository.Telegram, logger Logger) *TelegramServiceImpl {
	return &TelegramServiceImpl{
		repo:   repo,
		logger: logger,
	}
}

// logWrite reports a failed repository write.
//
// The registration flow is a state machine persisted in the database, and every
// write here used to discard its error: a failed save advanced the user to the
// next question anyway, so their answer was lost with no trace. The flow still
// continues — interrupting a half-finished registration would be worse — but the
// failure is now visible.
func (s *TelegramServiceImpl) logWrite(op string, err error) {
	if err != nil {
		s.logger.Error("telegram: %s failed: %v", op, err)
	}
}

func (s *TelegramServiceImpl) RegisterUser(ctx context.Context, tgID int64, username, firstName string) string {
	p := &models.TelegramPlayer{TelegramID: &tgID, TelegramUsername: username, FirstName: firstName}
	s.logWrite("CreateOrUpdatePlayer", s.repo.CreateOrUpdatePlayer(ctx, p))
	return fmt.Sprintf("Привет, %s!", firstName)
}

func (s *TelegramServiceImpl) HandleUserInput(ctx context.Context, tgID int64, input string) (string, string) {
	player, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if player == nil {
		return "Используйте /start для начала.", KbNone
	}

	if input == "Отмена" || input == "/cancel" {
		if isRegistrationState(player.FSMState) {
			return s.HandleRegAction(ctx, tgID, "cancel", "")
		}
		s.setState(ctx, tgID, models.StateIdle)
		return "Действие отменено. Возврат в меню.", KbNone
	}

	if isLegacyState(player.FSMState) {
		s.setState(ctx, tgID, models.StateIdle)
		return msgLegacyReset, KbNone
	}
	if isRegistrationState(player.FSMState) {
		return s.handleRegistrationText(ctx, player, input)
	}
	return "Используйте меню для управления.", KbNone
}

func (s *TelegramServiceImpl) StartSoloRegistration(ctx context.Context, tgID int64) (string, string) {
	if !s.IsRegistrationOpen(ctx) {
		return "Регистрация закрыта.", KbNone
	}
	if name := s.currentTeamName(ctx, tgID); name != "" {
		return fmt.Sprintf("Вы уже в команде '%s'. Соло-регистрация недоступна, пока вы в команде (/delete_team).", name), KbNone
	}
	s.setState(ctx, tgID, models.StateSoloLine)
	return soloLinePrompt, KbRegCancel
}

func (s *TelegramServiceImpl) StartTeamRegistration(ctx context.Context, tgID int64) (string, string) {
	if !s.IsRegistrationOpen(ctx) {
		return "Регистрация закрыта.", KbNone
	}
	// A second /reg_team used to create a fresh team and repoint the captain,
	// leaving the old roster orphaned.
	if name := s.currentTeamName(ctx, tgID); name != "" {
		return fmt.Sprintf("Вы уже в команде '%s'. Чтобы зарегистрировать новую, сначала удалите её: /delete_team", name), KbNone
	}
	s.setState(ctx, tgID, models.StateWaitingTeamName)
	return "Введите Название команды:", KbRegCancel
}

func (s *TelegramServiceImpl) StartEditPlayer(ctx context.Context, tgID int64, slot int) (string, string) {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return "Вы не в команде.", KbNone
	}
	if !p.IsCaptain {
		return "Только капитан может редактировать состав.", KbNone
	}
	if p.FSMState != models.StateIdle {
		return "Сначала завершите текущее действие или нажмите Отмена.", KbNone
	}
	members := s.roster(ctx, *p.TeamID)
	if slot < 1 || slot > len(members) {
		return fmt.Sprintf("Игрок №%d не найден. В команде %d игрок(ов), см. /my_team", slot, len(members)), KbNone
	}
	// /edit_player N is the typed form of the ✏️ N button on the /my_team card.
	return s.HandleRegAction(ctx, tgID, "fix", strconv.Itoa(slot))
}

func (s *TelegramServiceImpl) StartReport(ctx context.Context, tgID int64) (string, string) {
	s.setState(ctx, tgID, models.StateWaitingReport)
	return "Отправьте скриншот результата матча:", KbRegCancel
}

func (s *TelegramServiceImpl) GetTeamInfo(ctx context.Context, tgID int64) (string, string) {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return "Вы не в команде.", KbNone
	}
	return s.teamCard(ctx, p)
}

func (s *TelegramServiceImpl) ToggleCheckIn(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может делать Check-in."
	}
	t, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || t == nil {
		return "Команда не найдена."
	}
	checkedIn := !t.IsCheckedIn
	if err := s.repo.SetCheckIn(ctx, t.ID, checkedIn); err != nil {
		s.logWrite("SetCheckIn", err)
		return "Не удалось изменить статус. Попробуйте ещё раз."
	}
	// It is a toggle, so the reply must say which way it went: a captain who
	// tapped twice used to un-check silently and collect a technical defeat.
	if checkedIn {
		return fmt.Sprintf("✅ Check-in подтверждён. Команда '%s' участвует в турнире.", t.Name)
	}
	return fmt.Sprintf("⚪ Check-in снят. Команда '%s' НЕ подтверждена — нажмите /checkin ещё раз, чтобы подтвердить.", t.Name)
}

func (s *TelegramServiceImpl) DeleteTeam(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может удалить команду."
	}
	id := *p.TeamID
	s.logWrite("ReleaseTeamMembers", s.repo.ReleaseTeamMembers(ctx, id))
	s.logWrite("DeleteTeam", s.repo.DeleteTeam(ctx, id))
	return "Команда удалена."
}

func (s *TelegramServiceImpl) SetRegistrationOpen(ctx context.Context, isOpen bool) {
	val := "false"
	if isOpen {
		val = "true"
	}
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, "registration_open", val))
}

func (s *TelegramServiceImpl) IsRegistrationOpen(ctx context.Context) bool {
	val, _ := s.repo.GetSetting(ctx, "registration_open")
	return val != "false"
}

func (s *TelegramServiceImpl) AdminDeleteTeam(ctx context.Context, name string) string {
	t, err := s.repo.GetTeamByName(ctx, name)
	if err != nil {
		return "Не найдена."
	}
	s.logWrite("ReleaseTeamMembers", s.repo.ReleaseTeamMembers(ctx, t.ID))
	s.logWrite("DeleteTeam", s.repo.DeleteTeam(ctx, t.ID))
	return "Удалена."
}

func (s *TelegramServiceImpl) AdminResetUser(ctx context.Context, id int64) string {
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, id, models.StateIdle))
	return "Сброшен."
}

func (s *TelegramServiceImpl) GetBroadcastList(ctx context.Context) ([]int64, error) {
	caps, err := s.repo.GetAllCaptains(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, c := range caps {
		if c.TelegramID != nil {
			ids = append(ids, *c.TelegramID)
		}
	}
	return ids, nil
}

func (s *TelegramServiceImpl) GenerateTeamsCSV(ctx context.Context) ([]byte, error) {
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	_ = w.Write([]string{"Team", "CheckIn", "Nick", "ID", "Zone", "Role"})
	for _, t := range teams {
		for _, m := range t.Players {
			_ = w.Write([]string{t.Name, strconv.FormatBool(t.IsCheckedIn), m.GameNickname, m.GameID, m.ZoneID, m.MainRole})
		}
	}
	w.Flush()
	// csv.Writer buffers errors until Flush; without this check a failed export
	// came back as a silently truncated file.
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("failed to write teams CSV: %w", err)
	}
	return b.Bytes(), nil
}

func (s *TelegramServiceImpl) HandleReport(ctx context.Context, tgID int64, fileID, caption string) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.FSMState != models.StateWaitingReport {
		return "Используйте /report"
	}
	if p.TeamID == nil {
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateIdle))
		return "Вы не в команде."
	}
	t, _ := s.repo.GetTeamByID(ctx, *p.TeamID)
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateIdle))
	return fmt.Sprintf("ADMIN_REPORT:%s:Команда: %s\nКапитан: @%s\nИнфо: %s", fileID, t.Name, p.TelegramUsername, caption)
}

func (s *TelegramServiceImpl) SetTournamentTime(ctx context.Context, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tournamentTime = t
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, "tournament_time", t.Format(time.RFC3339)))
}

func (s *TelegramServiceImpl) GetTournamentTime(ctx context.Context) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tournamentTime.IsZero() {
		return s.tournamentTime
	}
	val, _ := s.repo.GetSetting(ctx, "tournament_time")
	if val != "" {
		t, _ := time.Parse(time.RFC3339, val)
		return t
	}
	return time.Time{}
}

func (s *TelegramServiceImpl) GetUncheckedTeams(ctx context.Context) ([]models.TelegramTeam, error) {
	allTeams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	var unchecked []models.TelegramTeam
	for _, t := range allTeams {
		if !t.IsCheckedIn {
			unchecked = append(unchecked, t)
		}
	}
	return unchecked, nil
}

func (s *TelegramServiceImpl) GetTeamsList(ctx context.Context) string {
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return "Ошибка при получении списка команд."
	}
	if len(teams) == 0 {
		return "Команд пока нет."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Список команд (%d):\n\n", len(teams)))
	for i, t := range teams {
		check := "⚪"
		if t.IsCheckedIn {
			check = "✅"
		}
		sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, check, t.Name))
	}
	return sb.String()
}

func (s *TelegramServiceImpl) AdminGetTeamDetails(ctx context.Context, name string) string {
	team, err := s.repo.GetTeamByName(ctx, name)
	if err != nil {
		return fmt.Sprintf("Команда '%s' не найдена.", name)
	}

	status := "Не подтверждена"
	if team.IsCheckedIn {
		status = "Подтверждена"
	}

	res := fmt.Sprintf("Команда: %s\nСтатус: %s\nID команды: %d\n\n", team.Name, status, team.ID)
	for i, m := range team.Players {
		role := "Основа"
		if m.IsSubstitute {
			role = "Замена"
		}
		res += fmt.Sprintf("%d. %s [%s]\n   ID: %s (%s)\n   TG: %s\n\n", i+1, m.GameNickname, role, m.GameID, m.ZoneID, m.TelegramUsername)
	}
	return res
}

func (s *TelegramServiceImpl) GenerateSoloPlayersCSV(ctx context.Context) ([]byte, error) {
	players, err := s.repo.GetSoloPlayers(ctx)
	if err != nil {
		return nil, err
	}

	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	_ = w.Write([]string{"TG Username", "Nickname", "Game ID", "Zone ID", "Stars", "Role", "First Name"})

	for _, p := range players {
		record := []string{
			p.TelegramUsername,
			p.GameNickname,
			p.GameID,
			p.ZoneID,
			fmt.Sprintf("%d", p.Stars),
			p.MainRole,
			p.FirstName,
		}
		_ = w.Write(record)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("failed to write solo players CSV: %w", err)
	}
	return b.Bytes(), nil
}

func (s *TelegramServiceImpl) GetSoloPlayersList(ctx context.Context) string {
	players, err := s.repo.GetSoloPlayers(ctx)
	if err != nil || len(players) == 0 {
		return "Соло-игроков пока нет."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Соло-игроки (%d):\n\n", len(players)))
	for i, p := range players {
		sb.WriteString(fmt.Sprintf("%d. %s (@%s) — %s\n", i+1, p.GameNickname, p.TelegramUsername, p.MainRole))
	}
	return sb.String()
}

// currentTeamName returns the name of the team the user belongs to, or "".
func (s *TelegramServiceImpl) currentTeamName(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return ""
	}
	t, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || t == nil {
		return ""
	}
	return t.Name
}

// parseStars accepts a non-negative integer star count. Anything else used to
// be stored as 0 without a word to the user.
func parseStars(input string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
