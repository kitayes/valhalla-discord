package telegram

import (
	"fmt"
	"strings"

	"blackwatch/internal/application"
	"blackwatch/internal/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	callbackBetTeamA = "bet_team_a"
	callbackBetTeamB = "bet_team_b"
)

type BettingBot struct {
	bot            *Bot
	bettingService *application.BettingService
	logger         application.Logger
	channelID      string
}

func NewBettingBot(bot *Bot, bettingService *application.BettingService, channelID string, logger application.Logger) *BettingBot {
	return &BettingBot{
		bot:            bot,
		bettingService: bettingService,
		logger:         logger,
		channelID:      channelID,
	}
}

func (bb *BettingBot) NotifyMatchLive(matchID int, captainA, captainB string) {
	if bb.channelID == "" {
		bb.logger.Warn("betting: TELEGRAM_CHANNEL_ID not configured — skipping match notification")
		return
	}

	text := fmt.Sprintf(
		"МАТЧ #%d АКТИВЕН!**\n\n"+
			"Team A: Капитан %s\n"+
			"Team B: Капитан %s\n\n"+
			"Ставьте свои очки! У вас есть 100 очков по умолчанию.\n"+
			"Используйте кнопки ниже, чтобы сделать ставку.",
		matchID, captainA, captainB,
	)

	inlineKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("Team A — %s", captainA),
				fmt.Sprintf("%s_%d", callbackBetTeamA, matchID),
			),
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("Team B — %s", captainB),
				fmt.Sprintf("%s_%d", callbackBetTeamB, matchID),
			),
		),
	)

	channelChatID := bb.resolveChannelID()
	if channelChatID == 0 {
		bb.logger.Error("betting: failed to resolve channel ID %q", bb.channelID)
		return
	}

	msg := tgbotapi.NewMessage(channelChatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = inlineKeyboard

	_, err := bb.bot.bot.Send(msg)
	if err != nil {
		bb.logger.Error("betting: failed to send match notification to channel: %v", err)
	} else {
		bb.logger.Info("betting: match #%d published to Telegram channel", matchID)
	}
}

func (bb *BettingBot) HandleCallback(callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	userID := callback.From.ID
	username := callback.From.UserName

	bb.logger.Debug("betting: callback from %s (%d): %s", username, userID, data)

	if strings.HasPrefix(data, callbackBetTeamA) || strings.HasPrefix(data, callbackBetTeamB) {
		bb.handleBetCallback(callback)
		return
	}
}

func (bb *BettingBot) handleBetCallback(callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	userID := callback.From.ID
	username := callback.From.UserName

	var teamChosen string
	var matchID int
	if strings.HasPrefix(data, callbackBetTeamA) {
		teamChosen = "Team A"
		fmt.Sscanf(data, callbackBetTeamA+"_%d", &matchID)
	} else {
		teamChosen = "Team B"
		fmt.Sscanf(data, callbackBetTeamB+"_%d", &matchID)
	}

	// Check if betting window is still open
	if bb.bettingService != nil {
		open, err := bb.bettingService.IsBettingOpen(matchID)
		if err == nil && !open {
			bb.bot.apiRespond(callback, "⌛️ Ставки заблокированы. Игра перешла в мид-гейм.", true)
			return
		}
	}

	points, err := bb.bettingService.GetPlayerPoints(userID)
	if err != nil {
		bb.bot.apiRespond(callback, fmt.Sprintf("Ваш Telegram не привязан к игроку. Используйте /link <код> в Discord."), true)
		return
	}

	if points <= 0 {
		bb.bot.apiRespond(callback, "У вас нет очков для ставок. Заработайте очки участием в матчах.", true)
		return
	}

	amount := points
	if amount > 50 {
		amount = 50
	}

	req := models.PlaceBetRequest{
		MatchID:    matchID,
		TgUserID:   userID,
		TeamChosen: teamChosen,
		Amount:     amount,
	}

	err = bb.bettingService.PlaceBet(req)
	if err != nil {
		bb.logger.Error("betting: user %d failed to place bet: %v", userID, err)
		bb.bot.apiRespond(callback, fmt.Sprintf("Ошибка ставки: %s", err.Error()), true)
		return
	}

	bb.bot.apiRespond(callback, fmt.Sprintf(
		"Ставка принята!\n\n Матч #%d\n Команда: %s\n Сумма: %d очков\n\nОжидайте результатов матча.",
		matchID, teamChosen, amount,
	), true)

	bb.logger.Info("betting: user %d (%s) bet %d on %s (match #%d)", userID, username, amount, teamChosen, matchID)
}
func (bb *BettingBot) NotifyMatchResult(matchID int, winningTeam string, payouts map[int64]int) {

	if bb.channelID == "" {
		return
	}

	channelChatID := bb.resolveChannelID()
	if channelChatID == 0 {
		return
	}

	summary := application.FormatPayoutSummary(payouts, winningTeam)

	msg := tgbotapi.NewMessage(channelChatID, summary)
	msg.ParseMode = "Markdown"

	_, err := bb.bot.bot.Send(msg)
	if err != nil {
		bb.logger.Error("betting: failed to send match result to channel: %v", err)
	} else {
		bb.logger.Info("betting: match #%d result published to Telegram channel", matchID)
	}
}

