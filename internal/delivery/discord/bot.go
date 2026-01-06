package discord

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"valhalla/internal/application"
	"valhalla/pkg/config"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	session  *discordgo.Session
	services *application.Service
	logger   application.Logger

	adminIDs         map[string]struct{}
	allowedChannelID string
}

func NewBot(cfg *config.Config, services *application.Service, logger application.Logger) *Bot {
	s, _ := discordgo.New("Bot " + cfg.DiscordToken)

	admins := make(map[string]struct{})
	for _, id := range cfg.AdminUserIDs {
		cleanID := strings.TrimSpace(id)
		if cleanID != "" {
			admins[cleanID] = struct{}{}
		}
	}

	return &Bot{
		session:          s,
		services:         services,
		logger:           logger,
		adminIDs:         admins,
		allowedChannelID: cfg.AllowedChannelID,
	}
}

var commands = []*discordgo.ApplicationCommand{
	{Name: "export", Description: "Экспорт отчета в Excel (Только админы)"},
	{Name: "reset", Description: "Сброс сезона (Только админы)"},
	{
		Name:        "set_timer",
		Description: "Установить дату начала сезона (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "date", Description: "YYYY-MM-DD", Required: true},
		},
	},
	{
		Name:        "delete_match",
		Description: "Удалить матч по ID (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID матча", Required: true},
		},
	},
	{
		Name:        "wipe",
		Description: "ПОЛНОЕ УДАЛЕНИЕ всех данных и очистка таблиц (ОПАСНО)",
	},
	{Name: "sync_sheet", Description: "Синхронизация с Google Sheet (Только админы)"},

	{
		Name:        "reset_player",
		Description: "Сброс игрока по ID (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "date", Description: "YYYY-MM-DD", Required: false},
		},
	},
	{
		Name:        "wipe_player",
		Description: "Полное удаление игрока по ID (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	},

	{
		Name:        "players",
		Description: "Список всех игроков и их ID",
	},
	{
		Name:        "top",
		Description: "Таблица лидеров",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "sort",
				Description: "Критерий сортировки",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "По KDA", Value: "kda"},
					{Name: "По Винрейту", Value: "winrate"},
				},
			},
		},
	},
	{
		Name:        "profile",
		Description: "Статистика игрока (по ID)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	},
	{
		Name:        "history",
		Description: "История матчей игрока (по ID)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	},
}

func (b *Bot) Init() error {
	b.session.AddHandler(b.onInteraction)
	b.session.AddHandler(b.onMessage)
	return nil
}

func (b *Bot) Run(ctx context.Context) error {
	if err := b.session.Open(); err != nil {
		return err
	}

	b.logger.Info("Discord Bot Started. Registering slash commands...")

	_, err := b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, "1458104409677627576", commands)
	if err != nil {
		b.logger.Error("Failed to register commands: %v", err)
	} else {
		b.logger.Info("Slash commands registered successfully")
	}

	return nil
}

func (b *Bot) Stop() {
	b.session.Close()
}

