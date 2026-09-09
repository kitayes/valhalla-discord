package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleTop(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	sortBy := "kda"
	options := i.ApplicationCommandData().Options
	if len(options) > 0 {
		sortBy = options[0].StringValue()
	}

	stats, err := b.services.MatchService.GetLeaderboard(ctx, sortBy)
	if err != nil {
		b.reportFailure(s, i, "top", "⚠️ Не удалось получить лидерборд. Попробуйте позже.", err)
		return
	}

	if len(stats) == 0 {
		b.respondMessage(s, i, "Статистики пока нет. Сыграйте матч!", false)
		return
	}

	topCount := topPlayersLimit
	if len(stats) < topCount {
		topCount = len(stats)
	}

	var sb strings.Builder
	for idx, p := range stats[:topCount] {
		medal := getMedalEmoji(idx)
		wr := calculateWinRate(p)
		kda := calculateKDA(p.Kills, p.Deaths, p.Assists)

		sb.WriteString(fmt.Sprintf("%s %s — WR: `%.0f%%` | KDA: `%.2f` (%d игр)",
			medal, p.Name, wr, kda, p.Matches))
		if p.MVP > 0 {
			sb.WriteString(fmt.Sprintf(" 🏅×%d", p.MVP))
		}
		sb.WriteString("\n")
	}

	title := "Таблица лидеров (по KDA)"
	if sortBy == "winrate" {
		title = "Таблица лидеров (по Винрейту)"
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: sb.String(),
		Color:       colorGold,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Valhalla Ranked Season"},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

// resolveTargetPlayer works out whose profile a command is asking about.
//
// Nobody knows their own numeric id, so the id argument is gone: with no
// arguments the command answers about the caller, resolved through the Discord
// binding. A nickname still works for the many profiles that came out of
// screenshots and have nobody bound to them.
//
// Answers the interaction and reports false when the target cannot be resolved.
func (b *Bot) resolveTargetPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) (int, string, bool) {
	data := i.ApplicationCommandData()

	if opt, ok := commandOption(data, "nickname"); ok {
		nickname := strings.TrimSpace(opt.StringValue())
		id, name, err := b.services.MatchService.FindPlayerByName(ctx, nickname)
		if err != nil {
			b.respondMessage(s, i, fmt.Sprintf("⚠️ Игрок **%s** не найден.", nickname), true)
			return 0, "", false
		}
		return id, name, true
	}

	if opt, ok := commandOption(data, "user"); ok {
		target := opt.UserValue(s)
		if target == nil {
			b.respondMessage(s, i, "⚠️ Не удалось определить пользователя.", true)
			return 0, "", false
		}
		id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, target.ID)
		if err != nil {
			b.respondMessage(s, i, fmt.Sprintf("⚠️ У <@%s> нет привязанного профиля.", target.ID), true)
			return 0, "", false
		}
		return id, name, true
	}

	return b.callerProfile(s, i)
}

// commandOption finds a named option, which is how optional arguments have to
// be read: Discord sends only the options the user actually filled in, so
// indexing into the slice by position lands on the wrong one.
func commandOption(data discordgo.ApplicationCommandInteractionData, name string) (*discordgo.ApplicationCommandInteractionDataOption, bool) {
	for _, opt := range data.Options {
		if opt.Name == name {
			return opt, true
		}
	}
	return nil, false
}

