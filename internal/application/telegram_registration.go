package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Keyboard types for the registration flow. Delivery renders them as inline
// keyboards; the ones with a suffix carry rendering parameters after ":".
const (
	KbRegCancel        = "reg_cancel"
	KbRegRoles         = "reg_roles"
	KbRegSkip          = "reg_skip"
	KbRegConfirm       = "reg_confirm"        // "reg_confirm:<n>": 1..n, confirm, delete
	KbRegCard          = "reg_card"           // "reg_card:<n>:<add>": 1..n, delete, add when add != ""
	KbRegSoloConfirm   = "reg_solo_confirm"   // confirm, fix
	KbRegCheckin       = "reg_checkin"        // confirm participation (in the reminder)
	KbRegDeleteConfirm = "reg_delete_confirm" // delete confirm / cancel
	KbRegPrefill       = "reg_prefill"        // take / retype / cancel
	KbRegSubFix        = "reg_sub_fix"        // delete sub / cancel
	KbRegProfile       = "reg_profile"        // edit in tg / link discord
)

const playerLineFormat = "Формат:\nНик\nGameID (ZoneID)\nЗвёзды"

// registrationRoles is the whitelist for reg:role:<x>. Callback data is
// client-supplied, so anything else is refused.
var registrationRoles = []string{"Gold", "Exp", "Mid", "Roam", "Jungle"}

func validRole(s string) bool {
	for _, r := range registrationRoles {
		if r == s {
			return true
		}
	}
	return false
}

const (
	msgStaleButton = "Эта кнопка больше не действует."
	msgLegacyReset = "Регистрация обновилась, старый ввод сброшен. Начните заново: /reg_team"
	msgPickRole    = "Выберите роль кнопкой:"
)

// isRegistrationState reports whether the state belongs to the new flow.
func isRegistrationState(st string) bool {
	return st == models.StateWaitingTeamName || strings.HasPrefix(st, "team_") || strings.HasPrefix(st, "solo_") || strings.HasPrefix(st, "profile_")
}

// isLegacyState matches states from the pre-inline flow that may still sit
// in the database for people who were mid-registration at deploy time.
func isLegacyState(st string) bool {
	switch st {
	case "waiting_nickname", "waiting_game_id", "waiting_zone_id", "waiting_stars", "waiting_role":
		return true
	}
	return strings.HasPrefix(st, "team_reg_") || strings.HasPrefix(st, "edit_player_")
}

// slotState splits "team_line_3" into ("team_line_", 3).
func slotState(st string) (prefix string, slot int, ok bool) {
	for _, p := range []string{
		models.StateTeamLinePrefix, models.StateTeamRolePrefix,
		models.StateTeamFixPrefix, models.StateTeamFixRolePrefix,
		models.StateTeamEditPrefix, models.StateTeamEditRolePrefix,
	} {
		if strings.HasPrefix(st, p) {
			n, err := strconv.Atoi(strings.TrimPrefix(st, p))
			if err != nil || n < 1 || n > maxTeamSlots {
				return "", 0, false
			}
			return p, n, true
		}
	}
	return "", 0, false
}

func roleStateFor(linePrefix string) string {
	switch linePrefix {
	case models.StateTeamFixPrefix:
		return models.StateTeamFixRolePrefix
	case models.StateTeamEditPrefix:
		return models.StateTeamEditRolePrefix
	}
	return models.StateTeamRolePrefix
}

func (s *TelegramServiceImpl) setState(ctx context.Context, tgID int64, st string) {
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, st))
}

// ---- prompts ---------------------------------------------------------------

func slotLabel(slot int) string {
	switch {
	case slot == 1:
		return fmt.Sprintf("Игрок %d/%d (капитан)", slot, mainRosterSlots)
	case slot >= firstSubstituteSlot:
		subIndex := slot - firstSubstituteSlot + 1
		maxSubs := maxTeamSlots - firstSubstituteSlot + 1
		return fmt.Sprintf("Замена %d/%d", subIndex, maxSubs)
	default:
		return fmt.Sprintf("Игрок %d/%d", slot, mainRosterSlots)
	}
}

