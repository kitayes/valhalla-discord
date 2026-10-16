package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Series played game by game: any player sends a game's screenshots here, says
// who won, and any player of the other team confirms. Callbacks:
//
//	game:claim:<matchID>:<1 we won | 0 they won>
//	game:ok:<gameID>   game:no:<gameID>
//	game:judge:<gameID>:<winner team id | 0 to strike the game out>
const gameCallbackPrefix = "game:"

// albumSettle is how long the bot waits for the rest of an album before it
// asks who won: Telegram delivers every photo of an album as its own message.
const albumSettle = 700 * time.Millisecond

type seriesBatch struct {
	prompt *application.SeriesPrompt
	err    error
	timer  *time.Timer
}

// handleSeriesPhoto takes a screenshot sent outside any dialogue as a game of
// the player's current series.
func (b *Bot) handleSeriesPhoto(ctx context.Context, chatID int64, photoID string) {
	prompt, err := b.service.AddGamePhoto(ctx, chatID, photoID)

	b.photoTimersMu.Lock()
	defer b.photoTimersMu.Unlock()
	if b.seriesBatches == nil {
		b.seriesBatches = make(map[int64]*seriesBatch)
	}
	batch := b.seriesBatches[chatID]
	if batch == nil {
		batch = &seriesBatch{}
		b.seriesBatches[chatID] = batch
	}
	if err != nil {
		batch.err = err
	} else {
		batch.prompt, batch.err = prompt, nil
	}
	if batch.timer != nil {
		batch.timer.Stop()
	}
	batch.timer = time.AfterFunc(albumSettle, func() {
		b.photoTimersMu.Lock()
		done := b.seriesBatches[chatID]
		delete(b.seriesBatches, chatID)
		b.photoTimersMu.Unlock()
		if done == nil {
			return
		}
		if done.prompt == nil {
			b.sendMessage(chatID, capitalize(done.err.Error())+".", "empty")
			return
		}
		b.sendSeriesPrompt(chatID, done.prompt)
	})
}

func (b *Bot) sendSeriesPrompt(chatID int64, p *application.SeriesPrompt) {
	text := fmt.Sprintf("Матч #%d: %s vs %s\n%s\n\nКатка %d из максимум %d. Скринов получено: %d.\nКто победил в этой катке?\n\nМожно дослать ещё скрины до нажатия кнопки.",
		p.Match.PlayOrder, p.Match.Team1Name, p.Match.Team2Name, application.SeriesLine(p.Match, p.Series), p.GameNo, p.Series.BO, p.Photos)
	b.sendInline(chatID, text, tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Победили мы", fmt.Sprintf("game:claim:%d:1", p.Match.ID)),
		tgbotapi.NewInlineKeyboardButtonData("Победил соперник", fmt.Sprintf("game:claim:%d:0", p.Match.ID)),
	)))
}

func (b *Bot) handleGameCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil || cb.Message.Chat == nil || !cb.Message.Chat.IsPrivate() {
		b.apiRespond(cb, "Откройте личный чат с ботом.", true)
		return
	}
	parts := strings.Split(strings.TrimPrefix(cb.Data, gameCallbackPrefix), ":")
	num := func(i int) int {
		if i >= len(parts) {
			return -1
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return -1
		}
		return n
	}
	actor := cb.From.ID
	var ev *application.GameEvent
	var err error
	switch {
	case parts[0] == "claim" && len(parts) == 3:
		ev, err = b.service.ClaimGame(ctx, actor, num(1), parts[2] == "1")
		if err == nil {
			b.announceGameClaimed(ctx, actor, ev)
		}
	case parts[0] == "ok" && len(parts) == 2:
		ev, err = b.service.ConfirmGame(ctx, actor, num(1))
		if err == nil {
			b.announceGameCounted(ctx, ev, "")
		}
	case parts[0] == "no" && len(parts) == 2:
		ev, err = b.service.DisputeGame(ctx, actor, num(1))
		if err == nil {
			b.announceGameDisputed(ctx, ev)
		}
	case parts[0] == "judge" && len(parts) == 3:
		if !b.isAdmin(actor) {
			b.apiRespond(cb, "Решение по катке принимает судья.", true)
			return
		}
		ev, err = b.service.JudgeGame(ctx, num(1), num(2))
		if err == nil {
			if ev.Game.Status == models.GameAnnulled {
				b.announceGameAnnulled(ctx, ev)
			} else {
				b.announceGameCounted(ctx, ev, "Решение судьи. ")
			}
		}
	default:
		b.apiRespond(cb, "Кнопка устарела.", true)
		return
	}
	if err != nil {
		b.apiRespond(cb, capitalize(err.Error()), true)
		return
	}
	b.apiRespond(cb, "Готово", false)
	// The answer is given; the buttons must not be pressed twice.
	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})
	if _, err := b.bot.Request(edit); err != nil {
		b.logger.Warn("telegram: clear game buttons: %v", err)
	}
	if ev != nil && ev.Final != nil {
		b.wakeMatchDesk()
	}
}

