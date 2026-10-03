package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"

	"github.com/bwmarrin/discordgo"
)

// Custom IDs of the mix draft. The draft number rides in the components on the
// draft message, so a press finds its draft without any state on the message.
const (
	mixCaptainsSelect = "mix_captains"
	mixPickPrefix     = "mix_pick_"
	mixFreeButton     = "mix_free"
	mixCancelPrefix   = "mix_cancel_"

	draftEmbedColor = 0x3498DB
)

// handleCreateMix opens a draft: the referee names the two captains.
func (b *Bot) handleCreateMix(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if !b.requireReferee(s, i) {
		return
	}
	if n := len(b.services.Lobby.GetActivePlayers()); n < teamSize*2 {
		b.respondMessage(s, i, fmt.Sprintf(
			"Для игры 5х5 в лобби нужно минимум %d свободных игроков, сейчас %d.", teamSize*2, n), true)
		return
	}

	two := 2
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Выберите двух капитанов. Оба должны быть в лобби.",
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						MenuType:    discordgo.UserSelectMenu,
						CustomID:    mixCaptainsSelect,
						Placeholder: "Капитаны...",
						MinValues:   &two,
						MaxValues:   2,
					},
				}},
			},
		},
	})
}

// onMixComponent routes every component of the mix draft.
func (b *Bot) onMixComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	customID := i.MessageComponentData().CustomID
	isPick := strings.HasPrefix(customID, mixPickPrefix)
	isCancel := strings.HasPrefix(customID, mixCancelPrefix)
	if customID != mixCaptainsSelect && customID != mixFreeButton && !isPick && !isCancel {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	// Component presses never pass through onInteraction, so the licence gate
	// is applied here.
	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}

	switch {
	case customID == mixCaptainsSelect:
		b.handleMixCaptains(s, i)
	case customID == mixFreeButton:
		b.handleMixFree(s, i.Interaction)
	case isPick:
		if draftID, ok := b.draftIDOf(s, i.Interaction, customID, mixPickPrefix); ok {
			b.handleMixPick(ctx, s, i, draftID)
		}
	case isCancel:
		if draftID, ok := b.draftIDOf(s, i.Interaction, customID, mixCancelPrefix); ok {
			b.handleMixCancel(s, i.Interaction, draftID)
		}
	}
}

// draftIDOf reads the draft number off a component, answering the press when
// it cannot.
func (b *Bot) draftIDOf(s *discordgo.Session, i *discordgo.Interaction, customID, prefix string) (int, bool) {
	draftID, ok := parseDraftID(customID, prefix)
	if !ok {
		b.logger.Error("mix: malformed draft custom ID %q", customID)
		b.respondMessage(s, i, "Некорректная кнопка драфта.", true)
	}
	return draftID, ok
}

func draftCustomID(prefix string, draftID int) string {
	return prefix + strconv.Itoa(draftID)
}

func parseDraftID(customID, prefix string) (int, bool) {
	rest, found := strings.CutPrefix(customID, prefix)
	if !found {
		return 0, false
	}
	id := parseID(rest)
	return id, id > 0
}

func (b *Bot) handleMixCaptains(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !b.requireReferee(s, i.Interaction) {
		return
	}
	values := i.MessageComponentData().Values
	if len(values) != 2 {
		b.respondMessage(s, i.Interaction, "Нужно выбрать ровно двух капитанов.", true)
		return
	}

	view, err := b.services.Lobby.StartDraft(values[0], values[1])
	if err != nil {
		b.respondMessage(s, i.Interaction, captainsErrorMessage(err), true)
		return
	}

	// A new public message: the captain picker is ephemeral, and the draft has
	// to be seen by both captains and by everyone waiting in the lobby.
	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{draftEmbed(view)},
			Components: draftComponents(view.ID),
		},
	})
}

func (b *Bot) handleMixPick(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, draftID int) {
	member := interactionMember(i.Interaction)
	values := i.MessageComponentData().Values
	if member == nil || len(values) != 1 {
		return
	}

	view, err := b.services.Lobby.PickPlayer(draftID, member.User.ID, values[0])
	if err != nil {
		b.respondMessage(s, i.Interaction, draftErrorMessage(err), true)
		return
	}
	if view.Complete {
		b.completeDraft(ctx, s, i.Interaction, view)
		return
	}

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{draftEmbed(view)},
			Components: draftComponents(view.ID),
		},
	})
}

