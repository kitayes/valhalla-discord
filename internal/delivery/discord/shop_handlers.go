package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

const (
	selectShopItem         = "shop_select_item"
	buttonShopMyBalance    = "shop_my_balance"
	buttonShopRefresh      = "shop_refresh_showcase"
	buttonShopConfirmBuy   = "shop_confirm_buy_"
	buttonShopCancelBuy    = "shop_cancel_buy"
	modalShopTitlePrefix   = "shop_modal_title_"
	modalShopPrizePrefix   = "shop_modal_prize_"
	inputShopCustomValue   = "shop_custom_value"
	buttonShopOrderApprove = "shop_order_approve_"
	buttonShopOrderCancel  = "shop_order_cancel_"

	colorShopTeal = 0x1ABC9C
)

// RegisterShopHandlers registers select-menu and admin order-resolution handlers.
// Modal submissions are routed from onInteraction alongside modalBind.
func (b *Bot) RegisterShopHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onShopComponent))
}

func (b *Bot) handleShop(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.cfg.ShopChannelID != "" && i.ChannelID != b.cfg.ShopChannelID {
		b.respondMessage(s, i, fmt.Sprintf("🛒 Магазин работает в канале <#%s> — загляните туда!", b.cfg.ShopChannelID), true)
		return
	}

	items, err := b.services.EconomyService.ListShopItems(ctx, false)
	if err != nil {
		b.reportFailure(s, i, "shop", "Не удалось загрузить каталог магазина.", err)
		return
	}
	if len(items) == 0 {
		b.respondMessage(s, i, "Магазин пока пуст.", true)
		return
	}

	balanceLine := ""
	if member := interactionMember(i); member != nil && member.User != nil {
		if pid, _, err := b.services.MatchService.GetPlayerByDiscordID(ctx, member.User.ID); err == nil {
			if econ, eErr := b.services.EconomyService.GetPlayerEconomy(ctx, pid); eErr == nil {
				balanceLine = fmt.Sprintf("Ваш баланс: **%s** • Уровень: **%d** • 🛡️ Страховок: **%d**\n\n",
					domain.FormatCoins(econ.Points), econ.Level, econ.BetInsuranceCount)
			}
		}
	}

	embed, components := buildShopCatalogView(items, balanceLine, false)

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

func buildShopCatalogView(items []models.ShopItem, headerPrefix string, includeShowcaseButtons bool) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	var sb strings.Builder
	sb.WriteString(headerPrefix)

	options := make([]discordgo.SelectMenuOption, 0, min(len(items), 25))
	for _, it := range items {
		icon := shopKindIcon(it.Kind)
		stockStr := ""
		if it.Stock != nil {
			if *it.Stock <= 0 {
				stockStr = " *(нет в наличии)*"
			} else {
				stockStr = fmt.Sprintf(" *(осталось: %d)*", *it.Stock)
			}
		}
		fmt.Fprintf(&sb, "%s **%s** — **%s**%s\n", icon, it.Name, domain.FormatCoins(it.Price), stockStr)
		if it.Description != "" {
			fmt.Fprintf(&sb, "└ %s\n", it.Description)
		}

		if len(options) < 25 && (it.Stock == nil || *it.Stock > 0) {
			desc := it.Description
			if len([]rune(desc)) > 90 {
				desc = string([]rune(desc)[:87]) + "..."
			}
			label := fmt.Sprintf("%s — %d %s", it.Name, it.Price, domain.CurrencySymbol)
			if len([]rune(label)) > 95 {
				label = string([]rune(label)[:92]) + "..."
			}
			options = append(options, discordgo.SelectMenuOption{
				Label:       label,
				Value:       strconv.Itoa(it.ID),
				Description: desc,
			})
		}
	}

	var components []discordgo.MessageComponent
	if len(options) > 0 {
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    selectShopItem,
				Placeholder: "Выберите товар для покупки...",
				Options:     options,
			},
		}})
	}
	if includeShowcaseButtons {
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: fmt.Sprintf("%s Мой баланс", domain.CurrencySymbol), Style: discordgo.PrimaryButton, CustomID: buttonShopMyBalance},
			discordgo.Button{Label: "Обновить витрину", Style: discordgo.SecondaryButton, CustomID: buttonShopRefresh},
		}})
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Магазин Valhalla (%s)", domain.CurrencyName),
		Description: sb.String(),
		Color:       colorShopTeal,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Выберите товар ниже • Титулы и рамки сразу видны в /profile"},
	}
	return embed, components
}

