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
	colorWalletGold  = 0xF1C40F
	colorRichPurple  = 0x9B59B6
	buttonTipConfirm = "tip_confirm_"
	buttonTipCancel  = "tip_cancel"
)

// RegisterEconomyComponentHandlers registers confirmation buttons for large /tip transfers.
func (b *Bot) RegisterEconomyComponentHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onEconomyComponent))
}

func (b *Bot) onEconomyComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	cid := i.MessageComponentData().CustomID
	if cid != buttonTipCancel && !strings.HasPrefix(cid, buttonTipConfirm) {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}

	if cid == buttonTipCancel {
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "Перевод отменён.",
				Components: []discordgo.MessageComponent{},
			},
		})
		return
	}

	// Format: tip_confirm_<senderDiscordID>_<receiverDiscordID>_<receiverPlayerID>_<amount>
	parts := strings.Split(strings.TrimPrefix(cid, buttonTipConfirm), "_")
	if len(parts) != 4 {
		b.respondMessage(s, i.Interaction, "Некорректная кнопка подтверждения перевода.", true)
		return
	}
	senderDiscordID, receiverDiscordID := parts[0], parts[1]
	receiverID, err1 := strconv.Atoi(parts[2])
	amount, err2 := strconv.Atoi(parts[3])
	member := interactionMember(i.Interaction)
	if err1 != nil || err2 != nil || member == nil || member.User == nil || member.User.ID != senderDiscordID {
		b.respondMessage(s, i.Interaction, "Подтвердить этот перевод может только его отправитель.", true)
		return
	}

	senderID, senderName, err := b.services.MatchService.GetPlayerByDiscordID(ctx, senderDiscordID)
	if err != nil {
		b.respondMessage(s, i.Interaction, "Ваш профиль не найден.", true)
		return
	}
	receiverName, err := b.services.MatchService.GetPlayerNameByID(ctx, receiverID)
	if err != nil {
		b.respondMessage(s, i.Interaction, "Получатель не найден.", true)
		return
	}

	b.executeTip(ctx, s, i.Interaction, senderID, senderName, receiverID, receiverName, receiverDiscordID, amount, true)
}

// requireBoundPlayer resolves the caller's bound player profile or prompts them
// with the one-click bind modal button.
func (b *Bot) requireBoundPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) (int, string, bool) {
	member := interactionMember(i)
	if member == nil || member.User == nil {
		b.respondMessage(s, i, "Эта команда работает только на сервере.", true)
		return 0, "", false
	}

	id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, member.User.ID)
	if err != nil {
		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content:    "Ваш Discord ещё не привязан к игровому профилю. Нажмите кнопку ниже или выполните `/bind <ник>`.",
				Components: bindPromptComponents(),
				Flags:      discordgo.MessageFlagsEphemeral,
			},
		})
		return 0, "", false
	}
	return id, name, true
}

