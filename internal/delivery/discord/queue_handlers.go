package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

const (
	lobbyEmbedColor = 0x9B59B6

	componentLabelMarkActive = "Отметить активность / Войти в лобби"
	componentLabelLeave      = "Выйти из лобби"
	componentIDMarkActive    = "lobby_mark_active"
	componentIDLeave         = "lobby_leave"
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
		Description: "Драфт игры 5х5: назначить двух капитанов (Только SUDЬЯ/админы)",
	}
}

// RegisterQueueHandlers registers button and select-menu handlers.
func (b *Bot) RegisterQueueHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onLobbyButton))
	b.session.AddHandler(b.wrapRecover(b.onMixComponent))
}

func (b *Bot) onLobbyButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if data.CustomID != componentIDMarkActive && data.CustomID != componentIDLeave {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	// See guardComponent: the lobby buttons are the only entry into the queue
	// for an ordinary user, and they carried neither the rate limit nor the
	// licence check that /lobby itself goes through.
	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}

	switch data.CustomID {
	case componentIDMarkActive:
		b.handleMarkActive(ctx, s, i)
	case componentIDLeave:
		b.handleLeaveLobby(ctx, s, i)
	}
}

// handleLobbyPost creates or updates the permanent lobby message.
func (b *Bot) handleLobbyPost(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				b.buildLobbyEmbed(),
			},
			Components: b.buildLobbyComponents(),
		},
	})
}

func (b *Bot) handleMarkActive(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check if lobby is open
	if !b.services.Lobby.IsOpen() {
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Лобби закрыто администратором. Ожидайте открытия.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	member := interactionMember(i.Interaction)
	if member == nil {
		return
	}
	userID := member.User.ID
	username := member.User.Username

	// Look up player by discord_id
	player, err := b.findPlayerByDiscordID(ctx, userID)
	if err != nil {
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Ваш Discord не привязан к профилю игрока.\n\n" +
					"Нажмите кнопку ниже и укажите свой ник в игре — тот, что показывает " +
					"таблица результатов.",
				Components: bindPromptComponents(),
				Flags:      discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	place, joinErr := b.services.Lobby.TryAddPlayer(ctx, player, userID)
	if errors.Is(joinErr, domain.ErrAlreadyQueued) {
		// Already in lobby — the press is the activity confirmation. Name the
		// outcome so the log line does not read "marked active ()".
		b.services.Lobby.UpdateActivity(player.ID)
		place, joinErr = application.PlaceAlreadyQueued, nil
	}

	// Refresh the lobby embed in place. Discord accepts exactly one response per
	// interaction, so the warning cannot be a second InteractionRespond — it is
	// sent as a follow-up once the update has been acknowledged.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{b.buildLobbyEmbed()},
			Components: b.buildLobbyComponents(),
		},
	}); err != nil {
		b.logger.Error("lobby: failed to refresh embed for %s: %v", username, err)
		return
	}

	if joinErr != nil {
		b.logger.Warn("lobby: %s (ID: %d) refused: %v", username, player.ID, joinErr)
		b.followupEphemeral(s, i.Interaction, joinMessage(joinErr))
		return
	}

	if place == application.PlaceWaitlist {
		b.followupEphemeral(s, i.Interaction, fmt.Sprintf(
			"Основное лобби заполнено. Вы в резерве (позиция %d). Вы автоматически займёте место при выходе игрока из основного состава.",
			b.services.Lobby.WaitlistPosition(player.ID)))
	}

	b.logger.Info("lobby: %s (ID: %d) marked active (%s)", username, player.ID, place)
}

// followupEphemeral sends a private follow-up after an interaction has already
// been acknowledged.
func (b *Bot) followupEphemeral(s *discordgo.Session, i *discordgo.Interaction, content string) {
	if _, err := s.FollowupMessageCreate(i, true, &discordgo.WebhookParams{
		Content: truncateMessage(content),
		Flags:   discordgo.MessageFlagsEphemeral,
	}); err != nil {
		b.logger.Error("discord: failed to send follow-up for %q: %v", interactionLabel(i), err)
	}
}

// joinMessage renders a queue refusal for the user. The service returns typed
// errors now, so the wording lives here instead of being carried up from the
// application layer as a pre-rendered string.
func joinMessage(err error) string {
	var banned *application.QueueBanError
	switch {
	case errors.As(err, &banned):
		return fmt.Sprintf("Вы заблокированы в очереди до %s. Причина: %s",
			banned.Until.Format("02.01.2006 15:04"), banned.Reason)
	case errors.Is(err, domain.ErrLobbyClosed):
		return "Лобби закрыто. Дождитесь открытия следующей сессии."
	case errors.Is(err, domain.ErrInDraft):
		return "Вы уже в драфте игры. Вернуться в лобби можно после матча."
	default:
		return "Не удалось встать в очередь. Попробуйте ещё раз через минуту."
	}
}

func (b *Bot) handleLeaveLobby(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	member := interactionMember(i.Interaction)
	if member == nil {
		return
	}

	player, err := b.findPlayerByDiscordID(ctx, member.User.ID)
	if err == nil {
		b.services.Lobby.RemovePlayer(player.ID)
	}

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{b.buildLobbyEmbed()},
			Components: b.buildLobbyComponents(),
		},
	})
}

func (b *Bot) buildLobbyEmbed() *discordgo.MessageEmbed {
	// A full lobby of a hundred names can run past the embed description limit,
	// and Discord rejects the whole update rather than cutting it.
	description := truncateText(b.services.Lobby.FormatPlayerList(), maxEmbedDescriptionLength)

	return &discordgo.MessageEmbed{
		Title:       "Игровое Лобби",
		Description: description,
		Color:       lobbyEmbedColor,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "BlackWatch Community — Ежедневные миксы",
		},
	}
}

