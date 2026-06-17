package application

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
)

const (
	KbNone   = "empty"
	KbCancel = "cancel"
	KbRole   = "role"
	KbSkip   = "skip"
)

type TelegramService interface {
	RegisterUser(tgID int64, username, firstName string) string
	HandleUserInput(tgID int64, input string) (string, string)

	StartSoloRegistration(tgID int64) (string, string)
	StartTeamRegistration(tgID int64) (string, string)
	StartEditPlayer(tgID int64, slot int) (string, string)
	StartReport(tgID int64) (string, string)

	DeleteTeam(tgID int64) string
	GetTeamInfo(tgID int64) string
	ToggleCheckIn(tgID int64) string

	SetRegistrationOpen(isOpen bool)
	IsRegistrationOpen() bool
	GenerateTeamsCSV() ([]byte, error)
	GetBroadcastList() ([]int64, error)
	AdminDeleteTeam(teamName string) string
	AdminResetUser(tgID int64) string
	HandleReport(tgID int64, photoFileID, caption string) string

	SetTournamentTime(t time.Time)
	GetTournamentTime() time.Time
	GetUncheckedTeams() ([]models.TelegramTeam, error)

	GetTeamsList() string
	AdminGetTeamDetails(name string) string

	GenerateSoloPlayersCSV() ([]byte, error)
	GetSoloPlayersList() string
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

func (s *TelegramServiceImpl) RegisterUser(tgID int64, username, firstName string) string {
	p := &models.TelegramPlayer{TelegramID: &tgID, TelegramUsername: username, FirstName: firstName}
	s.repo.CreateOrUpdatePlayer(p)
	return fmt.Sprintf("Привет, %s!", firstName)
}

func (s *TelegramServiceImpl) HandleUserInput(tgID int64, input string) (string, string) {
	if input == "Отмена" || input == "/cancel" {
		s.repo.UpdatePlayerState(tgID, models.StateIdle)
		return "Действие отменено. Возврат в меню.", KbNone
	}

	player, _ := s.repo.GetPlayerByTelegramID(tgID)
	if player == nil {
		return "Используйте /start для начала.", KbNone
	}

	if strings.HasPrefix(player.FSMState, "team_reg_") {
		return s.handleTeamLoop(player, input)
	}
	if strings.HasPrefix(player.FSMState, "edit_player_") {
		return s.handleEditLoop(player, input)
	}

	switch player.FSMState {
	case models.StateWaitingNickname:
		s.repo.UpdatePlayerField(tgID, "game_nickname", input)
		s.repo.UpdatePlayerState(tgID, models.StateWaitingGameID)
		return "Введите ваш Game ID (цифры):", KbCancel

	case models.StateWaitingGameID:
		s.repo.UpdatePlayerField(tgID, "game_id", input)
		s.repo.UpdatePlayerState(tgID, models.StateWaitingZoneID)
		return "Введите Zone ID (в скобках):", KbCancel

	case models.StateWaitingZoneID:
		s.repo.UpdatePlayerField(tgID, "zone_id", input)
		s.repo.UpdatePlayerState(tgID, models.StateWaitingStars)
		return "Сколько звезд (Rank) в этом сезоне?", KbCancel

	case models.StateWaitingStars:
		stars, _ := strconv.Atoi(input)
		s.repo.UpdatePlayerField(tgID, "stars", stars)
		s.repo.UpdatePlayerState(tgID, models.StateWaitingRole)
		return "Выберите вашу роль:", KbRole

	case models.StateWaitingRole:
		s.repo.UpdatePlayerField(tgID, "main_role", input)
		s.repo.UpdatePlayerState(tgID, models.StateIdle)
		return "Соло-регистрация завершена!", KbNone

	case models.StateWaitingTeamName:
		team, err := s.repo.CreateTeam(input)
		if err != nil {
			return "Это имя занято, попробуйте другое:", KbCancel
		}
		s.repo.UpdatePlayerField(tgID, "team_id", team.ID)
		s.repo.UpdatePlayerField(tgID, "is_captain", true)
		s.repo.UpdatePlayerState(tgID, "team_reg_nick_1")
		return fmt.Sprintf("Команда '%s' создана!\n\n--- Игрок №1 (Капитан) ---\nВведите ваш Ник:", input), KbCancel

	default:
		return "Используйте меню для управления.", KbNone
	}
}

func (s *TelegramServiceImpl) handleTeamLoop(captain *models.TelegramPlayer, input string) (string, string) {
	parts := strings.Split(captain.FSMState, "_")
	step := parts[2]
	slot, _ := strconv.Atoi(parts[3])
	teamID := *captain.TeamID
	captainTgID := *captain.TelegramID
	isCapSlot := slot == 1

	if (input == "Пропустить" || input == "/skip") && slot >= 6 && step == "nick" {
		if slot < 7 {
			next := slot + 1
			s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("team_reg_nick_%d", next))
			return fmt.Sprintf("Игрок №%d пропущен.\n\n--- Игрок №%d (ЗАМЕНА) ---\nВведите Ник:", slot, next), KbSkip
		} else {
			s.repo.UpdatePlayerState(captainTgID, models.StateIdle)
			return "Регистрация завершена! Команда укомплектована.", KbNone
		}
	}

	switch step {
	case "nick":
		if isCapSlot {
			s.repo.UpdatePlayerField(captainTgID, "game_nickname", input)
		} else {
			newP := &models.TelegramPlayer{TeamID: &teamID, GameNickname: input, IsSubstitute: slot >= 6}
			s.repo.CreateTeammate(newP)
		}
		s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("team_reg_id_%d", slot))
		return "Введите Game ID:", KbCancel

	case "id":
		if isCapSlot {
			s.repo.UpdatePlayerField(captainTgID, "game_id", input)
		} else {
			s.repo.UpdateLastTeammateData(teamID, "game_id", input)
		}
		s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("team_reg_zone_%d", slot))
		return "Введите Zone ID:", KbCancel

	case "zone":
		if isCapSlot {
			s.repo.UpdatePlayerField(captainTgID, "zone_id", input)
		} else {
			s.repo.UpdateLastTeammateData(teamID, "zone_id", input)
		}
		s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("team_reg_rank_%d", slot))
		return "Кол-во звезд (Rank):", KbCancel

	case "rank":
		stars, _ := strconv.Atoi(input)
		if isCapSlot {
			s.repo.UpdatePlayerField(captainTgID, "stars", stars)
		} else {
			s.repo.UpdateLastTeammateData(teamID, "stars", stars)
		}
		s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("team_reg_role_%d", slot))
		return "Выберите роль:", KbRole

	case "role":
		if isCapSlot {
			s.repo.UpdatePlayerField(captainTgID, "main_role", input)
		} else {
			s.repo.UpdateLastTeammateData(teamID, "main_role", input)
		}
		s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("team_reg_contact_%d", slot))
		return "Telegram контакт (например @user или '-'):", KbCancel

	case "contact":
		if isCapSlot {
			s.repo.UpdatePlayerField(captainTgID, "telegram_username", input)
		} else {
			s.repo.UpdateLastTeammateData(teamID, "telegram_username", input)
		}

		if slot < 7 {
			next := slot + 1
			s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("team_reg_nick_%d", next))
			msg := fmt.Sprintf("Игрок %d готов.\n\n--- Игрок №%d ---\nВведите Ник:", slot, next)
			if next >= 6 {
				return msg, KbSkip
			}
			return msg, KbCancel
		}

		s.repo.UpdatePlayerState(captainTgID, models.StateIdle)
		return "Регистрация всей команды завершена!", KbNone
	}

	return "Ошибка.", KbNone
}

