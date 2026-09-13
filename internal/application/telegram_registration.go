package application

import (
	"blackwatch/internal/models"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Keyboard types for the registration flow. Delivery renders them as inline
// keyboards; the ones with a suffix carry rendering parameters after ":".
const (
	KbRegCancel      = "reg_cancel"
	KbRegRoles       = "reg_roles"
	KbRegSkip        = "reg_skip"
	KbRegConfirm     = "reg_confirm"      // "reg_confirm:<n>": ✏️ 1..n, ✅, 🗑
	KbRegCard        = "reg_card"         // "reg_card:<n>:<add>": ✏️ 1..n, 🗑, ➕ when add != ""
	KbRegSoloConfirm = "reg_solo_confirm" // ✅, ✏️
	KbRegCheckin     = "reg_checkin"      // ✅ Подтвердить участие (in the reminder)
)

const playerLineFormat = "Формат: Ник GameID ZoneID Звёзды"

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
	msgLegacyReset = "Регистрация обновилась, старый ввод сброшен. Начните заново: /reg_team или /reg_solo"
	msgPickRole    = "Выберите роль кнопкой 👆"
)

// isRegistrationState reports whether the state belongs to the new flow.
func isRegistrationState(st string) bool {
	return st == models.StateWaitingTeamName || strings.HasPrefix(st, "team_") || strings.HasPrefix(st, "solo_")
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
		return fmt.Sprintf("👑 Игрок %d/%d (капитан)", slot, maxTeamSlots)
	case slot >= firstSubstituteSlot:
		return fmt.Sprintf("🔁 Замена %d/%d", slot, maxTeamSlots)
	default:
		return fmt.Sprintf("👤 Игрок %d/%d", slot, maxTeamSlots)
	}
}

// linePrompt asks for slot N's line. During initial registration substitutes
// get a skip button; everywhere else the only button is cancel.
func linePrompt(slot int, initial bool) (string, string) {
	format := "Отправь одной строкой: Ник GameID ZoneID Звёзды"
	if slot != 1 {
		format += " [@telegram]"
	}
	msg := slotLabel(slot) + "\n" + format + "\nПример: Kitayes 123456789 1234 25"
	if initial && slot >= firstSubstituteSlot {
		return msg, KbRegSkip
	}
	return msg, KbRegCancel
}

const soloLinePrompt = "Отправь одной строкой: Ник GameID ZoneID Звёзды\nПример: Kitayes 123456789 1234 25"

func rolePrompt(line playerLine) string {
	return fmt.Sprintf("%s · %s (%s) · %d⭐\nРоль?", line.Nick, line.GameID, line.ZoneID, line.Stars)
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
	return fmt.Sprintf("%s · %s · %s (%s) · %d⭐", m.GameNickname, m.MainRole, m.GameID, m.ZoneID, m.Stars)
}

func renderTeamCard(team *models.TelegramTeam, members []models.TelegramPlayer) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Команда '%s'\n", team.Name)
	for i, m := range members {
		icon := "👤"
		switch {
		case i == 0:
			icon = "👑"
		case m.IsSubstitute:
			icon = "🔁"
		}
		fmt.Fprintf(&sb, "%d. %s %s", i+1, icon, renderSlot(m))
		if i != 0 && m.TelegramUsername != "" {
			fmt.Fprintf(&sb, " · %s", m.TelegramUsername)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// confirmCard is the pre-confirmation card: ✅ / ✏️ N / 🗑.
func (s *TelegramServiceImpl) confirmCard(ctx context.Context, captain *models.TelegramPlayer) (string, string) {
	team, err := s.repo.GetTeamByID(ctx, *captain.TeamID)
	if err != nil || team == nil {
		return "Команда не найдена.", KbNone
	}
	members := s.roster(ctx, team.ID)
	return renderTeamCard(team, members) + "\nВсё верно?", fmt.Sprintf("%s:%d", KbRegConfirm, len(members))
}

// teamCard is the /my_team card: ✏️ N / 🗑 / ➕.
func (s *TelegramServiceImpl) teamCard(ctx context.Context, captain *models.TelegramPlayer) (string, string) {
	team, err := s.repo.GetTeamByID(ctx, *captain.TeamID)
	if err != nil || team == nil {
		return "Команда не найдена.", KbNone
	}
	members := s.roster(ctx, team.ID)
	status := "⚪ Check-in не пройден"
	if team.IsCheckedIn {
		status = "✅ Check-in пройден"
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
		msg, kb := linePrompt(1, true)
		return fmt.Sprintf("Команда '%s' создана.\n%s", name, msg), kb

	case models.StateSoloLine:
		line, problem := parsePlayerLine(input)
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

	// Solo.
	switch p.FSMState {
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
			return "✅ Соло-регистрация завершена!", "main_menu"
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
			return "✅ Команда зарегистрирована. В день турнира нажми /checkin.", "main_menu"
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
		return s.afterSlot(ctx, p, slot, fmt.Sprintf("✅ Игрок %d: %s\n", slot, s.slotSummary(ctx, p, slot)))
	}
	return msgStaleButton, KbNone
}

// handleCardAction serves the ✏️ / ➕ / 🗑 buttons of both cards. editPrefix
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
		return fmt.Sprintf("Сейчас: %s\n%s", renderSlot(members[slot-1]), msg), KbRegCancel
	case "sub":
		if editPrefix != models.StateTeamEditPrefix {
			return msgStaleButton, KbNone
		}
		slot := len(members) + 1
		if slot > maxTeamSlots {
			return msgStaleButton, KbNone
		}
		s.setState(ctx, tgID, editPrefix+strconv.Itoa(slot))
		msg, _ := linePrompt(slot, false)
		return msg, KbRegCancel
	case "delete":
		s.setState(ctx, tgID, models.StateIdle)
		return s.DeleteTeam(ctx, tgID), "main_menu"
	}
	return msgStaleButton, KbNone
}

// cancelRegistration drops the user back to the menu. A team created so far
// is kept: the captain can finish it from /my_team or delete it there.
func (s *TelegramServiceImpl) cancelRegistration(ctx context.Context, p *models.TelegramPlayer) (string, string) {
	tgID := *p.TelegramID
	// The same button backs /report's "waiting for a screenshot" prompt.
	if !isRegistrationState(p.FSMState) && p.FSMState != models.StateWaitingReport {
		return msgStaleButton, KbNone
	}
	s.setState(ctx, tgID, models.StateIdle)
	if p.FSMState == models.StateWaitingReport {
		return "Действие отменено.", "main_menu"
	}
	if p.TeamID != nil {
		if name := s.currentTeamName(ctx, tgID); name != "" {
			return fmt.Sprintf("Регистрация прервана. Команда '%s' сохранена с введёнными игроками — дополнить или удалить можно через /my_team.", name), "main_menu"
		}
	}
	return "Действие отменено.", "main_menu"
}

// afterSlot moves on from slot N during initial registration: the next line
// prompt, or the confirmation card after the last slot.
func (s *TelegramServiceImpl) afterSlot(ctx context.Context, p *models.TelegramPlayer, slot int, ack string) (string, string) {
	tg := *p.TelegramID
	if slot >= maxTeamSlots {
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
	if !team.IsCheckedIn {
		if err := s.repo.SetCheckIn(ctx, team.ID, true); err != nil {
			s.logWrite("SetCheckIn", err)
			return "Не удалось подтвердить участие. Попробуйте /checkin.", KbNone
		}
	}
	return fmt.Sprintf("✅ Check-in подтверждён. Команда '%s' участвует в турнире.", team.Name), KbNone
}
