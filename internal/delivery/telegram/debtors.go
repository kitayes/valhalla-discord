package telegram

import (
	"blackwatch/internal/application"
	"blackwatch/internal/models"
	"context"
	"fmt"
	"strings"
	"time"
)

// formatCaptainMention formats a mention for team captain.
// If username is present, tags as @username (Nick) to trigger Telegram push mention.
func formatCaptainMention(t models.TelegramTeam) string {
	for _, m := range t.Players {
		if m.IsCaptain {
			nick := m.GameNickname
			if nick == "" {
				nick = m.FirstName
			}
			if m.TelegramUsername != "" {
				userTag := "@" + strings.TrimPrefix(m.TelegramUsername, "@")
				if nick != "" {
					return fmt.Sprintf("%s (%s)", userTag, nick)
				}
				return userTag
			}
			if nick != "" {
				return nick
			}
			return "Капитан"
		}
	}
	return "Капитан не указан"
}

// buildDebtorsChatAnnouncement formats a public mention announcement for the tournament chat.
func buildDebtorsChatAnnouncement(pendingTeams, incompleteTeams []models.TelegramTeam, customMsg, botUsername string) string {
	var sb strings.Builder
	sb.WriteString("ВНИМАНИЕ, ДОЛЖНИКИ ТУРНИРА!\n\n")

	if len(pendingTeams) > 0 {
		sb.WriteString(fmt.Sprintf("Ожидают Check-in (%d):\n", len(pendingTeams)))
		for i, t := range pendingTeams {
			sb.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, t.Name, formatCaptainMention(t)))
		}
	}

	if len(incompleteTeams) > 0 {
		if len(pendingTeams) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("Неполный состав (%d):\n", len(incompleteTeams)))
		for i, t := range incompleteTeams {
			sb.WriteString(fmt.Sprintf("%d. %s (%d/%d) — %s\n", i+1, t.Name, len(t.Players), application.MainRosterSlots, formatCaptainMention(t)))
		}
	}

	if customMsg != "" {
		sb.WriteString(fmt.Sprintf("\nСообщение от организаторов:\n%s\n", customMsg))
	}

	if botUsername != "" {
		sb.WriteString(fmt.Sprintf("\nСрочно подтвердите участие или доберите состав в боте @%s!", strings.TrimPrefix(botUsername, "@")))
	} else {
		sb.WriteString("\nСрочно подтвердите участие или доберите состав в боте!")
	}

	return sb.String()
}

// buildDebtorsAdminReport formats the execution report sent back to the admin.
func buildDebtorsAdminReport(pendingTeams, incompleteTeams []models.TelegramTeam, delivered, failed int, chatNotified bool) string {
	var sb strings.Builder
	sb.WriteString("Пинг должников завершён:\n\n")
	sb.WriteString(fmt.Sprintf("• Ожидают Check-in: %d\n", len(pendingTeams)))
	sb.WriteString(fmt.Sprintf("• Неполный состав: %d\n", len(incompleteTeams)))
	sb.WriteString(fmt.Sprintf("• Доставлено в ЛС: %d", delivered))
	if failed > 0 {
		sb.WriteString(fmt.Sprintf(" (ошибок: %d)", failed))
	}
	sb.WriteString("\n")

	if chatNotified {
		sb.WriteString("• Турнирный чат: объявление с тегами отправлено\n")
	} else {
		sb.WriteString("• Турнирный чат: не настроен\n")
	}

	sb.WriteString("\nСписок должников:\n")
	for _, t := range pendingTeams {
		sb.WriteString(fmt.Sprintf("- [-] %s: %s\n", t.Name, formatCaptainMention(t)))
	}
	for _, t := range incompleteTeams {
		sb.WriteString(fmt.Sprintf("- [!] %s (%d/%d): %s\n", t.Name, len(t.Players), application.MainRosterSlots, formatCaptainMention(t)))
	}

	return sb.String()
}

func (b *Bot) PingDebtors(ctx context.Context, adminChatID int64, customMsg string) error {
	b.pingDebtors(ctx, adminChatID, customMsg)
	return nil
}