func (s *TelegramServiceImpl) handleEditLoop(captain *models.TelegramPlayer, input string) (string, string) {
	parts := strings.Split(captain.FSMState, "_")
	step := parts[2]
	slot, _ := strconv.Atoi(parts[3])
	members, _ := s.repo.GetTeamMembers(*captain.TeamID)
	captainTgID := *captain.TelegramID

	if slot > len(members) {
		s.repo.UpdatePlayerState(captainTgID, models.StateIdle)
		return "Игрок не найден.", KbNone
	}
	targetID := members[slot-1].ID

	switch step {
	case "nick":
		s.repo.UpdatePlayerFieldByID(targetID, "game_nickname", input)
		s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("edit_player_id_%d", slot))
		return "Ник изменен. Введите Game ID:", KbCancel
	case "id":
		s.repo.UpdatePlayerFieldByID(targetID, "game_id", input)
		s.repo.UpdatePlayerState(captainTgID, fmt.Sprintf("edit_player_role_%d", slot))
		return "ID изменен. Выберите роль:", KbRole
	case "role":
		s.repo.UpdatePlayerFieldByID(targetID, "main_role", input)
		s.repo.UpdatePlayerState(captainTgID, models.StateIdle)
		return "Данные обновлены!", KbNone
	}
	return "Ошибка.", KbNone
}

func (s *TelegramServiceImpl) StartSoloRegistration(tgID int64) (string, string) {
	if !s.IsRegistrationOpen() {
		return "Регистрация закрыта.", KbNone
	}
	s.repo.UpdatePlayerState(tgID, models.StateWaitingNickname)
	return "Начинаем соло-регистрацию. Введите Ник:", KbCancel
}

func (s *TelegramServiceImpl) StartTeamRegistration(tgID int64) (string, string) {
	if !s.IsRegistrationOpen() {
		return "Регистрация закрыта.", KbNone
	}
	s.repo.UpdatePlayerState(tgID, models.StateWaitingTeamName)
	return "Введите Название команды:", KbCancel
}

func (s *TelegramServiceImpl) StartEditPlayer(tgID int64, slot int) (string, string) {
	s.repo.UpdatePlayerState(tgID, fmt.Sprintf("edit_player_nick_%d", slot))
	return fmt.Sprintf("Редактируем игрока %d. Введите новый Ник:", slot), KbCancel
}