func (b *Bot) handleWallet(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id, _, ok := b.resolveTargetPlayer(ctx, s, i)
	if !ok {
		return
	}

	econ, err := b.services.EconomyService.GetPlayerEconomy(ctx, id)
	if err != nil {
		b.reportFailure(s, i, "wallet", "Не удалось загрузить кошелёк игрока.", err)
		return
	}

	lvl, curXP, needXP := domain.LevelFromXP(econ.XP)
	bar := domain.FormatProgressBar(curXP, needXP, 10)
	nextReward := domain.LevelUpCoinReward(lvl + 1)

	betStats, _ := b.services.EconomyService.GetPlayerBetStats(ctx, id)
	ledger, _ := b.services.EconomyService.GetLedger(ctx, id, 8)

	color := colorWalletGold
	if econ.ProfileColor > 0 {
		color = econ.ProfileColor
	}

	title := fmt.Sprintf("Кошелёк: %s", econ.Name)
	desc := ""
	if econ.ProfileTitle != "" {
		desc = fmt.Sprintf("«%s»", econ.ProfileTitle)
	}

	fields := []*discordgo.MessageEmbedField{
		{
			Name:   fmt.Sprintf("Баланс (%s)", domain.CurrencyName),
			Value:  fmt.Sprintf("**%s**", domain.FormatCoins(econ.Points)),
			Inline: true,
		},
		{
			Name:   "Уровень",
			Value:  fmt.Sprintf("**%d ур.** (след. награда +%s)", lvl, domain.FormatCoins(nextReward)),
			Inline: true,
		},
		{
			Name:   "Страховки ставок",
			Value:  fmt.Sprintf("🛡️ **%d** шт.", econ.BetInsuranceCount),
			Inline: true,
		},
		{
			Name:   "Прогресс XP",
			Value:  fmt.Sprintf("`%s` (`%d / %d XP`, всего `%d XP`)", bar, curXP, needXP, econ.XP),
			Inline: false,
		},
		{
			Name:   "Активность",
			Value:  fmt.Sprintf("💬 Сообщений: **%d** • 🎙️ В войсе: **%s** • 🔥 Daily-серия: **%d дн.**", econ.MessagesCount, domain.FormatVoiceDuration(econ.VoiceSeconds), econ.DailyStreak),
			Inline: false,
		},
	}

	if betStats.TotalBets > 0 {
		net := betStats.TotalPayout - betStats.TotalWagered
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: "Статистика ставок",
			Value: fmt.Sprintf("Ставок: **%d** (выиграно **%d**) • Профит: **%+d %s**",
				betStats.TotalBets, betStats.WonBets, net, domain.CurrencySymbol),
			Inline: false,
		})
	}

	if len(ledger) > 0 {
		var sb strings.Builder
		for _, e := range ledger {
			note := e.Note
			if note == "" {
				note = formatLedgerReason(e.Reason)
			}
			fmt.Fprintf(&sb, "`%+d` %s — %s (`%s`)\n", e.Amount, domain.CurrencySymbol, note, e.CreatedAt.Format("02.01 15:04"))
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("Последние операции (%d)", len(ledger)),
			Value:  sb.String(),
			Inline: false,
		})
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: desc,
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: "/daily — ежедневный бонус • /shop — магазин • /tip — перевод"},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handleDaily(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	playerID, playerName, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}

	res, err := b.services.EconomyService.ClaimDaily(ctx, playerID)
	if errors.Is(err, domain.ErrDailyAlreadyClaimed) && res != nil {
		b.respondMessage(s, i, fmt.Sprintf(
			"Вы уже забирали ежедневный бонус! Следующий будет доступен <t:%d:R>.\nТекущая серия: **%d дн.** • Баланс: **%s**",
			res.NextClaimAt.Unix(), res.Streak, domain.FormatCoins(res.NewBalance),
		), true)
		return
	}
	if err != nil {
		b.reportFailure(s, i, "daily", "Не удалось выдать ежедневный бонус.", err)
		return
	}

	nextBonus := domain.DailyRewardForStreak(res.Streak + 1)
	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("Ежедневный бонус — %s", playerName),
		Description: fmt.Sprintf(
			"Получено: **+%s**\nСерия: **%d дн.** 🔥 *(завтра: +%s)*\nБаланс: **%s**\n\nСледующий `/daily`: <t:%d:R>",
			domain.FormatCoins(res.CoinsAwarded),
			res.Streak,
			domain.FormatCoins(nextBonus),
			domain.FormatCoins(res.NewBalance),
			res.NextClaimAt.Unix(),
		),
		Color: winColor,
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handleTip(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	senderID, senderName, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}

	opts := i.ApplicationCommandData().Options
	targetUser := opts[0].UserValue(s)
	amount := int(opts[1].IntValue())
	if targetUser == nil {
		b.respondMessage(s, i, "Не удалось определить получателя.", true)
		return
	}

	receiverID, receiverName, err := b.services.MatchService.GetPlayerByDiscordID(ctx, targetUser.ID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("<@%s> ещё не привязал игровой профиль (`/bind`).", targetUser.ID), true)
		return
	}

	if amount < domain.TipMinAmount || amount > domain.TipMaxAmount {
		b.respondMessage(s, i, fmt.Sprintf("Сумма перевода должна быть от %d до %d %s.", domain.TipMinAmount, domain.TipMaxAmount, domain.CurrencySymbol), true)
		return
	}
	if senderID == receiverID {
		b.respondMessage(s, i, "Нельзя переводить монеты самому себе.", true)
		return
	}

	// Large tips (>= TipConfirmThreshold) ask for confirmation so a typo with an extra zero doesn't burn 5% instantly.
	if amount >= domain.TipConfirmThreshold {
		member := interactionMember(i)
		burn := domain.CalculateTipBurn(amount)
		net := amount - burn
		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf(
					"⚠️ **Подтвердите перевод %s** → <@%s> (**%s**)\nКомиссия %d%%: **%s** (сжигается) • Получатель получит: **%s**",
					domain.FormatCoins(amount), targetUser.ID, receiverName, domain.TipBurnPercent, domain.FormatCoins(burn), domain.FormatCoins(net),
				),
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    fmt.Sprintf("Перевести %d %s", amount, domain.CurrencySymbol),
							Style:    discordgo.SuccessButton,
							CustomID: fmt.Sprintf("%s%s_%s_%d_%d", buttonTipConfirm, member.User.ID, targetUser.ID, receiverID, amount),
						},
						discordgo.Button{
							Label:    "Отмена",
							Style:    discordgo.SecondaryButton,
							CustomID: buttonTipCancel,
						},
					}},
				},
				Flags: discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	b.executeTip(ctx, s, i, senderID, senderName, receiverID, receiverName, targetUser.ID, amount, false)
}

