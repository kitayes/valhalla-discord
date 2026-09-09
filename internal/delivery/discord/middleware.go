package discord

import (
	"github.com/bwmarrin/discordgo"
)

func (b *Bot) isAdmin(userID string) bool {
	_, ok := b.adminIDs[userID]
	return ok
}

func (b *Bot) respondMessage(s *discordgo.Session, i *discordgo.Interaction, msg string, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: truncateMessage(msg),
			Flags:   flags,
		},
	})
}

// respond sends an interaction response and logs failures.
//
// Every call site used to discard the error. When Discord rejects a response —
// an expired token, an already-acknowledged interaction, an over-length embed —
// the user is left staring at "the application did not respond" and nothing
// reaches the log, which is exactly how the duplicate-response bug in the lobby
// survived unnoticed.
func (b *Bot) respond(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) {
	if err := s.InteractionRespond(i, resp); err != nil {
		b.logger.Error("discord: failed to respond to interaction %q: %v", interactionLabel(i), err)
	}
}

// reportFailure logs the real error and tells the user what failed without
// handing them the error text.
//
// Piping err.Error() into a Discord message put raw driver output — "pq:
// relation ... does not exist", connection strings, internal table names — in
// front of anyone who could run the command, and several of these commands are
// public. The detail belongs in the log; the user gets the outcome.
func (b *Bot) reportFailure(s *discordgo.Session, i *discordgo.Interaction, op, userMsg string, err error) {
	b.logger.Error("discord: %s failed: %v", op, err)
	b.respondMessage(s, i, userMsg, true)
}

// reportFailureEdit is reportFailure for handlers that already deferred.
func (b *Bot) reportFailureEdit(s *discordgo.Session, i *discordgo.Interaction, op, userMsg string, err error) {
	b.logger.Error("discord: %s failed: %v", op, err)
	b.editContent(s, i, userMsg)
}

// sendChannelMessage posts to a channel and logs failures, returning the message
// so the caller can edit or delete it later.
func (b *Bot) sendChannelMessage(s *discordgo.Session, channelID, content string) *discordgo.Message {
	msg, err := s.ChannelMessageSend(channelID, truncateMessage(content))
	if err != nil {
		b.logger.Error("discord: failed to send message to %s: %v", channelID, err)
		return nil
	}
	return msg
}

// startTyping shows the typing indicator while a slow handler works.
func (b *Bot) startTyping(s *discordgo.Session, channelID string) {
	if err := s.ChannelTyping(channelID); err != nil {
		b.logger.Debug("discord: failed to start typing in %s: %v", channelID, err)
	}
}

// editResponse edits a previously deferred response and logs failures.
func (b *Bot) editResponse(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) {
	if _, err := s.InteractionResponseEdit(i, edit); err != nil {
		b.logger.Error("discord: failed to edit response for %q: %v", interactionLabel(i), err)
	}
}

// editContent is the common case of editResponse: replacing the text.
func (b *Bot) editContent(s *discordgo.Session, i *discordgo.Interaction, content string) {
	content = truncateMessage(content)
	b.editResponse(s, i, &discordgo.WebhookEdit{Content: &content})
}

// deferResponse acknowledges an interaction that needs more than Discord's
// three-second budget.
func (b *Bot) deferResponse(s *discordgo.Session, i *discordgo.Interaction, ephemeral bool) {
	data := &discordgo.InteractionResponseData{}
	if ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: data,
	})
}

// interactionLabel names an interaction for logs: the command or component that
// triggered it.
func interactionLabel(i *discordgo.Interaction) string {
	if i == nil {
		return "unknown"
	}
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		return i.ApplicationCommandData().Name
	case discordgo.InteractionMessageComponent:
		return i.MessageComponentData().CustomID
	default:
		return i.Type.String()
	}
}
