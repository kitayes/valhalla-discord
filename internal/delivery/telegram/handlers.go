package telegram

import (
	"blackwatch/internal/application"
	"blackwatch/internal/models"
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
			"/attention - Матчи, требующие внимания\n" +
			"/pause_matches / /resume_matches - Пауза таймеров матчей\n" +
			"/pause_match <номер> / /resume_match <номер> - Пауза одного матча\n" +
			"/match_history <номер> - История матча\n" +
			"/list_teams - Краткий список и кол-во\n" +
			"/checkin_status - Дашборд Check-in команд\n" +
			"/ping_debtors [текст] - Пинг должников (в ЛС и чат)\n" +
			"/reports - Список последних отчетов о матчах\n" +
			"/check_team [название] - Детальный состав\n" +
			"/export - CSV файл\n" +
			"/export_sheet - Составы и путь по сетке в Google Таблицу\n" +
			"/list_solo - Список соло-игроков\n" +
			"/export_solo - CSV соло-игроков\n\n" +
			"/broadcast [текст] - Рассылка\n" +
			"/set_tourney [дата] - Установить время\n" +
			"/close_reg / /open_reg - Регистрация\n" +
			"/del_team [название] - Удалить\n" +
			"/reinstate [название] - Вернуть после тех. поражения\n" +
			"/build_bracket - Построить или пересобрать сетку\n" +
			"/set_winner <№> <команда> [счёт] - Исправить результат\n" +
			"/reset_user [ID] - Сброс FSM"
		b.sendMessage(chatID, response, "main_menu")
		return
	}

	if text == "/checkin_status" || text == "/checkins" {
		statusText := b.service.GetCheckInStatus(ctx)
		teams, _ := b.service.GetUncheckedTeams(ctx)
		if len(teams) > 0 {
			b.sendMessage(chatID, statusText, "checkin_status_admin")
		} else {
			b.sendMessage(chatID, statusText, "main_menu")
		}
		return
	}

	if text == "/ping_debtors" || text == "/ping" || text == "/remind_debtors" || text == "/remind_checkin" {
		b.handlePingDebtors(ctx, chatID, "")
		return
	}
	if strings.HasPrefix(text, "/ping_debtors ") {
		b.handlePingDebtors(ctx, chatID, strings.TrimSpace(strings.TrimPrefix(text, "/ping_debtors ")))
		return
	}
	if strings.HasPrefix(text, "/ping ") {
		b.handlePingDebtors(ctx, chatID, strings.TrimSpace(strings.TrimPrefix(text, "/ping ")))
		return
	}
	if strings.HasPrefix(text, "/remind_debtors ") {
		b.handlePingDebtors(ctx, chatID, strings.TrimSpace(strings.TrimPrefix(text, "/remind_debtors ")))
		return
	}
	if strings.HasPrefix(text, "/remind_checkin ") {
		b.handlePingDebtors(ctx, chatID, strings.TrimSpace(strings.TrimPrefix(text, "/remind_checkin ")))
		return
	}

	if text == "/reports" {
		list, err := b.service.GetRecentMatchReports(ctx, 10)
		if err != nil {
			b.sendMessage(chatID, "Ошибка: "+err.Error(), "main_menu")
		} else {
			b.sendMessage(chatID, list, "main_menu")
		}
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

	if text == "/export_sheet" {
		url, err := b.service.ExportTeamsToSheet(ctx)
		if err != nil {
			b.sendMessage(chatID, "Ошибка: "+err.Error(), "main_menu")
		} else {
			b.sendMessage(chatID, "Составы и путь по сетке выгружены:\n"+url+
				"\n\nЛисты «Teams» и «Matches». Сетка — на момент последней синхронизации с Challonge.", "main_menu")
		}
		return
	}

	if strings.HasPrefix(text, "/set_tourney ") {
		t, summary, err := b.parseTournamentTime(strings.TrimPrefix(text, "/set_tourney "))
		if err != nil {
			b.sendMessage(chatID, "Ошибка! Формат: /set_tourney 20.05.2026 18:00", "main_menu")
			return
		}
		b.service.SetTournamentTime(ctx, t)
		b.sendMessage(chatID, summary, "main_menu")
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
		b.sendMessage(chatID, "Регистрация открыта принудительно: автозакрытие за час до турнира отключено до /close_reg.", "main_menu")
		return
	}

	if strings.HasPrefix(text, "/del_team ") {
		name := strings.TrimPrefix(text, "/del_team ")
		resp := b.service.AdminDeleteTeam(ctx, name)
		b.sendMessage(chatID, resp, "main_menu")
		if resp == "Удалена." {
			b.notifyTeamDeletedFromResponse(fmt.Sprintf("Команда '%s' удалена.", name), false)
		}
		return
	}

	if strings.HasPrefix(text, "/reinstate ") {
		name := strings.TrimPrefix(text, "/reinstate ")
		resp := b.service.AdminReinstateTeam(ctx, name)
		b.sendMessage(chatID, resp, "main_menu")
		b.notifyTeamReinstated(resp)
		if b.bracket != nil && strings.Contains(resp, "возвращена в турнир") {
			change, err := b.bracket.Reinstate(ctx, name)
			b.reportBracketError(ctx, "reinstate", err)
			if err == nil {
				b.applyBracketChange(ctx, change)
			}
		}
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

	var response, kbType string
	switch text {
	case "/start":
		response = "Добро пожаловать в Valhalla Cup Bot!\n\nВыберите действие:"
		if b.webAppURL != "" {
			response += "\nТурнирное приложение: /app"
		}
		if b.isAdmin(chatID) {
			response += "\n\nВы вошли как Администратор. Используйте команду /admin или кнопку меню для открытия панели управления."
		}
		kbType = "main_menu"

	case "/app":
		if b.webAppURL == "" {
			b.sendMessage(chatID, "Веб-приложение турнира пока не сконфигурировано (WEB_APP_URL не задан).", "main_menu")
			return
		}
		msg := tgbotapi.NewMessage(chatID, "Нажмите кнопку ниже для перехода в турнирное приложение:")
		msg.ReplyMarkup = struct {
			Keyboard [][]struct {
				Text   string `json:"text"`
				WebApp struct {
					URL string `json:"url"`
				} `json:"web_app"`
			} `json:"inline_keyboard"`
		}{
			Keyboard: [][]struct {
				Text   string `json:"text"`
				WebApp struct {
					URL string `json:"url"`
				} `json:"web_app"`
			}{
				{
					{
						Text: "Открыть Valhalla App",
						WebApp: struct {
							URL string `json:"url"`
						}{URL: b.webAppURL},
					},
				},
			},
		}
		_, _ = b.bot.Send(msg)
		return

	case "/reg_solo":
		response = "Соло-регистрация отключена. Для участия регистрируйте команду: /reg_team"
		kbType = "main_menu"
	case "/reg_team":
		response, kbType = b.service.StartTeamRegistration(ctx, chatID)
	case "/my_team":
		response, kbType = b.service.GetTeamInfo(ctx, chatID)
	case "/checkin":
		response = b.service.ToggleCheckIn(ctx, chatID)
		kbType = "main_menu"
		b.notifyCheckIn(response)
	case "/checkin_status", "/checkins":
		response = b.service.GetCheckInStatus(ctx)
		kbType = "main_menu"
	case "/delete_team":
		response, kbType = b.service.HandleRegAction(ctx, chatID, "delete", "")
	case "/profile":
		response, kbType = b.handleProfile(ctx, chatID)
	case "/report":
		response, kbType = b.service.StartReport(ctx, chatID)

	default:
		response, kbType = b.service.HandleUserInput(ctx, chatID, text)
	}

	b.sendMessage(chatID, response, kbType)
}

func (b *Bot) handleProfile(ctx context.Context, chatID int64) (string, string) {
	player, _ := b.service.GetPlayer(ctx, chatID)
	var link *application.LinkedProfile
	if b.profileLinkService != nil {
		link, _ = b.profileLinkService.GetLinkedProfileByTelegram(ctx, chatID)
	}

	var sb strings.Builder
	sb.WriteString("Ваш профиль:\n\n")

	if link != nil && link.DiscordPlayerName != "" {
		sb.WriteString(fmt.Sprintf("Discord: %s\n", link.DiscordPlayerName))
	} else {
		sb.WriteString("Discord: Не привязан\n")
	}

	tgUsername := ""
	if player != nil && player.TelegramUsername != "" {
		tgUsername = player.TelegramUsername
	} else if link != nil && link.TelegramUsername != "" {
		tgUsername = link.TelegramUsername
	}
	if tgUsername != "" {
		sb.WriteString(fmt.Sprintf("Telegram: @%s\n", strings.TrimPrefix(tgUsername, "@")))
	}

	nick := ""
	gameID := ""
	zoneID := ""
	stars := 0
	role := ""

	if player != nil && player.GameNickname != "" {
		nick = player.GameNickname
		gameID = player.GameID
		zoneID = player.ZoneID
		stars = player.Stars
		role = player.MainRole
	} else if link != nil && link.GameNickname != "" {
		nick = link.GameNickname
		gameID = link.GameID
		zoneID = link.ZoneID
		stars = link.Stars
		role = link.MainRole
	}

	hasGameData := nick != ""

	sb.WriteString("\n-- Игровые данные --\n")
	sb.WriteString(fmt.Sprintf("Ник: %s\n", valueOrDefault(nick, "Не указан")))
	if gameID != "" && zoneID != "" {
		sb.WriteString(fmt.Sprintf("ID: %s (%s)\n", gameID, zoneID))
	} else {
		sb.WriteString(fmt.Sprintf("ID: %s\n", valueOrDefault(gameID, "-")))
	}
	sb.WriteString(fmt.Sprintf("Звёзды: %d | Роль: %s\n", stars, valueOrDefault(role, "Не указана")))

	if link != nil {
		matches := link.Wins + link.Losses
		wr := 0.0
		if matches > 0 {
			wr = float64(link.Wins) / float64(matches) * 100
		}
		d := link.Deaths
		if d == 0 {
			d = 1
		}
		kda := float64(link.Kills+link.Assists) / float64(d)

		sb.WriteString("\n-- Статистика Discord --\n")
		sb.WriteString(fmt.Sprintf("Матчей: %d\n", matches))
		sb.WriteString(fmt.Sprintf("Побед: %d | Поражений: %d\n", link.Wins, link.Losses))
		sb.WriteString(fmt.Sprintf("Винрейт: %.1f%%\n", wr))
		sb.WriteString(fmt.Sprintf("K/D/A: %d/%d/%d (%.2f)", link.Kills, link.Deaths, link.Assists, kda))
	}

	kbParam := "empty"
	if hasGameData {
		kbParam = "filled"
	}

	return sb.String(), application.KbRegProfile + ":" + kbParam
}

func (b *Bot) handlePhoto(ctx context.Context, chatID int64, msg *tgbotapi.Message) {
	p, err := b.service.GetPlayer(ctx, chatID)
	if err != nil || p == nil {
		b.sendMessage(chatID, "Используйте /start для начала.", "empty")
		return
	}

	if p.FSMState != models.StateReportScreenshots && p.FSMState != models.StateWaitingReport {
		b.sendMessage(chatID, "Чтобы отправить скриншот результата матча, сначала нажмите кнопку /report в меню.", "main_menu")
		return
	}

	photoID := msg.Photo[len(msg.Photo)-1].FileID
	resp, kbType, count := b.service.AddReportPhoto(ctx, chatID, photoID)
	if count == 0 {
		b.sendMessage(chatID, resp, kbType)
		return
	}

	b.photoTimersMu.Lock()
	if timer, ok := b.photoTimers[chatID]; ok && timer != nil {
		timer.Stop()
	}
	b.photoTimers[chatID] = time.AfterFunc(500*time.Millisecond, func() {
		b.photoTimersMu.Lock()
		delete(b.photoTimers, chatID)
		b.photoTimersMu.Unlock()

		draft := b.service.GetReportDraft(chatID)
		if draft != nil && len(draft.PhotoFileIDs) > 0 {
			n := len(draft.PhotoFileIDs)
			confirmText := fmt.Sprintf("Загружено скриншотов: %d.\nМатч: %s %s %s\n\nНажмите кнопку ниже для отправки отчета судьям или отправьте еще скриншоты:",
				n, draft.WinnerTeamName, draft.Score, draft.LoserTeamName)
			b.sendMessage(chatID, confirmText, application.KbReportPhotos+":"+strconv.Itoa(n))
		}
	})
	b.photoTimersMu.Unlock()
}
