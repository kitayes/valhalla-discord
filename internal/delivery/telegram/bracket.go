package telegram

import (
	"blackwatch/internal/application"
	"blackwatch/internal/challonge"
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const telegramMessageLimit = 4000
const bracketBuildFailureAlert = 5

func matchLine(m models.BracketMatch) string {
	name := func(id *int, n string) string {
		if id == nil {
			return "?"
		}
		return n
	}
	switch {
	case m.State == models.BracketComplete && (m.Team1ID == nil || m.Team2ID == nil):
		if m.Team1ID != nil {
			return fmt.Sprintf("#%d %s — автопроход", m.PlayOrder, m.Team1Name)
		}
		return fmt.Sprintf("#%d %s — автопроход", m.PlayOrder, m.Team2Name)
	case m.State == models.BracketComplete:
		winner := m.Team1Name
		if m.WinnerID != nil && m.Team2ID != nil && *m.WinnerID == *m.Team2ID {
			winner = m.Team2Name
		}
		return fmt.Sprintf("#%d %s vs %s — %s, победа %s", m.PlayOrder, m.Team1Name, m.Team2Name, m.ScoresCSV, winner)
	case m.Ready():
		return fmt.Sprintf("#%d %s vs %s — идёт", m.PlayOrder, m.Team1Name, m.Team2Name)
	default:
		return fmt.Sprintf("#%d %s vs %s — ожидает соперника", m.PlayOrder, name(m.Team1ID, m.Team1Name), name(m.Team2ID, m.Team2Name))
	}
}

// formatBracketRound lists one round of the bracket under its title and
// best-of format. t may be nil: the layout is then read from the matches.
func formatBracketRound(t *models.TelegramTournament, url string, ms []models.BracketMatch, round int) []string {
	head := fmt.Sprintf("Сетка: %s\n\n%s, %s\n", url, application.RoundTitle(t, ms, round),
		application.FormatLabel(application.MatchFormat(t, ms, round)))
	var lines []string
	for _, m := range ms {
		if m.Round == round {
			lines = append(lines, matchLine(m))
		}
	}
	if len(lines) == 0 {
		return []string{fmt.Sprintf("Сетка: %s\n\nВ раунде %d нет матчей.", url, round)}
	}
	var parts []string
	cur := head
	for _, line := range lines {
		if len(cur)+len(line)+1 > telegramMessageLimit {
			parts = append(parts, strings.TrimRight(cur, "\n"))
			cur = ""
		}
		cur += line + "\n"
	}
	return append(parts, strings.TrimRight(cur, "\n"))
}

func defaultBracketRound(ms []models.BracketMatch) int {
	if len(ms) == 0 {
		return 1
	}
	best := 0
	for _, m := range ms {
		if m.State != models.BracketComplete && (best == 0 || m.Round < best) {
			best = m.Round
		}
	}
	if best == 0 {
		return application.TotalRounds(ms)
	}
	return best
}

func parseSetWinner(args string, defWin, defLose int) (playOrder int, team string, win, lose int, err error) {
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return 0, "", 0, 0, errors.New("формат: /set_winner <№ матча> <название команды> [счёт, например 2:1]")
	}
	playOrder, err = strconv.Atoi(strings.TrimPrefix(fields[0], "#"))
	if err != nil || playOrder <= 0 {
		return 0, "", 0, 0, errors.New("номер матча должен быть числом, например 7")
	}
	win, lose = defWin, defLose
	rest := fields[1:]
	if len(rest) >= 2 {
		if w, l, ok := parseScorePair(rest[len(rest)-1]); ok {
			if w <= l {
				return 0, "", 0, 0, errors.New("счёт должен быть в пользу победителя, например 2:1")
			}
			win, lose = w, l
			rest = rest[:len(rest)-1]
		}
	}
	return playOrder, strings.Join(rest, " "), win, lose, nil
}

