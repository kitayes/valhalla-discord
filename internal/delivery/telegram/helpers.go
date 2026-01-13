package telegram

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

func (b *Bot) isAdmin(id int64) bool {
	_, ok := b.adminIDs[id]
	return ok
}

func (b *Bot) sendMessage(chatID int64, text string, kbType string) {
	if text == "" {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)

	switch kbType {
	case "skip":
		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("Пропустить"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("Отмена"),
			),
		)
	case "role":
		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("Gold"),
				tgbotapi.NewKeyboardButton("Exp"),
				tgbotapi.NewKeyboardButton("Mid"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("Roam"),
				tgbotapi.NewKeyboardButton("Jungle"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("Замена"),
				tgbotapi.NewKeyboardButton("Любая"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("Отмена"),
			),
		)
	case "cancel":
		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("Отмена"),
			),
		)
	case "main_menu":
		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/profile"),
				tgbotapi.NewKeyboardButton("/my_team"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/reg_solo"),
				tgbotapi.NewKeyboardButton("/reg_team"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/checkin"),
				tgbotapi.NewKeyboardButton("/report"),
			),
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/delete_team"),
			),
		)
	default:
		msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	}

	b.bot.Send(msg)
}

func valueOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
