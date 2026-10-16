package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"blackwatch/internal/application"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	// scheduleCatchUpWindow bounds how late a missed deadline may still fire, so
	// a restart does not replay an old tournament's notifications.
	scheduleCatchUpWindow = time.Hour
)

type Bot struct {
	matchDesk          *application.MatchDeskService
	deskWake           chan struct{}
	bot                *tgbotapi.BotAPI
	service            application.TelegramService
	profileLinkService application.ProfileLinkService
	bettingBot         *BettingBot
	bracket            *application.BracketService
	logger             application.Logger
	adminIDs           map[int64]struct{}
	// tournamentChatID is the Telegram chat/channel where tournament events
	// (team registrations, check-ins, deletions) are posted. Empty = disabled.
	tournamentChatID string
	// location is the zone /set_tourney dates are read in and schedule times
	// are printed in.
	location  *time.Location
	webAppURL string

	photoTimers map[int64]*time.Timer
	// seriesBatches gathers an album of game screenshots per chat; guarded by
	// photoTimersMu like photoTimers.
	seriesBatches map[int64]*seriesBatch
	photoTimersMu sync.Mutex

	// Tournament time each scheduled stage last fired for. Only touched by the
	// single background worker goroutine.
	// checkInFired holds, per check-in wave, the tournament time it last went
	// out for; rescheduling the tournament re-arms the waves.
	checkInFired [len(checkInWaves)]time.Time
	// qualifierAlert is the last qualifier-closing error sent to admins, so a
	// stuck qualifier is reported once rather than every minute.
	qualifierAlert string
	quotaAlerted   bool
}

func NewBot(token string, adminIDs []int64, service application.TelegramService, profileLinkService application.ProfileLinkService, bettingService *application.BettingService, telegramChannelID string, tournamentChatID string, bets BetSettings, bracket *application.BracketService, location *time.Location, logger application.Logger) (*Bot, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	admins := make(map[int64]struct{})
	for _, id := range adminIDs {
		admins[id] = struct{}{}
	}

	logger.Info("Telegram bot authorized on account %s", bot.Self.UserName)

	if location == nil {
		location = time.Local
	}
	b := &Bot{
		bot:                bot,
		service:            service,
		profileLinkService: profileLinkService,
		bracket:            bracket,
		logger:             logger,
		adminIDs:           admins,
		tournamentChatID:   tournamentChatID,
		location:           location,
		photoTimers:        make(map[int64]*time.Timer),
		seriesBatches:      make(map[int64]*seriesBatch),
	}

	// Initialize betting extension
	if bettingService != nil && telegramChannelID != "" {
		b.bettingBot = NewBettingBot(b, bettingService, telegramChannelID, bets, logger)
		logger.Info("Telegram betting engine initialized for channel %s", telegramChannelID)
	}

	if tournamentChatID != "" {
		logger.Info("Tournament notifications configured for chat %s", tournamentChatID)
	}
	if bracket != nil {
		logger.Info("Telegram bracket commands enabled (Challonge)")
	}

	return b, nil
}

// Username is the bot's @handle without the "@", as reported by Telegram.
func (b *Bot) Username() string {
	return b.bot.Self.UserName
}

// WithWebAppURL sets the public URL for the Telegram Mini App.
func (b *Bot) WithWebAppURL(url string) *Bot {
	b.webAppURL = url
	return b
}

