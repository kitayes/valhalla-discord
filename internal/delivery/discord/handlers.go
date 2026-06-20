package discord

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleTop(s *discordgo.Session, i *discordgo.Interaction) {
	sortBy := "kda"
	options := i.ApplicationCommandData().Options
	if len(options) > 0 {
		sortBy = options[0].StringValue()
	}

	stats, err := b.services.MatchService.GetLeaderboard(sortBy)
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
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

		sb.WriteString(fmt.Sprintf("%s %s — WR: `%.0f%%` | KDA: `%.2f` (%d игр)\n",
			medal, p.Name, wr, kda, p.Matches))
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

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handleProfile(s *discordgo.Session, i *discordgo.Interaction) {
	id := i.ApplicationCommandData().Options[0].IntValue()

	p, err := b.services.MatchService.GetPlayerStatsByID(int(id))
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
		},
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handlePlayersList(s *discordgo.Session, i *discordgo.Interaction) {
	players, err := b.services.MatchService.GetPlayerList()
	if err != nil {
		b.respondMessage(s, i, "Ошибка получения списка: "+err.Error(), true)
		return
	}

	var sb strings.Builder
	sb.WriteString("Список зарегистрированных игроков:\n\n")
	for _, p := range players {
		sb.WriteString(fmt.Sprintf("`[%d]` **%s**\n", p.ID, p.Name))
	}

	msg := sb.String()
	if len(msg) > maxMessageLength {
		msg = msg[:maxMessageTruncation] + "...\n(список обрезан)"
	}

	b.respondMessage(s, i, msg, false)
}

func (b *Bot) handleHistory(s *discordgo.Session, i *discordgo.Interaction) {
	id := i.ApplicationCommandData().Options[0].IntValue()

	lines, err := b.services.MatchService.GetHistoryByID(int(id))
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
		return
	}

	if len(lines) == 0 {
		b.respondMessage(s, i, fmt.Sprintf("У игрока с ID %d нет истории матчей.", id), false)
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("История матчей (ID: %d)", id),
		Description: strings.Join(lines, "\n"),
		Color:       colorBlue,
		Footer:      &discordgo.MessageEmbedFooter{Text: "ID Матча | Результат | K/D/A | Дата"},
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

// =====================================================================
// DANGEROUS COMMANDS with confirmation buttons
// =====================================================================

func (b *Bot) handleWipePlayer(s *discordgo.Session, i *discordgo.Interaction) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())
	name, _ := b.services.MatchService.GetPlayerNameByID(id)
	if name == "" {
		name = fmt.Sprintf("ID:%d", id)
	}
	s.InteractionRespond(i, &discordgo.InteractionResponse{
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

func (b *Bot) handleResetPlayer(s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	id := int(options[0].IntValue())
	name, _ := b.services.MatchService.GetPlayerNameByID(id)
	if name == "" {
		name = fmt.Sprintf("ID:%d", id)
	}
	s.InteractionRespond(i, &discordgo.InteractionResponse{
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

func (b *Bot) handleWipe(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
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

func (b *Bot) handleReset(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
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

func (b *Bot) handleExport(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	data, err := b.services.MatchService.GetExcelReport()
	if err != nil {
		b.logger.Error("Export error: %v", err)
		s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
			Content: &[]string{"Ошибка экспорта: " + err.Error()}[0],
		})
		return
	}

	s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
		Content: &[]string{"Ваш отчет готов!"}[0],
		Files: []*discordgo.File{
			{Name: "статистика.xlsx", Reader: bytes.NewReader(data)},
		},
	})
}

func (b *Bot) handleSetTimer(s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	dateStr := options[0].StringValue()

	err := b.services.MatchService.SetTimer(dateStr)
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
	} else {
		b.respondMessage(s, i, fmt.Sprintf("Дата начала сезона установлена: %s", dateStr), false)
	}
}

func (b *Bot) handleSyncSheet(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	url, err := b.services.MatchService.SyncToGoogleSheet()
	if err != nil {
		s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
			Content: &[]string{"Ошибка синхронизации: " + err.Error()}[0],
		})
		return
	}

	s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
		Content: &[]string{fmt.Sprintf("Таблица успешно обновлена!\nСсылка: %s", url)}[0],
	})
}

func (b *Bot) handleDeleteMatch(s *discordgo.Session, i *discordgo.Interaction) {
	id := i.ApplicationCommandData().Options[0].IntValue()

	err := b.services.MatchService.DeleteMatch(int(id))
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Ошибка удаления: %v", err), true)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Матч #%d успешно удален из базы.", id), false)
}

