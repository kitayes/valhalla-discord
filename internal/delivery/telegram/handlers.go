package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) handleAdminCommand(ctx context.Context, chatID int64, text string) {
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
		b.sendMessage(chatID, response, "main_menu")
		return
	}

	if text == "/export" {
		csvData, err := b.service.GenerateTeamsCSV(ctx)
		if err != nil {
			b.sendMessage(chatID, "Ошибка: "+err.Error(), "main_menu")
		} else {
			fileBytes := tgbotapi.FileBytes{Name: "teams.csv", Bytes: csvData}
			if _, err := b.bot.Send(tgbotapi.NewDocument(chatID, fileBytes)); err != nil {
				b.logger.Error("telegram: failed to send teams.csv to %d: %v", chatID, err)
			}
		}
		return
	}

	if strings.HasPrefix(text, "/set_tourney ") {
		layout := "02.01.2006 15:04"
		dateStr := strings.TrimPrefix(text, "/set_tourney ")
		t, err := time.ParseInLocation(layout, dateStr, time.Local)
		if err != nil {
			b.sendMessage(chatID, "Ошибка! Формат: /set_tourney 20.05.2024 18:00", "main_menu")
		} else {
			b.service.SetTournamentTime(ctx, t)
			b.sendMessage(chatID, fmt.Sprintf("Время турнира установлено: %s\nНапоминание в: %s\nТех. поражение в: %s",
				t.Format(layout),
				t.Add(-30*time.Minute).Format("15:04"),
				t.Add(10*time.Minute).Format("15:04")), "main_menu")
		}
		return
	}

	if text == "/list_solo" {
		b.sendMessage(chatID, b.service.GetSoloPlayersList(ctx), "main_menu")
		return
	}

	if text == "/export_solo" {
		data, err := b.service.GenerateSoloPlayersCSV(ctx)
		if err != nil {
			b.sendMessage(chatID, "Ошибка: "+err.Error(), "main_menu")
		} else {
			file := tgbotapi.FileBytes{Name: "solo_players.csv", Bytes: data}
			if _, err := b.bot.Send(tgbotapi.NewDocument(chatID, file)); err != nil {
				b.logger.Error("telegram: failed to send solo_players.csv to %d: %v", chatID, err)
			}
		}
		return
	}

	if text == "/list_teams" {
		b.sendMessage(chatID, b.service.GetTeamsList(ctx), "main_menu")
		return
	}

	if strings.HasPrefix(text, "/check_team ") {
		teamName := strings.TrimPrefix(text, "/check_team ")
		b.sendMessage(chatID, b.service.AdminGetTeamDetails(ctx, teamName), "main_menu")
		return
	}

	if strings.HasPrefix(text, "/broadcast ") {
		msgText := strings.TrimPrefix(text, "/broadcast ")
		ids, err := b.service.GetBroadcastList(ctx)
		if err != nil {
			b.sendMessage(chatID, "Ошибка получения списка рассылки: "+err.Error(), "main_menu")
			return
		}
		if len(ids) == 0 {
			b.sendMessage(chatID, "Некому рассылать: список пуст.", "main_menu")
			return
		}

		// A paced broadcast outlives this update's deadline, so it runs detached
		// with its own budget and reports back when finished.
		b.sendMessage(chatID, fmt.Sprintf("Рассылка на %d чел. запущена...", len(ids)), "main_menu")
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), broadcastTimeout)
			defer cancel()

			delivered, failed := b.broadcast(bgCtx, ids, "СООБЩЕНИЕ ОТ ОРГАНИЗАТОРОВ:\n\n"+msgText, "empty")
			report := fmt.Sprintf("Рассылка завершена.\nДоставлено: %d из %d", delivered, len(ids))
			if failed > 0 {
				report += fmt.Sprintf("\nНе доставлено: %d (заблокировали бота или удалили чат)", failed)
			}
			b.sendMessage(chatID, report, "main_menu")
		}()
		return
	}

	if text == "/close_reg" {
		b.service.SetRegistrationOpen(ctx, false)
		b.sendMessage(chatID, "Регистрация закрыта.", "main_menu")
		return
	}
	if text == "/open_reg" {
		b.service.SetRegistrationOpen(ctx, true)
		b.sendMessage(chatID, "Регистрация открыта.", "main_menu")
		return
	}

	if strings.HasPrefix(text, "/del_team ") {
		name := strings.TrimPrefix(text, "/del_team ")
		b.sendMessage(chatID, b.service.AdminDeleteTeam(ctx, name), "main_menu")
		return
	}

	if strings.HasPrefix(text, "/reset_user ") {
		idStr := strings.TrimPrefix(text, "/reset_user ")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		b.sendMessage(chatID, b.service.AdminResetUser(ctx, id), "main_menu")
		return
	}
}