// linePrompt asks for slot N's line. During initial registration substitutes
// get a skip button; everywhere else the only button is cancel.
func linePrompt(slot int, initial bool) (string, string) {
	var sb strings.Builder
	sb.WriteString(slotLabel(slot))
	sb.WriteString("\n\nОтправьте данные игрока:\nНик\nGameID (ZoneID)\nЗвёзды")
	if slot != 1 {
		sb.WriteString("\n@telegram (необязательно)")
	}
	sb.WriteString("\n\nПример:\nKitayes\n5374343843 (6732)\n25")
	if slot != 1 {
		sb.WriteString("\n@username")
	}
	msg := sb.String()
	if initial && slot >= firstSubstituteSlot {
		return msg, KbRegSkip
	}
	return msg, KbRegCancel
}

const soloLinePrompt = "Отправьте данные игрока:\nНик\nGameID (ZoneID)\nЗвёзды\n\nПример:\nKitayes\n5374343843 (6732)\n25"

func rolePrompt(line playerLine) string {
	var sb strings.Builder
	for _, w := range plausibilityWarnings(line) {
		sb.WriteString(w)
		sb.WriteString("\n")
	}
	if sb.Len() > 0 {
		sb.WriteString("Если ошибка — «Исправить строку».\n\n")
	}
	fmt.Fprintf(&sb, "%s · %s (%s) · %d зв.\nРоль?", line.Nick, line.GameID, line.ZoneID, line.Stars)
	return sb.String()
}

func rolePromptFromRow(p *models.TelegramPlayer) string {
	return rolePrompt(playerLine{Nick: p.GameNickname, GameID: p.GameID, ZoneID: p.ZoneID, Stars: p.Stars})
}

// ---- roster helpers --------------------------------------------------------

// roster returns team members with the captain first; slot N is index N-1.
func (s *TelegramServiceImpl) roster(ctx context.Context, teamID int) []models.TelegramPlayer {
	members, _ := s.repo.GetTeamMembers(ctx, teamID)
	for i, m := range members {
		if m.IsCaptain && i != 0 {
			members[0], members[i] = members[i], members[0]
			break
		}
	}
	return members
}

// slotRowID is the id of the row slot N currently maps to, or 0 for a slot
// that has no row yet.
func (s *TelegramServiceImpl) slotRowID(ctx context.Context, captain *models.TelegramPlayer, slot int) int {
	if slot == 1 {
		return captain.ID
	}
	members := s.roster(ctx, *captain.TeamID)
	if slot <= len(members) {
		return members[slot-1].ID
	}
	return 0
}

// duplicateGameID refuses an in-game id that is already on another team's
// roster. Solo rows are not a conflict: people sign up solo first and join
// a team later. excludeID is the row being (re)written, so fixing a slot
// with its own id passes.
func (s *TelegramServiceImpl) duplicateGameID(ctx context.Context, gameID string, excludeID int) string {
	rows, err := s.repo.FindByGameID(ctx, gameID)
	if err != nil {
		s.logger.Error("telegram: FindByGameID failed: %v", err)
		return ""
	}
	for _, r := range rows {
		if r.ID == excludeID || r.TeamID == nil {
			continue
		}
		team, err := s.repo.GetTeamByID(ctx, *r.TeamID)
		if err != nil || team == nil {
			continue
		}
		return fmt.Sprintf("GameID %s уже заявлен в команде '%s' (%s). Один игрок — одна команда.", gameID, team.Name, r.GameNickname)
	}
	return ""
}

// saveLine writes a parsed line into slot N: the captain's own row for slot
// 1, an existing roster row when the slot is taken, a new row otherwise.
func (s *TelegramServiceImpl) saveLine(ctx context.Context, captain *models.TelegramPlayer, slot int, line playerLine) {
	teamID := *captain.TeamID
	tg := *captain.TelegramID
	if slot == 1 {
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_nickname", line.Nick))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_id", line.GameID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "zone_id", line.ZoneID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "stars", line.Stars))
		return
	}
	members := s.roster(ctx, teamID)
	if slot <= len(members) {
		id := members[slot-1].ID
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "game_nickname", line.Nick))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "game_id", line.GameID))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "zone_id", line.ZoneID))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "stars", line.Stars))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "telegram_username", line.Contact))
		return
	}
	s.logWrite("CreateTeammate", s.repo.CreateTeammate(ctx, &models.TelegramPlayer{
		TeamID:           &teamID,
		GameNickname:     line.Nick,
		GameID:           line.GameID,
		ZoneID:           line.ZoneID,
		Stars:            line.Stars,
		TelegramUsername: line.Contact,
		IsSubstitute:     slot >= firstSubstituteSlot,
	}))
}