// SendMatchNotification delivers a tournament notification to a captain with an optional WebApp button.
func (b *Bot) SendMatchNotification(chatID int64, text string, hasWebAppBtn bool) {
	if text == "" || chatID <= 0 {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	if hasWebAppBtn && b.webAppURL != "" {
		msg.ReplyMarkup = struct {
			Keyboard [][]struct {
				Text   string `json:"text"`
				WebApp struct {
					URL string `json:"url"`
				} `json:"web_app"`
			} `json:"inline_keyboard"`
		}{
			Keyboard: [][]struct {
				Text   string `json:"text"`
				WebApp struct {
					URL string `json:"url"`
				} `json:"web_app"`
			}{
				{
					{
						Text: "Открыть в приложении",
						WebApp: struct {
							URL string `json:"url"`
						}{URL: b.webAppURL},
					},
				},
			},
		}
	}
	if _, err := b.bot.Send(msg); err != nil {
		b.logger.Warn("telegram: failed to send match notification to %d: %v", chatID, err)
	}
}

// Start consumes updates until ctx is cancelled. Each update is handled under
// its own deadline, so one stuck query cannot wedge the whole update loop.
func (b *Bot) Start(ctx context.Context) {
	b.EnsureChatMenuButton(0)
	b.SetBotCommands()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.bot.GetUpdatesChan(u)

	go b.startBackgroundWorker(ctx)
	if b.matchDesk != nil {
		go b.startMatchDeskWorker(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Telegram bot: update loop stopped: %v", ctx.Err())
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			switch {
			case update.Message != nil:
				b.safely("message", func() { b.handleUpdate(ctx, update.Message) })
			case update.CallbackQuery != nil:
				b.safely("callback", func() { b.handleCallbackQuery(ctx, update.CallbackQuery) })
			}
		}
	}
}

// handleCallbackQuery routes inline-keyboard presses. Without it the betting
// buttons published to the Telegram channel were inert: the update loop only
// ever looked at update.Message, so every tap on "Team A"/"Team B" was dropped
// and the user was left with a spinner.
func (b *Bot) handleCallbackQuery(parent context.Context, callback *tgbotapi.CallbackQuery) {
	if callback.From == nil {
		return
	}
	b.logger.Info("telegram: callback query from user %d: %q", callback.From.ID, callback.Data)

	ctx, cancel := context.WithTimeout(parent, updateTimeout)
	defer cancel()
	if strings.HasPrefix(callback.Data, "desk:") {
		b.handleDeskCallback(ctx, callback)
		return
	}
	if strings.HasPrefix(callback.Data, gameCallbackPrefix) {
		b.handleGameCallback(ctx, callback)
		return
	}
	if strings.HasPrefix(callback.Data, tourneyCallbackPrefix) {
		b.handleTourneyCallback(ctx, callback)
		return
	}

	if callback.Data == "admin_ping_debtors" {
		if !b.isAdmin(callback.From.ID) {
			b.apiRespond(callback, "Только для администраторов.", true)
			return
		}
		b.apiRespond(callback, "Запуск оповещения должников...", false)
		b.handlePingDebtors(ctx, callback.From.ID, "")
		return
	}

	if strings.HasPrefix(callback.Data, "rep:") {
		if !b.isAdmin(callback.From.ID) {
			b.apiRespond(callback, "Отчёты о матчах подаются через турнирное приложение.", true)
			return
		}
		b.handleReportCallback(ctx, callback)
		return
	}

	if strings.HasPrefix(callback.Data, callbackRegPrefix+callbackSep) {
		if !b.isAdmin(callback.From.ID) {
			b.apiRespond(callback, "Регистрация и управление составом доступны в турнирном приложении.", true)
			return
		}
		b.handleRegCallback(ctx, callback)
		return
	}
	if b.bettingBot == nil {
		b.apiRespond(callback, "Ставки сейчас недоступны.", true)
		return
	}
	b.bettingBot.HandleCallback(ctx, callback)
}

func (b *Bot) handleUpdate(parent context.Context, msg *tgbotapi.Message) {
	ctx, cancel := context.WithTimeout(parent, updateTimeout)
	defer cancel()

	if !servesChat(msg.Chat) {
		if msg.Chat != nil {
			b.logger.Info("telegram: group/channel update: chat_id=%d title=%q text=%q", msg.Chat.ID, msg.Chat.Title, msg.Text)
			if strings.HasPrefix(msg.Text, "/id") || strings.HasPrefix(msg.Text, "/chatid") {
				reply := fmt.Sprintf("ID этого чата: %d\nНазвание: %s", msg.Chat.ID, msg.Chat.Title)
				sendMsg := tgbotapi.NewMessage(msg.Chat.ID, reply)
				_, _ = b.bot.Send(sendMsg)
			}
		}
		return
	}
	chatID := msg.Chat.ID
	text := msg.Text
	user := msg.From
	username := ""
	if user != nil {
		username = user.UserName
	}
	b.logger.Info("telegram: received private message: chat_id=%d user=%q text=%q", chatID, username, text)

	if len(msg.Photo) > 0 {
		b.handlePhoto(ctx, chatID, msg)
		return
	}

	if user != nil {
		b.service.RegisterUser(ctx, chatID, user.UserName, user.FirstName)
	}

	if b.handleDeskCommand(ctx, chatID, text) {
		return
	}
	if b.handleBracketCommand(ctx, chatID, text) {
		return
	}
	if b.isAdmin(chatID) && isAdminCommand(text) {
		b.handleAdminCommand(ctx, chatID, text)
		return
	}

	b.handleUserCommand(ctx, chatID, text, username)
}

// isAdminCommand reports whether handleAdminCommand owns this text. Keep it
// in step with that handler: a command missing here is not rejected, it is
// answered by the user handler as an unknown command.
func isAdminCommand(text string) bool {
	switch text {
	case "/admin", "/list_teams", "/checkin_status", "/checkins",
		"/export", "/export_solo", "/export_sheet", "/list_solo",
		"/close_reg", "/open_reg", "/reports",
		"/ping_debtors", "/ping", "/remind_debtors", "/remind_checkin",
		"/tournaments", "/finish_tourney", "/next_qual":
		return true
	}
	// These take an argument. The prefixes are bare, as they have always
	// been, so a bare "/del_team" still reaches the handler that explains the
	// expected form instead of being answered as an unknown command.
	for _, prefix := range []string{
		"/check_team", "/broadcast", "/set_tourney", "/del_team",
		"/reinstate", "/reset_user", "/new_tourney", "/select_tourney",
		"/ping_debtors ", "/ping ", "/remind_debtors ", "/remind_checkin ",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// safely runs fn and turns a panic into a log line. Handlers here run on the
// polling goroutine, and the Discord bot shares the process: before this, one
// bad dereference in a Telegram handler took both bots down.
func (b *Bot) safely(what string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("telegram: PANIC recovered in %s: %v\nStack:\n%s", what, r, string(debug.Stack()))
		}
	}()
	fn()
}

// servesChat reports whether the bot talks in this chat at all. Players are
// keyed by chat id, so a group would be registered as a player and answered
// in front of everyone; the bot only works one-to-one.
func servesChat(chat *tgbotapi.Chat) bool {
	return chat != nil && chat.IsPrivate()
}

// tournamentLayout is how admins type the date: "20.05.2026 18:00".
const tournamentLayout = "02.01.2006 15:04"

// parseTournamentTime reads a /set_tourney date in the configured zone and
// builds the confirmation line, zone included, so a mismatch between the
// admin's clock and the server's is visible right there.
func (b *Bot) parseTournamentTime(dateStr string) (time.Time, string, error) {
	t, err := time.ParseInLocation(tournamentLayout, strings.TrimSpace(dateStr), b.location)
	if err != nil {
		return time.Time{}, "", err
	}
	waves := make([]string, 0, len(checkInWaves))
	for _, lead := range checkInWaves {
		waves = append(waves, t.Add(-lead).Format("15:04"))
	}
	summary := fmt.Sprintf("Время турнира: %s (%s)\nРегистрация закроется: %s\nНапоминания о check-in: %s\nCheck-in закроется и сетка построится: %s",
		t.Format(tournamentLayout), b.location,
		t.Add(-application.RegistrationCloseLead).Format("15:04"),
		strings.Join(waves, ", "),
		t.Format("15:04"))
	return t, summary, nil
}

func (b *Bot) Stop() {
	b.bot.StopReceivingUpdates()
}

// startBackgroundWorker fires the check-in reminder and technical-defeat sweeps.
// It now exits on cancellation and stops its ticker — previously it ran for the
// lifetime of the process with no way to shut down.
func (b *Bot) startBackgroundWorker(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		tickCtx, cancel := context.WithTimeout(ctx, updateTimeout)
		b.safely("scheduled checks", func() { b.runScheduledChecks(tickCtx) })
		cancel()
	}
}