func (b *Bot) onShopComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	cid := i.MessageComponentData().CustomID
	if cid != selectShopItem &&
		cid != buttonShopMyBalance &&
		cid != buttonShopRefresh &&
		cid != buttonShopCancelBuy &&
		!strings.HasPrefix(cid, buttonShopConfirmBuy) &&
		!strings.HasPrefix(cid, buttonShopOrderApprove) &&
		!strings.HasPrefix(cid, buttonShopOrderCancel) {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}

	switch {
	case cid == selectShopItem:
		b.handleShopItemSelected(ctx, s, i.Interaction)
	case cid == buttonShopMyBalance:
		b.handleShopMyBalance(ctx, s, i.Interaction)
	case cid == buttonShopRefresh:
		b.handleShopRefreshShowcase(ctx, s, i.Interaction)
	case cid == buttonShopCancelBuy:
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "Покупка отменена.",
				Components: []discordgo.MessageComponent{},
			},
		})
	case strings.HasPrefix(cid, buttonShopConfirmBuy):
		itemID := parseID(strings.TrimPrefix(cid, buttonShopConfirmBuy))
		if itemID <= 0 {
			b.respondMessage(s, i.Interaction, "Некорректный товар.", true)
			return
		}
		playerID, _, ok := b.requireBoundPlayer(ctx, s, i.Interaction)
		if !ok {
			return
		}
		b.executeShopPurchase(ctx, s, i.Interaction, playerID, itemID, "", true)
	case strings.HasPrefix(cid, buttonShopOrderApprove):
		orderID := parseID(strings.TrimPrefix(cid, buttonShopOrderApprove))
		b.handleShopOrderDecision(ctx, s, i.Interaction, orderID, true)
	case strings.HasPrefix(cid, buttonShopOrderCancel):
		orderID := parseID(strings.TrimPrefix(cid, buttonShopOrderCancel))
		b.handleShopOrderDecision(ctx, s, i.Interaction, orderID, false)
	}
}

func (b *Bot) handleShopMyBalance(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	playerID, _, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}
	econ, err := b.services.EconomyService.GetPlayerEconomy(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "shop_balance", "Не удалось загрузить баланс.", err)
		return
	}
	lvl, curXP, needXP := domain.LevelFromXP(econ.XP)
	b.respondMessage(s, i, fmt.Sprintf(
		"🪙 **%s** • Баланс: **%s**\nУровень: **%d ур.** (`%d / %d XP`) • 🛡️ Страховок ставок: **%d**",
		econ.Name, domain.FormatCoins(econ.Points), lvl, curXP, needXP, econ.BetInsuranceCount,
	), true)
}

