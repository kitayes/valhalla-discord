package telegram

import (
	"blackwatch/internal/application"
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// callbackRegPrefix marks registration buttons: "reg:<action>[:<arg>]".
const callbackRegPrefix = "reg"

// parseRegCallback splits "reg:<action>[:<arg>]". Anything with a different
// prefix, an empty action, or more than one argument is rejected.
func parseRegCallback(data string) (action, arg string, ok bool) {
	parts := strings.Split(data, callbackSep)
	if len(parts) < 2 || len(parts) > 3 || parts[0] != callbackRegPrefix || parts[1] == "" {
		return "", "", false
	}
	if len(parts) == 3 {
		if parts[2] == "" {
			return "", "", false
		}
		arg = parts[2]
	}
	return parts[1], arg, true
}

func regData(parts ...string) string {
	return callbackRegPrefix + callbackSep + strings.Join(parts, callbackSep)
}

func regBtn(label string, data ...string) tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData(label, regData(data...))
}

// regKeyboard renders the application's registration keyboard types. Types
// with parameters carry them after ":" (see application.KbReg* docs).
func regKeyboard(kbType string) (tgbotapi.InlineKeyboardMarkup, bool) {
	kind, params, _ := strings.Cut(kbType, ":")
	cancel := tgbotapi.NewInlineKeyboardRow(regBtn("❌ Отмена", "cancel"))

	switch kind {
	case application.KbRegCancel:
		return tgbotapi.NewInlineKeyboardMarkup(cancel), true

	case application.KbRegSkip:
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(regBtn("⏭ Пропустить", "skip")),
			cancel,
		), true

	case application.KbRegRoles:
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(regBtn("Gold", "role", "Gold"), regBtn("Exp", "role", "Exp"), regBtn("Mid", "role", "Mid")),
			tgbotapi.NewInlineKeyboardRow(regBtn("Roam", "role", "Roam"), regBtn("Jungle", "role", "Jungle")),
			cancel,
		), true

	case application.KbRegConfirm:
		n, _ := strconv.Atoi(params)
		rows := [][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(regBtn("✅ Подтвердить", "confirm")),
		}
		rows = append(rows, fixRows(n)...)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("🗑 Удалить команду", "delete")))
		return tgbotapi.NewInlineKeyboardMarkup(rows...), true

	case application.KbRegCard:
		nStr, add, _ := strings.Cut(params, ":")
		n, _ := strconv.Atoi(nStr)
		rows := fixRows(n)
		switch add {
		case "sub":
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("➕ Замена", "sub")))
		case "player":
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("➕ Игрок", "sub")))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("🗑 Удалить команду", "delete")))
		return tgbotapi.NewInlineKeyboardMarkup(rows...), true

	case application.KbRegCheckin:
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(regBtn("✅ Подтвердить участие", "checkin")),
		), true

	case application.KbRegSoloConfirm:
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(regBtn("✅ Подтвердить", "confirm"), regBtn("✏️ Исправить", "fix")),
		), true
	}
	return tgbotapi.InlineKeyboardMarkup{}, false
}

// fixRows lays out ✏️ 1..n, up to four per row.
func fixRows(n int) [][]tgbotapi.InlineKeyboardButton {
	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for i := 1; i <= n; i++ {
		row = append(row, regBtn(fmt.Sprintf("✏️ %d", i), "fix", strconv.Itoa(i)))
		if len(row) == 4 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// handleRegCallback answers a registration button: strips the keyboard from
// the message that carried it (so a second tap does nothing), runs the
// action, and replies with a fresh message.
func (b *Bot) handleRegCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	action, arg, ok := parseRegCallback(cb.Data)
	if !ok || cb.Message == nil || !servesChat(cb.Message.Chat) {
		b.apiRespond(cb, "Эта кнопка больше не действует.", true)
		return
	}
	chatID := cb.Message.Chat.ID

	noKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	if _, err := b.bot.Request(tgbotapi.NewEditMessageReplyMarkup(chatID, cb.Message.MessageID, noKeyboard)); err != nil {
		b.logger.Warn("telegram: failed to strip registration keyboard in %d: %v", chatID, err)
	}

	text, kbType := b.service.HandleRegAction(ctx, cb.From.ID, action, arg)
	b.apiRespond(cb, "", false)
	b.sendMessage(chatID, text, kbType)
}