// runScheduledChecks fires the check-in reminder once per tournament and runs
// the bracket checks: the build at the start, forfeits, queued reports and the
// end of a qualifier.
//
// It used to compare only the hour and minute of the current time against the
// tournament time. That fired every day at that time of day regardless of the
// tournament's date, ran twice whenever two ticks landed in the same minute, and
// skipped the event entirely if a tick was late. Now each deadline is compared
// as a full timestamp and remembered once it has fired.
func (b *Bot) runScheduledChecks(ctx context.Context) {
	b.autoConfirmReports(ctx)
	b.autoConfirmGames(ctx)

	tTime := b.service.GetTournamentTime(ctx)
	if tTime.IsZero() {
		return
	}

	now := time.Now()

	if lead, ok := b.dueCheckInWave(tTime, now); ok {
		b.broadcastCheckInReminder(ctx, lead)
	}

	b.runBracketChecks(ctx, tTime, now)
}

// autoConfirmReports applies match results the opposing captain never
// answered, so an absent opponent cannot hold the bracket. Runs every tick,
// independent of the tournament clock.
func (b *Bot) autoConfirmReports(ctx context.Context) {
	done, err := b.service.AutoConfirmExpiredReports(ctx)
	if err != nil {
		b.logger.Error("telegram: auto-confirm sweep failed: %v", err)
		return
	}
	for _, rep := range done {
		b.logger.Info("telegram: report %d auto-confirmed (%s %s %s)", rep.ID, rep.WinnerTeamName, rep.Score, rep.LoserTeamName)
		b.notifyTournamentChat(fmt.Sprintf("РЕЗУЛЬТАТ МАТЧА\n\n%s %s %s — принят автоматически (соперник не возразил).",
			rep.WinnerTeamName, rep.Score, rep.LoserTeamName))
	}
	if len(done) > 0 && b.matchDesk != nil {
		b.wakeMatchDesk()
	}
}

