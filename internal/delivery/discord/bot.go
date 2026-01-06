package discord

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// Определяем список команд
var commands = []*discordgo.ApplicationCommand{
	{
		Name:        "export",
		Description: "Экспорт отчета в Excel (Только админы)",
	},
	{
		Name:        "reset",
		Description: "Сброс всей статистики сезона (Только админы)",
	},
	{
		Name:        "set_timer",
		Description: "Установить дату начала сезона (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "date",
				Description: "Формат: YYYY-MM-DD",
				Required:    true,
			},
		},
	},
	{
		Name:        "reset_player",
		Description: "Сброс статистики конкретного игрока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "nickname",
				Description: "Никнейм игрока",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "date",
				Description: "Дата сброса (YYYY-MM-DD) или оставьте пустым для 'сейчас'",
				Required:    false,
			},
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

	_, err := b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, "", commands)
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

	if !b.isAdmin(i.Member.User.ID) {
		b.respondMessage(s, i.Interaction, "У вас нет прав для выполнения этой команды.", true)
		return
	}

	switch i.ApplicationCommandData().Name {
	case "export":
		b.handleExport(s, i.Interaction)
	case "reset":
		b.handleReset(s, i.Interaction)
	case "set_timer":
		b.handleSetTimer(s, i.Interaction)
	case "reset_player":
		b.handleResetPlayer(s, i.Interaction)
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
			{Name: "report.xlsx", Reader: bytes.NewReader(data)},
		},
	})
}

func (b *Bot) handleReset(s *discordgo.Session, i *discordgo.Interaction) {
	err := b.services.MatchService.ResetGlobal()
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
	} else {
		b.respondMessage(s, i, "Статистика полностью сброшена (таймер обновлен).", false)
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

func (b *Bot) handleResetPlayer(s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	nickname := options[0].StringValue()
	dateStr := "now"
	if len(options) > 1 {
		dateStr = options[1].StringValue()
	}

	err := b.services.MatchService.ResetPlayer(nickname, dateStr)
	if err != nil {
		b.respondMessage(s, i, "Ошибка: "+err.Error(), true)
	} else {
		b.respondMessage(s, i, fmt.Sprintf("Статистика игрока **%s** сброшена.", nickname), false)
	}
}

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

func (b *Bot) handleScreenshot(s *discordgo.Session, m *discordgo.MessageCreate) {
	filename := strings.ToLower(m.Attachments[0].Filename)
	if !strings.HasSuffix(filename, ".png") && !strings.HasSuffix(filename, ".jpg") && !strings.HasSuffix(filename, ".jpeg") {
		return
	}

	s.ChannelTyping(m.ChannelID)

	resp, err := http.Get(m.Attachments[0].URL)
	if err != nil {
		b.logger.Error("Failed to download image: %v", err)
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

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
