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

const (
	KbNone   = "empty"
	KbCancel = "cancel"
	KbRole   = "role"
	KbSkip   = "skip"
)

type TelegramService interface {
	RegisterUser(ctx context.Context, tgID int64, username, firstName string) string
	HandleUserInput(ctx context.Context, tgID int64, input string) (string, string)

	StartSoloRegistration(ctx context.Context, tgID int64) (string, string)
	StartTeamRegistration(ctx context.Context, tgID int64) (string, string)
	StartEditPlayer(ctx context.Context, tgID int64, slot int) (string, string)
	StartReport(ctx context.Context, tgID int64) (string, string)

	DeleteTeam(ctx context.Context, tgID int64) string
	GetTeamInfo(ctx context.Context, tgID int64) string
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
	if input == "Отмена" || input == "/cancel" {
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateIdle))
		return "Действие отменено. Возврат в меню.", KbNone
	}

	player, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if player == nil {
		return "Используйте /start для начала.", KbNone
	}

	if strings.HasPrefix(player.FSMState, "team_reg_") {
		return s.handleTeamLoop(ctx, player, input)
	}
	if strings.HasPrefix(player.FSMState, "edit_player_") {
		return s.handleEditLoop(ctx, player, input)
	}

	switch player.FSMState {
	case models.StateWaitingNickname:
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "game_nickname", input))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateWaitingGameID))
		return "Введите ваш Game ID (цифры):", KbCancel

	case models.StateWaitingGameID:
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "game_id", input))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateWaitingZoneID))
		return "Введите Zone ID (в скобках):", KbCancel

	case models.StateWaitingZoneID:
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "zone_id", input))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateWaitingStars))
		return "Сколько звезд (Rank) в этом сезоне?", KbCancel

	case models.StateWaitingStars:
		stars, _ := strconv.Atoi(input)
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "stars", stars))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateWaitingRole))
		return "Выберите вашу роль:", KbRole

	case models.StateWaitingRole:
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "main_role", input))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateIdle))
		return "Соло-регистрация завершена!", KbNone

	case models.StateWaitingTeamName:
		team, err := s.repo.CreateTeam(ctx, input)
		if err != nil {
			return "Это имя занято, попробуйте другое:", KbCancel
		}
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "team_id", team.ID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "is_captain", true))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, "team_reg_nick_1"))
		return fmt.Sprintf("Команда '%s' создана!\n\n--- Игрок №1 (Капитан) ---\nВведите ваш Ник:", input), KbCancel

	default:
		return "Используйте меню для управления.", KbNone
	}
}

func (s *TelegramServiceImpl) handleTeamLoop(ctx context.Context, captain *models.TelegramPlayer, input string) (string, string) {
	parts := strings.Split(captain.FSMState, "_")
	step := parts[2]
	slot, _ := strconv.Atoi(parts[3])
	teamID := *captain.TeamID
	captainTgID := *captain.TelegramID
	isCapSlot := slot == 1

	if (input == "Пропустить" || input == "/skip") && slot >= 6 && step == "nick" {
		if slot < 7 {
			next := slot + 1
			s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("team_reg_nick_%d", next)))
			return fmt.Sprintf("Игрок №%d пропущен.\n\n--- Игрок №%d (ЗАМЕНА) ---\nВведите Ник:", slot, next), KbSkip
		} else {
			s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, models.StateIdle))
			return "Регистрация завершена! Команда укомплектована.", KbNone
		}
	}

	switch step {
	case "nick":
		if isCapSlot {
			s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, captainTgID, "game_nickname", input))
		} else {
			newP := &models.TelegramPlayer{TeamID: &teamID, GameNickname: input, IsSubstitute: slot >= 6}
			s.logWrite("CreateTeammate", s.repo.CreateTeammate(ctx, newP))
		}
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("team_reg_id_%d", slot)))
		return "Введите Game ID:", KbCancel

	case "id":
		if isCapSlot {
			s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, captainTgID, "game_id", input))
		} else {
			s.logWrite("UpdateLastTeammateData", s.repo.UpdateLastTeammateData(ctx, teamID, "game_id", input))
		}
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("team_reg_zone_%d", slot)))
		return "Введите Zone ID:", KbCancel

	case "zone":
		if isCapSlot {
			s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, captainTgID, "zone_id", input))
		} else {
			s.logWrite("UpdateLastTeammateData", s.repo.UpdateLastTeammateData(ctx, teamID, "zone_id", input))
		}
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("team_reg_rank_%d", slot)))
		return "Кол-во звезд (Rank):", KbCancel

	case "rank":
		stars, _ := strconv.Atoi(input)
		if isCapSlot {
			s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, captainTgID, "stars", stars))
		} else {
			s.logWrite("UpdateLastTeammateData", s.repo.UpdateLastTeammateData(ctx, teamID, "stars", stars))
		}
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("team_reg_role_%d", slot)))
		return "Выберите роль:", KbRole

	case "role":
		if isCapSlot {
			s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, captainTgID, "main_role", input))
		} else {
			s.logWrite("UpdateLastTeammateData", s.repo.UpdateLastTeammateData(ctx, teamID, "main_role", input))
		}
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("team_reg_contact_%d", slot)))
		return "Telegram контакт (например @user или '-'):", KbCancel

	case "contact":
		if isCapSlot {
			s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, captainTgID, "telegram_username", input))
		} else {
			s.logWrite("UpdateLastTeammateData", s.repo.UpdateLastTeammateData(ctx, teamID, "telegram_username", input))
		}

		if slot < 7 {
			next := slot + 1
			s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("team_reg_nick_%d", next)))
			msg := fmt.Sprintf("Игрок %d готов.\n\n--- Игрок №%d ---\nВведите Ник:", slot, next)
			if next >= 6 {
				return msg, KbSkip
			}
			return msg, KbCancel
		}

		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, models.StateIdle))
		return "Регистрация всей команды завершена!", KbNone
	}

	return "Ошибка.", KbNone
}

