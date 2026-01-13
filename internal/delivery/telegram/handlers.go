package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) handleAdminCommand(chatID int64, text string) {
	if text == "/start" || strings.HasPrefix(text, "/admin") {
		response := "Админ-панель:\n\n" +
			"/list_teams - Краткий список и кол-во\n" +
			"/check_team [Название] - Детальный состав\n" +
			"/export - CSV файл\n" +
			"/list_solo - Список соло-игроков\n" +
			"/export_solo - CSV соло-игроков\n\n" +
			"/broadcast [текст] - Рассылка\n" +
			"/set_tourney [дата] - Установить время\n" +
			"/close_reg / /open_reg - Регистрация\n" +
			"/del_team [Название] - Удалить\n" +
			"/reset_user [ID] - Сброс FSM"
		b.sendMessage(chatID, response, "empty")
		return
	}

	if text == "/export" {
		csvData, err := b.service.GenerateTeamsCSV()
		if err != nil {
			b.sendMessage(chatID, "Ошибка: "+err.Error(), "empty")
		} else {
			fileBytes := tgbotapi.FileBytes{Name: "teams.csv", Bytes: csvData}
			b.bot.Send(tgbotapi.NewDocument(chatID, fileBytes))
		}
		return
	}

	if strings.HasPrefix(text, "/set_tourney ") {
		layout := "02.01.2006 15:04"
		dateStr := strings.TrimPrefix(text, "/set_tourney ")
		t, err := time.ParseInLocation(layout, dateStr, time.Local)
		if err != nil {
			b.sendMessage(chatID, "Ошибка! Формат: /set_tourney 20.05.2024 18:00", "empty")
		} else {
			b.service.SetTournamentTime(t)
			b.sendMessage(chatID, fmt.Sprintf("Время турнира установлено: %s\nНапоминание в: %s\nТех. поражение в: %s",
				t.Format(layout),
				t.Add(-30*time.Minute).Format("15:04"),
				t.Add(10*time.Minute).Format("15:04")), "empty")
		}
		return
	}

	if text == "/list_solo" {
		b.sendMessage(chatID, b.service.GetSoloPlayersList(), "empty")
		return
	}

	if text == "/export_solo" {
		data, err := b.service.GenerateSoloPlayersCSV()
		if err != nil {
			b.sendMessage(chatID, "Ошибка: "+err.Error(), "empty")
		} else {
			file := tgbotapi.FileBytes{Name: "solo_players.csv", Bytes: data}
			b.bot.Send(tgbotapi.NewDocument(chatID, file))
		}
		return
	}

	if text == "/list_teams" {
		b.sendMessage(chatID, b.service.GetTeamsList(), "empty")
		return
	}

	if strings.HasPrefix(text, "/check_team ") {
		teamName := strings.TrimPrefix(text, "/check_team ")
		b.sendMessage(chatID, b.service.AdminGetTeamDetails(teamName), "empty")
		return
	}

	if strings.HasPrefix(text, "/broadcast ") {
		msgText := strings.TrimPrefix(text, "/broadcast ")
		ids, _ := b.service.GetBroadcastList()
		for _, id := range ids {
			b.sendMessage(id, "СООБЩЕНИЕ ОТ ОРГАНИЗАТОРОВ:\n\n"+msgText, "empty")
		}
		b.sendMessage(chatID, fmt.Sprintf("Рассылка на %d чел. завершена.", len(ids)), "empty")
		return
	}

	if text == "/close_reg" {
		b.service.SetRegistrationOpen(false)
		b.sendMessage(chatID, "Регистрация закрыта.", "empty")
		return
	}
	if text == "/open_reg" {
		b.service.SetRegistrationOpen(true)
		b.sendMessage(chatID, "Регистрация открыта.", "empty")
		return
	}

	if strings.HasPrefix(text, "/del_team ") {
		name := strings.TrimPrefix(text, "/del_team ")
		b.sendMessage(chatID, b.service.AdminDeleteTeam(name), "empty")
		return
	}

	if strings.HasPrefix(text, "/reset_user ") {
		idStr := strings.TrimPrefix(text, "/reset_user ")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		b.sendMessage(chatID, b.service.AdminResetUser(id), "empty")
		return
	}
}

func (b *Bot) handleUserCommand(chatID int64, text string) {
	if strings.HasPrefix(text, "/edit_player") {
		parts := strings.Fields(text)
		if len(parts) != 2 {
			b.sendMessage(chatID, "Используйте: /edit_player [номер]", "empty")
		} else {
			slot, _ := strconv.Atoi(parts[1])
			response, kbType := b.service.StartEditPlayer(chatID, slot)
			b.sendMessage(chatID, response, kbType)
		}
		return
	}

	var response string
	var kbType string = "empty"

	switch text {
	case "/start":
		response = "Valhalla Cup Bot\n\n" +
			"/reg_solo - Регистрация (соло)\n" +
			"/reg_team - Регистрация (команда)\n" +
			"/my_team - Мой состав\n" +
			"/edit_player [№] - Изменить данные игрока\n" +
			"/checkin - Подтвердить участие\n" +
			"/report - Отправить результат матча\n" +
			"/delete_team - Удалить команду"
		kbType = "empty"

	case "/reg_solo":
		response, kbType = b.service.StartSoloRegistration(chatID)
	case "/reg_team":
		response, kbType = b.service.StartTeamRegistration(chatID)
	case "/my_team":
		response = b.service.GetTeamInfo(chatID)
		kbType = "empty"
	case "/checkin":
		response = b.service.ToggleCheckIn(chatID)
		kbType = "empty"
	case "/delete_team":
		response = b.service.DeleteTeam(chatID)
		kbType = "empty"
	case "/report":
		response, kbType = b.service.StartReport(chatID)

	default:
		response, kbType = b.service.HandleUserInput(chatID, text)
	}

	b.sendMessage(chatID, response, kbType)
}

func (b *Bot) handlePhoto(chatID int64, msg *tgbotapi.Message) {
	photoID := msg.Photo[len(msg.Photo)-1].FileID
	caption := msg.Caption
	resp := b.service.HandleReport(chatID, photoID, caption)

	if strings.HasPrefix(resp, "ADMIN_REPORT:") {
		parts := strings.SplitN(resp, ":", 3)
		if len(parts) == 3 {
			fileID := parts[1]
			reportText := parts[2]

			for adminID := range b.adminIDs {
				photoMsg := tgbotapi.NewPhoto(adminID, tgbotapi.FileID(fileID))
				photoMsg.Caption = "НОВЫЙ РЕЗУЛЬТАТ МАТЧА:\n\n" + reportText
				b.bot.Send(photoMsg)
			}
			b.sendMessage(chatID, "Скриншот отправлен судьям!", "empty")
		}
	} else {
		b.sendMessage(chatID, resp, "empty")
	}
}