func (b *Bot) handleProfile(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id, _, ok := b.resolveTargetPlayer(ctx, s, i)
	if !ok {
		return
	}

	p, err := b.services.MatchService.GetPlayerStatsByID(ctx, id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	wr := calculateWinRate(p)
	kda := calculateKDA(p.Kills, p.Deaths, p.Assists)
	color := getColorByWinRate(wr)

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("Профиль: %s (ID: %d)", p.Name, id),
		Color: color,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Матчей", Value: fmt.Sprintf("%d", p.Matches), Inline: true},
			{Name: "Винрейт", Value: fmt.Sprintf("%.1f%%", wr), Inline: true},
			{Name: "KDA", Value: fmt.Sprintf("%.2f", kda), Inline: true},
			{Name: "Статистика", Value: fmt.Sprintf("⚔️ K: %d | 💀 D: %d | 🤝 A: %d", p.Kills, p.Deaths, p.Assists), Inline: false},
			{Name: "Результаты", Value: fmt.Sprintf("✅ Побед: %d | ❌ Поражений: %d", p.Wins, p.Losses), Inline: false},
			{Name: "Медали (сезон)", Value: fmt.Sprintf("🏅 MVP: %d | 🥈 SVPG: %d", p.MVP, p.SVP), Inline: false},
		},
	}

	if lifetime, err := b.services.MatchService.GetLifetimeMedals(ctx, id); err != nil {
		b.logger.Warn("profile: failed to get lifetime medals for player %d: %v", id, err)
	} else if lifetime.MVP > p.MVP || lifetime.SVP > p.SVP {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Медали (за всё время)",
			Value:  fmt.Sprintf("🏅 MVP: %d | 🥈 SVPG: %d", lifetime.MVP, lifetime.SVP),
			Inline: false,
		})
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handlePlayersList(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	players, err := b.services.MatchService.GetPlayerList(ctx)
	if err != nil {
		b.reportFailure(s, i, "players", "⚠️ Не удалось получить список игроков. Попробуйте позже.", err)
		return
	}

	var sb strings.Builder
	sb.WriteString("Список зарегистрированных игроков:\n\n")
	for _, p := range players {
		sb.WriteString(fmt.Sprintf("`[%d]` **%s**\n", p.ID, p.Name))
	}

	b.respondMessage(s, i, truncateMessage(sb.String()), false)
}

func (b *Bot) handleHistory(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id, name, ok := b.resolveTargetPlayer(ctx, s, i)
	if !ok {
		return
	}

	lines, err := b.services.MatchService.GetHistoryByID(ctx, id)
	if err != nil {
		b.reportFailure(s, i, "history", "⚠️ Не удалось получить историю матчей.", err)
		return
	}

	if len(lines) == 0 {
		b.respondMessage(s, i, fmt.Sprintf("У игрока **%s** нет истории матчей.", name), false)
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("История матчей: %s", name),
		Description: strings.Join(lines, "\n"),
		Color:       colorBlue,
		Footer:      &discordgo.MessageEmbedFooter{Text: "ID Матча | Результат | K/D/A | Дата"},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

// =====================================================================
// DANGEROUS COMMANDS with confirmation buttons
// =====================================================================

func (b *Bot) handleWipePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())
	name, _ := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if name == "" {
		name = fmt.Sprintf("ID:%d", id)
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("⚠️ **Вы уверены, что хотите удалить игрока %s (ID: %d)?**\n\nЭто действие необратимо!", name, id),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "🔴 Да, удалить",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("confirm_wipe_player_%d", id),
						},
						discordgo.Button{
							Label:    "🟢 Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_wipe_player",
						},
					},
				},
			},
		},
	})
}

func (b *Bot) handleResetPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	id := int(options[0].IntValue())
	name, _ := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if name == "" {
		name = fmt.Sprintf("ID:%d", id)
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("⚠️ **Сбросить сезонную статистику игрока %s (ID: %d)?**", name, id),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "🟡 Да, сбросить",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("confirm_reset_player_%d", id),
						},
						discordgo.Button{
							Label:    "🟢 Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_reset_player",
						},
					},
				},
			},
		},
	})
}

func (b *Bot) handleWipe(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "⚠️ **Вы уверены, что хотите полностью очистить базу данных? Это действие необратимо!**\n\nВсе матчи, игроки и статистика будут удалены.",
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "🔴 Да, очистить всё",
							Style:    discordgo.DangerButton,
							CustomID: "confirm_wipe",
						},
						discordgo.Button{
							Label:    "🟢 Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_wipe",
						},
					},
				},
			},
		},
	})
}