func (b *Bot) handleShopRefreshShowcase(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	items, err := b.services.EconomyService.ListShopItems(ctx, false)
	if err != nil {
		b.reportFailure(s, i, "shop_refresh", "Не удалось обновить витрину.", err)
		return
	}
	embed, components := buildShopCatalogView(items, "Выберите товар из меню ниже — покупка проходит приватно, чат остаётся чистым!\n\n", true)
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

func (b *Bot) handleShopItemSelected(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		b.respondMessage(s, i, "Товар не выбран.", true)
		return
	}
	itemID, err := strconv.Atoi(vals[0])
	if err != nil || itemID <= 0 {
		b.respondMessage(s, i, "Некорректный товар.", true)
		return
	}

	playerID, _, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}

	item, err := b.services.EconomyService.GetShopItemByID(ctx, itemID)
	if err != nil {
		b.respondMessage(s, i, shopErrorMessage(err), true)
		return
	}

	// Items requiring text input open a modal BEFORE debiting coins.
	switch item.Kind {
	case domain.ShopKindTitle:
		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseModal,
			Data: &discordgo.InteractionResponseData{
				CustomID: fmt.Sprintf("%s%d", modalShopTitlePrefix, item.ID),
				Title:    fmt.Sprintf("Покупка титула (%d монет)", item.Price),
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    inputShopCustomValue,
							Label:       "Ваш новый титул (до 40 символов)",
							Style:       discordgo.TextInputShort,
							Placeholder: "Например: Мейнер Лейлы / Бог Валгаллы",
							Required:    true,
							MaxLength:   domain.MaxProfileTitleRunes,
						},
					}},
				},
			},
		})
		return
	case domain.ShopKindPrize:
		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseModal,
			Data: &discordgo.InteractionResponseData{
				CustomID: fmt.Sprintf("%s%d", modalShopPrizePrefix, item.ID),
				Title:    truncateModalTitle(fmt.Sprintf("Заявка: %s", item.Name)),
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    inputShopCustomValue,
							Label:       "Game ID / комментарий для админа",
							Style:       discordgo.TextInputShort,
							Placeholder: "Например: ID 123456789 (1234)",
							Required:    false,
							MaxLength:   120,
						},
					}},
				},
			},
		})
		return
	}

	// Instant-buy items ask for one quick ephemeral confirmation before debiting
	// coins so a phone tap on the select menu cannot accidentally spend balance.
	balNote := ""
	if econ, eErr := b.services.EconomyService.GetPlayerEconomy(ctx, playerID); eErr == nil {
		balNote = fmt.Sprintf("\nВаш баланс: **%s**", domain.FormatCoins(econ.Points))
		if econ.Points < item.Price {
			b.respondMessage(s, i, fmt.Sprintf(
				"Недостаточно %s для **%s**: нужно **%s**, а у вас **%s**.",
				domain.CurrencyName, item.Name, domain.FormatCoins(item.Price), domain.FormatCoins(econ.Points),
			), true)
			return
		}
	}

	desc := fmt.Sprintf("Подтвердите покупку: %s **%s** за **%s**%s", shopKindIcon(item.Kind), item.Name, domain.FormatCoins(item.Price), balNote)
	if item.Description != "" {
		desc += "\n\n" + item.Description
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: desc,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    fmt.Sprintf("Купить за %d %s", item.Price, domain.CurrencySymbol),
						Style:    discordgo.SuccessButton,
						CustomID: fmt.Sprintf("%s%d", buttonShopConfirmBuy, item.ID),
					},
					discordgo.Button{
						Label:    "Отмена",
						Style:    discordgo.SecondaryButton,
						CustomID: buttonShopCancelBuy,
					},
				}},
			},
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) handleShopModalSubmit(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	data := i.ModalSubmitData()
	cid := data.CustomID

	var itemID int
	switch {
	case strings.HasPrefix(cid, modalShopTitlePrefix):
		itemID = parseID(strings.TrimPrefix(cid, modalShopTitlePrefix))
	case strings.HasPrefix(cid, modalShopPrizePrefix):
		itemID = parseID(strings.TrimPrefix(cid, modalShopPrizePrefix))
	default:
		b.respondMessage(s, i, "Неизвестная форма магазина.", true)
		return
	}
	if itemID <= 0 {
		b.respondMessage(s, i, "Некорректный товар.", true)
		return
	}

	playerID, _, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}

	customInput := extractModalInput(data, inputShopCustomValue)
	b.executeShopPurchase(ctx, s, i, playerID, itemID, customInput, false)
}