func (b *Bot) handleRenamePlayer(s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	id := int(opts[0].IntValue())
	newName := opts[1].StringValue()

	oldName, err := b.services.MatchService.GetPlayerNameByID(id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	err = b.services.MatchService.RenamePlayer(id, newName)
	if err != nil {
		b.respondMessage(s, i, "Ошибка переименования: "+err.Error(), true)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Игрок переименован:\n**%s** → **%s**", oldName, newName), false)
}

func (b *Bot) handleUpdateNick(s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	id := int(opts[0].IntValue())
	newName := opts[1].StringValue()

	oldName, err := b.services.MatchService.GetPlayerNameByID(id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	err = b.services.MatchService.RenamePlayer(id, newName)
	if err != nil {
		s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
			Content: &[]string{"Ошибка переименования: " + err.Error()}[0],
		})
		return
	}

	go b.services.MatchService.SyncToGoogleSheet()

	s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
		Content: &[]string{fmt.Sprintf("✅ Никнейм обновлён!\n**%s** → **%s**\n\nКаскадное переименование: все прошлые матчи, база данных и Google Таблицы обновлены.", oldName, newName)}[0],
	})
}

func (b *Bot) handleScreenshots(s *discordgo.Session, m *discordgo.MessageCreate) {
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

	userID := m.Author.ID
	for range imageAttachments {
		if err := b.rateLimiter.Allow(userID, 5, 1); err != nil {
			b.logger.Warn("Rate limit exceeded for user %s", userID)
			s.ChannelMessageSend(m.ChannelID,
				"⚠️ Слишком много запросов. Пожалуйста, подождите немного перед следующей загрузкой.")
			return
		}
	}

	s.ChannelTyping(m.ChannelID)
	msg, _ := s.ChannelMessageSend(m.ChannelID,
		fmt.Sprintf("⏳ Анализирую %d скриншот(ов)...", len(imageAttachments)))

	expectedPlayers := b.threadPlayerCache[m.ChannelID]

	type result struct {
		matchID int
		err     error
		index   int
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
			matchID, err := b.services.MatchService.ProcessImageFromURLWithPlayers(attachment.URL, expectedPlayers)
			results[idx] = result{matchID: matchID, err: err, index: idx}
		}(i, att)
	}
	wg.Wait()

	if msg != nil {
		s.ChannelMessageDelete(m.ChannelID, msg.ID)
	}

	var successCount, duplicateCount, errorCount int
	var messages []string

	for _, res := range results {
		if res.err != nil {
			if strings.Contains(res.err.Error(), "duplicate match detected") {
				duplicateCount++
			} else {
				errorCount++
				messages = append(messages,
					fmt.Sprintf("❌ Скриншот %d: %v", res.index+1, res.err))
			}
		} else {
			successCount++
			messages = append(messages,
				fmt.Sprintf("✅ Скриншот %d: Матч #%d записан", res.index+1, res.matchID))
		}
	}

	summary := fmt.Sprintf("**Обработано: %d скриншотов**\n✅ Успешно: %d\n⚠️ Дубликаты: %d\n❌ Ошибки: %d",
		len(imageAttachments), successCount, duplicateCount, errorCount)

	if len(messages) > 0 {
		summary += "\n\n" + strings.Join(messages, "\n")
	}

	s.ChannelMessageSend(m.ChannelID, summary)

	// Archive the thread if screenshots were processed in a known match thread
	if len(expectedPlayers) > 0 {
		b.archiveThreadIfMatchFinished(s, m.ChannelID)
	}
}

func (b *Bot) handleLink(s *discordgo.Session, i *discordgo.Interaction) {
	playerID := int(i.ApplicationCommandData().Options[0].IntValue())
	playerName, err := b.services.MatchService.GetPlayerNameByID(playerID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", playerID), true)
		return
	}

	code, err := b.services.ProfileLinkService.GenerateLinkCodeByID(playerID)
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
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

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) handleUnlink(s *discordgo.Session, i *discordgo.Interaction) {
	playerName := i.ApplicationCommandData().Options[0].StringValue()
	err := b.services.ProfileLinkService.UnlinkByDiscordPlayer(playerName)
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
		return
	}
	b.respondMessage(s, i, fmt.Sprintf("✅ Telegram аккаунт отвязан от профиля **%s**", playerName), false)
}

// =====================================================================
// FAQ Commands
// =====================================================================

func (b *Bot) handleFAQ(s *discordgo.Session, i *discordgo.Interaction) {
	if b.services.FAQService == nil {
		b.respondMessage(s, i, "⚠️ FAQ-сервис не настроен. Укажите DEEPSEEK_KEY в .env", true)
		return
	}
	question := i.ApplicationCommandData().Options[0].StringValue()
	if len(strings.TrimSpace(question)) < 3 {
		b.respondMessage(s, i, "⚠️ Задайте вопрос длиной хотя бы 3 символа.", true)
		return
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	answer := b.services.FAQService.AnswerQuestion(question)
	s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
		Content: &answer,
	})
}

func (b *Bot) handleFAQReload(s *discordgo.Session, i *discordgo.Interaction) {
	if b.services.FAQService == nil {
		b.respondMessage(s, i, "⚠️ FAQ-сервис не настроен.", true)
		return
	}
	if err := b.services.FAQService.ReloadFAQ(); err != nil {
		b.respondMessage(s, i, "❌ Ошибка: "+err.Error(), true)
		return
	}
	b.respondMessage(s, i, "✅ База знаний FAQ перезагружена.", true)
}

