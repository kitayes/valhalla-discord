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

func (b *Bot) Init() error {
	b.session.AddHandler(b.onMessage)
	return nil
}

func (b *Bot) Run(ctx context.Context) error {
	b.logger.Info("Discord Bot Started")
	return b.session.Open()
}

func (b *Bot) Stop() {
	b.session.Close()
}

func (b *Bot) isAdmin(userID string) bool {
	_, ok := b.adminIDs[userID]
	return ok
}

func (b *Bot) handleExport(s *discordgo.Session, m *discordgo.MessageCreate) {
	s.ChannelMessageSend(m.ChannelID, "Генерирую отчет... 📊")

	data, err := b.services.MatchService.GetExcelReport()
	if err != nil {
		b.logger.Error("Export error: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Ошибка экспорта: "+err.Error())
		return
	}

	s.ChannelFileSend(m.ChannelID, "report.xlsx", bytes.NewReader(data))
}

func (b *Bot) handleScreenshot(s *discordgo.Session, m *discordgo.MessageCreate) {
	filename := strings.ToLower(m.Attachments[0].Filename)
	if !strings.HasSuffix(filename, ".png") && !strings.HasSuffix(filename, ".jpg") && !strings.HasSuffix(filename, ".jpeg") {
		return
	}

	resp, err := http.Get(m.Attachments[0].URL)
	if err != nil {
		b.logger.Error("Failed to download image: %v", err)
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	s.ChannelMessageSend(m.ChannelID, "Анализирую скриншот... ⏳")

	err = b.services.MatchService.ProcessImage(data)
	if err != nil {
		if err.Error() == "duplicate match detected" {
			s.ChannelMessageSend(m.ChannelID, "⚠️ Этот матч уже был загружен ранее.")
		} else {
			s.ChannelMessageSend(m.ChannelID, "Ошибка анализа: "+err.Error())
			b.logger.Error("Analysis error: %v", err)
		}
	} else {
		s.ChannelMessageSend(m.ChannelID, "Результаты матча успешно записаны! ✅")
	}
}

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if strings.HasPrefix(m.Content, "!") {
		if !b.isAdmin(m.Author.ID) {
			return
		}

		args := strings.Fields(m.Content)
		if len(args) == 0 {
			return
		}
		cmd := args[0]

		switch cmd {
		case "!export":
			b.handleExport(s, m)

		case "!set_timer":
			if len(args) < 2 {
				s.ChannelMessageSend(m.ChannelID, "⚠️ Формат: `!set_timer YYYY-MM-DD`")
				return
			}
			err := b.services.MatchService.SetTimer(args[1])
			if err != nil {
				s.ChannelMessageSend(m.ChannelID, "Ошибка: "+err.Error())
			} else {
				s.ChannelMessageSend(m.ChannelID, "📅 Таймер статистики установлен на "+args[1])
			}

		case "!reset":
			err := b.services.MatchService.ResetGlobal()
			if err != nil {
				s.ChannelMessageSend(m.ChannelID, "Ошибка: "+err.Error())
			} else {
				s.ChannelMessageSend(m.ChannelID, "🔄 Статистика сброшена (таймер установлен на сейчас).")
			}

		case "!reset_player":
			if len(args) < 2 {
				s.ChannelMessageSend(m.ChannelID, "⚠️ Формат: `!reset_player Nickname`")
				return
			}
			dateArg := "now"
			if len(args) > 2 {
				dateArg = args[2]
			}
			err := b.services.MatchService.ResetPlayer(args[1], dateArg)
			if err != nil {
				s.ChannelMessageSend(m.ChannelID, "Ошибка: "+err.Error())
			} else {
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("👤 Статистика игрока **%s** сброшена.", args[1]))
			}
		}
		return
	}

	if len(m.Attachments) > 0 {
		if m.ChannelID != b.allowedChannelID {
			return
		}
		b.handleScreenshot(s, m)
	}
}