func (b *Bot) executeShopPurchase(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, playerID, itemID int, customInput string, updateMsg bool) {
	reply := func(text string) {
		if updateMsg {
			b.respond(s, i, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Content:    text,
					Components: []discordgo.MessageComponent{},
				},
			})
			return
		}
		b.respondMessage(s, i, text, true)
	}

	order, err := b.services.EconomyService.PurchaseItem(ctx, playerID, itemID, customInput)
	if err != nil {
		reply(shopErrorMessage(err))
		return
	}

	switch order.ItemKind {
	case domain.ShopKindTitle:
		reply(fmt.Sprintf(
			"✅ Куплено: **%s** за **%s** (заказ `#%d`)!\nВаш новый титул: **«%s»**\nОстаток баланса: **%s**",
			order.ItemName, domain.FormatCoins(order.PricePaid), order.ID, order.CustomInput, domain.FormatCoins(order.NewBalance),
		))

	case domain.ShopKindProfileColor:
		reply(fmt.Sprintf(
			"✅ Куплено: **%s** за **%s** (заказ `#%d`)!\nЦвет рамки `/profile` обновлён.\nОстаток баланса: **%s**",
			order.ItemName, domain.FormatCoins(order.PricePaid), order.ID, domain.FormatCoins(order.NewBalance),
		))

	case domain.ShopKindBetInsurance:
		reply(fmt.Sprintf(
			"✅ Куплено: **%s** за **%s** (заказ `#%d`)!\n🛡️ +1 купон страховки добавлен в инвентарь. Используйте кнопки с 🛡️ при ставке на матч.\nОстаток баланса: **%s**",
			order.ItemName, domain.FormatCoins(order.PricePaid), order.ID, domain.FormatCoins(order.NewBalance),
		))

	case domain.ShopKindDiscordRole:
		member := interactionMember(i)
		guildID := b.guildID(i.GuildID)
		if member == nil || member.User == nil || order.ItemPayload == "" {
			_ = b.services.EconomyService.RefundRoleOrder(ctx, order.ID, "invalid role config")
			reply("У этого товара не указана Discord-роль. Монеты возвращены на баланс.")
			return
		}
		if err := s.GuildMemberRoleAdd(guildID, member.User.ID, order.ItemPayload); err != nil {
			b.logger.Error("shop: failed to grant role %s to user %s (order #%d): %v", order.ItemPayload, member.User.ID, order.ID, err)
			if rErr := b.services.EconomyService.RefundRoleOrder(ctx, order.ID, "discord role error"); rErr != nil {
				b.logger.Error("shop: CRITICAL failed to refund order #%d after role error: %v", order.ID, rErr)
			}
			reply("Бот не смог выдать эту роль (проверьте права/иерархию ролей). Монеты автоматически возвращены.")
			return
		}
		reply(fmt.Sprintf(
			"✅ Куплено: **%s** за **%s** (заказ `#%d`)!\nРоль <@&%s> выдана!\nОстаток баланса: **%s**",
			order.ItemName, domain.FormatCoins(order.PricePaid), order.ID, order.ItemPayload, domain.FormatCoins(order.NewBalance),
		))

	case domain.ShopKindPrize:
		b.postPrizeOrderToAdmins(s, i.ChannelID, order)
		reply(fmt.Sprintf(
			"🎁 Заявка **#%d** на **%s** (за **%s**) отправлена администраторам!\nОстаток баланса: **%s**\n*(Если заявку отменят, монеты автоматически вернутся.)*",
			order.ID, order.ItemName, domain.FormatCoins(order.PricePaid), domain.FormatCoins(order.NewBalance),
		))
	}
}

