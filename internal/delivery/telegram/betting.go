package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	callbackBetTeamA = "bet_team_a"
	callbackBetTeamB = "bet_team_b"

	// maxQuickBet caps what one tap on the team button stakes. The button has no
	// amount picker, so it bets the whole balance up to this ceiling.
	maxQuickBet = 50
)

// betFailureMessage turns a betting error into something the user can act on.
// The raw error used to be echoed straight into the Telegram alert, which put
// driver messages in front of players.
func betFailureMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrBettingClosed):
		return "⌛️ Ставки заблокированы. Игра перешла в мид-гейм."
	case errors.Is(err, domain.ErrAlreadyBet):
		return "Вы уже поставили на этот матч."
	case errors.Is(err, domain.ErrInsufficientPoints):
		return "Недостаточно очков для ставки."
	case errors.Is(err, domain.ErrPlayerNotFound):
		return "Ваш Telegram не привязан к игроку. Используйте /link <код> в Discord."
	case errors.Is(err, domain.ErrMatchNotFound):
		return "Матч не найден."
	default:
		return "Не удалось принять ставку. Попробуйте позже."
	}
}

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

// NotifyMatchLive announces a match and opens the betting keyboard.
//
// window is how long bets are accepted. It is spelled out in the message
// because the window is short and closes silently: without it a player has no
// way to tell whether they have five minutes or five seconds.
func (bb *BettingBot) NotifyMatchLive(matchID int, captainA, captainB string, window time.Duration) {
	if bb.channelID == "" {
		bb.logger.Warn("betting: TELEGRAM_CHANNEL_ID not configured — skipping match notification")
		return
	}

	text := fmt.Sprintf(
		"*МАТЧ #%d АКТИВЕН*\n\n"+
			"Team A: Капитан %s\n"+
			"Team B: Капитан %s\n\n"+
			"Ставки принимаются до *%s* (%d мин).\n"+
			"Используйте кнопки ниже, чтобы сделать ставку.",
		matchID, captainA, captainB,
		time.Now().Add(window).Format("15:04"), int(window.Minutes()),
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

func (bb *BettingBot) HandleCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	userID := callback.From.ID
	username := callback.From.UserName

	bb.logger.Debug("betting: callback from %s (%d): %s", username, userID, data)

	if strings.HasPrefix(data, callbackBetTeamA) || strings.HasPrefix(data, callbackBetTeamB) {
		bb.handleBetCallback(ctx, callback)
		return
	}

	// Every callback must be answered, otherwise Telegram leaves the button
	// spinning on the client until it times out.
	bb.bot.apiRespond(callback, "", false)
}

func (bb *BettingBot) handleBetCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	userID := callback.From.ID
	username := callback.From.UserName

	var teamChosen, prefix string
	if strings.HasPrefix(data, callbackBetTeamA) {
		teamChosen, prefix = domain.TeamA, callbackBetTeamA
	} else {
		teamChosen, prefix = domain.TeamB, callbackBetTeamB
	}

	// A malformed callback used to leave matchID at 0 and surface as a confusing
	// "match 0 not found" further down.
	matchID, err := strconv.Atoi(strings.TrimPrefix(data, prefix+"_"))
	if err != nil || matchID <= 0 {
		bb.logger.Warn("betting: malformed callback data %q from user %d", data, userID)
		bb.bot.apiRespond(callback, "Некорректная кнопка ставки.", true)
		return
	}

	// The betting window is not pre-checked here any more. PlaceBet re-reads it
	// inside its own transaction anyway, and the pre-check queried a second,
	// different implementation that ignored betting_closes_at — so it answered
	// "open" past the deadline, drew the keyboard, and let PlaceBet do the
	// refusing.
	points, err := bb.bettingService.GetPlayerPoints(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			bb.bot.apiRespond(callback, "Ваш Telegram не привязан к игроку. Используйте /link <код> в Discord.", true)
			return
		}
		bb.logger.Error("betting: failed to read points for user %d: %v", userID, err)
		bb.bot.apiRespond(callback, "Не удалось прочитать баланс. Попробуйте позже.", true)
		return
	}

	if points <= 0 {
		bb.bot.apiRespond(callback, "У вас нет очков для ставок. Заработайте очки участием в матчах.", true)
		return
	}

	amount := points
	if amount > maxQuickBet {
		amount = maxQuickBet
	}

	req := models.PlaceBetRequest{
		MatchID:    matchID,
		TgUserID:   userID,
		TeamChosen: teamChosen,
		Amount:     amount,
	}

	if err := bb.bettingService.PlaceBet(ctx, req); err != nil {
		bb.logger.Error("betting: user %d failed to place bet on match #%d: %v", userID, matchID, err)
		bb.bot.apiRespond(callback, betFailureMessage(err), true)
		return
	}

	bb.bot.apiRespond(callback, fmt.Sprintf(
		"Ставка принята!\n\n Матч #%d\n Команда: %s\n Сумма: %d очков\n\nОжидайте результатов матча.",
		matchID, teamChosen, amount,
	), true)

	bb.logger.Info("betting: user %d (%s) bet %d on %s (match #%d)", userID, username, amount, teamChosen, matchID)
}

func (bb *BettingBot) NotifyMatchResult(res application.PayoutResult) {
	if bb.channelID == "" {
		return
	}

	channelChatID := bb.resolveChannelID()
	if channelChatID == 0 {
		return
	}

	summary := application.FormatPayoutSummary(res)

	msg := tgbotapi.NewMessage(channelChatID, summary)
	msg.ParseMode = "Markdown"

	_, err := bb.bot.bot.Send(msg)
	if err != nil {
		bb.logger.Error("betting: failed to send match result to channel: %v", err)
	} else {
		bb.logger.Info("betting: match #%d result published to Telegram channel", res.MatchID)
	}
}

// HandleBetCommand and HandleBalanceCommand were removed: nothing dispatched to
// them. They duplicated the callback flow with weaker validation (two unchecked
// Sscanf calls, their own "Team A"/"Team B" literals) and identified the bettor
// by chat id rather than From.ID, which is the group's id in a group chat.
// Betting is placed through the inline keyboard in handleBetCallback.

func (bb *BettingBot) resolveChannelID() int64 {
	if bb.channelID == "" {
		return 0
	}

	if id, err := strconv.ParseInt(bb.channelID, 10, 64); err == nil && id != 0 {
		return id
	}

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
func (bb *BettingBot) NotifyMatchCancelled(matchID int, refundedCount int) {
	if bb.channelID == "" {
		return
	}

	channelChatID := bb.resolveChannelID()
	if channelChatID == 0 {
		return
	}

	text := fmt.Sprintf("🚫 **МАТЧ #%d ОТМЕНЕН СУДЬЕЙ!**\n\nВсе сделанные ставки (%d) были отменены, очки возвращены на баланс пользователей.", matchID, refundedCount)
	msg := tgbotapi.NewMessage(channelChatID, text)
	msg.ParseMode = "Markdown"

	_, err := bb.bot.bot.Send(msg)
	if err != nil {
		bb.logger.Error("betting: failed to send cancel notification to channel: %v", err)
	} else {
		bb.logger.Info("betting: match #%d cancellation published to Telegram channel", matchID)
	}
}

func (b *Bot) apiRespond(callback *tgbotapi.CallbackQuery, text string, showAlert bool) {
	resp := tgbotapi.NewCallback(callback.ID, text)
	resp.ShowAlert = showAlert
	if _, err := b.bot.Request(resp); err != nil {
		b.logger.Error("telegram: callback response failed: %v", err)
	}
}
