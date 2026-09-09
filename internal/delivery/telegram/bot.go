package telegram

import (
	"blackwatch/internal/application"
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	// checkInReminderLead is how long before the tournament captains are nudged.
	checkInReminderLead = 30 * time.Minute
	// technicalDefeatGrace is how long after the start teams have to check in.
	technicalDefeatGrace = 10 * time.Minute
	// scheduleCatchUpWindow bounds how late a missed deadline may still fire, so
	// a restart does not replay an old tournament's notifications.
	scheduleCatchUpWindow = time.Hour
)

type Bot struct {
	bot                *tgbotapi.BotAPI
	service            application.TelegramService
	profileLinkService application.ProfileLinkService
	bettingBot         *BettingBot
	logger             application.Logger
	adminIDs           map[int64]struct{}

	// Tournament time each scheduled stage last fired for. Only touched by the
	// single background worker goroutine.
	remindedFor     time.Time
	disqualifiedFor time.Time
}

func NewBot(token string, adminIDs []int64, service application.TelegramService, profileLinkService application.ProfileLinkService, bettingService *application.BettingService, telegramChannelID string, logger application.Logger) (*Bot, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	admins := make(map[int64]struct{})
	for _, id := range adminIDs {
		admins[id] = struct{}{}
	}

	logger.Info("Telegram bot authorized on account %s", bot.Self.UserName)

	b := &Bot{
		bot:                bot,
		service:            service,
		profileLinkService: profileLinkService,
		logger:             logger,
		adminIDs:           admins,
	}

	// Initialize betting extension
	if bettingService != nil && telegramChannelID != "" {
		b.bettingBot = NewBettingBot(b, bettingService, telegramChannelID, logger)
		logger.Info("Telegram betting engine initialized for channel %s", telegramChannelID)
	}

	return b, nil
}

// Start consumes updates until ctx is cancelled. Each update is handled under
// its own deadline, so one stuck query cannot wedge the whole update loop.
func (b *Bot) Start(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.bot.GetUpdatesChan(u)

	go b.startBackgroundWorker(ctx)

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
				b.handleUpdate(ctx, update.Message)
			case update.CallbackQuery != nil:
				b.handleCallbackQuery(ctx, update.CallbackQuery)
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
	if b.bettingBot == nil {
		b.apiRespond(callback, "Ставки сейчас недоступны.", true)
		return
	}

	ctx, cancel := context.WithTimeout(parent, updateTimeout)
	defer cancel()

	b.bettingBot.HandleCallback(ctx, callback)
}

func (b *Bot) handleUpdate(parent context.Context, msg *tgbotapi.Message) {
	ctx, cancel := context.WithTimeout(parent, updateTimeout)
	defer cancel()

	chatID := msg.Chat.ID
	text := msg.Text
	user := msg.From
	username := ""
	if user != nil {
		username = user.UserName
	}

	if len(msg.Photo) > 0 {
		b.handlePhoto(ctx, chatID, msg)
		return
	}

	if user != nil {
		b.service.RegisterUser(ctx, chatID, user.UserName, user.FirstName)
	}

	if b.isAdmin(chatID) && (text == "/admin" ||
		text == "/list_teams" || strings.HasPrefix(text, "/check_team") ||
		text == "/export" || text == "/list_solo" || text == "/export_solo" ||
		strings.HasPrefix(text, "/broadcast") || strings.HasPrefix(text, "/set_tourney") ||
		text == "/close_reg" || text == "/open_reg" || strings.HasPrefix(text, "/del_team") ||
		strings.HasPrefix(text, "/reset_user")) {

		b.handleAdminCommand(ctx, chatID, text)
		return
	}

	b.handleUserCommand(ctx, chatID, text, username)
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
		b.runScheduledChecks(tickCtx)
		cancel()
	}
}

// runScheduledChecks fires the check-in reminder and the technical-defeat sweep
// once each per tournament.
//
// It used to compare only the hour and minute of the current time against the
// tournament time. That fired every day at that time of day regardless of the
// tournament's date, ran twice whenever two ticks landed in the same minute, and
// skipped the event entirely if a tick was late. Now each deadline is compared
// as a full timestamp and remembered once it has fired.
func (b *Bot) runScheduledChecks(ctx context.Context) {
	tTime := b.service.GetTournamentTime(ctx)
	if tTime.IsZero() {
		return
	}

	now := time.Now()

	if b.shouldFire(tTime, tTime.Add(-checkInReminderLead), now, &b.remindedFor) {
		b.broadcastCheckInReminder(ctx)
	}

	if b.shouldFire(tTime, tTime.Add(technicalDefeatGrace), now, &b.disqualifiedFor) {
		b.processTechnicalDefeat(ctx)
	}
}

// shouldFire reports whether a deadline has passed and has not been handled yet
// for this tournament. lastFired holds the tournament time the stage last ran
// for, so rescheduling the tournament re-arms it.
func (b *Bot) shouldFire(tournament, deadline, now time.Time, lastFired *time.Time) bool {
	if now.Before(deadline) {
		return false
	}
	// Do not fire for a deadline that passed long before the bot came up — a
	// restart days later must not blast out a stale tournament's notices.
	if now.Sub(deadline) > scheduleCatchUpWindow {
		return false
	}
	if lastFired.Equal(tournament) {
		return false
	}
	*lastFired = tournament
	return true
}

func (b *Bot) broadcastCheckInReminder(ctx context.Context) {
	teams, _ := b.service.GetUncheckedTeams(ctx)
	tTime := b.service.GetTournamentTime(ctx)

	for _, team := range teams {
		for _, p := range team.Players {
			if p.IsCaptain && p.TelegramID != nil {
				msg := fmt.Sprintf("ВНИМАНИЕ, Капитан!\nВаша команда '%s' не прошла Check-in.\n\nУ вас есть время до %s, чтобы нажать /checkin, иначе — ТЕХНИЧЕСКОЕ ПОРАЖЕНИЕ.",
					team.Name, tTime.Add(10*time.Minute).Format("15:04"))
				b.sendMessage(*p.TelegramID, msg, "empty")
			}
		}
	}
}

func (b *Bot) processTechnicalDefeat(ctx context.Context) {
	teams, _ := b.service.GetUncheckedTeams(ctx)
	if len(teams) == 0 {
		return
	}

	var report strings.Builder
	report.WriteString("СПИСОК ТЕХ. ПОРАЖЕНИЙ (Не прошли чекин):\n\n")

	for _, team := range teams {
		report.WriteString(fmt.Sprintf("- %s\n", team.Name))

		for _, p := range team.Players {
			if p.IsCaptain && p.TelegramID != nil {
				b.sendMessage(*p.TelegramID, "ТЕХНИЧЕСКОЕ ПОРАЖЕНИЕ.\nВы не подтвердили участие вовремя. Ваша команда снята с турнира.", "empty")
			}
		}
	}

	for adminID := range b.adminIDs {
		b.sendMessage(adminID, report.String(), "empty")
	}
}

func (b *Bot) BettingBot() *BettingBot {
	return b.bettingBot
}
