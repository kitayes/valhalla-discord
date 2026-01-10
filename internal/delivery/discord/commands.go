package discord

import "github.com/bwmarrin/discordgo"

func (b *Bot) addCommands(commands ...*discordgo.ApplicationCommand) {
	b.commands = append(b.commands, commands...)
}

func (b *Bot) newResetCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "reset",
		Description: "Сброс сезона (Только админы)",
	}
}

func (b *Bot) newExportCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "export",
		Description: "Экспорт отчета в Excel (Только админы)",
	}
}