func teamName(m models.BracketMatch, id int) string {
	if m.Team1ID != nil && *m.Team1ID == id {
		return m.Team1Name
	}
	if m.Team2ID != nil && *m.Team2ID == id {
		return m.Team2Name
	}
	return fmt.Sprintf("Команда #%d", id)
}

// reportingTeam is the team whose player reported the game.
func (b *Bot) reportingTeam(ctx context.Context, g models.TelegramMatchGame) int {
	p, _ := b.service.GetPlayer(ctx, g.ReporterTelegramID)
	if p == nil || p.TeamID == nil {
		return 0
	}
	return *p.TeamID
}

// announceGameClaimed tells the reporter's team the game went out and asks
// every player of the other team to confirm it: the captain is often in the
// draft of the next game, and an unanswered game counts by default.
func (b *Bot) announceGameClaimed(ctx context.Context, actor int64, ev *application.GameEvent) {
	g, m := ev.Game, ev.Match
	reporter := b.reportingTeam(ctx, g)
	opponent := 0
	if o := m.Opponent(reporter); o != nil {
		opponent = *o
	}
	sent := fmt.Sprintf("Катка %d (победа «%s») отправлена команде «%s» на подтверждение. Без ответа за %d минут она засчитается автоматически.",
		g.GameNo, teamName(m, g.WinnerTeamID), teamName(m, opponent), int(application.GameConfirmWindow.Minutes()))
	chats := b.teamChatIDs(ctx, reporter)
	if len(chats) == 0 {
		chats = []int64{actor}
	}
	for _, chatID := range chats {
		b.sendMessage(chatID, sent, "empty")
	}
	caption := fmt.Sprintf("Матч #%d, катка %d: команда «%s» сообщает, что катку выиграла команда «%s».",
		m.PlayOrder, g.GameNo, teamName(m, reporter), teamName(m, g.WinnerTeamID))
	for _, chatID := range b.teamChatIDs(ctx, opponent) {
		b.sendReportMediaToChat(chatID, g.PhotoFileIDs, caption)
		b.sendInline(chatID, caption+fmt.Sprintf("\n\nВсё верно? Ответить может любой игрок команды. Без ответа за %d минут катка засчитается.", int(application.GameConfirmWindow.Minutes())),
			tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Подтверждаю", fmt.Sprintf("game:ok:%d", g.ID)),
				tgbotapi.NewInlineKeyboardButtonData("Оспорить", fmt.Sprintf("game:no:%d", g.ID)),
			)))
	}
}