func (s *TelegramServiceImpl) saveRole(ctx context.Context, captain *models.TelegramPlayer, slot int, role string) {
	if slot == 1 {
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, *captain.TelegramID, "main_role", role))
		return
	}
	members := s.roster(ctx, *captain.TeamID)
	if slot <= len(members) {
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, members[slot-1].ID, "main_role", role))
	}
}

// slotSummary is the "Nick — Role" form used in the role acknowledgement.
func (s *TelegramServiceImpl) slotSummary(ctx context.Context, captain *models.TelegramPlayer, slot int) string {
	members := s.roster(ctx, *captain.TeamID)
	if slot < 1 || slot > len(members) {
		return ""
	}
	m := members[slot-1]
	return fmt.Sprintf("%s — %s", m.GameNickname, m.MainRole)
}

// ---- cards -----------------------------------------------------------------

func renderSlot(m models.TelegramPlayer) string {
	role := m.MainRole
	if role == "" {
		role = "роль не указана"
	}
	return fmt.Sprintf("%s · %s · %s (%s) · %d зв.", m.GameNickname, role, m.GameID, m.ZoneID, m.Stars)
}

func renderTeamCard(team *models.TelegramTeam, members []models.TelegramPlayer) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Команда '%s'\n", team.Name)
	for i, m := range members {
		prefix := ""
		switch {
		case i == 0:
			prefix = "[Капитан] "
		case m.IsSubstitute:
			prefix = "[Замена] "
		}
		fmt.Fprintf(&sb, "%d. %s%s", i+1, prefix, renderSlot(m))
		if i != 0 && m.TelegramUsername != "" {
			fmt.Fprintf(&sb, " · %s", m.TelegramUsername)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// confirmCard is the pre-confirmation card: confirm / slot N / delete.
func (s *TelegramServiceImpl) confirmCard(ctx context.Context, captain *models.TelegramPlayer) (string, string) {
	team, err := s.repo.GetTeamByID(ctx, *captain.TeamID)
	if err != nil || team == nil {
		return "Команда не найдена.", KbNone
	}
	members := s.roster(ctx, team.ID)
	return renderTeamCard(team, members) + "\nВсё верно?", fmt.Sprintf("%s:%d", KbRegConfirm, len(members))
}

// teamCard is the /my_team card: slot N / delete / add.
func (s *TelegramServiceImpl) teamCard(ctx context.Context, captain *models.TelegramPlayer) (string, string) {
	team, err := s.repo.GetTeamByID(ctx, *captain.TeamID)
	if err != nil || team == nil {
		return "Команда не найдена.", KbNone
	}
	members := s.roster(ctx, team.ID)
	status := "Check-in: не пройден"
	switch {
	case team.Status == models.TeamStatusDisqualified:
		status = "Статус: Снята с турнира (тех. поражение)"
	case len(members) < mainRosterSlots:
		status = fmt.Sprintf("Check-in: недоступен (неполный состав: %d/%d)", len(members), mainRosterSlots)
	case team.IsCheckedIn:
		status = "Check-in: пройден"
	}
	add := ""
	switch next := len(members) + 1; {
	case next > maxTeamSlots:
	case next >= firstSubstituteSlot:
		add = "sub"
	default:
		add = "player"
	}
	return renderTeamCard(team, members) + status, fmt.Sprintf("%s:%d:%s", KbRegCard, len(members), add)
}

func renderSoloCard(p *models.TelegramPlayer) string {
	return renderSlot(*p) + "\nВсё верно?"
}

func (s *TelegramServiceImpl) teamGone(ctx context.Context, tg int64) (string, string) {
	s.setState(ctx, tg, models.StateIdle)
	return "Команда была удалена. Начните регистрацию заново: /reg_team", KbNone
}

// ---- text steps ------------------------------------------------------------

// handleRegistrationText is the text half of the FSM. Called by
// HandleUserInput for any state isRegistrationState accepts.
func (s *TelegramServiceImpl) handleRegistrationText(ctx context.Context, p *models.TelegramPlayer, input string) (string, string) {
	tg := *p.TelegramID

	switch p.FSMState {
	case models.StateWaitingTeamName:
		name := strings.TrimSpace(input)
		if name == "" || len([]rune(name)) > maxTeamNameLen {
			return fmt.Sprintf("Название должно быть от 1 до %d символов. Введите другое:", maxTeamNameLen), KbRegCancel
		}
		team, err := s.repo.CreateTeam(ctx, name)
		if err != nil {
			return "Это имя занято, попробуйте другое:", KbRegCancel
		}
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "team_id", team.ID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "is_captain", true))
		s.setState(ctx, tg, models.StateTeamLinePrefix+"1")
		if known, ok := s.knownProfile(ctx, tg); ok {
			return fmt.Sprintf("Команда '%s' создана.\n\n%s", name, prefillPrompt(known)), KbRegPrefill
		}
		msg, kb := linePrompt(1, true)
		return fmt.Sprintf("Команда '%s' создана.\n\n%s", name, msg), kb

	case models.StateSoloLine:
		line, problem := parsePlayerLine(input)
		if problem == "" {
			problem = s.duplicateGameID(ctx, line.GameID, p.ID)
		}
		if problem != "" {
			return problem + "\n" + playerLineFormat, KbRegCancel
		}
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_nickname", line.Nick))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_id", line.GameID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "zone_id", line.ZoneID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "stars", line.Stars))
		s.setState(ctx, tg, models.StateSoloRole)
		return rolePrompt(line), KbRegRoles

	case models.StateSoloRole:
		// Typed role text is accepted for clients without inline buttons.
		if validRole(input) {
			return s.HandleRegAction(ctx, tg, "role", input)
		}
		return msgPickRole, KbRegRoles

	case models.StateSoloConfirm:
		return renderSoloCard(p), KbRegSoloConfirm

	case models.StateProfileLine:
		line, problem := parsePlayerLine(input)
		if problem == "" {
			problem = s.duplicateGameID(ctx, line.GameID, p.ID)
		}
		if problem != "" {
			return problem + "\n" + playerLineFormat, KbRegCancel
		}
		s.saveProfileLine(ctx, tg, line)
		s.setState(ctx, tg, models.StateProfileRole)
		return rolePrompt(line), KbRegRoles

	case models.StateProfileRole:
		if validRole(input) {
			return s.saveProfileRole(ctx, tg, input)
		}
		return msgPickRole, KbRegRoles

	case models.StateTeamConfirm:
		if p.TeamID == nil {
			return s.teamGone(ctx, tg)
		}
		return s.confirmCard(ctx, p)
	}

	prefix, slot, ok := slotState(p.FSMState)
	if !ok {
		s.setState(ctx, tg, models.StateIdle)
		return msgLegacyReset, KbNone
	}
	if p.TeamID == nil {
		return s.teamGone(ctx, tg)
	}

	switch prefix {
	case models.StateTeamLinePrefix, models.StateTeamFixPrefix, models.StateTeamEditPrefix:
		line, problem := parsePlayerLine(input)
		if problem == "" {
			problem = s.duplicateGameID(ctx, line.GameID, s.slotRowID(ctx, p, slot))
		}
		if problem != "" {
			_, kb := linePrompt(slot, prefix == models.StateTeamLinePrefix)
			return problem + "\n" + playerLineFormat, kb
		}
		if slot == 1 {
			// The captain's contact is their own account, refreshed on every message.
			line.Contact = ""
		}
		s.saveLine(ctx, p, slot, line)
		s.setState(ctx, tg, roleStateFor(prefix)+strconv.Itoa(slot))
		return rolePrompt(line), KbRegRoles

	case models.StateTeamRolePrefix, models.StateTeamFixRolePrefix, models.StateTeamEditRolePrefix:
		if validRole(input) {
			return s.HandleRegAction(ctx, tg, "role", input)
		}
		return msgPickRole, KbRegRoles
	}
	return msgStaleButton, KbNone
}