// hasExplicitScore reports whether /set_winner arguments end with a score
// ("<№> <team> 2:1") rather than leaving it to the walkover default.
func hasExplicitScore(args string) bool {
	fields := strings.Fields(args)
	if len(fields) < 3 {
		return false
	}
	_, _, ok := parseScorePair(fields[len(fields)-1])
	return ok
}

// scoreMismatch explains why a referee's score does not fit the match's
// best-of, or returns "".
func (b *Bot) scoreMismatch(ctx context.Context, playOrder, win, lose int) string {
	ms, err := b.bracket.Matches(ctx)
	if err != nil {
		return ""
	}
	t, _ := b.service.GetActiveTournament(ctx)
	for _, m := range ms {
		if m.PlayOrder != playOrder {
			continue
		}
		bo := application.MatchFormat(t, ms, m.Round)
		if err := application.ValidateScore(bo, win, lose); err != nil {
			return fmt.Sprintf("Матч #%d: %s. Для технического поражения не указывайте счёт.", playOrder, err.Error())
		}
		return ""
	}
	return ""
}

func parseScorePair(s string) (int, int, bool) {
	sep := ":"
	if !strings.Contains(s, sep) {
		sep = "-"
	}
	parts := strings.Split(s, sep)
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || a < 0 || b < 0 {
		return 0, 0, false
	}
	return a, b, true
}

// bracketBuildDue: the bracket is built at the start, when check-in closes,
// from the teams that checked in. It used to be built an hour early from
// every entry, and the teams that never showed handed out walkovers.
func bracketBuildDue(tournament, now time.Time) bool {
	if tournament.IsZero() {
		return false
	}
	return !now.Before(tournament) && now.Sub(tournament) <= scheduleCatchUpWindow
}

func (b *Bot) reportBracketError(ctx context.Context, what string, err error) {
	if err == nil {
		return
	}
	if b.bracket != nil {
		b.bracket.RecordAPIError(ctx, err)
	}
	b.logger.Error("telegram: bracket %s: %v", what, err)
	if errors.Is(err, application.ErrResultConflict) {
		b.notifyAdmins("КОНФЛИКТ РЕЗУЛЬТАТА: " + err.Error() + ". Проверьте сетку и отчёт, затем при необходимости используйте /set_winner.")
	}
	if errors.Is(err, challonge.ErrQuotaExceeded) && !b.quotaAlerted {
		b.quotaAlerted = true
		b.notifyAdmins("Challonge: исчерпана квота API. Сетка временно не обновляется; отчёты сохраняются локально и будут отправлены после восстановления доступа.")
	}
}

func (b *Bot) notifyAdmins(text string) {
	for id := range b.adminIDs {
		b.sendMessage(id, text, "main_menu")
	}
}

func (b *Bot) notifyTeam(ctx context.Context, teamID int, text, kb string) {
	var chatIDs []int64
	seen := make(map[int64]bool)
	if b.bracket != nil {
		for _, id := range b.bracket.TeamChatIDs(ctx, teamID) {
			if !seen[id] {
				seen[id] = true
				chatIDs = append(chatIDs, id)
			}
		}
	} else if b.service != nil {
		_, members, err := b.service.GetTeamDetails(ctx, teamID)
		if err == nil {
			for _, m := range members {
				if m.TelegramID != nil && *m.TelegramID > 0 && !seen[*m.TelegramID] {
					seen[*m.TelegramID] = true
					chatIDs = append(chatIDs, *m.TelegramID)
				}
			}
		}
	}
	for _, chatID := range chatIDs {
		if b.webAppURL != "" {
			b.SendMatchNotification(chatID, text, true)
		} else {
			b.sendMessage(chatID, text, kb)
		}
	}
}

