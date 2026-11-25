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

func (b *Bot) newAliasesCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "aliases",
		Description: "Управление алиасами (доп. никами) игрока для OCR (Рефери / админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "action",
				Description: "Действие (по умолчанию — показать)",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Показать", Value: "list"},
					{Name: "Добавить", Value: "add"},
					{Name: "Удалить", Value: "delete"},
				},
			},
			{Type: discordgo.ApplicationCommandOptionString, Name: "alias", Description: "Ник-алиас (для add/delete)", Required: false},
		},
	}
}

func (b *Bot) newMergePlayerCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "merge_player",
		Description: "Объединить дубль (фантомный профиль) в основной профиль игрока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "source_id", Description: "ID дубля (будет удалён, стата перенесётся)", Required: true},
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "target_id", Description: "ID основного профиля (останется)", Required: true},
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
		Description: "Таблица лидеров (по умолчанию — по MMR)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "sort",
				Description: "Критерий сортировки",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "По MMR (Рейтинг)", Value: "mmr"},
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

func (b *Bot) newWalletCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "wallet",
		Description: "Кошелёк, уровень XP, страховки ставок и история ValCoins (без аргументов — свой)",
		Options:     targetPlayerOptions(),
	}
}

func (b *Bot) newDailyCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "daily",
		Description: "Забрать ежедневный бонус ValCoins (серия увеличивает награду)",
	}
}

func (b *Bot) newTipCommand() *discordgo.ApplicationCommand {
	minVal := float64(5)
	return &discordgo.ApplicationCommand{
		Name:        "tip",
		Description: "Перевести ValCoins другому игроку (комиссия 5%)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Получатель", Required: true},
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "Сумма ValCoins (от 5)", Required: true, MinValue: &minVal},
		},
	}
}

func (b *Bot) newRichCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "rich",
		Description: "Таблица лидеров по ValCoins или по уровню/опыту XP",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "mode",
				Description: "Режим рейтинга",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Монеты (ValCoins)", Value: "coins"},
					{Name: "Актив (Уровень и XP)", Value: "xp"},
				},
			},
		},
	}
}

func (b *Bot) newShopCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "shop",
		Description: "Магазин ValCoins: титулы, рамки профиля, страховки ставок, роли и призы",
	}
}

func (b *Bot) newCoinsAdminCommand() *discordgo.ApplicationCommand {
	minVal := float64(1)
	return &discordgo.ApplicationCommand{
		Name:        "coins_admin",
		Description: "Выдать или списать ValCoins игрока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Начислить ValCoins игроку",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "Сумма", Required: true, MinValue: &minVal},
					{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Комментарий", Required: false},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "remove",
				Description: "Списать ValCoins у игрока",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "Сумма", Required: true, MinValue: &minVal},
					{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Комментарий", Required: false},
				},
			},
		},
	}
}

func (b *Bot) newShopAdminCommand() *discordgo.ApplicationCommand {
	minPrice := float64(1)
	minStock := float64(0)
	return &discordgo.ApplicationCommand{
		Name:        "shop_admin",
		Description: "Управление каталогом магазина ValCoins (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Добавить или обновить товар в магазине",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "code", Description: "Уникальный код (латиница, напр. vip_role)", Required: true},
					{Type: discordgo.ApplicationCommandOptionString, Name: "name", Description: "Название товара", Required: true},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "kind",
						Description: "Тип товара",
						Required:    true,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "Discord-роль", Value: "discord_role"},
							{Name: "Призовой лот (выдаёт админ)", Value: "prize"},
							{Name: "Цвет рамки профиля (HEX)", Value: "profile_color"},
							{Name: "Кастомный титул", Value: "title"},
							{Name: "Страховка ставки", Value: "bet_insurance"},
						},
					},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "price", Description: "Цена в ValCoins", Required: true, MinValue: &minPrice},
					{Type: discordgo.ApplicationCommandOptionString, Name: "description", Description: "Описание товара", Required: false},
					{Type: discordgo.ApplicationCommandOptionRole, Name: "role", Description: "Discord-роль (для типа discord_role)", Required: false},
					{Type: discordgo.ApplicationCommandOptionString, Name: "payload", Description: "HEX-цвет (для profile_color) или доп. данные", Required: false},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "stock", Description: "Лимит штук (оставьте пустым для безлимита)", Required: false, MinValue: &minStock},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "toggle",
				Description: "Включить или выключить товар",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "code", Description: "Код товара", Required: true},
					{Type: discordgo.ApplicationCommandOptionBoolean, Name: "enabled", Description: "Включён?", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "list",
				Description: "Показать все товары (включая выключенные)",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "post",
				Description: "Опубликовать постоянную интерактивную витрину магазина в канале магазина",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "orders",
				Description: "Показать последние покупки игрока (для получения ID заказа)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "refund",
				Description: "Отменить заказ, вернуть монеты/сток и откатить эффект (роль/титул/цвет)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "order_id", Description: "ID заказа из /shop_admin orders", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "delete",
				Description: "Удалить товар из каталога (если по нему ещё не было покупок)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "code", Description: "Код товара", Required: true},
				},
			},
		},
	}
}

func (b *Bot) newRevertMatchCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "revert_match",
		Description: "Откатить ошибочно закрытый матч: MMR, монеты, ставки и скриншот (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID матча", Required: true},
		},
	}
}

func (b *Bot) newQueueUnbanCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "queue_unban",
		Description: "Снять бан очереди с игрока раньше срока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "ID игрока", Required: true},
		},
	}
}

func (b *Bot) newXPAdminCommand() *discordgo.ApplicationCommand {
	minVal := float64(1)
	maxLvl := float64(200)
	return &discordgo.ApplicationCommand{
		Name:        "xp_admin",
		Description: "Скорректировать XP или уровень игрока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Добавить XP игроку",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "Сколько XP добавить", Required: true, MinValue: &minVal},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "remove",
				Description: "Снять XP у игрока",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "Сколько XP снять", Required: true, MinValue: &minVal},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "set_level",
				Description: "Установить точный уровень игрока (без начисления монет)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "level", Description: "Уровень (1..200)", Required: true, MinValue: &minVal, MaxValue: maxLvl},
				},
			},
		},
	}
}

func (b *Bot) newProfileAdminCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "profile_admin",
		Description: "Модерация титула и цвета рамки профиля игрока (Только админы)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "clear_title",
				Description: "Сбросить кастомный титул игрока",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "set_title",
				Description: "Поправить/установить титул игрока (например, исправить опечатку)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
					{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Новый титул (до 40 символов)", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "clear_color",
				Description: "Сбросить цвет рамки профиля игрока к стандартному",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "player", Description: "ID игрока", Required: true},
				},
			},
		},
	}
}