func (b *Bot) postPrizeOrderToAdmins(s *discordgo.Session, fallbackChannelID string, order *models.ShopOrder) {
	targetChannel := b.cfg.ShopAdminChannelID
	if targetChannel == "" {
		targetChannel = fallbackChannelID
	}
	if targetChannel == "" {
		return
	}

	buyer := fmt.Sprintf("**%s** (ID: %d)", order.PlayerName, order.PlayerID)
	if order.DiscordID != "" {
		buyer = fmt.Sprintf("<@%s> (**%s**, ID: %d)", order.DiscordID, order.PlayerName, order.PlayerID)
	}
	noteLine := "*(не указан)*"
	if order.CustomInput != "" {
		noteLine = order.CustomInput
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("Заявка на приз #%d", order.ID),
		Color: colorBettingGold,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Покупатель", Value: buyer, Inline: false},
			{Name: "Приз", Value: order.ItemName, Inline: true},
			{Name: "Оплачено", Value: domain.FormatCoins(order.PricePaid), Inline: true},
			{Name: "Комментарий / Game ID", Value: noteLine, Inline: false},
		},
	}

	_, err := s.ChannelMessageSendComplex(targetChannel, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Выдано",
					Style:    discordgo.SuccessButton,
					CustomID: fmt.Sprintf("%s%d", buttonShopOrderApprove, order.ID),
				},
				discordgo.Button{
					Label:    "Отменить (возврат)",
					Style:    discordgo.DangerButton,
					CustomID: fmt.Sprintf("%s%d", buttonShopOrderCancel, order.ID),
				},
			}},
		},
	})
	if err != nil {
		b.logger.Error("shop: failed to post prize order #%d to channel %s: %v", order.ID, targetChannel, err)
	}
}