func (b *Bot) handleReset(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "⚠️ **Сбросить сезонную статистику всех игроков?**",
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "🟡 Да, сбросить сезон",
							Style:    discordgo.DangerButton,
							CustomID: "confirm_reset",
						},
						discordgo.Button{
							Label:    "🟢 Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_reset",
						},
					},
				},
			},
		},
	})
}

// =====================================================================
// Admin commands (safe, no confirmation needed or already deferred)
// =====================================================================

func (b *Bot) handleExport(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, false)

	data, err := b.services.MatchService.GetExcelReport(ctx)
	if err != nil {
		b.reportFailureEdit(s, i, "export", "⚠️ Не удалось сформировать отчёт.", err)
		return
	}

	content := "Ваш отчет готов!"
	b.editResponse(s, i, &discordgo.WebhookEdit{
		Content: &content,
		Files: []*discordgo.File{
			{Name: "статистика.xlsx", Reader: bytes.NewReader(data)},
		},
	})
}

func (b *Bot) handleSetTimer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	dateStr := options[0].StringValue()

	err := b.services.MatchService.SetTimer(ctx, dateStr)
	if err != nil {
		b.reportFailure(s, i, "set_timer", "⚠️ Не удалось установить дату начала сезона. Проверьте формат.", err)
	} else {
		b.respondMessage(s, i, fmt.Sprintf("Дата начала сезона установлена: %s", dateStr), false)
	}
}

func (b *Bot) handleSyncSheet(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, false)

	url, err := b.services.MatchService.SyncToGoogleSheet(ctx)
	if err != nil {
		b.reportFailureEdit(s, i, "sync_sheet", "⚠️ Не удалось синхронизировать таблицу.", err)
		return
	}

	b.editContent(s, i, fmt.Sprintf("Таблица успешно обновлена!\nСсылка: %s", url))
}

func (b *Bot) handleDeleteMatch(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id := i.ApplicationCommandData().Options[0].IntValue()

	err := b.services.MatchService.DeleteMatch(ctx, int(id))
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Ошибка удаления: %v", err), true)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Матч #%d успешно удален из базы.", id), false)
}

func (b *Bot) handleRenamePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	id := int(opts[0].IntValue())
	newName := opts[1].StringValue()

	oldName, err := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	err = b.services.MatchService.RenamePlayer(ctx, id, newName)
	if err != nil {
		b.reportFailure(s, i, "rename_player", "⚠️ Не удалось переименовать игрока.", err)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Игрок переименован:\n**%s** → **%s**", oldName, newName), false)
}

func (b *Bot) handleUpdateNick(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	id := int(opts[0].IntValue())
	newName := opts[1].StringValue()

	oldName, err := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	b.deferResponse(s, i, false)

	err = b.services.MatchService.RenamePlayer(ctx, id, newName)
	if err != nil {
		b.reportFailureEdit(s, i, "rename_player", "⚠️ Не удалось переименовать игрока.", err)
		return
	}

	b.goBackground("sync-sheet-after-rename", backgroundTimeout, func(bg context.Context) {
		if _, err := b.services.MatchService.SyncToGoogleSheet(bg); err != nil {
			b.logger.Error("update_nick: failed to sync sheet: %v", err)
		}
	})

	b.editContent(s, i, fmt.Sprintf("✅ Никнейм обновлён!\n**%s** → **%s**\n\nКаскадное переименование: все прошлые матчи, база данных и Google Таблицы обновлены.", oldName, newName))
}