func (b *Bot) notifyMatchesReady(ctx context.Context, ms []models.BracketMatch) {
	if len(ms) == 0 {
		return
	}
	t, _ := b.service.GetActiveTournament(ctx)
	var all []models.BracketMatch
	if b.bracket != nil {
		all, _ = b.bracket.Matches(ctx)
	}
	for _, m := range ms {
		if m.Team1ID == nil || m.Team2ID == nil {
			continue
		}
		head := fmt.Sprintf("Матч #%d — %s, %s: %s vs %s", m.PlayOrder, application.RoundTitle(t, all, m.Round),
			application.FormatLong(application.MatchFormat(t, all, m.Round)), m.Team1Name, m.Team2Name)
		text1 := fmt.Sprintf("%s\n\nВаш соперник — команда «%s»!\nЗайдите в приложение для подтверждения готовности к игре.", head, m.Team2Name)
		text2 := fmt.Sprintf("%s\n\nВаш соперник — команда «%s»!\nЗайдите в приложение для подтверждения готовности к игре.", head, m.Team1Name)
		b.notifyTeam(ctx, *m.Team1ID, text1, "main_menu")
		b.notifyTeam(ctx, *m.Team2ID, text2, "main_menu")
	}
}

func (b *Bot) notifyMatchesReset(ctx context.Context, ms []models.BracketMatch) {
	for _, m := range ms {
		text := fmt.Sprintf("Результат матча #%d (%s vs %s) отменён администратором. Бот пришлёт нового соперника или попросит переиграть матч.", m.PlayOrder, m.Team1Name, m.Team2Name)
		if m.Team1ID != nil {
			b.notifyTeam(ctx, *m.Team1ID, text, "main_menu")
		}
		if m.Team2ID != nil {
			b.notifyTeam(ctx, *m.Team2ID, text, "main_menu")
		}
	}
}

// ApplyBracketChange tells the teams of reset matches and of new pairs. The
// service calls it after a result it recorded itself.
func (b *Bot) ApplyBracketChange(ctx context.Context, ch *application.BracketChange) {
	b.applyBracketChange(ctx, ch)
}

func (b *Bot) applyBracketChange(ctx context.Context, ch *application.BracketChange) {
	if ch == nil {
		return
	}
	b.notifyMatchesReset(ctx, ch.Reset)
	b.notifyMatchesReady(ctx, ch.Ready)
}

func (b *Bot) announceBracket(ctx context.Context, built *application.BracketBuilt) {
	t, _ := b.service.GetActiveTournament(ctx)
	ms, _ := b.bracket.Matches(ctx)
	for _, part := range formatBracketRound(t, built.URL, ms, 1) {
		b.notifyTournamentChat("СЕТКА ГОТОВА\n\n" + part)
	}
	if len(built.Byes) > 0 {
		names := make([]string, 0, len(built.Byes))
		for _, team := range built.Byes {
			names = append(names, team.Name)
		}
		b.notifyTournamentChat("Автопроход во 2-й раунд: " + strings.Join(names, ", "))
	}
	if len(built.Incomplete) > 0 {
		names := make([]string, 0, len(built.Incomplete))
		for _, team := range built.Incomplete {
			names = append(names, team.Name)
		}
		b.notifyAdmins("Не включены в сетку: неполный основной состав — " + strings.Join(names, ", "))
	}
	for _, m := range built.Round1 {
		if m.Team1ID == nil || m.Team2ID == nil {
			continue
		}
		text := fmt.Sprintf("Сетка готова! %s, %s, матч #%d: %s vs %s.\nСетка: %s\n\nЗайдите в приложение для подтверждения готовности к игре.",
			application.RoundTitle(t, ms, m.Round), application.FormatLong(application.MatchFormat(t, ms, m.Round)), m.PlayOrder, m.Team1Name, m.Team2Name, built.URL)
		b.notifyTeam(ctx, *m.Team1ID, text, "main_menu")
		b.notifyTeam(ctx, *m.Team2ID, text, "main_menu")
	}
	for _, team := range built.Byes {
		text := fmt.Sprintf("Сетка готова! В 1-м раунде у команды '%s' автопроход.\nСетка: %s", team.Name, built.URL)
		b.notifyTeam(ctx, team.ID, text, "main_menu")
	}
	if len(built.NoShows) > 0 {
		names := make([]string, 0, len(built.NoShows))
		for _, team := range built.NoShows {
			names = append(names, team.Name)
			b.notifyTeam(ctx, team.ID, fmt.Sprintf("Команда '%s' не прошла check-in до старта и не попала в сетку.", team.Name), "main_menu")
		}
		b.notifyAdmins("Не прошли check-in и не попали в сетку: " + strings.Join(names, ", "))
	}
	if len(built.Later) > 0 {
		b.notifyMatchesReady(ctx, built.Later)
	}
}