func (b *Bot) handleUserCommand(ctx context.Context, chatID int64, text string, username string) {
	if strings.HasPrefix(text, "/link ") {
		code := strings.TrimPrefix(text, "/link ")
		code = strings.TrimSpace(code)
		if code == "" {
			b.sendMessage(chatID, "Используйте: /link <код>\n\nКод можно получить в Discord командой /link <ID игрока>", "empty")
			return
		}

		err := b.profileLinkService.LinkTelegramAccount(ctx, code, chatID, username)
		if err != nil {
			b.sendMessage(chatID, "Ошибка привязки: "+err.Error(), "empty")
		} else {
			b.sendMessage(chatID, "Аккаунт успешно привязан к вашему Discord профилю!", "main_menu")
		}
		return
	}

	if strings.HasPrefix(text, "/edit_player") {
		parts := strings.Fields(text)
		if len(parts) != 2 {
			b.sendMessage(chatID, "Используйте: /edit_player [номер]", "empty")
		} else {
			slot, _ := strconv.Atoi(parts[1])
			response, kbType := b.service.StartEditPlayer(ctx, chatID, slot)
			b.sendMessage(chatID, response, kbType)
		}
		return
	}

	// Every branch below, default included, sets both values.
	var response, kbType string

	switch text {
	case "/start":
		response = "Добро пожаловать в Valhalla Cup Bot!\n\nВыберите действие:"
		if b.isAdmin(chatID) {
			response += "\n\nВы вошли как Администратор. Используйте команду /admin или кнопку меню для открытия панели управления."
		}
		kbType = "main_menu"

	case "/reg_solo":
		response, kbType = b.service.StartSoloRegistration(ctx, chatID)
	case "/reg_team":
		response, kbType = b.service.StartTeamRegistration(ctx, chatID)
	case "/my_team":
		response = b.service.GetTeamInfo(ctx, chatID)
		kbType = "empty"
	case "/checkin":
		response = b.service.ToggleCheckIn(ctx, chatID)
		kbType = "empty"
	case "/delete_team":
		response = b.service.DeleteTeam(ctx, chatID)
		kbType = "empty"
	case "/profile":
		profile, err := b.profileLinkService.GetLinkedProfileByTelegram(ctx, chatID)
		if err != nil || profile == nil {
			response = "Ваш аккаунт не привязан к Discord профилю.\n\nИспользуйте /link <код> для привязки.\nКод можно получить в Discord командой /link <ID игрока>"
		} else {
			wr := 0.0
			matches := profile.Wins + profile.Losses
			if matches > 0 {
				wr = float64(profile.Wins) / float64(matches) * 100
			}
			d := profile.Deaths
			if d == 0 {
				d = 1
			}
			kda := float64(profile.Kills+profile.Assists) / float64(d)

			response = fmt.Sprintf("Ваш профиль:\n\n"+
				"Discord: %s\n"+
				"Telegram: @%s\n\n"+
				"-- Игровые данные --\n"+
				"Ник: %s\n"+
				"ID: %s | Zone: %s\n"+
				"Звезды: %d | Роль: %s\n\n"+
				"-- Статистика Discord --\n"+
				"Матчей: %d\n"+
				"Побед: %d | Поражений: %d\n"+
				"Винрейт: %.1f%%\n"+
				"K/D/A: %d/%d/%d (%.2f)",
				profile.DiscordPlayerName,
				profile.TelegramUsername,
				valueOrDefault(profile.GameNickname, "Не указан"),
				valueOrDefault(profile.GameID, "-"),
				valueOrDefault(profile.ZoneID, "-"),
				profile.Stars,
				valueOrDefault(profile.MainRole, "Не указана"),
				matches, profile.Wins, profile.Losses, wr,
				profile.Kills, profile.Deaths, profile.Assists, kda)
		}
		kbType = "empty"
	case "/report":
		response, kbType = b.service.StartReport(ctx, chatID)

	default:
		response, kbType = b.service.HandleUserInput(ctx, chatID, text)
	}

	b.sendMessage(chatID, response, kbType)
}

func (b *Bot) handlePhoto(ctx context.Context, chatID int64, msg *tgbotapi.Message) {
	photoID := msg.Photo[len(msg.Photo)-1].FileID
	caption := msg.Caption
	resp := b.service.HandleReport(ctx, chatID, photoID, caption)

	if strings.HasPrefix(resp, "ADMIN_REPORT:") {
		parts := strings.SplitN(resp, ":", 3)
		if len(parts) == 3 {
			fileID := parts[1]
			reportText := parts[2]

			delivered := 0
			for adminID := range b.adminIDs {
				photoMsg := tgbotapi.NewPhoto(adminID, tgbotapi.FileID(fileID))
				photoMsg.Caption = "НОВЫЙ РЕЗУЛЬТАТ МАТЧА:\n\n" + reportText
				if _, err := b.bot.Send(photoMsg); err != nil {
					b.logger.Error("telegram: failed to forward match report to admin %d: %v", adminID, err)
					continue
				}
				delivered++
			}
			// Reported honestly: the previous version claimed the screenshot had
			// reached the referees even when every send had failed.
			if delivered == 0 {
				b.sendMessage(chatID, "Не удалось отправить скриншот судьям. Попробуйте ещё раз.", "empty")
			} else {
				b.sendMessage(chatID, "Скриншот отправлен судьям!", "empty")
			}
		}
	} else {
		b.sendMessage(chatID, resp, "empty")
	}
}