func (b *Bot) handleScreenshots(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate) {
	var imageAttachments []*discordgo.MessageAttachment
	for _, att := range m.Attachments {
		filename := strings.ToLower(att.Filename)
		if strings.HasSuffix(filename, ".png") ||
			strings.HasSuffix(filename, ".jpg") ||
			strings.HasSuffix(filename, ".jpeg") {
			imageAttachments = append(imageAttachments, att)
		}
	}

	if len(imageAttachments) == 0 {
		return
	}

	// One token per attachment, and the whole batch is refused together: taking
	// them one at a time left the tokens already consumed spent on a batch that
	// was then dropped, so the user paid for work that never ran.
	userID := m.Author.ID
	taken := 0
	for range imageAttachments {
		if err := b.rateLimiter.Allow(userID, uploadBurstPerUser, uploadRefillPerUser); err != nil {
			b.rateLimiter.Refund(userID, taken)
			b.logger.Warn("Rate limit exceeded for user %s (%d screenshot(s) requested)", userID, len(imageAttachments))
			b.sendChannelMessage(s, m.ChannelID,
				"⚠️ Слишком много запросов. Пожалуйста, подождите немного перед следующей загрузкой.")
			return
		}
		taken++
	}

	b.startTyping(s, m.ChannelID)
	msg := b.sendChannelMessage(s, m.ChannelID,
		fmt.Sprintf("⏳ Анализирую %d скриншот(ов)...", len(imageAttachments)))

	expectedPlayers, _ := b.getThreadPlayers(m.ChannelID)

	type result struct {
		parsed *models.MatchResult
		err    error
		index  int
	}

	results := make([]result, len(imageAttachments))
	semaphore := make(chan struct{}, 3)

	var wg sync.WaitGroup
	for i, att := range imageAttachments {
		wg.Add(1)
		go func(idx int, attachment *discordgo.MessageAttachment) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			parsed, err := b.services.MatchService.ProcessImageFromURLWithPlayers(ctx, attachment.URL, expectedPlayers)
			results[idx] = result{parsed: parsed, err: err, index: idx}
		}(i, att)
	}
	wg.Wait()

	if msg != nil {
		if err := s.ChannelMessageDelete(m.ChannelID, msg.ID); err != nil {
			b.logger.Warn("discord: failed to delete progress message in %s: %v", m.ChannelID, err)
		}
	}

	var successCount, duplicateCount, errorCount int
	var messages []string

	for _, res := range results {
		if res.err != nil {
			// Matched on the sentinel, not on the text of the message: the
			// substring check silently stopped counting duplicates the moment
			// anyone reworded the error.
			if errors.Is(res.err, domain.ErrDuplicateMatch) {
				duplicateCount++
			} else {
				errorCount++
				messages = append(messages,
					fmt.Sprintf("❌ Скриншот %d: %v", res.index+1, res.err))
			}
			continue
		}

		if res.parsed == nil {
			errorCount++
			messages = append(messages,
				fmt.Sprintf("❌ Скриншот %d: пустой результат распознавания", res.index+1))
			continue
		}

		successCount++
		line := fmt.Sprintf("✅ Скриншот %d: Матч #%d записан", res.index+1, res.parsed.MatchID)
		if medals := formatMedals(res.parsed); medals != "" {
			line += " " + medals
		}
		messages = append(messages, line)

		b.attachMedalsToLobbyMatch(ctx, m.ChannelID, res.parsed)
	}

	summary := fmt.Sprintf("**Обработано: %d скриншотов**\n✅ Успешно: %d\n⚠️ Дубликаты: %d\n❌ Ошибки: %d",
		len(imageAttachments), successCount, duplicateCount, errorCount)

	if len(messages) > 0 {
		summary += "\n\n" + strings.Join(messages, "\n")
	}

	s.ChannelMessageSend(m.ChannelID, summary)

	// Archive the thread if screenshots were processed in a known match thread
	if len(expectedPlayers) > 0 {
		b.archiveThreadIfMatchFinished(ctx, s, m.ChannelID)
	}
}

// attachMedalsToLobbyMatch records the medals recognised on a screenshot onto
// the lobby match the thread belongs to, so the referee closing the match awards
// the MVP/SVPG bonus. Screenshots posted outside a match thread only count
// towards the global medal tallies.
func (b *Bot) attachMedalsToLobbyMatch(ctx context.Context, channelID string, parsed *models.MatchResult) {
	if parsed == nil || (parsed.MVP == "" && parsed.SVP == "") {
		return
	}

	match, err := b.services.Lobby.GetMatchByThreadID(ctx, channelID)
	if err != nil || match == nil {
		return // not a match thread — nothing to attach to
	}

	if err := b.services.Lobby.SaveMedals(ctx, match.ID, parsed.MVP, parsed.SVP); err != nil {
		b.logger.Error("match: failed to save medals for #%d: %v", match.ID, err)
		return
	}
	b.logger.Info("match: medals recorded for #%d — MVP: %q, SVP: %q", match.ID, parsed.MVP, parsed.SVP)
}