func (b *Bot) handleShopOrderDecision(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, orderID int, approve bool) {
	member := interactionMember(i)
	if member == nil || member.User == nil || !b.isAdmin(member.User.ID) {
		b.respondMessage(s, i, "Подтверждать или отменять призовые заявки могут только администраторы.", true)
		return
	}
	if orderID <= 0 {
		b.respondMessage(s, i, "Некорректный ID заявки.", true)
		return
	}

	order, err := b.services.EconomyService.ResolveOrder(ctx, orderID, approve, member.User.ID)
	if err != nil {
		b.respondMessage(s, i, shopErrorMessage(err), true)
		return
	}

	statusTitle := fmt.Sprintf("✅ Заявка #%d — ВЫДАНО", order.ID)
	statusColor := winColor
	detail := fmt.Sprintf("Приз **%s** выдан игроку **%s**.\nОбработал: <@%s>", order.ItemName, order.PlayerName, member.User.ID)
	if !approve {
		statusTitle = fmt.Sprintf("❌ Заявка #%d — ОТМЕНЕНА", order.ID)
		statusColor = loseColor
		detail = fmt.Sprintf("Заявка на **%s** отменена.\nИгроку **%s** возвращено **%s** (баланс: **%s**).\nОбработал: <@%s>",
			order.ItemName, order.PlayerName, domain.FormatCoins(order.PricePaid), domain.FormatCoins(order.NewBalance), member.User.ID)
	}
	if order.CustomInput != "" {
		detail += fmt.Sprintf("\nКомментарий: %s", order.CustomInput)
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{{
				Title:       statusTitle,
				Description: detail,
				Color:       statusColor,
			}},
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) handleShopAdmin(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		b.respondMessage(s, i, "Выберите подкоманду: `add`, `toggle` или `list`.", true)
		return
	}
	sub := opts[0]
	switch sub.Name {
	case "post":
		items, err := b.services.EconomyService.ListShopItems(ctx, false)
		if err != nil {
			b.reportFailure(s, i, "shop_admin", "Не удалось загрузить каталог магазина.", err)
			return
		}
		if len(items) == 0 {
			b.respondMessage(s, i, "В магазине нет активных товаров.", true)
			return
		}
		targetChannel := b.cfg.ShopChannelID
		if targetChannel == "" {
			targetChannel = i.ChannelID
		}
		embed, components := buildShopCatalogView(items, "Выберите товар из меню ниже — покупка проходит приватно, чат остаётся чистым!\n\n", true)
		if _, err := s.ChannelMessageSendComplex(targetChannel, &discordgo.MessageSend{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		}); err != nil {
			b.reportFailure(s, i, "shop_admin", "Не удалось опубликовать витрину магазина.", err)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("✅ Интерактивная витрина магазина опубликована в <#%s>.", targetChannel), true)

	case "list":
		items, err := b.services.EconomyService.ListShopItems(ctx, true)
		if err != nil {
			b.reportFailure(s, i, "shop_admin", "Не удалось загрузить товары.", err)
			return
		}
		var sb strings.Builder
		for _, it := range items {
			st := "🟢"
			if !it.Enabled {
				st = "🔴"
			}
			stock := "∞"
			if it.Stock != nil {
				stock = strconv.Itoa(*it.Stock)
			}
			fmt.Fprintf(&sb, "%s `%s` (%s) — **%s** | %s | остаток: `%s`\n",
				st, it.Code, it.Kind, domain.FormatCoins(it.Price), it.Name, stock)
		}
		b.respondMessage(s, i, sb.String(), true)

	case "toggle":
		var code string
		var enabled bool
		for _, o := range sub.Options {
			switch o.Name {
			case "code":
				code = o.StringValue()
			case "enabled":
				enabled = o.BoolValue()
			}
		}
		item, err := b.services.EconomyService.SetShopItemEnabled(ctx, code, enabled)
		if err != nil {
			b.respondMessage(s, i, shopErrorMessage(err), true)
			return
		}
		state := "включён"
		if !item.Enabled {
			state = "выключен"
		}
		b.respondMessage(s, i, fmt.Sprintf("Товар `%s` (**%s**) %s.", item.Code, item.Name, state), false)

	case "add":
		item := &models.ShopItem{Enabled: true, SortOrder: 100}
		for _, o := range sub.Options {
			switch o.Name {
			case "code":
				item.Code = o.StringValue()
			case "name":
				item.Name = o.StringValue()
			case "kind":
				item.Kind = o.StringValue()
			case "price":
				item.Price = int(o.IntValue())
			case "description":
				item.Description = o.StringValue()
			case "role":
				if role := o.RoleValue(s, b.guildID(i.GuildID)); role != nil {
					item.Payload = role.ID
				}
			case "payload":
				if item.Payload == "" {
					item.Payload = o.StringValue()
				}
			case "stock":
				v := int(o.IntValue())
				if v >= 0 {
					item.Stock = &v
				}
			}
		}
		if item.Kind == domain.ShopKindDiscordRole && item.Payload == "" {
			b.respondMessage(s, i, "Для товара типа `discord_role` укажите параметр `role`.", true)
			return
		}
		saved, err := b.services.EconomyService.UpsertShopItem(ctx, item)
		if err != nil {
			b.respondMessage(s, i, fmt.Sprintf("Не удалось сохранить товар: %v", err), true)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf(
			"✅ Товар `%s` (**%s**, `%s`) сохранён по цене **%s**.",
			saved.Code, saved.Name, saved.Kind, domain.FormatCoins(saved.Price),
		), false)

	case "orders":
		playerID := int(sub.Options[0].IntValue())
		orders, err := b.services.EconomyService.GetPlayerOrders(ctx, playerID, 10)
		if err != nil {
			b.reportFailure(s, i, "shop_admin", "Не удалось загрузить заказы.", err)
			return
		}
		if len(orders) == 0 {
			b.respondMessage(s, i, fmt.Sprintf("У игрока ID %d нет покупок в магазине.", playerID), true)
			return
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "**Последние заказы игрока ID %d:**\n", playerID)
		for _, o := range orders {
			fmt.Fprintf(&sb, "• `#%d` | **%s** (`%s`) — %s | `%s` (%s)\n",
				o.ID, o.ItemName, o.ItemKind, domain.FormatCoins(o.PricePaid), o.Status, o.CreatedAt.Format("02.01 15:04"))
		}
		b.respondMessage(s, i, sb.String(), true)

	case "refund":
		orderID := int(sub.Options[0].IntValue())
		adminID := ""
		if m := interactionMember(i); m != nil && m.User != nil {
			adminID = m.User.ID
		}
		order, err := b.services.EconomyService.AdminRefundShopOrder(ctx, orderID, adminID)
		if err != nil {
			b.respondMessage(s, i, shopErrorMessage(err), true)
			return
		}
		extra := ""
		if order.ItemKind == domain.ShopKindDiscordRole && order.DiscordID != "" && order.ItemPayload != "" {
			if rErr := s.GuildMemberRoleRemove(b.guildID(i.GuildID), order.DiscordID, order.ItemPayload); rErr != nil {
				b.logger.Warn("shop_admin refund: failed to remove role %s from %s: %v", order.ItemPayload, order.DiscordID, rErr)
				extra = "\n⚠️ Не удалось автоматически снять роль — снимите вручную."
			} else {
				extra = fmt.Sprintf("\nРоль <@&%s> снята.", order.ItemPayload)
			}
		}
		b.respondMessage(s, i, fmt.Sprintf(
			"↩️ Заказ **#%d** (**%s**) отменён.\nИгроку **%s** возвращено **%s** (новый баланс: **%s**).%s",
			order.ID, order.ItemName, order.PlayerName, domain.FormatCoins(order.PricePaid), domain.FormatCoins(order.NewBalance), extra,
		), false)

	case "delete":
		code := sub.Options[0].StringValue()
		if err := b.services.EconomyService.DeleteShopItem(ctx, code); err != nil {
			b.respondMessage(s, i, shopErrorMessage(err), true)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("🗑 Товар `%s` удалён из магазина.", code), false)
	}
}