func (b *Bot) runBracketChecks(ctx context.Context, tTime, now time.Time) {
	if b.bracket == nil {
		return
	}
	if !b.bracket.CanAttempt(ctx, now) {
		if !b.quotaAlerted {
			b.quotaAlerted = true
			b.notifyAdmins("Challonge: исчерпана квота API. Автоматические запросы временно остановлены; отчёты сохраняются локально.")
		}
		return
	}
	// Re-arm the alert after Retry-After (or the fallback cooldown) expires.
	b.quotaAlerted = false
	if bracketBuildDue(tTime, now) && !b.bracket.IsBuiltFor(ctx, tTime) {
		b.buildBracket(ctx, tTime, 0)
	}
	if b.bracket.IsBuiltFor(ctx, tTime) {
		ready, err := b.bracket.ForfeitDisqualified(ctx)
		b.reportBracketError(ctx, "forfeit sweep", err)
		b.notifyMatchesReady(ctx, ready)
		ready, err = b.bracket.FlushPendingReports(ctx)
		b.reportBracketError(ctx, "flush reports", err)
		b.notifyMatchesReady(ctx, ready)
		b.finishQualifierIfDone(ctx)
	}
}

// finishQualifierIfDone closes the active qualifier once its last match before
// the advance line is played and announces who goes on. A count that does not
// add up is reported to admins once, who then close it by hand.
func (b *Bot) finishQualifierIfDone(ctx context.Context) {
	res, err := b.service.TryFinishQualifier(ctx)
	if err != nil {
		if msg := err.Error(); msg != b.qualifierAlert {
			b.qualifierAlert = msg
			b.notifyAdmins("Квалификация не закрылась автоматически: " + msg)
		}
		return
	}
	b.qualifierAlert = ""
	if res != nil {
		b.announceQualified(ctx, res)
		if b.matchDesk != nil {
			b.wakeMatchDesk()
		}
	}
}

func (b *Bot) buildBracket(ctx context.Context, tTime time.Time, adminChat int64) {
	built, err := b.bracket.Build(ctx, tTime)
	if err != nil {
		b.reportBracketError(ctx, "build", err)
		if adminChat != 0 {
			b.sendMessage(adminChat, "Не удалось построить сетку: "+err.Error(), "main_menu")
		} else if b.bracket.BuildFailures(ctx) == bracketBuildFailureAlert {
			b.notifyAdmins(fmt.Sprintf("Не удалось построить сетку в Challonge (%d попыток подряд): %v\nБот продолжает пробовать раз в минуту до старта. Вручную: /build_bracket", bracketBuildFailureAlert, err))
		}
		return
	}
	b.announceBracket(ctx, built)
	if adminChat != 0 {
		b.sendMessage(adminChat, fmt.Sprintf("Сетка построена: %s\nМатчей в 1-м раунде: %d, автопроходов: %d.", built.URL, len(built.Round1), len(built.Byes)), "main_menu")
	}
}

