package telegram

import (
	"blackwatch/internal/application"
	"context"
	"fmt"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// updateTimeout bounds the work done for a single Telegram update or scheduled
// tick, so a slow query cannot stall the bot indefinitely.
const updateTimeout = 30 * time.Second

// broadcastInterval paces bulk sends under Telegram's ~30 messages/second cap.
const broadcastInterval = 50 * time.Millisecond

// broadcastTimeout bounds a whole bulk send, which at the paced rate can take a
// while for a large list.
const broadcastTimeout = 10 * time.Minute

func (b *Bot) isAdmin(id int64) bool {
	_, ok := b.adminIDs[id]
	return ok
}

// sendMessage delivers a message and logs delivery failures. It used to discard
// the send error entirely, so a blocked bot, a deleted chat or a 429 was
// completely invisible — broadcasts silently reached a fraction of their list.
func (b *Bot) sendMessage(chatID int64, text string, kbType string) {
	if err := b.trySendMessage(chatID, text, kbType); err != nil {
		b.logger.Warn("telegram: failed to send message to %d: %v", chatID, err)
	}
}

func (b *Bot) trySendMessage(chatID int64, text string, kbType string) error {
	if text == "" {
		return nil
	}
	msg := tgbotapi.NewMessage(chatID, text)

	if kbType == application.KbReportOpponent {
		msg.ReplyMarkup = b.reportOpponentKeyboard(context.Background(), chatID)
		_, err := b.bot.Send(msg)
		return err
	}

	// Registration controls are inline buttons under the message; the reply
	// keyboard at the bottom is reserved for the main menu.
	if kb, ok := regKeyboard(kbType); ok {
		msg.ReplyMarkup = kb
		_, err := b.bot.Send(msg)
		return err
	}

	switch kbType {
	case "checkin_status_admin":
		rows := [][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📢 Пингануть должников", "admin_ping_debtors"),
			),
		}
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	case "main_menu":
		rows := [][]tgbotapi.KeyboardButton{
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/profile"),
				tgbotapi.NewKeyboardButton("/my_team"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/reg_team"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/checkin"),
				tgbotapi.NewKeyboardButton("/report"),
			),
			tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("/match"), tgbotapi.NewKeyboardButton("/judge")),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/delete_team"),
			),
		}
		if b.isAdmin(chatID) {
			rows = append(rows, tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/admin"),
			))
		}
		kb := tgbotapi.NewReplyKeyboard(rows...)
		kb.ResizeKeyboard = true
		msg.ReplyMarkup = kb
	case "remove":
		msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	default:
		// Keep the reply keyboard visible at all times
	}

	_, err := b.bot.Send(msg)
	return err
}

// broadcast delivers the same message to many chats, pacing the sends.
//
// Telegram caps bulk delivery at roughly 30 messages per second and answers 429
// beyond it. The previous tight loop simply lost everyone past the limit, and
// with the send error discarded, nobody noticed. Returns delivered and failed
// counts.
func (b *Bot) broadcast(ctx context.Context, chatIDs []int64, text, kbType string) (delivered, failed int) {
	ticker := time.NewTicker(broadcastInterval)
	defer ticker.Stop()

	for _, id := range chatIDs {
		select {
		case <-ctx.Done():
			b.logger.Warn("telegram: broadcast interrupted after %d of %d: %v",
				delivered+failed, len(chatIDs), ctx.Err())
			return delivered, failed
		case <-ticker.C:
		}

		if err := b.trySendMessage(id, text, kbType); err != nil {
			failed++
			b.logger.Warn("telegram: broadcast to %d failed: %v", id, err)
			continue
		}
		delivered++
	}
	return delivered, failed
}

func valueOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func (b *Bot) reportOpponentKeyboard(ctx context.Context, chatID int64) tgbotapi.InlineKeyboardMarkup {
	opponents, _ := b.service.GetEligibleOpponents(ctx, chatID)
	var rows [][]tgbotapi.InlineKeyboardButton
	var currentRow []tgbotapi.InlineKeyboardButton

	for _, team := range opponents {
		btn := tgbotapi.NewInlineKeyboardButtonData(team.Name, fmt.Sprintf("rep:opp:%d", team.ID))
		currentRow = append(currentRow, btn)
		if len(currentRow) == 2 {
			rows = append(rows, currentRow)
			currentRow = nil
		}
	}
	if len(currentRow) > 0 {
		rows = append(rows, currentRow)
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "rep:cancel"),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}