func (b *Bot) buildLobbyComponents() []discordgo.MessageComponent {
	isOpen := b.services.Lobby.IsOpen()

	joinStyle := discordgo.SuccessButton
	leaveStyle := discordgo.DangerButton
	if !isOpen {
		joinStyle = discordgo.SecondaryButton
		leaveStyle = discordgo.SecondaryButton
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    componentLabelMarkActive,
					Style:    joinStyle,
					CustomID: componentIDMarkActive,
					Disabled: !isOpen,
				},
				discordgo.Button{
					Label:    componentLabelLeave,
					Style:    leaveStyle,
					CustomID: componentIDLeave,
					Disabled: !isOpen,
				},
			},
		},
	}
}

// =====================================================================
// Admin Lobby Management Commands
// =====================================================================

func (b *Bot) handleLobbyKick(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())
	name, err := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if err != nil {
		b.logger.Warn("lobby: cannot resolve name for player %d: %v", id, err)
		name = fmt.Sprintf("ID:%d", id)
	}
	if !b.services.Lobby.RemovePlayer(id) {
		b.respondMessage(s, i, fmt.Sprintf("Игрока **%s** не было в лобби.", name), true)
		return
	}
	b.respondMessage(s, i, fmt.Sprintf("Игрок **%s** кикнут из лобби.", name), false)
}

func (b *Bot) handleLobbyClose(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.services.Lobby.CloseLobby()
	b.respondMessage(s, i, "Лобби закрыто. Кнопки входа отключены.", false)
}

func (b *Bot) handleLobbyOpen(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.services.Lobby.OpenLobby()
	b.respondMessage(s, i, "Лобби открыто. Игроки могут регистрироваться.", false)
}

func (b *Bot) handleLobbyClear(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.services.Lobby.ClearAll()
	b.respondMessage(s, i, "Лобби полностью очищено.", false)
}

func (b *Bot) handleLobbyStatus(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	activePlayers := b.services.Lobby.GetActivePlayers()
	playerIDs := make([]int, len(activePlayers))
	for idx, p := range activePlayers {
		playerIDs[idx] = p.ID
	}
	mmrMap, err := b.services.Lobby.GetPlayerMMRsBatch(ctx, playerIDs)
	if err != nil {
		b.respondMessage(s, i, "Ошибка получения MMR: "+err.Error(), true)
		return
	}
	status := b.services.Lobby.GetStatusDetail(mmrMap)
	b.respondMessage(s, i, status, false)
}

func (b *Bot) handleQueueBan(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	playerID := int(opts[0].IntValue())
	durationStr := opts[1].StringValue()
	reason := "Нарушение правил"
	if len(opts) > 2 {
		reason = opts[2].StringValue()
	}

	duration, err := parseDuration(durationStr)
	if err != nil {
		b.respondMessage(s, i, "Неверный формат. Используйте: 1h, 12h, 24h, 7d, 30d (максимум 365d).", true)
		return
	}

	// Look up discord_id
	discordID, err := b.services.MatchService.GetDiscordIDByPlayerID(ctx, playerID)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) || errors.Is(err, domain.ErrDiscordNotLinked) {
			b.respondMessage(s, i, "Игрок не найден или не привязан к Discord (`/bind_player <ID> <@user>`).", true)
			return
		}
		b.logger.Error("queue_ban: failed to resolve discord id for player %d: %v", playerID, err)
		b.respondMessage(s, i, "Ошибка обращения к базе. Повторите позже.", true)
		return
	}

	name, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.logger.Warn("queue_ban: cannot resolve name for player %d: %v", playerID, err)
		name = fmt.Sprintf("ID:%d", playerID)
	}

	if err := b.services.Lobby.QueueBan(ctx, discordID, reason, duration); err != nil {
		b.logger.Error("queue_ban: failed to ban player %d: %v", playerID, err)
		b.respondMessage(s, i, "Не удалось выдать бан. Повторите позже.", true)
		return
	}

	b.logger.Info("queue_ban: player %s (ID: %d, discord %s) banned for %s: %s", name, playerID, discordID, duration, reason)
	b.respondMessage(s, i, fmt.Sprintf("Игрок **%s** забанен в очереди на %s. Причина: %s", name, durationStr, reason), false)
}

// maxQueueBanDuration caps a ban so a typo cannot exile somebody until the next
// century. Nothing else bounds the value: it goes straight into banned_until.
const maxQueueBanDuration = 365 * 24 * time.Hour

// parseDuration parses a ban length. time.ParseDuration already understands
// "1h"/"12h"/"24h"; the only thing it lacks is a day suffix, so that is all this
// adds. Zero and negative values are rejected — they would write a ban row that
// has already expired.
func parseDuration(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q: %w", days, err)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return 0, err
		}
		d = parsed
	}

	if d <= 0 {
		return 0, fmt.Errorf("ban duration must be positive, got %s", d)
	}
	if d > maxQueueBanDuration {
		return 0, fmt.Errorf("ban duration %s exceeds the %s maximum", d, maxQueueBanDuration)
	}
	return d, nil
}

// findPlayerByDiscordID looks up a player by discord_id in the database.
// Uses O(1) direct SQL query — no longer loads all players into memory.
func (b *Bot) findPlayerByDiscordID(ctx context.Context, discordID string) (models.Player, error) {
	id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, discordID)
	if err != nil {
		return models.Player{}, fmt.Errorf("player not found for discord id %s", discordID)
	}
	return models.Player{ID: id, Name: name}, nil
}