func (s *TelegramServiceImpl) handleEditLoop(ctx context.Context, captain *models.TelegramPlayer, input string) (string, string) {
	parts := strings.Split(captain.FSMState, "_")
	step := parts[2]
	slot, _ := strconv.Atoi(parts[3])
	members, _ := s.repo.GetTeamMembers(ctx, *captain.TeamID)
	captainTgID := *captain.TelegramID

	if slot > len(members) {
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, models.StateIdle))
		return "Игрок не найден.", KbNone
	}
	targetID := members[slot-1].ID

	switch step {
	case "nick":
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, targetID, "game_nickname", input))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("edit_player_id_%d", slot)))
		return "Ник изменен. Введите Game ID:", KbCancel
	case "id":
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, targetID, "game_id", input))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, fmt.Sprintf("edit_player_role_%d", slot)))
		return "ID изменен. Выберите роль:", KbRole
	case "role":
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, targetID, "main_role", input))
		s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, captainTgID, models.StateIdle))
		return "Данные обновлены!", KbNone
	}
	return "Ошибка.", KbNone
}

func (s *TelegramServiceImpl) StartSoloRegistration(ctx context.Context, tgID int64) (string, string) {
	if !s.IsRegistrationOpen(ctx) {
		return "Регистрация закрыта.", KbNone
	}
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateWaitingNickname))
	return "Начинаем соло-регистрацию. Введите Ник:", KbCancel
}

func (s *TelegramServiceImpl) StartTeamRegistration(ctx context.Context, tgID int64) (string, string) {
	if !s.IsRegistrationOpen(ctx) {
		return "Регистрация закрыта.", KbNone
	}
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateWaitingTeamName))
	return "Введите Название команды:", KbCancel
}

func (s *TelegramServiceImpl) StartEditPlayer(ctx context.Context, tgID int64, slot int) (string, string) {
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, fmt.Sprintf("edit_player_nick_%d", slot)))
	return fmt.Sprintf("Редактируем игрока %d. Введите новый Ник:", slot), KbCancel
}

func (s *TelegramServiceImpl) StartReport(ctx context.Context, tgID int64) (string, string) {
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, models.StateWaitingReport))
	return "Отправьте скриншот результата матча:", KbCancel
}

func (s *TelegramServiceImpl) GetTeamInfo(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return "Вы не в команде."
	}
	team, _ := s.repo.GetTeamByID(ctx, *p.TeamID)
	members, _ := s.repo.GetTeamMembers(ctx, *p.TeamID)

	status := "Не подтверждена"
	if team.IsCheckedIn {
		status = "Подтверждена"
	}

	res := fmt.Sprintf("Команда: %s\nСтатус: %s\n\n", team.Name, status)
	for i, m := range members {
		res += fmt.Sprintf("%d. %s (%s)\n   ID: %s (%s)\n\n", i+1, m.GameNickname, m.MainRole, m.GameID, m.ZoneID)
	}
	return res
}

func (s *TelegramServiceImpl) ToggleCheckIn(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может делать Check-in."
	}
	t, _ := s.repo.GetTeamByID(ctx, *p.TeamID)
	s.logWrite("SetCheckIn", s.repo.SetCheckIn(ctx, t.ID, !t.IsCheckedIn))
	return "Статус Check-in изменен."
}

func (s *TelegramServiceImpl) DeleteTeam(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может удалить команду."
	}
	id := *p.TeamID
	s.logWrite("ResetTeamID", s.repo.ResetTeamID(ctx, id))
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
	s.logWrite("ResetTeamID", s.repo.ResetTeamID(ctx, t.ID))
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
