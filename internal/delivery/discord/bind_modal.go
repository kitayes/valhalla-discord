package discord

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

const (
	// buttonBindOpen opens the registration modal from wherever a user hit the
	// "you have no profile" wall.
	buttonBindOpen = "bind_modal_open"
	// modalBind is the modal itself, and inputBindNickname the field inside it.
	modalBind         = "bind_modal"
	inputBindNickname = "bind_nickname"
	autocompleteLimit = 25
)

// bindPromptComponents is the button offered next to every "your Discord is not
// bound" message.
//
// Those messages used to end the interaction with an instruction to go and type
// a command somewhere else. The button keeps the user where they are: it opens a
// modal with one field and runs the same BindDiscordByName the command does.
func bindPromptComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Привязать профиль",
				Style:    discordgo.PrimaryButton,
				CustomID: buttonBindOpen,
				Emoji:    &discordgo.ComponentEmoji{Name: "🔗"},
			},
		}},
	}
}

// RegisterBindModalHandlers registers the button that opens the registration
// modal. The modal submission itself arrives as an InteractionModalSubmit and is
// routed from onInteraction.
func (b *Bot) RegisterBindModalHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onBindButton))
}

func (b *Bot) onBindButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	if i.MessageComponentData().CustomID != buttonBindOpen {
		return
	}

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: modalBind,
			Title:    "Привязка профиля",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:    inputBindNickname,
						Label:       "Ваш ник в игре",
						Style:       discordgo.TextInputShort,
						Placeholder: "точно как в таблице результатов",
						Required:    true,
						MaxLength:   64,
					},
				}},
			},
		},
	})
}

// onModalSubmit handles a submitted modal. Routed from onInteraction, which is
// where every non-component interaction lands.
func (b *Bot) onModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()
	if data.CustomID != modalBind {
		return
	}

	member := interactionMember(i.Interaction)
	if member == nil {
		b.respondMessage(s, i.Interaction, "⚠️ Эта команда работает только на сервере.", true)
		return
	}

	nickname := strings.TrimSpace(modalValue(data, inputBindNickname))

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	playerID, created, err := b.services.MatchService.BindDiscordByName(ctx, nickname, member.User.ID)
	if err != nil {
		b.reportFailure(s, i.Interaction, "bind modal", bindFailureMessage(err, nickname), err)
		return
	}

	if created {
		b.respondMessage(s, i.Interaction, bindCreatedMessage(nickname, playerID), true)
		return
	}
	b.respondMessage(s, i.Interaction, bindClaimedMessage(nickname, playerID), true)
}

// modalValue pulls one field out of a submitted modal.
func modalValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, row := range data.Components {
		actionRow, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, c := range actionRow.Components {
			if input, ok := c.(*discordgo.TextInput); ok && input.CustomID == customID {
				return input.Value
			}
		}
	}
	return ""
}

// onAutocomplete answers Discord's as-you-type requests for a nickname.
//
// Without it a typo in /bind silently creates a second, phantom profile and the
// player's results attach to it instead of to them. Suggesting the nicknames
// that already exist turns "claim my old profile" into something visible rather
// than something you have to guess.
//
// Discord expects an answer within three seconds and accepts at most 25 choices.
func (b *Bot) onAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	var prefix string
	for _, opt := range data.Options {
		if opt.Focused && opt.Name == "nickname" {
			prefix = strings.TrimSpace(opt.StringValue())
			break
		}
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	players, err := b.services.MatchService.SuggestPlayerNames(ctx, prefix, autocompleteLimit)
	if err != nil {
		// An empty list is a usable answer; leaving the request unanswered is
		// not, so a lookup failure still gets a reply.
		b.logger.Warn("autocomplete: failed to suggest names for %q: %v", prefix, err)
		players = nil
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(players))
	for _, p := range players {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  p.Name,
			Value: p.Name,
		})
	}

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}