// announceGameCounted tells both teams a game counts, with the series score,
// and closes the series in the chat when it decided it.
func (b *Bot) announceGameCounted(ctx context.Context, ev *application.GameEvent, prefix string) {
	g, m, st := ev.Game, ev.Match, ev.Series
	text := fmt.Sprintf("%sМатч #%d, катка %d засчитана: победа «%s».\n%s", prefix, m.PlayOrder, g.GameNo, teamName(m, g.WinnerTeamID), application.SeriesLine(m, st))
	switch {
	case st.Decided() && ev.Queued:
		text += fmt.Sprintf("\n\nСерия завершена: «%s» побеждает. Сетка сейчас недоступна — результат сохранён, бот запишет его автоматически в течение нескольких минут.", teamName(m, st.WinnerID))
	case st.Decided():
		text += fmt.Sprintf("\n\nСерия завершена: «%s» побеждает. Результат записан в сетку.", teamName(m, st.WinnerID))
	default:
		text += fmt.Sprintf("\n\nДальше катка %d из максимум %d. После неё любой игрок команды присылает скрин сюда же.", st.NextGameNo, st.BO)
	}
	for _, id := range []*int{m.Team1ID, m.Team2ID} {
		if id != nil {
			b.notifyTeam(ctx, *id, text, "empty")
		}
	}
	if st.Decided() && m.Team1ID != nil && m.Team2ID != nil {
		loser := *m.Opponent(st.WinnerID)
		b.notifyTournamentChat(fmt.Sprintf("РЕЗУЛЬТАТ МАТЧА\n\nМатч #%d: %s %d:%d %s (%s).",
			m.PlayOrder, teamName(m, st.WinnerID), st.Wins[st.WinnerID], st.Wins[loser], teamName(m, loser), application.FormatLong(st.BO)))
	}
}

func (b *Bot) announceGameDisputed(ctx context.Context, ev *application.GameEvent) {
	g, m := ev.Game, ev.Match
	reporter := b.reportingTeam(ctx, g)
	for _, chatID := range b.teamChatIDs(ctx, reporter) {
		b.sendMessage(chatID, fmt.Sprintf("Соперник оспорил катку %d матча #%d. Судья посмотрит скрины и решит — результат придёт сюда.", g.GameNo, m.PlayOrder), "empty")
	}
	caption := fmt.Sprintf("СПОР ПО КАТКЕ\n\nМатч #%d: %s vs %s, катка %d.\nКоманда «%s» заявляет победу «%s», соперник не согласен.\n%s",
		m.PlayOrder, m.Team1Name, m.Team2Name, g.GameNo, teamName(m, reporter), teamName(m, g.WinnerTeamID), application.SeriesLine(m, ev.Series))
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, id := range []*int{m.Team1ID, m.Team2ID} {
		if id != nil {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(
				"Засчитать «"+teamName(m, *id)+"»", fmt.Sprintf("game:judge:%d:%d", g.ID, *id))))
		}
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Аннулировать катку", fmt.Sprintf("game:judge:%d:0", g.ID))))
	for adminID := range b.adminIDs {
		b.sendReportMediaToChat(adminID, g.PhotoFileIDs, caption)
		b.sendInline(adminID, caption, tgbotapi.NewInlineKeyboardMarkup(rows...))
	}
}

func (b *Bot) announceGameAnnulled(ctx context.Context, ev *application.GameEvent) {
	m := ev.Match
	text := fmt.Sprintf("Судья аннулировал катку %d матча #%d — её нужно переиграть.\n%s\n\nПосле катки любой игрок команды присылает скрин сюда же.", ev.Game.GameNo, m.PlayOrder, application.SeriesLine(m, ev.Series))
	for _, id := range []*int{m.Team1ID, m.Team2ID} {
		if id != nil {
			b.notifyTeam(ctx, *id, text, "empty")
		}
	}
}

// autoConfirmGames counts the games nobody answered in time.
func (b *Bot) autoConfirmGames(ctx context.Context) {
	evs, err := b.service.AutoConfirmExpiredGames(ctx)
	if err != nil {
		b.logger.Error("telegram: auto-confirm games: %v", err)
		return
	}
	decided := false
	for i := range evs {
		b.announceGameCounted(ctx, &evs[i], "Соперник не ответил вовремя. ")
		decided = decided || evs[i].Final != nil
	}
	if decided {
		b.wakeMatchDesk()
	}
}

func (b *Bot) sendInline(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = kb
	if _, err := b.bot.Send(msg); err != nil {
		b.logger.Warn("telegram: failed to send message to %d: %v", chatID, err)
	}
}

// capitalize upper-cases the first letter of a service error for a reply.
func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
