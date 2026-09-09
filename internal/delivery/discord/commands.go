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
		Description: "ПОЛНОЕ УДАЛЕНИЕ всех данных и очистка таблиц (ОПАСНО, только админы)",
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

// newBindCommand takes a nickname, not an id from /players. A player row was
// only ever created by parsing a match screenshot, so a newcomer had no id to
// give: joining the lobby needed a binding, the binding needed a profile, and
// the profile needed a match they could not join.
func (b *Bot) newBindCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "bind",
		Description: "Привязать Discord к профилю (нужно для входа в лобби)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "nickname",
				Description: "Ваш ник в игре — точно как в таблице результатов",
				Required:    true,
				// Suggests nicknames that already exist, so a typo cannot
				// quietly spawn a second, phantom profile.
				Autocomplete: true,
			},
		},
	}
}

func (b *Bot) newWhoamiCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "whoami",
		Description: "Показать, к какому профилю привязан ваш Discord",
	}
}

func (b *Bot) newBindPlayerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "bind_player",
		Description: "Привязать Discord пользователя к профилю игрока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			// Nickname, for the same reason as /bind: onboarding a newcomer by
			// id is impossible when the newcomer has no row yet.
			{Type: discordgo.ApplicationCommandOptionString, Name: "nickname", Description: "Ник игрока в игре", Required: true, Autocomplete: true},
			{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Пользователь Discord", Required: true},
		},
	}
}

func (b *Bot) newUnbindPlayerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "unbind_player",
		Description: "Отвязать профиль игрока от Discord (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
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

// targetPlayerOptions are the arguments of a command that reports on somebody.
//
// Both are optional and no argument means "me": the numeric id these used to
// require is something no player knows about themselves. The nickname stays
// because most profiles were born from a parsed screenshot and have nobody
// bound to them, so a mention cannot reach them.
func targetPlayerOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		{
			Type:         discordgo.ApplicationCommandOptionString,
			Name:         "nickname",
			Description:  "Ник игрока",
			Required:     false,
			Autocomplete: true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "Пользователь Discord с привязанным профилем",
			Required:    false,
		},
	}
}

func (b *Bot) newProfileCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "profile",
		Description: "Статистика: своя без аргументов, чужая — по нику или упоминанию",
		Options:     targetPlayerOptions(),
	}
}

func (b *Bot) newHistoryCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "history",
		Description: "История матчей: своя без аргументов, чужая — по нику или упоминанию",
		Options:     targetPlayerOptions(),
	}
}

// newLinkCommand takes no argument on purpose: the profile is the one the
// caller claimed with /bind. An id here let anyone request a link code for
// anyone's profile.
func (b *Bot) newLinkCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "link",
		Description: "Получить код для привязки своего Telegram (нужен /bind)",
	}
}

func (b *Bot) newUnlinkCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "unlink",
		Description: "Отвязать свой Telegram от профиля",
	}
}

func (b *Bot) newUnlinkPlayerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "unlink_player",
		Description: "Отвязать Telegram от профиля игрока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	}
}

func (b *Bot) newTelegramProfileCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "telegram_profile",
		Description: "Показать свой Telegram профиль (чужой — только админам)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "player_name", Description: "Имя игрока (только для админов)", Required: false},
		},
	}
}

func (b *Bot) newUpdateNickCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "update_nick",
		Description: "Обновить никнейм игрока, каскадно во всей истории (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "new_name", Description: "Новый никнейм", Required: true},
		},
	}
}

func (b *Bot) newFAQCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "faq",
		Description: "Поиск по базе знаний BlackWatch (DeepSeek AI)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "question", Description: "Ваш вопрос", Required: true},
		},
	}
}

func (b *Bot) newFAQReloadCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "faq_reload",
		Description: "Перезагрузить базу знаний FAQ (Только админы)",
	}
}

func (b *Bot) newLobbyKickCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lobby_kick",
		Description: "Кикнуть игрока из лобби (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	}
}

func (b *Bot) newLobbyCloseCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lobby_close",
		Description: "Закрыть лобби (отключить кнопки входа) (Только админы)",
	}
}

func (b *Bot) newLobbyOpenCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lobby_open",
		Description: "Открыть лобби (включить кнопки входа) (Только админы)",
	}
}

func (b *Bot) newLobbyClearCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lobby_clear",
		Description: "Полностью очистить лобби от всех игроков (Только админы)",
	}
}

func (b *Bot) newLobbyStatusCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lobby_status",
		Description: "Показать текущий состав лобби и средний MMR (Только админы)",
	}
}

func (b *Bot) newQueueBanCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "queue_ban",
		Description: "Забанить игрока в очереди на время (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "duration",
				Description: "Срок бана",
				Required:    true,
				// Choices instead of free text: the parser stays, but a typo
				// can no longer reach it.
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "1 час", Value: "1h"},
					{Name: "24 часа", Value: "24h"},
					{Name: "7 дней", Value: "7d"},
					{Name: "30 дней", Value: "30d"},
				},
			},
			{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Причина бана", Required: false},
		},
	}
}

func (b *Bot) newCancelMatchCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "cancel_match",
		Description: "Отменить матч, вернуть игроков в лобби и аннулировать ставки (Только SUDЬЯ/админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID матча", Required: true},
		},
	}
}
