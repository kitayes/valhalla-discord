package discord

import (
	"context"
	"strings"
	"valhalla/internal/application"
	"valhalla/pkg/config"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	session  *discordgo.Session
	services *application.Service
	logger   application.Logger
	commands []*discordgo.ApplicationCommand
	cfg      *config.Config

	adminIDs         map[string]struct{}
	allowedChannelID string
}

func NewBot(cfg *config.Config, services *application.Service, logger application.Logger) *Bot {
	return &Bot{
		cfg:              cfg,
		services:         services,
		logger:           logger,
		allowedChannelID: cfg.AllowedChannelID,
	}
}

func (b *Bot) Init() error {
	var err error

	b.session, err = discordgo.New("Bot " + b.cfg.DiscordToken)
	if err != nil {
		b.logger.Error("error creating Discord session: %v", err)
		return err
	}

	b.adminIDs = make(map[string]struct{})
	for _, id := range b.cfg.AdminUserIDs {
		cleanID := strings.TrimSpace(id)
		if cleanID != "" {
			b.adminIDs[cleanID] = struct{}{}
		}
	}

	b.addCommands(
		b.newExportCommand(),
		b.newResetCommand(),
		b.newSetTimerCommand(),
		b.newWipeCommand(),
		b.newDeleteMatchCommand(),
		b.newSyncSheetCommand(),
		b.newResetPlayerCommand(),
		b.newWipePlayerCommand(),
		b.newRenamePlayerCommand(),
		b.newPlayersCommand(),
		b.newTopCommand(),
		b.newProfileCommand(),
		b.newHistoryCommand(),
		b.newLinkCommand(),
		b.newUnlinkCommand(),
		b.newTelegramProfileCommand(),
	)

	b.session.AddHandler(b.onInteraction)
	b.session.AddHandler(b.onMessage)
	return nil
}

func (b *Bot) Run(ctx context.Context) error {
	if err := b.session.Open(); err != nil {
		return err
	}

	b.logger.Info("Discord Bot Started. Cleaning up and registering slash commands...")

	_, err := b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, "", nil)
	if err != nil {
		b.logger.Warn("Failed to clear global commands: %v", err)
	} else {
		b.logger.Info("Global commands cleared")
	}

	_, err = b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, defaultGuildID, b.commands)
	if err != nil {
		b.logger.Error("Failed to register commands: %v", err)
	} else {
		b.logger.Info("Slash commands registered successfully for guild %s", defaultGuildID)
	}

	return nil
}

func (b *Bot) Stop() {
	b.session.Close()
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
	case "link":
		b.handleLink(s, i.Interaction)
		return
	case "unlink":
		b.handleUnlink(s, i.Interaction)
		return
	case "telegram_profile":
		b.handleTelegramProfile(s, i.Interaction)
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
	case "rename_player":
		b.handleRenamePlayer(s, i.Interaction)
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
		b.handleScreenshots(s, m)
	}
}
