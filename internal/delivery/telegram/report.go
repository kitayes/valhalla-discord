package telegram

import (
	"blackwatch/internal/models"
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) handleReportCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	chatID := callback.From.ID
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		b.apiRespond(callback, "Некорректная кнопка.", true)
		return
	}

	action := parts[1]

	switch action {
	case "opp":
		if len(parts) != 3 {
			b.apiRespond(callback, "Некорректный выбор команды.", true)
			return
		}
		teamID, err := strconv.Atoi(parts[2])
		if err != nil {
			b.apiRespond(callback, "Некорректный ID команды.", true)
			return
		}
		resp, kb := b.service.SelectReportOpponent(ctx, chatID, teamID)
		b.apiRespond(callback, "", false)
		b.sendMessage(chatID, resp, kb)

	case "score":
		if len(parts) < 3 {
			b.apiRespond(callback, "Некорректный счет.", true)
			return
		}
		score := strings.Join(parts[2:], ":")
		resp, kb := b.service.SetReportScore(ctx, chatID, score)
		b.apiRespond(callback, "", false)
		b.sendMessage(chatID, resp, kb)

	case "reset_photos":
		resp, kb := b.service.ResetReportPhotos(ctx, chatID)
		b.apiRespond(callback, "Скриншоты сброшены.", false)
		b.sendMessage(chatID, resp, kb)

	case "cancel":
		resp, kb := b.service.CancelReport(ctx, chatID)
		b.apiRespond(callback, "Отчет отменен.", false)
		b.sendMessage(chatID, resp, kb)

	case "submit":
		resp, kb, report := b.service.SubmitReport(ctx, chatID)
		if report == nil {
			b.apiRespond(callback, resp, true)
			return
		}
		b.apiRespond(callback, "Отчет успешно отправлен!", false)
		b.sendMessage(chatID, resp, kb)
		b.forwardReportMedia(ctx, report, callback.From)

	default:
		b.apiRespond(callback, "Неизвестное действие.", true)
	}
}

func (b *Bot) forwardReportMedia(ctx context.Context, rep *models.TelegramMatchReport, reporter *tgbotapi.User) {
	if rep == nil || len(rep.PhotoFileIDs) == 0 {
		return
	}

	reporterInfo := ""
	if reporter != nil {
		if reporter.UserName != "" {
			reporterInfo = fmt.Sprintf("@%s (%s)", strings.TrimPrefix(reporter.UserName, "@"), reporter.FirstName)
		} else {
			reporterInfo = reporter.FirstName
		}
	} else {
		reporterInfo = fmt.Sprintf("TG ID: %d", rep.ReporterTelegramID)
	}

	caption := fmt.Sprintf("🏆 РЕЗУЛЬТАТ МАТЧА:\n\nПобедитель: %s\nПроигравший: %s\nСчет: %s\n\nОтправил: %s\nСкриншотов: %d",
		rep.WinnerTeamName, rep.LoserTeamName, rep.Score, reporterInfo, len(rep.PhotoFileIDs))

	// 1. Forward to all admins
	for adminID := range b.adminIDs {
		b.sendReportMediaToChat(adminID, rep.PhotoFileIDs, caption)
	}

	// 2. Forward to tournament chat
	tourneyChatID := b.resolveTournamentChatID()
	if tourneyChatID != 0 {
		b.sendReportMediaToChat(tourneyChatID, rep.PhotoFileIDs, caption)
	}
}

func (b *Bot) sendReportMediaToChat(chatID int64, photoIDs []string, caption string) {
	if len(photoIDs) == 1 {
		photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(photoIDs[0]))
		photoMsg.Caption = caption
		if _, err := b.bot.Send(photoMsg); err != nil {
			b.logger.Error("telegram: failed to send report photo to %d: %v", chatID, err)
		}
		return
	}

	var media []interface{}
	for i, fileID := range photoIDs {
		photo := tgbotapi.NewInputMediaPhoto(tgbotapi.FileID(fileID))
		if i == 0 {
			photo.Caption = caption
		}
		media = append(media, photo)
	}

	group := tgbotapi.NewMediaGroup(chatID, media)
	if _, err := b.bot.SendMediaGroup(group); err != nil {
		b.logger.Error("telegram: failed to send report media group to %d: %v", chatID, err)
	}
}
