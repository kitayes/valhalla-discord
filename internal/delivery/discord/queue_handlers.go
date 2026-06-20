package discord

import (
	"fmt"
	"sort"
	"strings"

	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

const (
	lobbyEmbedColor = 0x9B59B6

	componentLabelMarkActive = "📌 Отметить активность / Войти в лобби"
	componentLabelLeave      = "🚪 Выйти из лобби"
	componentIDMarkActive    = "lobby_mark_active"
	componentIDLeave         = "lobby_leave"

	// create mix
	selectMenuCreateMix = "create_mix_select"
)

func (b *Bot) newLobbyCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lobby",
		Description: "Отправить постоянное сообщение лобби с кнопкой активности (Только админы)",
	}
}

func (b *Bot) newCreateMixCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "create_mix",
		Description: "Создать микс из активного лобби (Только капитаны/админы)",
	}
}

// RegisterQueueHandlers registers button and select-menu handlers.
func (b *Bot) RegisterQueueHandlers() {
	b.session.AddHandler(b.onLobbyButton)
	b.session.AddHandler(b.onCreateMixSelect)
}

func (b *Bot) onLobbyButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	switch data.CustomID {
	case componentIDMarkActive:
		b.handleMarkActive(s, i)
	case componentIDLeave:
		b.handleLeaveLobby(s, i)
	}
}

// handleLobbyPost creates or updates the permanent lobby message.
func (b *Bot) handleLobbyPost(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				b.buildLobbyEmbed(),
			},
			Components: b.buildLobbyComponents(),
		},
	})
}

func (b *Bot) handleMarkActive(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID
	username := i.Member.User.Username

	// Look up player by discord_id
	player, err := b.findPlayerByDiscordID(userID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("⚠️ Ваш Discord не привязан ни к одному игроку. Обратитесь к админу."),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	b.services.Lobby.AddPlayer(player)

	// Update the original lobby embed
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{b.buildLobbyEmbed()},
			Components: b.buildLobbyComponents(),
		},
	})

	b.logger.Info("lobby: %s marked active", username)
}

func (b *Bot) handleLeaveLobby(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID

	player, err := b.findPlayerByDiscordID(userID)
	if err == nil {
		b.services.Lobby.RemovePlayer(player.ID)
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{b.buildLobbyEmbed()},
			Components: b.buildLobbyComponents(),
		},
	})
}

// handleCreateMix sends the captain a select menu with active players.
func (b *Bot) handleCreateMix(s *discordgo.Session, i *discordgo.Interaction) {
	activePlayers := b.services.Lobby.GetActivePlayers()
	if len(activePlayers) < 2 {
		b.respondMessage(s, i, "Недостаточно активных игроков для создания микса (минимум 2).", true)
		return
	}

	// Sort by ID for consistent display
	sort.Slice(activePlayers, func(a, b int) bool {
		return activePlayers[a].ID < activePlayers[b].ID
	})

	var options []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		options = append(options, discordgo.SelectMenuOption{
			Label: p.Name,
			Value: fmt.Sprintf("%d", p.ID),
		})
	}

	minValues := 2
	maxValues := len(activePlayers)
	if maxValues > 10 {
		maxValues = 10
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Выберите игроков для микса (от %d до %d):", minValues, maxValues),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							CustomID:    selectMenuCreateMix,
							Placeholder: "Выберите игроков...",
							MinValues:   &minValues,
							MaxValues:   maxValues,
							Options:     options,
						},
					},
				},
			},
		},
	})
}

