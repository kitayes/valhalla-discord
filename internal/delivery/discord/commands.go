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

func (b *Bot) newSetTimerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "set_timer",
		Description: "Установить дату начала сезона (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "date", Description: "YYYY-MM-DD", Required: true},
		},
	}
}

func (b *Bot) newDeleteMatchCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "delete_match",
		Description: "Удалить матч по ID (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID матча", Required: true},
		},
	}
}

func (b *Bot) newWipeCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "wipe",
		Description: "ПОЛНОЕ УДАЛЕНИЕ всех данных и очистка таблиц (ОПАСНО)",
	}
}

func (b *Bot) newSyncSheetCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "sync_sheet",
		Description: "Синхронизация с Google Sheet (Только админы)",
	}
}

func (b *Bot) newResetPlayerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "reset_player",
		Description: "Сброс игрока по ID (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "date", Description: "YYYY-MM-DD", Required: false},
		},
	}
}
func (b *Bot) newWipePlayerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "wipe_player",
		Description: "Полное удаление игрока по ID (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	}
}

func (b *Bot) newRenamePlayerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "rename_player",
		Description: "Переименовать игрока по ID (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "new_name", Description: "Новый ник", Required: true},
		},
	}
}

func (b *Bot) newPlayersCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "players",
		Description: "Список всех игроков и их ID",
	}
}

func (b *Bot) newTopCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "top",
		Description: "Таблица лидеров",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "sort",
				Description: "Критерий сортировки",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "По KDA", Value: "kda"},
					{Name: "По Винрейту", Value: "winrate"},
				},
			},
		},
	}
}

func (b *Bot) newProfileCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "profile",
		Description: "Статистика игрока (по ID)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	}
}

func (b *Bot) newHistoryCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "history",
		Description: "История матчей игрока (по ID)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	}
}

func (b *Bot) newLinkCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "link",
		Description: "Получить код для привязки Telegram аккаунта",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	}
}

func (b *Bot) newUnlinkCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "unlink",
		Description: "Отвязать Telegram аккаунт от профиля",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "player_name", Description: "Имя игрока в системе", Required: true},
		},
	}
}

func (b *Bot) newTelegramProfileCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "telegram_profile",
		Description: "Показать привязанный Telegram профиль",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "player_name", Description: "Имя игрока в системе", Required: true},
		},
	}
}