// handleFAQAutoAnswer responds to plain text messages in the FAQ channel automatically.
func (b *Bot) handleFAQAutoAnswer(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.services.FAQService == nil {
		return
	}
	if strings.HasPrefix(m.Content, "/") || strings.HasPrefix(m.Content, "!") {
		return
	}
	s.ChannelTyping(m.ChannelID)
	answer := b.services.FAQService.AnswerQuestion(m.Content)
	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("<@%s>, %s", m.Author.ID, answer))
}

func (b *Bot) handleTelegramProfile(s *discordgo.Session, i *discordgo.Interaction) {
	playerName := i.ApplicationCommandData().Options[0].StringValue()
	profile, err := b.services.ProfileLinkService.GetLinkedProfile(playerName)
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
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

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

// =====================================================================
// Confirmation button handlers (register in bot.go via RegisterDangerButtonHandlers)
// =====================================================================

// RegisterDangerButtonHandlers registers handlers for confirm/cancel buttons of dangerous commands.
func (b *Bot) RegisterDangerButtonHandlers() {
	b.session.AddHandler(b.onDangerButton)
}

func (b *Bot) onDangerButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	data := i.MessageComponentData()

	switch {
	case strings.HasPrefix(data.CustomID, "confirm_wipe_player_"):
		id := parseIDFromPrefix(data.CustomID, "confirm_wipe_player_")
		b.executeWipePlayer(s, i.Interaction, id)
	case strings.HasPrefix(data.CustomID, "confirm_reset_player_"):
		id := parseIDFromPrefix(data.CustomID, "confirm_reset_player_")
		b.executeResetPlayer(s, i.Interaction, id)
	case data.CustomID == "confirm_wipe":
		b.executeWipe(s, i.Interaction)
	case data.CustomID == "confirm_reset":
		b.executeReset(s, i.Interaction)
	case data.CustomID == "cancel_wipe_player",
		data.CustomID == "cancel_reset_player",
		data.CustomID == "cancel_wipe",
		data.CustomID == "cancel_reset":
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "✅ Операция отменена.",
				Components: []discordgo.MessageComponent{},
			},
		})
	}
}

func (b *Bot) executeWipePlayer(s *discordgo.Session, i *discordgo.Interaction, playerID int) {
	err := b.services.MatchService.WipePlayerByID(playerID)
	if err != nil {
		s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "❌ Ошибка удаления: " + err.Error(),
				Components: []discordgo.MessageComponent{},
			},
		})
		return
	}
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ Игрок с ID **%d** и вся его статистика полностью удалены.", playerID),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeResetPlayer(s *discordgo.Session, i *discordgo.Interaction, playerID int) {
	name, _ := b.services.MatchService.GetPlayerNameByID(playerID)
	if name == "" {
		name = "Unknown"
	}
	err := b.services.MatchService.ResetPlayer(name, "now")
	if err != nil {
		s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "❌ Ошибка: " + err.Error(),
				Components: []discordgo.MessageComponent{},
			},
		})
		return
	}
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ Статистика игрока **%s** (ID: %d) сброшена.", name, playerID),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeWipe(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	err := b.services.MatchService.WipeAllData()
	if err != nil {
		s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
			Content: &[]string{"❌ Ошибка при очистке: " + err.Error()}[0],
		})
		return
	}
	s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
		Content: &[]string{"✅ База данных полностью очищена, Google Таблица сброшена."}[0],
	})
}

func (b *Bot) executeReset(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	err := b.services.MatchService.ResetGlobal()
	if err != nil {
		s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
			Content: &[]string{"❌ Ошибка: " + err.Error()}[0],
		})
		return
	}
	s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
		Content: &[]string{"✅ Сезонная статистика всех игроков сброшена."}[0],
	})
}

// parseIDFromPrefix extracts an int from a custom ID like "confirm_wipe_player_42".
func parseIDFromPrefix(customID, prefix string) int {
	var id int
	fmt.Sscanf(strings.TrimPrefix(customID, prefix), "%d", &id)
	return id
}

// archiveThreadIfMatchFinished checks if the match linked to this thread is finished and archives it.
func (b *Bot) archiveThreadIfMatchFinished(s *discordgo.Session, threadID string) {
	match, err := b.services.Lobby.GetMatchByThreadID(threadID)
	if err != nil || match == nil {
		return
	}
	if match.Status != "FINISHED" {
		return
	}

	// Send final message before archiving
	names, _ := b.services.Lobby.GetPlayerNamesByMatchID(match.ID)
	winMsg := fmt.Sprintf("🏁 **Матч #%d завершён.** Ветка будет архивирована.\nУчастники: %s",
		match.ID, strings.Join(names, ", "))
	s.ChannelMessageSend(threadID, winMsg)

	// Archive the thread
	archived := true
	_, err = s.ChannelEdit(threadID, &discordgo.ChannelEdit{Archived: &archived})
	if err != nil {
		b.logger.Warn("match: failed to archive thread %s: %v", threadID, err)
	} else {
		b.logger.Info("match: thread %s archived after match #%d finished", threadID, match.ID)
		// Clean up memory cache
		delete(b.threadPlayerCache, threadID)
	}
}