func (b *Bot) handlePingDebtors(ctx context.Context, adminChatID int64, customMsg string) {
	b.sendMessage(adminChatID, "Запуск оповещения должников...", "main_menu")
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		b.pingDebtors(bgCtx, adminChatID, customMsg)
	}()
}

func (b *Bot) pingDebtors(ctx context.Context, adminChatID int64, customMsg string) {
	teams, err := b.service.GetUncheckedTeams(ctx)
	if err != nil {
		b.sendMessage(adminChatID, "Ошибка получения списка команд: "+err.Error(), "main_menu")
		return
	}

	var incompleteTeams, pendingTeams []models.TelegramTeam
	for _, t := range teams {
		if len(t.Players) < application.MainRosterSlots {
			incompleteTeams = append(incompleteTeams, t)
		} else {
			pendingTeams = append(pendingTeams, t)
		}
	}

	if len(incompleteTeams) == 0 && len(pendingTeams) == 0 {
		b.sendMessage(adminChatID, "Все команды укомплектованы и подтвердили Check-in! Должников нет.", "main_menu")
		return
	}

	tTime := b.service.GetTournamentTime(ctx)
	deadlineStr := ""
	if !tTime.IsZero() {
		deadlineStr = "до " + tTime.In(b.location).Add(technicalDefeatGrace).Format("15:04")
	} else {
		deadlineStr = "в ближайшее время"
	}

	delivered := 0
	failed := 0

	// 1. Notify captains of incomplete teams
	for _, team := range incompleteTeams {
		for _, p := range team.Players {
			if p.IsCaptain && p.TelegramID != nil {
				msg := fmt.Sprintf("ВНИМАНИЕ, Капитан!\nВ вашей команде '%s' не хватает игроков (%d из %d).\n\nСрочно доберите состав (минимум %d игроков) %s! Без полного состава команда не сможет подтвердить Check-in и получит ТЕХНИЧЕСКОЕ ПОРАЖЕНИЕ.",
					team.Name, len(team.Players), application.MainRosterSlots, application.MainRosterSlots, deadlineStr)
				if customMsg != "" {
					msg += "\n\nСообщение от организаторов:\n" + customMsg
				}
				if err := b.trySendMessage(*p.TelegramID, msg, "empty"); err != nil {
					failed++
					b.logger.Warn("telegram: failed to ping captain %d of incomplete team %s: %v", *p.TelegramID, team.Name, err)
				} else {
					delivered++
				}
			}
		}
	}

	// 2. Notify captains of pending complete teams
	for _, team := range pendingTeams {
		for _, p := range team.Players {
			if p.IsCaptain && p.TelegramID != nil {
				msg := fmt.Sprintf("ВНИМАНИЕ, Капитан!\nВаша команда '%s' ещё не подтвердила участие (Check-in).\n\nНажмите кнопку ниже %s или используйте /checkin, иначе — ТЕХНИЧЕСКОЕ ПОРАЖЕНИЕ.",
					team.Name, deadlineStr)
				if customMsg != "" {
					msg += "\n\nСообщение от организаторов:\n" + customMsg
				}
				if err := b.trySendMessage(*p.TelegramID, msg, application.KbRegCheckin); err != nil {
					failed++
					b.logger.Warn("telegram: failed to ping captain %d of pending team %s: %v", *p.TelegramID, team.Name, err)
				} else {
					delivered++
				}
			}
		}
	}

	// 3. Post public announcement in tournament chat
	chatNotified := false
	if strings.TrimSpace(b.tournamentChatID) != "" {
		botUser := ""
		if b.bot != nil && b.bot.Self.UserName != "" {
			botUser = b.bot.Self.UserName
		}
		ann := buildDebtorsChatAnnouncement(pendingTeams, incompleteTeams, customMsg, botUser)
		b.notifyTournamentChat(ann)
		chatNotified = true
	}

	// 4. Report back to admin
	report := buildDebtorsAdminReport(pendingTeams, incompleteTeams, delivered, failed, chatNotified)
	b.sendMessage(adminChatID, report, "main_menu")
}
