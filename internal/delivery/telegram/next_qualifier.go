package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"blackwatch/internal/application"
	"blackwatch/internal/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// When a qualifier closes the admins get a button to open the next one, and
// the admin who opens it a button to tell every captain. Callbacks:
//
//	tourney:next:<finished qualifier id>
//	tourney:announce:<tournament id>
const tourneyCallbackPrefix = "tourney:"

// offerNextQualifier asks the admins whether to open the next qualifier with
// the finished one's settings.
func (b *Bot) offerNextQualifier(res *application.FinishResult) {
	t := res.Tournament
	if t == nil || !t.Qualifier() {
		return
	}
	for id := range b.adminIDs {
		b.sendNextQualifierPrompt(id, t, fmt.Sprintf("Квалификация «%s» завершена, прошли %d команд.", t.Name, len(res.Qualified)))
	}
}

// offerLastQualifier sends the same prompt for the latest finished qualifier
// to one admin, for /next_qual: the prompt after a qualifier closes goes out
// once and may be lost or ignored.
func (b *Bot) offerLastQualifier(ctx context.Context, chatID int64) {
	all, err := b.service.GetAllTournaments(ctx)
	if err != nil {
		b.sendMessage(chatID, "Не удалось прочитать турниры: "+err.Error(), "main_menu")
		return
	}
	var last *models.TelegramTournament
	for i := range all {
		t := &all[i]
		if t.Qualifier() && t.Status == models.TournamentStatusCompleted && (last == nil || t.ID > last.ID) {
			last = t
		}
	}
	if last == nil {
		b.sendMessage(chatID, "Завершённых квалификаций нет. Следующая открывается после того, как закончится текущая.", "main_menu")
		return
	}
	b.sendNextQualifierPrompt(chatID, last, fmt.Sprintf("Последняя завершённая квалификация — «%s».", last.Name))
}

func (b *Bot) sendNextQualifierPrompt(chatID int64, t *models.TelegramTournament, head string) {
	next := application.NextQualifierName(t.Name)
	text := fmt.Sprintf("%s\n\nОткрыть «%s» с теми же настройками? Выходят %d команд, форматы матчей те же. Время старта зададите после создания.",
		head, next, t.AdvanceCount)
	b.sendInline(chatID, text, tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Создать «"+next+"»", fmt.Sprintf("%snext:%d", tourneyCallbackPrefix, t.ID)),
	)))
}

func (b *Bot) handleTourneyCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if !b.isAdmin(cb.From.ID) {
		b.apiRespond(cb, "Только для администраторов.", true)
		return
	}
	parts := strings.Split(strings.TrimPrefix(cb.Data, tourneyCallbackPrefix), ":")
	id := 0
	if len(parts) == 2 {
		id, _ = strconv.Atoi(parts[1])
	}
	if id <= 0 {
		b.apiRespond(cb, "Кнопка устарела.", true)
		return
	}
	switch parts[0] {
	case "next":
		t, err := b.service.CreateNextQualifier(ctx, id)
		if err != nil {
			b.apiRespond(cb, capitalize(err.Error()), true)
			return
		}
		b.apiRespond(cb, "Создано", false)
		b.clearButtons(cb)
		text := fmt.Sprintf("Создана «%s»: регистрация открыта, выходят %d команд. Число прошедших меняется там же, где время старта, пока сетка не построена: 7, 15 или 3 — с одним добивочным матчем.\n\n"+
			"Задайте время старта — в приложении (Админка → Турниры) или командой /set_tourney ДД.ММ.ГГГГ ЧЧ:ММ. От него считаются закрытие регистрации, напоминания о check-in и построение сетки.\n\n"+
			"Затем разошлите капитанам, что регистрация открыта: время старта попадёт в сообщение.", t.Name, t.AdvanceCount)
		b.sendInline(cb.From.ID, text, tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Разослать капитанам", fmt.Sprintf("%sannounce:%d", tourneyCallbackPrefix, t.ID)),
		)))
		for admin := range b.adminIDs {
			if admin != cb.From.ID {
				b.sendMessage(admin, fmt.Sprintf("Администратор открыл «%s».", t.Name), "main_menu")
			}
		}
	case "announce":
		n, err := b.AnnounceRegistration(ctx, id)
		if err != nil {
			b.apiRespond(cb, capitalize(err.Error()), true)
			return
		}
		b.apiRespond(cb, "Рассылка запущена", false)
		b.clearButtons(cb)
		b.sendMessage(cb.From.ID, fmt.Sprintf("Рассылка о регистрации запущена: %d капитанов.", n), "main_menu")
	default:
		b.apiRespond(cb, "Кнопка устарела.", true)
	}
}

// clearButtons removes the keyboard of the message a callback came from, so
// an answered prompt cannot be pressed again.
func (b *Bot) clearButtons(cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil || cb.Message.Chat == nil {
		return
	}
	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})
	if _, err := b.bot.Request(edit); err != nil {
		b.logger.Warn("telegram: clear buttons: %v", err)
	}
}