func (b *Bot) executeTip(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, senderID int, senderName string, receiverID int, receiverName, receiverDiscordID string, amount int, updateMsg bool) {
	replyErr := func(text string) {
		if updateMsg {
			b.respond(s, i, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{Content: text, Components: []discordgo.MessageComponent{}},
			})
			return
		}
		b.respondMessage(s, i, text, true)
	}

	res, err := b.services.EconomyService.TipCoins(ctx, senderID, receiverID, amount)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrSelfTip):
			replyErr("Нельзя переводить монеты самому себе.")
		case errors.Is(err, domain.ErrInsufficientPoints):
			replyErr(fmt.Sprintf("Недостаточно %s для перевода.", domain.CurrencyName))
		case errors.Is(err, domain.ErrInvalidAmount):
			replyErr(fmt.Sprintf("Сумма перевода должна быть от %d до %d %s.", domain.TipMinAmount, domain.TipMaxAmount, domain.CurrencySymbol))
		default:
			b.reportFailure(s, i, "tip", "Не удалось выполнить перевод.", err)
		}
		return
	}

	burnNote := ""
	if res.Burned > 0 {
		burnNote = fmt.Sprintf(" *(комиссия %d%%: %s сожжено)*", domain.TipBurnPercent, domain.FormatCoins(res.Burned))
	}
	publicMsg := fmt.Sprintf(
		"💸 **%s** перевёл **%s** → <@%s> (**%s**)%s",
		senderName, domain.FormatCoins(res.AmountReceived), receiverDiscordID, receiverName, burnNote,
	)

	if updateMsg {
		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    fmt.Sprintf("✅ %s\nВаш остаток: **%s**", publicMsg, domain.FormatCoins(res.SenderBalance)),
				Components: []discordgo.MessageComponent{},
			},
		})
		if i.ChannelID != "" {
			_, _ = s.ChannelMessageSend(i.ChannelID, publicMsg)
		}
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("%s\nВаш остаток: **%s**", publicMsg, domain.FormatCoins(res.SenderBalance)), false)
}