// formatMedals renders the recognised medals for the upload summary.
func formatMedals(parsed *models.MatchResult) string {
	var parts []string
	if parsed.MVP != "" {
		parts = append(parts, fmt.Sprintf("🏅 MVP: **%s**", parsed.MVP))
	}
	if parsed.SVP != "" {
		parts = append(parts, fmt.Sprintf("🥈 SVPG: **%s**", parsed.SVP))
	}
	return strings.Join(parts, " | ")
}

func (b *Bot) handleLink(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	// The profile comes from the caller's own Discord binding, never from an
	// argument. /link used to take any player id and hand out a code for it, so
	// anyone could claim anyone's profile from Telegram — and with /unlink
	// equally open, the "already linked" guard was two commands away from being
	// bypassed. Both platforms now attach to the row the caller already owns.
	playerID, playerName, ok := b.callerProfile(s, i)
	if !ok {
		return
	}

	code, err := b.services.ProfileLinkService.GenerateLinkCodeByID(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "link", "⚠️ Не удалось выдать код привязки.", err)
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🔗 Код привязки Telegram",
		Description: fmt.Sprintf("Отправьте этот код боту в Telegram:\n\n```\n/link %s\n```\n\n⏰ Код действителен 10 минут", code),
		Color:       colorBlue,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Игрок", Value: fmt.Sprintf("%s (ID: %d)", playerName, playerID), Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: "Valhalla Profile Sync"},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

// handleUnlink releases the caller's own Telegram binding. See handleLink for
// why the profile is not taken from an argument.
func (b *Bot) handleUnlink(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	playerID, playerName, ok := b.callerProfile(s, i)
	if !ok {
		return
	}
	if err := b.services.ProfileLinkService.UnlinkPlayerID(ctx, playerID); err != nil {
		b.reportFailure(s, i, "unlink", "⚠️ Не удалось отвязать аккаунт.", err)
		return
	}
	b.respondMessage(s, i, fmt.Sprintf("✅ Telegram аккаунт отвязан от профиля **%s**", playerName), true)
}

// handleUnlinkPlayer releases someone else's Telegram binding. Admin-only, the
// counterpart of /unbind_player on the Discord side of the same profile.
func (b *Bot) handleUnlinkPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	playerID := int(i.ApplicationCommandData().Options[0].IntValue())
	playerName, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "unlink_player",
			fmt.Sprintf("⚠️ Игрок с ID %d не найден.", playerID), err)
		return
	}
	if err := b.services.ProfileLinkService.UnlinkPlayerID(ctx, playerID); err != nil {
		b.reportFailure(s, i, "unlink_player", "⚠️ Не удалось отвязать аккаунт.", err)
		return
	}
	b.respondMessage(s, i, fmt.Sprintf("✅ Telegram отвязан от профиля **%s** (ID: %d)", playerName, playerID), false)
}

// callerProfile resolves the player the caller has claimed with /bind.
//
// It answers the interaction and reports false when there is no binding, so the
// commands that act on "your profile" have exactly one place that decides whose
// profile that is.
func (b *Bot) callerProfile(s *discordgo.Session, i *discordgo.Interaction) (int, string, bool) {
	member := interactionMember(i)
	if member == nil {
		b.respondMessage(s, i, "⚠️ Эта команда работает только на сервере.", true)
		return 0, "", false
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, member.User.ID)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			b.respond(s, i, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content:    "⚠️ Ваш Discord не привязан к профилю.\n\nНажмите кнопку и укажите свой ник в игре.",
					Components: bindPromptComponents(),
					Flags:      discordgo.MessageFlagsEphemeral,
				},
			})
			return 0, "", false
		}
		b.reportFailure(s, i, "caller profile", "⚠️ Не удалось определить ваш профиль. Попробуйте позже.", err)
		return 0, "", false
	}
	return id, name, true
}