func shopKindIcon(kind string) string {
	switch kind {
	case domain.ShopKindTitle:
		return "🏷️"
	case domain.ShopKindProfileColor:
		return "🎨"
	case domain.ShopKindDiscordRole:
		return "👑"
	case domain.ShopKindBetInsurance:
		return "🛡️"
	case domain.ShopKindPrize:
		return "🎁"
	default:
		return "🛒"
	}
}

func shopErrorMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrInsufficientPoints):
		return fmt.Sprintf("Недостаточно %s для покупки этого товара.", domain.CurrencyName)
	case errors.Is(err, domain.ErrShopItemNotFound):
		return "Товар не найден."
	case errors.Is(err, domain.ErrShopItemUnavailable):
		return "Этот товар временно снят с продажи."
	case errors.Is(err, domain.ErrShopOutOfStock):
		return "Этот товар закончился."
	case errors.Is(err, domain.ErrShopOrderNotFound):
		return "Заявка не найдена."
	case errors.Is(err, domain.ErrShopOrderAlreadyResolved):
		return "Эта заявка/заказ уже отменена или обработана."
	case errors.Is(err, domain.ErrShopItemHasOrders):
		return "По этому товару уже были покупки, его нельзя удалить. Выключите его через `/shop_admin toggle <code> false`."
	case errors.Is(err, domain.ErrInvalidProfileTitle):
		return fmt.Sprintf("Некорректный титул (1..%d символов, без @упоминаний).", domain.MaxProfileTitleRunes)
	case errors.Is(err, domain.ErrInvalidProfileColor):
		return "Некорректный HEX-цвет рамки."
	default:
		return "Не удалось обработать покупку. Попробуйте позже."
	}
}

func extractModalInput(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, comp := range data.Components {
		row, ok := comp.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, inner := range row.Components {
			ti, ok := inner.(*discordgo.TextInput)
			if ok && ti.CustomID == customID {
				return strings.TrimSpace(ti.Value)
			}
		}
	}
	return ""
}

func truncateModalTitle(s string) string {
	runes := []rune(s)
	if len(runes) <= 45 {
		return s
	}
	return string(runes[:42]) + "..."
}