func (b *Bot) isAdmin(userID string) bool {
	_, ok := b.adminIDs[userID]
	return ok
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	name := i.ApplicationCommandData().Name

	switch name {
	case "top":
		b.handleTop(s, i.Interaction)
		return
	case "profile":
		b.handleProfile(s, i.Interaction)
		return
	case "players":
		b.handlePlayersList(s, i.Interaction)
		return
	case "history":
		b.handleHistory(s, i.Interaction)
		return
	}

	if !b.isAdmin(i.Member.User.ID) {
		b.respondMessage(s, i.Interaction, "У вас нет прав.", true)
		return
	}

	switch name {
	case "export":
		b.handleExport(s, i.Interaction)
	case "reset":
		b.handleReset(s, i.Interaction)
	case "set_timer":
		b.handleSetTimer(s, i.Interaction)
	case "reset_player":
		b.handleResetPlayer(s, i.Interaction)
	case "sync_sheet":
		b.handleSyncSheet(s, i.Interaction)
	case "delete_match":
		b.handleDeleteMatch(s, i.Interaction)
	case "wipe":
		b.handleWipe(s, i.Interaction)
	case "wipe_player":
		b.handleWipePlayer(s, i.Interaction)
	}
}

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

	topCount := 10
	if len(stats) < topCount {
		topCount = len(stats)
	}

	var sb strings.Builder
	for idx, p := range stats[:topCount] {
		medal := "▪️"
		switch idx {
		case 0:
			medal = "🥇"
		case 1:
			medal = "🥈"
		case 2:
			medal = "🥉"
		}

		wr := 0.0
		if p.Matches > 0 {
			wr = (float64(p.Wins) / float64(p.Matches)) * 100
		}

		d := p.Deaths
		if d == 0 {
			d = 1
		}
		kda := float64(p.Kills+p.Assists) / float64(d)

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
		Color:       0xFFD700,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Valhalla Ranked Season"},
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handleProfile(s *discordgo.Session, i *discordgo.Interaction) {
	id := i.ApplicationCommandData().Options[0].IntValue()

	name, err := b.services.MatchService.GetPlayerNameByID(int(id))
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	p, err := b.services.MatchService.GetPlayerStats(name)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Нет данных для игрока %s.", name), true)
		return
	}

	wr := 0.0
	if p.Matches > 0 {
		wr = (float64(p.Wins) / float64(p.Matches)) * 100
	}

	d := p.Deaths
	if d == 0 {
		d = 1
	}
	kda := float64(p.Kills+p.Assists) / float64(d)

	color := 0x95A5A6
	if wr >= 60 {
		color = 0x2ECC71
	}
	if wr >= 75 {
		color = 0x9B59B6
	}
	if wr < 40 {
		color = 0xE74C3C
	}

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
	if len(msg) > 2000 {
		msg = msg[:1990] + "...\n(список обрезан)"
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
		Color:       0x3498DB,
		Footer:      &discordgo.MessageEmbedFooter{Text: "ID Матча | Результат | K/D/A | Дата"},
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handleWipePlayer(s *discordgo.Session, i *discordgo.Interaction) {
	id := i.ApplicationCommandData().Options[0].IntValue()

	err := b.services.MatchService.WipePlayerByID(int(id))
	if err != nil {
		b.respondMessage(s, i, "Ошибка удаления: "+err.Error(), true)
		return
	}
	b.respondMessage(s, i, fmt.Sprintf("Игрок с ID **%d** и вся его статистика полностью удалены.", id), false)
}

func (b *Bot) handleResetPlayer(s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	id := options[0].IntValue()
	dateStr := "now"
	if len(options) > 1 {
		dateStr = options[1].StringValue()
	}

	name, _ := b.services.MatchService.GetPlayerNameByID(int(id))
	if name == "" {
		name = "Unknown"
	}

	err := b.services.MatchService.ResetPlayer(name, dateStr)
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
	} else {
		b.respondMessage(s, i, fmt.Sprintf("Сезонная статистика игрока **%s** (ID: %d) сброшена.", name, id), false)
	}
}

func (b *Bot) handleWipe(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	err := b.services.MatchService.WipeAllData()
	if err != nil {
		s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
			Content: &[]string{"Ошибка при очистке: " + err.Error()}[0],
		})
		return
	}

	s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
		Content: &[]string{"УСПЕШНО! База данных полностью очищена, Google Таблица сброшена."}[0],
	})
}

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

func (b *Bot) handleReset(s *discordgo.Session, i *discordgo.Interaction) {
	err := b.services.MatchService.ResetGlobal()
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
	} else {
		b.respondMessage(s, i, "Статистика сезона полностью сброшена.", false)
	}
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

func (b *Bot) handleScreenshot(s *discordgo.Session, m *discordgo.MessageCreate) {
	filename := strings.ToLower(m.Attachments[0].Filename)
	if !strings.HasSuffix(filename, ".png") && !strings.HasSuffix(filename, ".jpg") && !strings.HasSuffix(filename, ".jpeg") {
		return
	}

	s.ChannelTyping(m.ChannelID)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(m.Attachments[0].URL)
	if err != nil {
		b.logger.Error("Failed to download image: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Ошибка загрузки изображения (таймаут или сеть).")
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		b.logger.Error("Failed to read image body: %v", err)
		return
	}

	msg, _ := s.ChannelMessageSend(m.ChannelID, "Анализирую скриншот... ")

	err = b.services.MatchService.ProcessImage(data)

	if msg != nil {
		s.ChannelMessageDelete(m.ChannelID, msg.ID)
	}

	if err != nil {
		if err.Error() == "duplicate match detected" {
			s.ChannelMessageSend(m.ChannelID, "Этот матч уже был загружен ранее.")
		} else {
			s.ChannelMessageSend(m.ChannelID, "Ошибка анализа: "+err.Error())
			b.logger.Error("Analysis error: %v", err)
		}
	} else {
		s.ChannelMessageSend(m.ChannelID, "Результаты матча успешно записаны!")
	}
}

// Вспомогательная функция
func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if b.allowedChannelID != "" && m.ChannelID != b.allowedChannelID {
		return
	}

	if len(m.Attachments) > 0 {
		b.handleScreenshot(s, m)
	}
}

func (b *Bot) respondMessage(s *discordgo.Session, i *discordgo.Interaction, msg string, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   flags,
		},
	})
}