// =====================================================================
// FAQ Commands
// =====================================================================

func (b *Bot) handleFAQ(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.services.FAQService == nil {
		b.respondMessage(s, i, "⚠️ FAQ-сервис не настроен. Укажите DEEPSEEK_KEY в .env", true)
		return
	}
	question := i.ApplicationCommandData().Options[0].StringValue()
	if len(strings.TrimSpace(question)) < 3 {
		b.respondMessage(s, i, "⚠️ Задайте вопрос длиной хотя бы 3 символа.", true)
		return
	}

	b.deferResponse(s, i, false)

	answer := b.services.FAQService.AnswerQuestion(ctx, question)
	b.editContent(s, i, answer)
}

func (b *Bot) handleFAQReload(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.services.FAQService == nil {
		b.respondMessage(s, i, "⚠️ FAQ-сервис не настроен.", true)
		return
	}
	if err := b.services.FAQService.ReloadFAQ(); err != nil {
		b.reportFailure(s, i, "faq_reload", "❌ Не удалось перезагрузить базу знаний FAQ.", err)
		return
	}
	b.respondMessage(s, i, "✅ База знаний FAQ перезагружена.", true)
}

// handleFAQAutoAnswer responds to plain text messages in the FAQ channel automatically.
//
// Every answer is a paid DeepSeek call, so it is rate limited per user: without
// it, anyone could run up the API bill just by typing in the channel.
func (b *Bot) handleFAQAutoAnswer(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.services.FAQService == nil {
		return
	}
	if strings.HasPrefix(m.Content, "/") || strings.HasPrefix(m.Content, "!") {
		return
	}

	if err := b.faqRateLimiter.AllowRate(m.Author.ID, faqBurstPerUser, faqRefillPerUser); err != nil {
		b.logger.Warn("faq: rate limit exceeded for user %s", m.Author.ID)
		return
	}

	b.startTyping(s, m.ChannelID)
	answer := b.services.FAQService.AnswerQuestion(ctx, m.Content)
	if _, err := s.ChannelMessageSend(m.ChannelID, truncateMessage(fmt.Sprintf("<@%s>, %s", m.Author.ID, answer))); err != nil {
		b.logger.Error("faq: failed to post auto answer: %v", err)
	}
}

func (b *Bot) handleTelegramProfile(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	// Someone else's profile is admin-only, and the answer is always ephemeral.
	//
	// This printed a stranger's @username and Telegram ID into the channel for
	// anyone who typed a name — a public command handing out other people's
	// contact details. /link and /whoami next to it were already ephemeral.
	member := interactionMember(i)
	if member == nil {
		b.respondMessage(s, i, "⚠️ Эта команда работает только на сервере.", true)
		return
	}

	playerName := ""
	if opts := i.ApplicationCommandData().Options; len(opts) > 0 {
		playerName = opts[0].StringValue()
	}

	if playerName == "" {
		_, own, ok := b.callerProfile(s, i)
		if !ok {
			return
		}
		playerName = own
	} else if !b.isAdmin(member.User.ID) {
		_, own, ok := b.callerProfile(s, i)
		if !ok {
			return
		}
		if !strings.EqualFold(playerName, own) {
			b.respondMessage(s, i,
				"⛔ Чужой Telegram-профиль может посмотреть только админ. Свой — `/telegram_profile` без аргумента.", true)
			return
		}
	}

	profile, err := b.services.ProfileLinkService.GetLinkedProfile(ctx, playerName)
	if err != nil {
		b.reportFailure(s, i, "telegram_profile", "⚠️ Не удалось получить профиль.", err)
		return
	}
	if profile == nil {
		b.respondMessage(s, i, fmt.Sprintf("Профиль **%s** не привязан к Telegram", playerName), true)
		return
	}

	tgInfo := "Не привязан"
	if profile.TelegramID != nil {
		tgInfo = fmt.Sprintf("@%s (ID: %d)", profile.TelegramUsername, *profile.TelegramID)
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("📱 Telegram профиль: %s", playerName),
		Color: colorTelegramBlue,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Telegram", Value: tgInfo, Inline: false},
			{Name: "Игровой ник", Value: valueOrDefault(profile.GameNickname, "Не указан"), Inline: true},
			{Name: "Game ID", Value: valueOrDefault(profile.GameID, "—"), Inline: true},
			{Name: "Zone ID", Value: valueOrDefault(profile.ZoneID, "—"), Inline: true},
			{Name: "⭐ Звёзды", Value: fmt.Sprintf("%d", profile.Stars), Inline: true},
			{Name: "🎮 Роль", Value: valueOrDefault(profile.MainRole, "Не указана"), Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: "Valhalla Profile Sync"},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

// =====================================================================
// Confirmation button handlers (register in bot.go via RegisterDangerButtonHandlers)
// =====================================================================

// RegisterDangerButtonHandlers registers handlers for confirm/cancel buttons of dangerous commands.
func (b *Bot) RegisterDangerButtonHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onDangerButton))
}