// ---- callbacks -------------------------------------------------------------

// HandleRegAction is the button half of the FSM: reg:<action>[:<arg>].
// A button pressed outside the state that showed it is inert.
func (s *TelegramServiceImpl) HandleRegAction(ctx context.Context, tgID int64, action, arg string) (string, string) {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil {
		return "Используйте /start для начала.", KbNone
	}

	if action == "cancel" {
		return s.cancelRegistration(ctx, p)
	}
	if action == "checkin" {
		return s.confirmCheckIn(ctx, p)
	}
	switch action {
	case "prof_edit":
		return s.StartProfileEdit(ctx, tgID)
	case "prof_discord":
		return s.discordLinkHelp(ctx, tgID)
	case "del_sub":
		return s.handleDeleteSub(ctx, p, arg)
	case "delete", "delete_yes", "delete_no":
		return s.handleDelete(ctx, p, action)
	case "redo":
		return s.redoLine(ctx, p)
	case "prefill", "retype":
		return s.handlePrefill(ctx, p, action)
	}

	switch p.FSMState {
	case models.StateProfileRole:
		if action != "role" || !validRole(arg) {
			return msgPickRole, KbRegRoles
		}
		return s.saveProfileRole(ctx, tgID, arg)
	case models.StateSoloRole:
		if action != "role" || !validRole(arg) {
			return rolePromptFromRow(p), KbRegRoles
		}
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "main_role", arg))
		p.MainRole = arg
		s.setState(ctx, tgID, models.StateSoloConfirm)
		return renderSoloCard(p), KbRegSoloConfirm
	case models.StateSoloConfirm:
		switch action {
		case "confirm":
			s.setState(ctx, tgID, models.StateIdle)
			return "Соло-регистрация завершена!", "main_menu"
		case "fix":
			s.setState(ctx, tgID, models.StateSoloLine)
			return soloLinePrompt, KbRegCancel
		}
		return msgStaleButton, KbNone
	}

	// Team: everything below needs one.
	if p.TeamID == nil {
		if isRegistrationState(p.FSMState) {
			return s.teamGone(ctx, tgID)
		}
		return msgStaleButton, KbNone
	}

	switch p.FSMState {
	case models.StateIdle:
		return s.handleCardAction(ctx, p, action, arg, models.StateTeamEditPrefix)
	case models.StateTeamConfirm:
		if action == "confirm" {
			s.setState(ctx, tgID, models.StateIdle)
			return "Команда зарегистрирована. В день турнира нажми /checkin.", "main_menu"
		}
		return s.handleCardAction(ctx, p, action, arg, models.StateTeamFixPrefix)
	}

	prefix, slot, ok := slotState(p.FSMState)
	if !ok {
		return msgStaleButton, KbNone
	}

	switch prefix {
	case models.StateTeamLinePrefix:
		if action == "skip" && slot >= firstSubstituteSlot {
			return s.afterSlot(ctx, p, slot, "")
		}
		return msgStaleButton, KbNone

	case models.StateTeamRolePrefix, models.StateTeamFixRolePrefix, models.StateTeamEditRolePrefix:
		if action != "role" || !validRole(arg) {
			return msgPickRole, KbRegRoles
		}
		s.saveRole(ctx, p, slot, arg)
		switch prefix {
		case models.StateTeamFixRolePrefix:
			s.setState(ctx, tgID, models.StateTeamConfirm)
			return s.confirmCard(ctx, p)
		case models.StateTeamEditRolePrefix:
			s.setState(ctx, tgID, models.StateIdle)
			return s.teamCard(ctx, p)
		}
		return s.afterSlot(ctx, p, slot, fmt.Sprintf("Игрок %d: %s\n", slot, s.slotSummary(ctx, p, slot)))
	}
	return msgStaleButton, KbNone
}