// BuildBracket builds the bracket for the active tournament and announces it to participants and tournament chat.
func (b *Bot) BuildBracket(ctx context.Context, adminChat int64) (*application.BracketBuilt, error) {
	if b.bracket == nil {
		return nil, errors.New("генератор сетки недоступен")
	}
	tTime := b.service.GetTournamentTime(ctx)
	if tTime.IsZero() {
		tTime = time.Now()
	}
	built, err := b.bracket.Build(ctx, tTime)
	if err != nil {
		b.reportBracketError(ctx, "build", err)
		return nil, err
	}
	b.announceBracket(ctx, built)
	if adminChat != 0 {
		b.sendMessage(adminChat, fmt.Sprintf("Сетка построена: %s\nМатчей в 1-м раунде: %d, автопроходов: %d.", built.URL, len(built.Round1), len(built.Byes)), "main_menu")
	}
	return built, nil
}

func (b *Bot) handleBracketCommand(ctx context.Context, chatID int64, text string) bool {
	if b.bracket == nil {
		return false
	}
	switch {
	case text == "/bracket" || strings.HasPrefix(text, "/bracket "):
		ms, err := b.bracket.Matches(ctx)
		if err != nil {
			b.sendMessage(chatID, "Не удалось прочитать сетку: "+err.Error(), "main_menu")
			return true
		}
		url := b.bracket.URL(ctx)
		if url == "" || len(ms) == 0 {
			b.sendMessage(chatID, "Сетка ещё не построена. Она появится в момент старта турнира из команд, прошедших check-in.", "main_menu")
			return true
		}
		round := defaultBracketRound(ms)
		if arg := strings.TrimSpace(strings.TrimPrefix(text, "/bracket")); arg != "" {
			// Negative numbers are lower-bracket rounds.
			if n, err := strconv.Atoi(arg); err == nil && n != 0 {
				round = n
			}
		}
		active, _ := b.service.GetActiveTournament(ctx)
		for _, part := range formatBracketRound(active, url, ms, round) {
			b.sendMessage(chatID, part, "main_menu")
		}
		return true
	case text == "/build_bracket" && b.isAdmin(chatID):
		tTime := b.service.GetTournamentTime(ctx)
		if tTime.IsZero() {
			b.sendMessage(chatID, "Сначала задайте время турнира: /set_tourney", "main_menu")
			return true
		}
		if has, _ := b.bracket.HasResults(ctx); has {
			b.sendMessage(chatID, "В сетке уже есть сыгранные матчи — пересобрать нельзя. Исправляйте результаты через /set_winner.", "main_menu")
			return true
		}
		b.sendMessage(chatID, "Строю сетку...", "main_menu")
		b.buildBracket(ctx, tTime, chatID)
		return true
	case strings.HasPrefix(text, "/set_winner") && b.isAdmin(chatID):
		defWin, defLose := b.bracket.Walkover()
		args := strings.TrimPrefix(text, "/set_winner")
		po, team, win, lose, err := parseSetWinner(args, defWin, defLose)
		if err != nil {
			b.sendMessage(chatID, err.Error(), "main_menu")
			return true
		}
		// A score typed by the referee must fit the round's best-of; without
		// one it is a technical defeat with the configured walkover score.
		if hasExplicitScore(args) {
			if msg := b.scoreMismatch(ctx, po, win, lose); msg != "" {
				b.sendMessage(chatID, msg, "main_menu")
				return true
			}
		}
		change, err := b.bracket.SetWinner(ctx, po, team, win, lose)
		if err != nil {
			b.reportBracketError(ctx, "set_winner", err)
			b.sendMessage(chatID, "Не удалось: "+err.Error(), "main_menu")
			return true
		}
		// The referee's decision closes the captains' report or dispute.
		b.service.CloseOpenReport(ctx, po)
		b.applyBracketChange(ctx, change)
		b.sendMessage(chatID, fmt.Sprintf("Матч #%d: победа '%s' %d:%d. Сброшено матчей дальше по сетке: %d.", po, team, win, lose, len(change.Reset)), "main_menu")
		b.notifyTournamentChat(fmt.Sprintf("РЕШЕНИЕ АДМИНА\n\nМатч #%d: победа '%s' %d:%d.", po, team, win, lose))
		return true
	}
	return false
}