func (b *Bot) onDangerButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if !strings.HasPrefix(data.CustomID, "confirm_") && !strings.HasPrefix(data.CustomID, "cancel_") {
		return
	}

	// The confirmation buttons live on an ephemeral message, but these actions
	// wipe the database — re-check the caller instead of trusting that only the
	// original admin can reach them.
	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}
	member := interactionMember(i.Interaction)
	if member == nil || !b.isAdmin(member.User.ID) {
		b.respondMessage(s, i.Interaction, "У вас нет прав.", true)
		return
	}

	switch {
	case strings.HasPrefix(data.CustomID, "confirm_wipe_player_"):
		id, ok := parseIDFromPrefix(data.CustomID, "confirm_wipe_player_")
		if !ok {
			b.rejectMalformedDangerButton(s, i.Interaction, data.CustomID)
			return
		}
		b.executeWipePlayer(ctx, s, i.Interaction, id)
	case strings.HasPrefix(data.CustomID, "confirm_reset_player_"):
		id, ok := parseIDFromPrefix(data.CustomID, "confirm_reset_player_")
		if !ok {
			b.rejectMalformedDangerButton(s, i.Interaction, data.CustomID)
			return
		}
		b.executeResetPlayer(ctx, s, i.Interaction, id)
	case data.CustomID == "confirm_wipe":
		b.executeWipe(ctx, s, i.Interaction)
	case data.CustomID == "confirm_reset":
		b.executeReset(ctx, s, i.Interaction)
	case data.CustomID == "cancel_wipe_player",
		data.CustomID == "cancel_reset_player",
		data.CustomID == "cancel_wipe",
		data.CustomID == "cancel_reset":
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "✅ Операция отменена.",
				Components: []discordgo.MessageComponent{},
			},
		})
	}
}

func (b *Bot) executeWipePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, playerID int) {
	if err := b.services.MatchService.WipePlayerByID(ctx, playerID); err != nil {
		b.logger.Error("wipe_player: failed to wipe player %d: %v", playerID, err)
		b.updateWithError(s, i, fmt.Sprintf("❌ Не удалось удалить игрока с ID %d.", playerID))
		return
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ Игрок с ID **%d** и вся его статистика полностью удалены.", playerID),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeResetPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, playerID int) {
	// The reset is keyed by name, so the name must be the real one. Falling back
	// to a literal "Unknown" on a failed lookup pointed a destructive operation
	// at whichever player happened to carry that nickname.
	name, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.logger.Error("reset_player: cannot resolve player %d: %v", playerID, err)
		b.updateWithError(s, i, fmt.Sprintf("❌ Не удалось найти игрока с ID %d. Сброс отменён.", playerID))
		return
	}

	if err := b.services.MatchService.ResetPlayer(ctx, name, "now"); err != nil {
		b.logger.Error("reset_player: failed to reset %s (ID: %d): %v", name, playerID, err)
		b.updateWithError(s, i, fmt.Sprintf("❌ Не удалось сбросить статистику игрока **%s**.", name))
		return
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ Статистика игрока **%s** (ID: %d) сброшена.", name, playerID),
			Components: []discordgo.MessageComponent{},
		},
	})
}