// knownProfile returns in-game data the bot already has for this account:
// its own row from an earlier registration, else the linked Discord profile.
func (s *TelegramServiceImpl) knownProfile(ctx context.Context, tgID int64) (playerLine, bool) {
	if p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID); p != nil && p.GameNickname != "" && p.GameID != "" && p.ZoneID != "" {
		return playerLine{Nick: p.GameNickname, GameID: p.GameID, ZoneID: p.ZoneID, Stars: p.Stars}, true
	}
	if s.profiles == nil {
		return playerLine{}, false
	}
	link, err := s.profiles.GetLinkByTelegramID(ctx, tgID)
	if err != nil || link == nil || link.GameNickname == "" || link.GameID == "" || link.ZoneID == "" {
		return playerLine{}, false
	}
	return playerLine{Nick: link.GameNickname, GameID: link.GameID, ZoneID: link.ZoneID, Stars: link.Stars}, true
}

func prefillPrompt(line playerLine) string {
	return fmt.Sprintf("Взять ваши данные из профиля?\n%s · %s (%s) · %d зв.", line.Nick, line.GameID, line.ZoneID, line.Stars)
}

// handlePrefill answers the offer made by knownProfile. Only the first slot
// and solo registration make it; anywhere else the button is stale.
func (s *TelegramServiceImpl) handlePrefill(ctx context.Context, p *models.TelegramPlayer, action string) (string, string) {
	tgID := *p.TelegramID
	isSolo := p.FSMState == models.StateSoloLine
	isCaptain := p.FSMState == models.StateTeamLinePrefix+"1" && p.TeamID != nil
	if !isSolo && !isCaptain {
		return msgStaleButton, KbNone
	}
	if action == "retype" {
		if isSolo {
			return soloLinePrompt, KbRegCancel
		}
		return linePrompt(1, true)
	}
	line, ok := s.knownProfile(ctx, tgID)
	if !ok {
		return msgStaleButton, KbNone
	}
	if problem := s.duplicateGameID(ctx, line.GameID, p.ID); problem != "" {
		return problem, KbRegCancel
	}
	if isSolo {
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "game_nickname", line.Nick))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "game_id", line.GameID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "zone_id", line.ZoneID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "stars", line.Stars))
		s.setState(ctx, tgID, models.StateSoloRole)
		return rolePrompt(line), KbRegRoles
	}
	s.saveLine(ctx, p, 1, line)
	s.setState(ctx, tgID, models.StateTeamRolePrefix+"1")
	return rolePrompt(line), KbRegRoles
}

