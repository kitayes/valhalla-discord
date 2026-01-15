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
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   flags,
		},
	})
}

func (b *Bot) ensureAdmin(s *discordgo.Session, i *discordgo.Interaction, handler func(*discordgo.Session, *discordgo.Interaction)) {
	if !b.isAdmin(i.Member.User.ID) {
		b.respondMessage(s, i, "У вас нет прав.", true)
		return
	}
	handler(s, i)
}
