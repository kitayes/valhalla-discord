package telegram

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// resolveTournamentChatID resolves the configured tournamentChatID to a numeric chat ID.
// Supports numeric IDs (like -1001234567890) and public channel usernames (like @my_channel).
func (b *Bot) resolveTournamentChatID() int64 {
	target := strings.TrimSpace(b.tournamentChatID)
	if target == "" {
		return 0
	}
	if id, err := strconv.ParseInt(target, 10, 64); err == nil && id != 0 {
		return id
	}
	chat, err := b.bot.GetChat(tgbotapi.ChatInfoConfig{
		ChatConfig: tgbotapi.ChatConfig{
			SuperGroupUsername: strings.TrimPrefix(target, "@"),
		},
	})
	if err != nil {
		b.logger.Error("telegram: failed to resolve tournament chat %q: %v", target, err)
		return 0
	}
	return chat.ID
}

// notifyTournamentChat sends a message to the configured tournament chat.
// Does nothing if tournamentChatID is empty (not configured).
func (b *Bot) notifyTournamentChat(text string) {
	chatID := b.resolveTournamentChatID()
	if chatID == 0 || text == "" {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = ""
	if _, err := b.bot.Send(msg); err != nil {
		b.logger.Error("telegram: failed to send tournament notification to %d: %v", chatID, err)
		// If chatID is of format -XXXXXXXXXX (from Web K without 100), retry with -100 prefix:
		if chatID < 0 && chatID > -1000000000000 {
			supergroupID, parseErr := strconv.ParseInt(fmt.Sprintf("-100%d", -chatID), 10, 64)
			if parseErr == nil {
				msgRetry := tgbotapi.NewMessage(supergroupID, text)
				msgRetry.ParseMode = ""
				_, retryErr := b.bot.Send(msgRetry)
				if retryErr == nil {
					b.logger.Info("telegram: tournament notification sent to supergroup %d", supergroupID)
					b.tournamentChatID = strconv.FormatInt(supergroupID, 10)
					return
				}
				b.logger.Error("telegram: failed to send tournament notification retry to %d: %v", supergroupID, retryErr)
			}
		}
	}
}

// notifyTeamRegistered sends the full team card to the tournament chat.
func (b *Bot) notifyTeamRegistered(ctx context.Context, captainChatID int64) {
	if strings.TrimSpace(b.tournamentChatID) == "" {
		return
	}
	card, _ := b.service.GetTeamInfo(ctx, captainChatID)
	if card == "" {
		return
	}
	b.notifyTournamentChat("НОВАЯ КОМАНДА\n\n" + card)
}

// notifyCheckIn sends a check-in status change to the tournament chat.
func (b *Bot) notifyCheckIn(response string) {
	if strings.TrimSpace(b.tournamentChatID) == "" {
		return
	}
	if strings.Contains(response, "Check-in подтверждён") {
		b.notifyTournamentChat("CHECK-IN\n\n" + response)
	} else if strings.Contains(response, "Check-in снят") {
		b.notifyTournamentChat("CHECK-IN СНЯТ\n\n" + response)
	}
}

// notifyTeamDeletedFromResponse parses the team name from the delete response
// and sends a notification. byCaptain indicates whether the captain or admin did it.
func (b *Bot) notifyTeamDeletedFromResponse(response string, byCaptain bool) {
	if strings.TrimSpace(b.tournamentChatID) == "" {
		return
	}
	if !strings.Contains(response, "удалена") && !strings.Contains(response, "Удалена") {
		return
	}
	who := "капитаном"
	if !byCaptain {
		who = "администратором"
	}
	b.notifyTournamentChat(fmt.Sprintf("КОМАНДА УДАЛЕНА (%s)\n\n%s", who, response))
}

// notifyTeamReinstated sends a team reinstatement notification to the tournament chat.
func (b *Bot) notifyTeamReinstated(response string) {
	if strings.TrimSpace(b.tournamentChatID) == "" {
		return
	}
	if strings.Contains(response, "возвращена в турнир") {
		b.notifyTournamentChat("КОМАНДА ВОССТАНОВЛЕНА\n\n" + response)
	}
}

// notifyTechnicalDefeats sends the disqualification report to the tournament chat.
func (b *Bot) notifyTechnicalDefeats(report string) {
	if strings.TrimSpace(b.tournamentChatID) == "" {
		return
	}
	b.notifyTournamentChat(report)
}

// AnnounceQualified is announceQualified for a qualifier finished from the
// Mini App.
func (b *Bot) AnnounceQualified(ctx context.Context, res *application.FinishResult) {
	b.announceQualified(ctx, res)
}

// announceQualified posts a finished qualifier's results to the tournament
// chat and tells each advancing team.
func (b *Bot) announceQualified(ctx context.Context, res *application.FinishResult) {
	if res == nil || res.Tournament == nil {
		return
	}
	b.notifyTournamentChat(qualifiedText(res))
	for _, team := range res.Qualified {
		b.notifyTeam(ctx, team.ID, fmt.Sprintf("Поздравляем! Команда '%s' прошла квалификацию «%s» и выходит в основной этап.\n\nМатчей в этой квалификации у вас больше нет — дальше ничего играть не нужно. О времени основного этапа сообщим отдельно.", team.Name, res.Tournament.Name), "main_menu")
	}
	b.offerNextQualifier(res)
}

func qualifiedText(res *application.FinishResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("КВАЛИФИКАЦИЯ ЗАВЕРШЕНА\n\n«%s»: в основной этап прошли:\n", res.Tournament.Name))
	names := make([]string, 0, len(res.Qualified))
	for _, t := range res.Qualified {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	for i, name := range names {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
	}
	sb.WriteString("\nДальнейшие матчи этой квалификации не играются.")
	return sb.String()
}

// AnnounceRegistration tells every captain that a tournament's registration is
// open: teams from the last qualifier enter again, teams that already
// advanced are told they sit this one out. It returns how many captains are
// messaged; the messages go out in the background.
func (b *Bot) AnnounceRegistration(ctx context.Context, tournamentID int) (int, error) {
	t, invites, err := b.service.RegistrationInvites(ctx, tournamentID)
	if err != nil {
		return 0, err
	}
	start := "время старта объявим отдельно"
	if t.TournamentTime != nil {
		start = "старт " + t.TournamentTime.In(application.DisplayZone).Format("02.01 в 15:04") + " (КЗ)"
	}
	var qualified []string
	for _, inv := range invites {
		if inv.Qualified {
			qualified = append(qualified, inv.TeamName)
		}
	}
	sort.Strings(qualified)
	go func() {
		for _, inv := range invites {
			text := fmt.Sprintf("Открыта регистрация на «%s», %s.\n\nКоманды из прошлой квалификации заявляются заново: нажмите «Заявиться» в приложении. В день турнира капитан проходит check-in — без него команда не попадёт в сетку.", t.Name, start)
			if inv.Qualified {
				text = fmt.Sprintf("Открыта регистрация на «%s». Команда '%s' уже прошла в основной этап и в этой квалификации не участвует — заявляться не нужно.", t.Name, inv.TeamName)
			}
			b.SendMatchNotification(inv.ChatID, text, !inv.Qualified)
			time.Sleep(broadcastInterval)
		}
	}()
	chat := fmt.Sprintf("РЕГИСТРАЦИЯ ОТКРЫТА\n\n«%s», %s.\nКапитаны заявляют команды в приложении, в том числе команды из прошлой квалификации.", t.Name, start)
	if len(qualified) > 0 {
		chat += "\n\nУже в основном этапе и в этой квалификации не участвуют: " + strings.Join(qualified, ", ") + "."
	}
	b.notifyTournamentChat(chat)
	return len(invites), nil
}