// redoLine is the "fix the line" button on the role step: back to the line
// prompt of the same slot. The row already written is simply overwritten.
func (s *TelegramServiceImpl) redoLine(ctx context.Context, p *models.TelegramPlayer) (string, string) {
	tgID := *p.TelegramID
	if p.FSMState == models.StateProfileRole {
		return s.StartProfileEdit(ctx, tgID)
	}
	if p.FSMState == models.StateSoloRole {
		s.setState(ctx, tgID, models.StateSoloLine)
		return soloLinePrompt, KbRegCancel
	}
	prefix, slot, ok := slotState(p.FSMState)
	if !ok || p.TeamID == nil {
		return msgStaleButton, KbNone
	}
	var linePrefix string
	switch prefix {
	case models.StateTeamRolePrefix:
		linePrefix = models.StateTeamLinePrefix
	case models.StateTeamFixRolePrefix:
		linePrefix = models.StateTeamFixPrefix
	case models.StateTeamEditRolePrefix:
		linePrefix = models.StateTeamEditPrefix
	default:
		return msgStaleButton, KbNone
	}
	s.setState(ctx, tgID, linePrefix+strconv.Itoa(slot))
	msg, _ := linePrompt(slot, false)
	return msg, KbRegCancel
}

// handleCardAction serves the fix / sub / delete buttons of both cards. editPrefix
// decides where the captain lands after the edit: team_fix_ returns to the
// confirmation card, team_edit_ back to the menu.
func (s *TelegramServiceImpl) handleCardAction(ctx context.Context, p *models.TelegramPlayer, action, arg, editPrefix string) (string, string) {
	tgID := *p.TelegramID
	if !p.IsCaptain {
		return "Только капитан может менять состав.", KbNone
	}
	members := s.roster(ctx, *p.TeamID)
	switch action {
	case "fix":
		slot, err := strconv.Atoi(arg)
		if err != nil || slot < 1 || slot > len(members) {
			return msgStaleButton, KbNone
		}
		s.setState(ctx, tgID, editPrefix+strconv.Itoa(slot))
		msg, _ := linePrompt(slot, false)
		if slot >= firstSubstituteSlot {
			return fmt.Sprintf("Сейчас: %s\n\n%s\n\nИли нажмите кнопку ниже, чтобы удалить замену:", renderSlot(members[slot-1]), msg), fmt.Sprintf("%s:%d", KbRegSubFix, slot)
		}
		return fmt.Sprintf("Сейчас: %s\n%s", renderSlot(members[slot-1]), msg), KbRegCancel
	case "sub":
		if editPrefix != models.StateTeamEditPrefix && editPrefix != models.StateTeamFixPrefix {
			return msgStaleButton, KbNone
		}
		slot := len(members) + 1
		if slot > maxTeamSlots {
			return msgStaleButton, KbNone
		}
		s.setState(ctx, tgID, editPrefix+strconv.Itoa(slot))
		msg, _ := linePrompt(slot, false)
		return msg, KbRegCancel
	}
	return msgStaleButton, KbNone
}