func (b *Bot) onCreateMixSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if data.CustomID != selectMenuCreateMix {
		return
	}

	selectedIDs := data.Values
	if len(selectedIDs) == 0 {
		return
	}

	// Parse selected player IDs
	var playerIDs []int
	var playerNames []string
	for _, val := range selectedIDs {
		var id int
		fmt.Sscanf(val, "%d", &id)
		idInt := id
		playerIDs = append(playerIDs, idInt)
	}

	// Look up player names
	players, err := b.services.MatchService.GetPlayerList()
	if err == nil {
		nameMap := make(map[int]string)
		for _, p := range players {
			nameMap[p.ID] = p.Name
		}
		for _, id := range playerIDs {
			if name, ok := nameMap[id]; ok {
				playerNames = append(playerNames, name)
			}
		}
	}

	signature := fmt.Sprintf("MIX-%s", strings.Join(selectedIDs, "-"))
	mixTitle := fmt.Sprintf("Микс %s", strings.Join(playerNames, ", "))

	// Open a Discord Thread from the interaction message
	thread, err := s.MessageThreadStart(i.ChannelID, i.Message.ID, mixTitle, 60)
	if err != nil {
		b.logger.Warn("mix: failed to create thread: %v", err)
		// Fallback: send ephemeral message without thread
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("✅ **Микс создан!**\n\n🗡️ Сигнатура: `%s`\n👥 Игроки: %s\n\n📸 Капитаны, скиньте скриншот результата в этот канал после игры, я его обработаю.",
					signature, strings.Join(playerNames, ", ")),
			},
		})
		return
	}

	// Send the confirmation message inside the new thread
	_, err = s.ChannelMessageSend(thread.ID, fmt.Sprintf(
		"✅ **Микс создан!**\n\n🗡️ Сигнатура: `%s`\n👥 Игроки: %s\n\n📸 Капитаны, скиньте скриншот результата **в эту ветку** после игры, я его обработаю.",
		signature, strings.Join(playerNames, ", "),
	))
	if err != nil {
		b.logger.Warn("mix: failed to send thread message: %v", err)
	}

	// Respond to the interaction (ephemeral) confirming thread creation
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ **Микс создан!**\n\n📁 Ветка: <#%s>\n👥 Игроки: %s\n\n📸 Скидывайте скриншоты в созданную ветку.",
				thread.ID, strings.Join(playerNames, ", ")),
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	b.logger.Info("mix: thread %s created for signature %s", thread.ID, signature)

	// Save the thread_id to the database so screenshots in this thread
	// can be matched to the correct player list for AI processing.
	// Note: create_mix doesn't create a lobby_matches row yet — for admins,
	// this is informational. The real thread_id assignment happens in
	// handleSelectTeamA and handleBalance via CreateMatch → SaveThreadID.
	// We store it on an in-memory map as fallback.
	b.threadPlayerCache[thread.ID] = playerNames
}

func (b *Bot) buildLobbyEmbed() *discordgo.MessageEmbed {
	description := b.services.Lobby.FormatPlayerList()

	return &discordgo.MessageEmbed{
		Title:       "⚔️ Игровое Лобби",
		Description: description,
		Color:       lobbyEmbedColor,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "BlackWatch Community — Ежедневные миксы",
		},
	}
}

func (b *Bot) buildLobbyComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    componentLabelMarkActive,
					Style:    discordgo.SuccessButton,
					CustomID: componentIDMarkActive,
					Emoji:    &discordgo.ComponentEmoji{Name: "📌"},
				},
				discordgo.Button{
					Label:    componentLabelLeave,
					Style:    discordgo.DangerButton,
					CustomID: componentIDLeave,
					Emoji:    &discordgo.ComponentEmoji{Name: "🚪"},
				},
			},
		},
	}
}

// findPlayerByDiscordID looks up a player by discord_id in the database.
// Uses O(1) direct SQL query — no longer loads all players into memory.
func (b *Bot) findPlayerByDiscordID(discordID string) (models.Player, error) {
	id, name, err := b.services.MatchService.GetPlayerByDiscordID(discordID)
	if err != nil {
		return models.Player{}, fmt.Errorf("player not found for discord id %s", discordID)
	}
	return models.Player{ID: id, Name: name}, nil
}