// updateWithError replaces a confirmation message with a failure notice and
// strips its buttons.
//
// The text is written for the operator, not copied from the error: err.Error()
// on these paths is a raw driver message and went straight into a Discord
// channel. The detail belongs in the log.
func (b *Bot) updateWithError(s *discordgo.Session, i *discordgo.Interaction, msg string) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    truncateMessage(msg),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeWipe(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, true)
	if err := b.services.MatchService.WipeAllData(ctx); err != nil {
		b.logger.Error("wipe: failed to wipe all data: %v", err)
		b.editContent(s, i, "❌ Ошибка при очистке. Подробности в логах.")
		return
	}
	b.editContent(s, i, "✅ База данных полностью очищена, Google Таблица сброшена.")
}

func (b *Bot) executeReset(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, true)
	if err := b.services.MatchService.ResetGlobal(ctx); err != nil {
		b.logger.Error("reset: failed to reset season: %v", err)
		b.editContent(s, i, "❌ Не удалось сбросить сезон. Подробности в логах.")
		return
	}
	b.editContent(s, i, "✅ Сезонная статистика всех игроков сброшена.")
}

// parseIDFromPrefix extracts an int from a custom ID like "confirm_wipe_player_42".
//
// It reports failure instead of falling back to 0: these IDs address destructive
// operations, and a swallowed parse error turned a malformed button into
// "player 0 deleted successfully" over a no-op.
func parseIDFromPrefix(customID, prefix string) (int, bool) {
	id, err := strconv.Atoi(strings.TrimPrefix(customID, prefix))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// rejectMalformedDangerButton refuses a confirmation whose target cannot be read.
func (b *Bot) rejectMalformedDangerButton(s *discordgo.Session, i *discordgo.Interaction, customID string) {
	b.logger.Error("discord: malformed danger button custom ID %q", customID)
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "⚠️ Некорректная кнопка подтверждения. Повторите команду.",
			Components: []discordgo.MessageComponent{},
		},
	})
}

// archiveThreadIfMatchFinished checks if the match linked to this thread is finished and archives it.
func (b *Bot) archiveThreadIfMatchFinished(ctx context.Context, s *discordgo.Session, threadID string) {
	match, err := b.services.Lobby.GetMatchByThreadID(ctx, threadID)
	if err != nil {
		b.logger.Error("discord: cannot resolve match for thread %s, not archiving: %v", threadID, err)
		return
	}
	if match == nil || match.Status != models.LobbyMatchStatusFinished {
		return
	}

	// Send final message before archiving
	names, err := b.services.Lobby.GetPlayerNamesByMatchID(ctx, match.ID)
	if err != nil {
		b.logger.Warn("discord: cannot list participants of match #%d: %v", match.ID, err)
	}
	winMsg := fmt.Sprintf("🏁 **Матч #%d завершён.** Ветка будет архивирована.\nУчастники: %s",
		match.ID, strings.Join(names, ", "))
	if _, err := s.ChannelMessageSend(threadID, winMsg); err != nil {
		b.logger.Warn("discord: cannot post closing note to thread %s: %v", threadID, err)
	}

	// Archive the thread
	archived := true
	_, err = s.ChannelEdit(threadID, &discordgo.ChannelEdit{Archived: &archived})
	if err != nil {
		b.logger.Warn("match: failed to archive thread %s: %v", threadID, err)
	} else {
		b.logger.Info("match: thread %s archived after match #%d finished", threadID, match.ID)
		// Clean up memory cache
		b.deleteThreadPlayers(threadID)
		b.deleteMatchPlayers(match.ID)
	}
}