func (s *TelegramServiceImpl) handleDeleteSub(ctx context.Context, captain *models.TelegramPlayer, arg string) (string, string) {
	tgID := *captain.TelegramID
	if captain.TeamID == nil || !captain.IsCaptain {
		return "Только капитан может менять состав.", KbNone
	}
	slot, err := strconv.Atoi(arg)
	if err != nil || slot < firstSubstituteSlot {
		return msgStaleButton, KbNone
	}
	members := s.roster(ctx, *captain.TeamID)
	if slot <= len(members) {
		s.logWrite("DeleteTeammate", s.repo.DeleteTeammate(ctx, members[slot-1].ID))
	}
	if strings.HasPrefix(captain.FSMState, models.StateTeamFixPrefix) || captain.FSMState == models.StateTeamConfirm {
		s.setState(ctx, tgID, models.StateTeamConfirm)
		text, kb := s.confirmCard(ctx, captain)
		return "Замена удалена.\n\n" + text, kb
	}
	s.setState(ctx, tgID, models.StateIdle)
	text, kb := s.teamCard(ctx, captain)
	return "Замена удалена.\n\n" + text, kb
}

// handleDelete is the delete button and /delete_team: ask first, act on "yes",
// and on "no" put the captain back on whichever card they came from.
func (s *TelegramServiceImpl) handleDelete(ctx context.Context, p *models.TelegramPlayer, action string) (string, string) {
	tgID := *p.TelegramID
	if p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может удалить команду.", KbNone
	}
	switch action {
	case "delete":
		team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
		if err != nil || team == nil {
			return "Команда не найдена.", KbNone
		}
		n := len(s.roster(ctx, team.ID))
		return fmt.Sprintf("Удалить команду '%s'? Будут удалены все %d игрок(ов). Это нельзя отменить.", team.Name, n), KbRegDeleteConfirm
	case "delete_yes":
		s.setState(ctx, tgID, models.StateIdle)
		return s.DeleteTeam(ctx, tgID), "main_menu"
	case "delete_no":
		switch p.FSMState {
		case models.StateTeamConfirm:
			return s.confirmCard(ctx, p)
		case models.StateIdle:
			return s.teamCard(ctx, p)
		}
		return "Удаление отменено.", KbNone
	}
	return msgStaleButton, KbNone
}

// cancelRegistration drops the user back to the menu. A team created so far
// is kept: the captain can finish it from /my_team or delete it there.
func (s *TelegramServiceImpl) cancelRegistration(ctx context.Context, p *models.TelegramPlayer) (string, string) {
	tgID := *p.TelegramID
	currentState := p.FSMState
	if isReportState(currentState) || currentState == models.StateWaitingReport {
		return s.CancelReport(ctx, tgID)
	}
	if !isRegistrationState(currentState) {
		return msgStaleButton, KbNone
	}

	if currentState == models.StateProfileLine || currentState == models.StateProfileRole {
		s.setState(ctx, tgID, models.StateIdle)
		return "Редактирование профиля отменено.", "main_menu"
	}

	// If the user cancelled while picking a role for a newly added teammate,
	// delete the incomplete teammate row that was created without a role.
	if p.TeamID != nil {
		prefix, slot, ok := slotState(currentState)
		if ok && slot > 1 && strings.Contains(prefix, "role") {
			members := s.roster(ctx, *p.TeamID)
			if slot <= len(members) && members[slot-1].MainRole == "" {
				s.logWrite("DeleteTeammate", s.repo.DeleteTeammate(ctx, members[slot-1].ID))
			}
		}
	}

	if strings.HasPrefix(currentState, models.StateTeamFixPrefix) || strings.HasPrefix(currentState, models.StateTeamFixRolePrefix) {
		s.setState(ctx, tgID, models.StateTeamConfirm)
		return s.confirmCard(ctx, p)
	}
	if strings.HasPrefix(currentState, models.StateTeamEditPrefix) || strings.HasPrefix(currentState, models.StateTeamEditRolePrefix) {
		s.setState(ctx, tgID, models.StateIdle)
		return s.teamCard(ctx, p)
	}
	s.setState(ctx, tgID, models.StateIdle)
	if currentState == models.StateWaitingReport {
		return "Действие отменено.", "main_menu"
	}
	if p.TeamID != nil {
		_, slot, ok := slotState(currentState)
		if ok && slot == 1 {
			s.DeleteTeam(ctx, tgID)
			return "Создание команды отменено. Название освобождено.", "main_menu"
		}
		if name := s.currentTeamName(ctx, tgID); name != "" {
			return fmt.Sprintf("Регистрация прервана. Команда '%s' сохранена с введёнными игроками — дополнить или удалить можно через /my_team.", name), "main_menu"
		}
	}
	return "Действие отменено.", "main_menu"
}