// completeDraft turns a fully picked draft into a rated match. The draft
// message itself becomes the match card; from there the match runs the same
// path as /balance — thread, betting window, Telegram announcement.
func (b *Bot) completeDraft(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, view application.DraftView) {
	teamAIDs, teamANames := sideRoster(view.Sides[0])
	teamBIDs, teamBNames := sideRoster(view.Sides[1])
	captainA, captainB := view.Sides[0].Captain, view.Sides[1].Captain

	// The draft must not outlive this function open: it is complete, so the
	// referee's cancel refuses it and nothing else would ever close it. Abort it
	// on every path that does not end in a created match, panics included.
	done := false
	defer func() {
		if done {
			return
		}
		if _, abortErr := b.services.Lobby.AbortDraft(view.ID); abortErr != nil {
			b.logger.Error("mix: failed to abort draft #%d: %v", view.ID, abortErr)
		}
	}()

	matchID, err := b.services.Lobby.CreateMatch(ctx, b.guildID(i.GuildID), captainA.ID, captainB.ID, teamAIDs, teamBIDs)
	if err != nil {
		b.logger.Error("mix: draft #%d complete but the match was not created: %v", view.ID, err)
		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "**Драфт отменён:** матч создать не удалось. Игроки возвращены в лобби, судье нужно создать игру заново.",
				Embeds:     []*discordgo.MessageEmbed{},
				Components: []discordgo.MessageComponent{},
			},
		})
		return
	}
	done = true
	if err := b.services.Lobby.FinishDraft(view.ID); err != nil {
		b.logger.Warn("mix: draft #%d not closed after match #%d: %v", view.ID, matchID, err)
	}

	embed := b.buildMatchEmbed(matchID, captainA.Name, captainB.Name, teamANames, teamBNames)
	b.publishCreatedMatch(ctx, s, i, discordgo.InteractionResponseUpdateMessage, matchID,
		teamAIDs, teamBIDs, concatNames(teamANames, teamBNames), embed, captainA.Name, captainB.Name)
	b.logger.Info("mix: draft #%d became match #%d", view.ID, matchID)
}

// sideRoster lists a draft side captain first, the order the match row and
// its card expect.
func sideRoster(side application.DraftSide) ([]int, []string) {
	ids := []int{side.Captain.ID}
	names := []string{side.Captain.Name}
	for _, p := range side.Picks {
		ids = append(ids, p.ID)
		names = append(names, p.Name)
	}
	return ids, names
}

// handleMixFree answers the "who is still free" button. Drafts have no state
// of their own here: it is simply the lobby.
func (b *Bot) handleMixFree(s *discordgo.Session, i *discordgo.Interaction) {
	players := b.services.Lobby.GetActivePlayers()
	if len(players) == 0 {
		b.respondMessage(s, i, "В лобби никого не осталось.", true)
		return
	}
	names := make([]string, len(players))
	for idx, p := range players {
		names[idx] = p.Name
	}
	b.respondMessage(s, i, fmt.Sprintf("**Свободны в лобби (%d):**\n%s", len(players), formatPlayerList(names)), true)
}

func (b *Bot) handleMixCancel(s *discordgo.Session, i *discordgo.Interaction, draftID int) {
	if !b.requireReferee(s, i) {
		return
	}
	view, err := b.services.Lobby.CancelDraft(draftID)
	if err != nil {
		b.respondMessage(s, i, draftErrorMessage(err), true)
		return
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("**Драфт %s vs %s отменён судьёй.** Игроки возвращены в лобби.",
				view.Sides[0].Captain.Name, view.Sides[1].Captain.Name),
			Embeds:     []*discordgo.MessageEmbed{},
			Components: []discordgo.MessageComponent{},
		},
	})
}

func draftEmbed(v application.DraftView) *discordgo.MessageEmbed {
	left := 2*application.DraftPicksPerTeam - len(v.Sides[0].Picks) - len(v.Sides[1].Picks)
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Драфт: %s vs %s", v.Sides[0].Captain.Name, v.Sides[1].Captain.Name),
		Description: fmt.Sprintf("Ход: **%s** · осталось выбрать: %d", v.Sides[v.Turn].Captain.Name, left),
		Color:       draftEmbedColor,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Команда A", Value: draftSideList(v.Sides[0]), Inline: true},
			{Name: "Команда B", Value: draftSideList(v.Sides[1]), Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Капитан, чей ход, выбирает игрока в поле ниже. Кто свободен — кнопка «Свободные игроки».",
		},
	}
}

func draftSideList(side application.DraftSide) string {
	names := []string{side.Captain.Name + " (капитан)"}
	for _, p := range side.Picks {
		names = append(names, p.Name)
	}
	return formatPlayerList(names)
}

func draftComponents(draftID int) []discordgo.MessageComponent {
	one := 1
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				MenuType:    discordgo.UserSelectMenu,
				CustomID:    draftCustomID(mixPickPrefix, draftID),
				Placeholder: "Выбрать игрока...",
				MinValues:   &one,
				MaxValues:   1,
			},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Свободные игроки", Style: discordgo.SecondaryButton, CustomID: mixFreeButton},
			discordgo.Button{Label: "Отменить драфт", Style: discordgo.DangerButton, CustomID: draftCustomID(mixCancelPrefix, draftID)},
		}},
	}
}

func captainsErrorMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrSameCaptain):
		return "Капитаны должны быть разными игроками."
	case errors.Is(err, domain.ErrNotInLobby):
		return "Оба капитана должны быть в лобби и не участвовать в другом драфте."
	default:
		return "Не удалось начать драфт. Попробуйте ещё раз."
	}
}

func draftErrorMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrDraftNotFound):
		return "Драфт не найден: он уже закрыт или бот перезапускался. Судье нужно создать игру заново."
	case errors.Is(err, domain.ErrDraftComplete):
		return "Драфт уже завершён."
	case errors.Is(err, domain.ErrNotCaptain):
		return "Выбирать игроков могут только капитаны этого драфта."
	case errors.Is(err, domain.ErrNotYourTurn):
		return "Сейчас ход другого капитана."
	case errors.Is(err, domain.ErrNotInLobby):
		return "Этого игрока нет среди свободных в лобби: он уже выбран или не входил в лобби."
	default:
		return "Не удалось выполнить действие. Попробуйте ещё раз."
	}
}
