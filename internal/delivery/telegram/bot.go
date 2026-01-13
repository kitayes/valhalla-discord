package telegram

import (
	"fmt"
	"strings"
	"time"
	"valhalla/internal/application"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	bot      *tgbotapi.BotAPI
	service  application.TelegramService
	logger   application.Logger
	adminIDs map[int64]struct{}
}

func NewBot(token string, adminIDs []int64, service application.TelegramService, logger application.Logger) (*Bot, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	admins := make(map[int64]struct{})
	for _, id := range adminIDs {
		admins[id] = struct{}{}
	}

	logger.Info("Telegram bot authorized on account %s", bot.Self.UserName)

	return &Bot{
		bot:      bot,
		service:  service,
		logger:   logger,
		adminIDs: admins,
	}, nil
}

func (b *Bot) Start() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.bot.GetUpdatesChan(u)

	go b.startBackgroundWorker()

	for update := range updates {
		if update.Message == nil {
			continue
		}

		msg := update.Message
		chatID := msg.Chat.ID
		text := msg.Text
		user := msg.From

		if msg.Photo != nil && len(msg.Photo) > 0 {
			b.handlePhoto(chatID, msg)
			continue
		}

		b.service.RegisterUser(chatID, user.UserName, user.FirstName)

		if b.isAdmin(chatID) {
			b.handleAdminCommand(chatID, text)
			// Admins can also use user commands if not intercepted above,
			// but for now we separate them. If an admin command matches, it returns in handleAdminCommand.
			// However, if we want admins to use /start etc., we should check if handleAdminCommand handled it.
			// Current implementation of handleAdminCommand returns directly if it handles something.
			// But since it doesn't return a bool, we need to be careful.
			// Let's assume admins primarily use admin tools or specific commands.
			// Actually, to allow both, we should let handleAdminCommand execute only admin commands.
			// But for now, let's keep it simple: if it looks like an admin command, handle it.
			// The current handlers.go implementation returns on match.
			// So we need to call user command handler if admin didn't catch it?
			// Refactoring strategy: `handleAdminCommand` handles ONLY admin commands.
		}

		// Re-checking logic:
		// handleAdminCommand handles prefixes. If it matches, it does work.
		// If we want to support admins playing too, we should fallthrough.

		// Let's simplify:
		// If isAdmin -> try admin command. if distinct admin command, return.
		// Else -> handle user command.
		// For now, in handlers.go logic handles returns.

		// Wait, the handler logic I wrote in previous step for `handleAdminCommand` returns on match.
		// But it doesn't return anything to caller.
		// So it will just execute.
		// We should add a check inside `handleAdminCommand` or check prefixes here.

		// For minimal disruption:
		// If isAdmin and text starts with specific admin prefixes, calls admin handler.
		// Else calls user handler.

		if b.isAdmin(chatID) && (text == "/start" || text == "/admin" ||
			text == "/list_teams" || strings.HasPrefix(text, "/check_team") ||
			text == "/export" || text == "/list_solo" || text == "/export_solo" ||
			strings.HasPrefix(text, "/broadcast") || strings.HasPrefix(text, "/set_tourney") ||
			text == "/close_reg" || text == "/open_reg" || strings.HasPrefix(text, "/del_team") ||
			strings.HasPrefix(text, "/reset_user")) {

			b.handleAdminCommand(chatID, text)
			continue
		}

		b.handleUserCommand(chatID, text)
	}
}

func (b *Bot) runAdminHandlerIfMatched(chatID int64, text string) bool {
	// Helper to check if text is admin command
	// This is getting messy. Let's trust the updated Start loop logic above.
	return false
}

func (b *Bot) Stop() {
	b.bot.StopReceivingUpdates()
}

func (b *Bot) startBackgroundWorker() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		tTime := b.service.GetTournamentTime()
		if tTime.IsZero() {
			continue
		}

		now := time.Now()

		remindTime := tTime.Add(-30 * time.Minute)
		if now.Hour() == remindTime.Hour() && now.Minute() == remindTime.Minute() {
			b.broadcastCheckInReminder()
		}

		disqualifyTime := tTime.Add(10 * time.Minute)
		if now.Hour() == disqualifyTime.Hour() && now.Minute() == disqualifyTime.Minute() {
			b.processTechnicalDefeat()
		}
	}
}

func (b *Bot) broadcastCheckInReminder() {
	teams, _ := b.service.GetUncheckedTeams()
	tTime := b.service.GetTournamentTime()

	for _, team := range teams {
		for _, p := range team.Players {
			if p.IsCaptain && p.TelegramID != nil {
				msg := fmt.Sprintf("⚠ВНИМАНИЕ, Капитан!\nВаша команда '%s' не прошла Check-in.\n\nУ вас есть время до %s, чтобы нажать /checkin, иначе — ТЕХНИЧЕСКОЕ ПОРАЖЕНИЕ.",
					team.Name, tTime.Add(10*time.Minute).Format("15:04"))
				b.sendMessage(*p.TelegramID, msg, "empty")
			}
		}
	}
}

func (b *Bot) processTechnicalDefeat() {
	teams, _ := b.service.GetUncheckedTeams()
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