func (b *Bot) handleRich(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	mode := "coins"
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "mode" {
			mode = opt.StringValue()
		}
	}

	var rows []models.RichPlayer
	var err error
	title := fmt.Sprintf("Топ богачей (%s)", domain.CurrencyName)
	if mode == "xp" {
		title = "Топ актива (Уровни и XP)"
		rows, err = b.services.EconomyService.GetXPLeaderboard(ctx, 15)
	} else {
		rows, err = b.services.EconomyService.GetRichLeaderboard(ctx, 15)
	}
	if err != nil {
		b.reportFailure(s, i, "rich", "Не удалось загрузить таблицу лидеров.", err)
		return
	}
	if len(rows) == 0 {
		b.respondMessage(s, i, "Таблица пока пуста.", true)
		return
	}

	var sb strings.Builder
	for idx, p := range rows {
		medal := fmt.Sprintf("`%2d.`", idx+1)
		switch idx {
		case 0:
			medal = "🥇"
		case 1:
			medal = "🥈"
		case 2:
			medal = "🥉"
		}
		tag := ""
		if p.ProfileTitle != "" {
			tag = fmt.Sprintf(" *«%s»*", p.ProfileTitle)
		}
		if mode == "xp" {
			fmt.Fprintf(&sb, "%s **%s**%s — **%d ур.** (`%d XP`, 🎙️ %s, %s)\n",
				medal, p.Name, tag, p.Level, p.XP, domain.FormatVoiceDuration(p.VoiceSeconds), domain.FormatCoins(p.Points))
		} else {
			fmt.Fprintf(&sb, "%s **%s**%s — **%s** (`%d ур.`)\n",
				medal, p.Name, tag, domain.FormatCoins(p.Points), p.Level)
		}
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: sb.String(),
		Color:       colorRichPurple,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Зарабатывай монеты в миксах, чате, войсе и ставках • /shop"},
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handleCoinsAdmin(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		b.respondMessage(s, i, "Выберите подкоманду: `add` или `remove`.", true)
		return
	}
	sub := opts[0]
	var playerID, amount int
	var reason string
	for _, o := range sub.Options {
		switch o.Name {
		case "player":
			playerID = int(o.IntValue())
		case "amount":
			amount = int(o.IntValue())
		case "reason":
			reason = strings.TrimSpace(o.StringValue())
		}
	}
	if playerID <= 0 || amount <= 0 {
		b.respondMessage(s, i, "Укажите игрока и положительную сумму.", true)
		return
	}
	delta := amount
	if sub.Name == "remove" {
		delta = -amount
	}

	playerName, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", playerID), true)
		return
	}

	applied, newBal, err := b.services.EconomyService.AdminAdjustCoins(ctx, playerID, delta, reason)
	if err != nil {
		b.reportFailure(s, i, "coins_admin", "Не удалось изменить баланс.", err)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf(
		"Баланс **%s** (ID: %d) изменён на **%+d %s**. Новый баланс: **%s**.",
		playerName, playerID, applied, domain.CurrencySymbol, domain.FormatCoins(newBal),
	), false)
}

func (b *Bot) handleXPAdmin(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		b.respondMessage(s, i, "Выберите подкоманду: `add`, `remove` или `set_level`.", true)
		return
	}
	sub := opts[0]
	var playerID int
	var value int64
	for _, o := range sub.Options {
		switch o.Name {
		case "player":
			playerID = int(o.IntValue())
		case "amount", "level":
			value = o.IntValue()
		}
	}
	if playerID <= 0 || value <= 0 {
		b.respondMessage(s, i, "Укажите игрока и положительное значение.", true)
		return
	}
	playerName, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", playerID), true)
		return
	}

	var res *models.XPGrantResult
	switch sub.Name {
	case "add":
		res, err = b.services.EconomyService.AdminAdjustXP(ctx, playerID, value)
	case "remove":
		res, err = b.services.EconomyService.AdminAdjustXP(ctx, playerID, -value)
	case "set_level":
		res, err = b.services.EconomyService.AdminSetLevel(ctx, playerID, int(value))
	}
	if err != nil {
		b.reportFailure(s, i, "xp_admin", "Не удалось изменить XP/уровень.", err)
		return
	}

	coinsNote := ""
	if res.CoinsAwarded > 0 {
		coinsNote = fmt.Sprintf(" (+%s за новые уровни)", domain.FormatCoins(res.CoinsAwarded))
	}
	b.respondMessage(s, i, fmt.Sprintf(
		"XP игрока **%s** (ID: %d) обновлён: **%d ур.** (`%d XP`, было `%d ур.`)%s.",
		playerName, playerID, res.NewLevel, res.NewXP, res.OldLevel, coinsNote,
	), false)
}