// afterSlot moves on from slot N during initial registration: the next line
// prompt, or the confirmation card after the last slot (mainRosterSlots = 5).
func (s *TelegramServiceImpl) afterSlot(ctx context.Context, p *models.TelegramPlayer, slot int, ack string) (string, string) {
	tg := *p.TelegramID
	if slot >= mainRosterSlots {
		s.setState(ctx, tg, models.StateTeamConfirm)
		text, kb := s.confirmCard(ctx, p)
		return ack + text, kb
	}
	next := slot + 1
	s.setState(ctx, tg, models.StateTeamLinePrefix+strconv.Itoa(next))
	msg, kb := linePrompt(next, true)
	return ack + msg, kb
}

// confirmCheckIn is the reminder's button. Unlike /checkin it never toggles
// off, and it works in any FSM state: the reminder arrives whatever the
// captain happens to be doing in the bot.
func (s *TelegramServiceImpl) confirmCheckIn(ctx context.Context, p *models.TelegramPlayer) (string, string) {
	if p.TeamID == nil || !p.IsCaptain {
		return "Только капитан зарегистрированной команды может пройти check-in.", KbNone
	}
	team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || team == nil {
		return "Команда не найдена.", KbNone
	}
	st, err := s.checkInState(ctx, team)
	if err != nil {
		s.logWrite("checkInState", err)
		return "Не удалось подтвердить участие. Попробуйте /checkin.", KbNone
	}
	if st.disqualified {
		return fmt.Sprintf("Команда '%s' снята с турнира (тех. поражение). Вернуть её могут только организаторы.", team.Name), KbNone
	}
	if !st.checkedIn {
		members := s.roster(ctx, team.ID)
		if len(members) < mainRosterSlots {
			return fmt.Sprintf("Check-in невозможен: в команде %d из %d обязательных игроков. Доукомплектуйте состав (минимум %d игроков).", len(members), mainRosterSlots, mainRosterSlots), KbNone
		}
		// This used to write only the team row, which the technical-defeat
		// sweep does not read: a captain who pressed the reminder's button
		// was still disqualified ten minutes after the start.
		if err := s.recordCheckIn(ctx, st, team.ID, true); err != nil {
			if errors.Is(err, errNotEntered) {
				return notEnteredMessage(team.Name, st.tournament), KbNone
			}
			s.logWrite("recordCheckIn", err)
			return "Не удалось подтвердить участие. Попробуйте /checkin.", KbNone
		}
	}
	return fmt.Sprintf("Check-in подтверждён. Команда '%s' участвует в турнире.", team.Name), KbNone
}

func (s *TelegramServiceImpl) StartProfileEdit(ctx context.Context, tgID int64) (string, string) {
	s.setState(ctx, tgID, models.StateProfileLine)
	prompt := "Отправьте ваши игровые данные:\nНик\nGameID (ZoneID)\nЗвёзды\n\nПример:\nKitayes\n5374343843 (6732)\n25"
	return prompt, KbRegCancel
}

func (s *TelegramServiceImpl) saveProfileLine(ctx context.Context, tgID int64, line playerLine) {
	s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "game_nickname", line.Nick))
	s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "game_id", line.GameID))
	s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "zone_id", line.ZoneID))
	s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "stars", line.Stars))
}

func (s *TelegramServiceImpl) saveProfileRole(ctx context.Context, tgID int64, role string) (string, string) {
	s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "main_role", role))
	s.setState(ctx, tgID, models.StateIdle)
	if s.profiles != nil {
		p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
		if p != nil {
			_ = s.profiles.UpdateTelegramProfile(ctx, tgID, p.GameNickname, p.GameID, p.ZoneID, p.Stars, role)
		}
	}
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	nick, gameID, zoneID, stars := "", "", "", 0
	if p != nil {
		nick, gameID, zoneID, stars = p.GameNickname, p.GameID, p.ZoneID, p.Stars
	}
	msg := fmt.Sprintf("Профиль успешно сохранён!\n\nНик: %s\nID: %s (%s)\nЗвёзды: %d | Роль: %s", nick, gameID, zoneID, stars, role)
	return msg, "main_menu"
}

func (s *TelegramServiceImpl) discordLinkHelp(_ context.Context, _ int64) (string, string) {
	msg := "Для привязки Discord профиля:\n\n1. Зайдите в Discord на сервер турнира\n2. Введите команду: /link <ваш ID игрока>\n3. Получите одноразовый код\n4. Отправьте его сюда командой: /link <код>"
	return msg, "main_menu"
}