func (bb *BettingBot) HandleBetCommand(chatID int64, text string) string {
	parts := strings.Fields(text)
	if len(parts) < 3 {
		return "Используйте: `/bet <ID матча> <Team A|Team B> <сумма>`\nНапример: `/bet 42 Team A 25`"
	}

	matchID := 0
	fmt.Sscanf(parts[1], "%d", &matchID)
	teamChosen := parts[2]
	amount := 0
	if len(parts) >= 4 {
		fmt.Sscanf(parts[3], "%d", &amount)
	}

	if matchID <= 0 {
		return "Неверный ID матча."
	}
	if teamChosen != "Team A" && teamChosen != "Team B" {
		return "Команда должна быть 'Team A' или 'Team B'."
	}
	if amount <= 0 {
		amount = 10
	}

	req := models.PlaceBetRequest{
		MatchID:    matchID,
		TgUserID:   chatID,
		TeamChosen: teamChosen,
		Amount:     amount,
	}

	err := bb.bettingService.PlaceBet(req)
	if err != nil {
		return fmt.Sprintf("Ошибка ставки: %s", err.Error())
	}

	return fmt.Sprintf("Ставка принята!\n\n Матч #%d\n Команда: **%s**\n Сумма: %d очков",
		matchID, teamChosen, amount)
}

func (bb *BettingBot) HandleBalanceCommand(chatID int64) string {
	points, err := bb.bettingService.GetPlayerPoints(chatID)
	if err != nil {
		return fmt.Sprintf("Ваш Telegram не привязан к игроку. Используйте /link <код> в Discord.")
	}
	return fmt.Sprintf("Ваш баланс: %d очков", points)
}

func (bb *BettingBot) resolveChannelID() int64 {
	if bb.channelID == "" {
		return 0
	}

	var id int64
	if _, err := fmt.Sscanf(bb.channelID, "%d", &id); err == nil && id != 0 {
		return id
	}

	chatConfig := tgbotapi.ChatConfig{
		ChatID: 0,
	}
	_ = chatConfig

	chat, err := bb.bot.bot.GetChat(tgbotapi.ChatInfoConfig{
		ChatConfig: tgbotapi.ChatConfig{
			SuperGroupUsername: strings.TrimPrefix(bb.channelID, "@"),
		},
	})
	if err != nil {
		bb.logger.Error("betting: failed to resolve channel %q: %v", bb.channelID, err)
		return 0
	}
	return chat.ID
}

func (b *Bot) apiRespond(callback *tgbotapi.CallbackQuery, text string, showAlert bool) {
	resp := tgbotapi.NewCallback(callback.ID, text)
	resp.ShowAlert = showAlert
	if _, err := b.bot.Request(resp); err != nil {
		b.logger.Error("telegram: callback response failed: %v", err)
	}
}