func (s *TelegramServiceImpl) StartReport(tgID int64) (string, string) {
	s.repo.UpdatePlayerState(tgID, models.StateWaitingReport)
	return "Отправьте скриншот результата матча:", KbCancel
}

func (s *TelegramServiceImpl) GetTeamInfo(tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(tgID)
	if p == nil || p.TeamID == nil {
		return "Вы не в команде."
	}
	team, _ := s.repo.GetTeamByID(*p.TeamID)
	members, _ := s.repo.GetTeamMembers(*p.TeamID)

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

func (s *TelegramServiceImpl) ToggleCheckIn(tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может делать Check-in."
	}
	t, _ := s.repo.GetTeamByID(*p.TeamID)
	s.repo.SetCheckIn(t.ID, !t.IsCheckedIn)
	return "Статус Check-in изменен."
}

func (s *TelegramServiceImpl) DeleteTeam(tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может удалить команду."
	}
	id := *p.TeamID
	s.repo.ResetTeamID(id)
	s.repo.DeleteTeam(id)
	return "Команда удалена."
}

func (s *TelegramServiceImpl) SetRegistrationOpen(isOpen bool) {
	val := "false"
	if isOpen {
		val = "true"
	}
	s.repo.SetSetting("registration_open", val)
}

func (s *TelegramServiceImpl) IsRegistrationOpen() bool {
	val, _ := s.repo.GetSetting("registration_open")
	return val != "false"
}

func (s *TelegramServiceImpl) AdminDeleteTeam(name string) string {
	t, err := s.repo.GetTeamByName(name)
	if err != nil {
		return "Не найдена."
	}
	s.repo.ResetTeamID(t.ID)
	s.repo.DeleteTeam(t.ID)
	return "Удалена."
}

func (s *TelegramServiceImpl) AdminResetUser(id int64) string {
	s.repo.UpdatePlayerState(id, models.StateIdle)
	return "Сброшен."
}

func (s *TelegramServiceImpl) GetBroadcastList() ([]int64, error) {
	caps, err := s.repo.GetAllCaptains()
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

func (s *TelegramServiceImpl) GenerateTeamsCSV() ([]byte, error) {
	teams, err := s.repo.GetAllTeams()
	if err != nil {
		return nil, err
	}
	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	w.Write([]string{"Team", "CheckIn", "Nick", "ID", "Zone", "Role"})
	for _, t := range teams {
		for _, m := range t.Players {
			w.Write([]string{t.Name, strconv.FormatBool(t.IsCheckedIn), m.GameNickname, m.GameID, m.ZoneID, m.MainRole})
		}
	}
	w.Flush()
	return b.Bytes(), nil
}

func (s *TelegramServiceImpl) HandleReport(tgID int64, fileID, caption string) string {
	p, _ := s.repo.GetPlayerByTelegramID(tgID)
	if p == nil || p.FSMState != models.StateWaitingReport {
		return "Используйте /report"
	}
	if p.TeamID == nil {
		s.repo.UpdatePlayerState(tgID, models.StateIdle)
		return "Вы не в команде."
	}
	t, _ := s.repo.GetTeamByID(*p.TeamID)
	s.repo.UpdatePlayerState(tgID, models.StateIdle)
	return fmt.Sprintf("ADMIN_REPORT:%s:Команда: %s\nКапитан: @%s\nИнфо: %s", fileID, t.Name, p.TelegramUsername, caption)
}

func (s *TelegramServiceImpl) SetTournamentTime(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tournamentTime = t
	s.repo.SetSetting("tournament_time", t.Format(time.RFC3339))
}

func (s *TelegramServiceImpl) GetTournamentTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tournamentTime.IsZero() {
		return s.tournamentTime
	}
	val, _ := s.repo.GetSetting("tournament_time")
	if val != "" {
		t, _ := time.Parse(time.RFC3339, val)
		return t
	}
	return time.Time{}
}

func (s *TelegramServiceImpl) GetUncheckedTeams() ([]models.TelegramTeam, error) {
	allTeams, err := s.repo.GetAllTeams()
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

func (s *TelegramServiceImpl) GetTeamsList() string {
	teams, err := s.repo.GetAllTeams()
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

func (s *TelegramServiceImpl) AdminGetTeamDetails(name string) string {
	team, err := s.repo.GetTeamByName(name)
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

func (s *TelegramServiceImpl) GenerateSoloPlayersCSV() ([]byte, error) {
	players, err := s.repo.GetSoloPlayers()
	if err != nil {
		return nil, err
	}

	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	w.Write([]string{"TG Username", "Nickname", "Game ID", "Zone ID", "Stars", "Role", "First Name"})

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
		w.Write(record)
	}
	w.Flush()
	return b.Bytes(), nil
}

func (s *TelegramServiceImpl) GetSoloPlayersList() string {
	players, err := s.repo.GetSoloPlayers()
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