func (b *Bot) handleProfileAdmin(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		b.respondMessage(s, i, "Выберите подкоманду: `clear_title`, `set_title` или `clear_color`.", true)
		return
	}
	sub := opts[0]
	var playerID int
	var title string
	for _, o := range sub.Options {
		switch o.Name {
		case "player":
			playerID = int(o.IntValue())
		case "title":
			title = o.StringValue()
		}
	}
	playerName, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", playerID), true)
		return
	}

	switch sub.Name {
	case "clear_title":
		if err := b.services.EconomyService.AdminSetProfileTitle(ctx, playerID, ""); err != nil {
			b.reportFailure(s, i, "profile_admin", "Не удалось сбросить титул.", err)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("Титул профиля **%s** (ID: %d) сброшен.", playerName, playerID), false)
	case "set_title":
		if err := b.services.EconomyService.AdminSetProfileTitle(ctx, playerID, title); err != nil {
			b.respondMessage(s, i, shopErrorMessage(err), true)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("Титул профиля **%s** (ID: %d) обновлён: **«%s»**.", playerName, playerID, title), false)
	case "clear_color":
		if err := b.services.EconomyService.AdminClearProfileColor(ctx, playerID); err != nil {
			b.reportFailure(s, i, "profile_admin", "Не удалось сбросить цвет рамки.", err)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("Цвет рамки профиля **%s** (ID: %d) сброшен к стандартному.", playerName, playerID), false)
	}
}

// awardAndFormatMatchCoins awards win/loss/MVP/SVP coins for a finished lobby
// match and returns both the per-player rewards and a short summary line
// suitable for the match result embed.
func (b *Bot) awardAndFormatMatchCoins(
	ctx context.Context,
	matchID int,
	winner string,
	mvpID, svpID int,
	mvpName, svpName string,
) ([]models.MatchCoinReward, string) {
	if b.services == nil || b.services.EconomyService == nil {
		return nil, ""
	}
	rewards, err := b.services.EconomyService.AwardMatchCoins(ctx, matchID, winner, mvpID, svpID, mvpName, svpName)
	if err != nil {
		b.logger.Error("economy: failed to award match #%d coins: %v", matchID, err)
		return nil, ""
	}

	awardedCount := 0
	mvpAwarded, svpAwarded := false, false
	for _, r := range rewards {
		if !r.Skipped && r.Coins > 0 {
			awardedCount++
			if r.IsMVP {
				mvpAwarded = true
			}
			if r.IsSVP {
				svpAwarded = true
			}
		}
	}
	if awardedCount == 0 {
		return rewards, ""
	}

	line := fmt.Sprintf("Победа: **+%s** • Участие: **+%s**",
		domain.FormatCoins(domain.MatchWinCoins), domain.FormatCoins(domain.MatchLossCoins))
	if mvpAwarded {
		line += fmt.Sprintf(" • MVP: **+%s**", domain.FormatCoins(domain.MatchMVPCoins))
	}
	if svpAwarded {
		line += fmt.Sprintf(" • SVPG: **+%s**", domain.FormatCoins(domain.MatchSVPCoins))
	}
	return rewards, line
}

func formatLedgerReason(reason string) string {
	switch reason {
	case domain.LedgerReasonMatchReward:
		return "Награда за матч"
	case domain.LedgerReasonLevelUp:
		return "Новый уровень"
	case domain.LedgerReasonDaily:
		return "Ежедневный бонус"
	case domain.LedgerReasonBetPlace:
		return "Ставка"
	case domain.LedgerReasonBetCancel:
		return "Отмена ставки"
	case domain.LedgerReasonBetWin:
		return "Выигрыш ставки"
	case domain.LedgerReasonBetRefund:
		return "Возврат ставки"
	case domain.LedgerReasonBetInsurance:
		return "Страховка ставки"
	case domain.LedgerReasonMatchRevert:
		return "Откат матча"
	case domain.LedgerReasonTipOut:
		return "Перевод игроку"
	case domain.LedgerReasonTipIn:
		return "Получен перевод"
	case domain.LedgerReasonShopBuy:
		return "Покупка в магазине"
	case domain.LedgerReasonOrderRefund:
		return "Возврат заказа"
	case domain.LedgerReasonAdminGrant:
		return "Корректировка админа"
	default:
		return reason
	}
}