// checkInWaves are how long before the start the check-in reminders go out.
var checkInWaves = [...]time.Duration{60 * time.Minute, 30 * time.Minute, 10 * time.Minute}

// dueCheckInWave picks the check-in reminder to send now: the latest wave whose
// time has come and that has not gone out for this tournament. Waves missed
// while the bot was down are marked sent with it, so a late start sends one
// reminder, not three at once; nothing goes out once the tournament started.
func (b *Bot) dueCheckInWave(tournament, now time.Time) (time.Duration, bool) {
	if tournament.IsZero() || !now.Before(tournament) {
		return 0, false
	}
	for i := len(checkInWaves) - 1; i >= 0; i-- {
		if now.Before(tournament.Add(-checkInWaves[i])) {
			continue
		}
		if b.checkInFired[i].Equal(tournament) {
			return 0, false
		}
		for j := 0; j <= i; j++ {
			b.checkInFired[j] = tournament
		}
		return checkInWaves[i], true
	}
	return 0, false
}

// broadcastCheckInReminder reaches every player of every entered team that has
// not checked in; lead is how long is left before the start.
func (b *Bot) broadcastCheckInReminder(ctx context.Context, lead time.Duration) {
	teams, _ := b.service.GetUncheckedTeams(ctx)
	tTime := b.service.GetTournamentTime(ctx)
	left := fmt.Sprintf("До старта %d минут. ", int(lead.Minutes()))

	for _, team := range teams {
		for _, p := range team.Players {
			if p.TelegramID != nil && *p.TelegramID > 0 {
				if p.IsCaptain {
					if application.MainRosterSize(team) < application.MainRosterSlots {
						msg := left + fmt.Sprintf("ВНИМАНИЕ, Капитан!\nВ вашей команде '%s' не хватает игроков (%d из %d).\n\nСрочно доберите основной состав (минимум %d игроков) и подтвердите участие до %s, иначе команда не попадёт в сетку.",
							team.Name, application.MainRosterSize(team), application.MainRosterSlots, application.MainRosterSlots, tTime.In(b.location).Format("15:04"))
						b.sendMessage(*p.TelegramID, msg, "empty")
					} else {
						msg := left + fmt.Sprintf("ВНИМАНИЕ, Капитан!\nВаша команда '%s' не прошла Check-in.\n\nПодтвердите участие в приложении до %s, иначе команда не попадёт в сетку.",
							team.Name, tTime.In(b.location).Format("15:04"))
						if b.webAppURL != "" {
							b.SendMatchNotification(*p.TelegramID, msg, true)
						} else {
							b.sendMessage(*p.TelegramID, msg, application.KbRegCheckin)
						}
					}
				} else {
					msg := left + fmt.Sprintf("ВНИМАНИЕ!\nВаша команда '%s' ещё не прошла Check-in.\nНапомните капитану подтвердить участие в приложении до %s, иначе команда не попадёт в сетку.",
						team.Name, tTime.In(b.location).Format("15:04"))
					if b.webAppURL != "" {
						b.SendMatchNotification(*p.TelegramID, msg, true)
					} else {
						b.sendMessage(*p.TelegramID, msg, "empty")
					}
				}
			}
		}
	}
}

func (b *Bot) BettingBot() *BettingBot {
	return b.bettingBot
}

// EnsureChatMenuButton sets the Telegram chat menu button to point to the current Mini App URL.
// If chatID is 0, it sets the default menu button for all users.
func (b *Bot) EnsureChatMenuButton(chatID int64) {
	if b.webAppURL == "" {
		return
	}
	payload := map[string]interface{}{
		"menu_button": map[string]interface{}{
			"type": "web_app",
			"text": "Турнир",
			"web_app": map[string]string{
				"url": b.webAppURL,
			},
		},
	}
	if chatID > 0 {
		payload["chat_id"] = chatID
	}
	data, _ := json.Marshal(payload)
	resp, err := http.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/setChatMenuButton", b.bot.Token),
		"application/json",
		bytes.NewReader(data),
	)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}

// SetBotCommands registers standard bot commands with Telegram so they appear in the UI autocomplete.
func (b *Bot) SetBotCommands() {
	commands := []map[string]string{
		{"command": "start", "description": "Главное меню и приложение"},
		{"command": "app", "description": "Открыть турнирное приложение"},
		{"command": "bracket", "description": "Турнирная сетка"},
	}
	data, _ := json.Marshal(map[string]interface{}{"commands": commands})
	resp, err := http.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", b.bot.Token),
		"application/json",
		bytes.NewReader(data),
	)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}
