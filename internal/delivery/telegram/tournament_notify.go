package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
