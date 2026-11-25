package discord

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

const (
	buttonDiscordBetOpenA  = "dbet_open_a"
	buttonDiscordBetOpenB  = "dbet_open_b"
	buttonDiscordBetPool   = "dbet_pool"
	buttonDiscordBetStake  = "dbet_stake"
	buttonDiscordBetCancel = "dbet_cancel"

	colorBettingGold       = 0xF39C12
	colorBettingClosed     = 0x7F8C8D
	betCardRefreshInterval = 2 * time.Second
)

// discordBetCard tracks a dedicated betting post in BETTING_CHANNEL_ID so live
// pool updates and window closures can edit it cleanly without re-fetching roster data.
type discordBetCard struct {
	ChannelID     string
	MessageID     string
	CaptainA      string
	CaptainB      string
	TeamANames    []string
	TeamBNames    []string
	ClosesAt      time.Time
	CreatedAt     time.Time
	LastRefreshAt time.Time
}

// bettingCardButtons returns the spectator betting controls row for a match.
func bettingCardButtons(matchID int) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: fmt.Sprintf("%s Ставка: Team A", domain.CurrencySymbol), Style: discordgo.PrimaryButton, CustomID: fmt.Sprintf("%s_%d", buttonDiscordBetOpenA, matchID)},
			discordgo.Button{Label: fmt.Sprintf("%s Ставка: Team B", domain.CurrencySymbol), Style: discordgo.PrimaryButton, CustomID: fmt.Sprintf("%s_%d", buttonDiscordBetOpenB, matchID)},
			discordgo.Button{Label: "Пул ставок", Style: discordgo.SecondaryButton, CustomID: fmt.Sprintf("%s_%d", buttonDiscordBetPool, matchID)},
		}},
	}
}

func bettingCardPoolOnlyButtons(matchID int) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Пул ставок", Style: discordgo.SecondaryButton, CustomID: fmt.Sprintf("%s_%d", buttonDiscordBetPool, matchID)},
		}},
	}
}

func (b *Bot) setBetCard(matchID int, card *discordBetCard) {
	b.cacheMu.Lock()
	b.evictStaleCachesLocked(time.Now())
	b.betCards[matchID] = card
	b.cacheMu.Unlock()
}

func (b *Bot) getBetCard(matchID int) *discordBetCard {
	b.cacheMu.RLock()
	card := b.betCards[matchID]
	b.cacheMu.RUnlock()
	if card == nil {
		return nil
	}
	cp := *card
	return &cp
}

func (b *Bot) deleteBetCard(matchID int) {
	b.cacheMu.Lock()
	delete(b.betCards, matchID)
	b.cacheMu.Unlock()
}

// publishDiscordBetCard posts a dedicated match betting card in BETTING_CHANNEL_ID.
func (b *Bot) publishDiscordBetCard(ctx context.Context, matchID int, captainA, captainB string, teamAIDs, teamBIDs []int) {
	if b.session == nil || b.cfg.BettingChannelID == "" {
		return
	}

	allIDs := concatIDs(teamAIDs, teamBIDs)
	names, err := b.services.MatchService.GetPlayerNamesByIDs(ctx, allIDs)
	if err != nil {
		b.logger.Warn("dbet: failed to load player names for betting card #%d: %v", matchID, err)
		names = map[int]string{}
	}

	teamANames := make([]string, 0, len(teamAIDs))
	for _, id := range teamAIDs {
		if n := names[id]; n != "" {
			teamANames = append(teamANames, n)
		} else {
			teamANames = append(teamANames, fmt.Sprintf("ID %d", id))
		}
	}
	teamBNames := make([]string, 0, len(teamBIDs))
	for _, id := range teamBIDs {
		if n := names[id]; n != "" {
			teamBNames = append(teamBNames, n)
		} else {
			teamBNames = append(teamBNames, fmt.Sprintf("ID %d", id))
		}
	}

	now := time.Now()
	card := &discordBetCard{
		ChannelID:  b.cfg.BettingChannelID,
		CaptainA:   captainA,
		CaptainB:   captainB,
		TeamANames: teamANames,
		TeamBNames: teamBNames,
		ClosesAt:   now.Add(bettingWindow),
		CreatedAt:  now,
	}

	embed := buildDiscordBetCardEmbed(matchID, card, models.BetPool{MatchID: matchID}, "", colorBettingGold)
	sent, err := b.session.ChannelMessageSendComplex(b.cfg.BettingChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: bettingCardButtons(matchID),
	})
	if err != nil {
		b.logger.Error("dbet: failed to post betting card for match #%d to channel %s: %v", matchID, b.cfg.BettingChannelID, err)
		return
	}

	card.MessageID = sent.ID
	b.setBetCard(matchID, card)
	if b.services != nil && b.services.BettingService != nil {
		if err := b.services.BettingService.SaveDiscordBetCard(ctx, matchID, b.cfg.BettingChannelID, sent.ID); err != nil {
			b.logger.Warn("dbet: failed to persist betting card ref for #%d: %v", matchID, err)
		}
	}
	b.logger.Info("dbet: published betting card for match #%d in channel %s (msg %s)", matchID, b.cfg.BettingChannelID, sent.ID)
}

func buildDiscordBetCardEmbed(matchID int, card *discordBetCard, pool models.BetPool, statusOverride string, color int) *discordgo.MessageEmbed {
	title := fmt.Sprintf("%s Ставки открыты — Матч #%d", domain.CurrencySymbol, matchID)
	statusLine := fmt.Sprintf("Окно ставок закроется <t:%d:R>", card.ClosesAt.Unix())
	if statusOverride != "" {
		title = fmt.Sprintf("Матч #%d — %s", matchID, statusOverride)
		statusLine = statusOverride
	}

	teamAList := strings.Join(card.TeamANames, "\n")
	if teamAList == "" {
		teamAList = "—"
	}
	teamBList := strings.Join(card.TeamBNames, "\n")
	if teamBList == "" {
		teamBList = "—"
	}

	return &discordgo.MessageEmbed{
		Title:       title,
		Description: fmt.Sprintf("**Team A** (кап. %s) vs **Team B** (кап. %s)\n%s", card.CaptainA, card.CaptainB, statusLine),
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "🔵 Team A", Value: teamAList, Inline: true},
			{Name: "🔴 Team B", Value: teamBList, Inline: true},
			{Name: "Пул и коэффициенты", Value: formatDiscordPoolBlock(pool), Inline: false},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: "1 ставка на матч • Игроки матча ставить не могут • Страховка 50% в /shop"},
	}
}

// refreshDiscordBetCard updates the live pool & odds on the dedicated betting
// channel card, throttled to avoid hitting Discord's per-channel edit rate limits.
func (b *Bot) refreshDiscordBetCard(matchID int) {
	if b.session == nil || b.cfg.BettingChannelID == "" {
		return
	}

	b.cacheMu.Lock()
	card := b.betCards[matchID]
	if card == nil || card.MessageID == "" {
		b.cacheMu.Unlock()
		return
	}
	now := time.Now()
	if now.Sub(card.LastRefreshAt) < betCardRefreshInterval {
		b.cacheMu.Unlock()
		return
	}
	card.LastRefreshAt = now
	snapshot := *card
	b.cacheMu.Unlock()

	b.goBackground("refresh-discord-bet-card", backgroundTimeout, func(ctx context.Context) {
		pool, err := b.services.BettingService.BetPool(ctx, matchID)
		if err != nil {
			b.logger.Warn("dbet: failed to load pool for card refresh #%d: %v", matchID, err)
			return
		}
		embed := buildDiscordBetCardEmbed(matchID, &snapshot, pool, "", colorBettingGold)
		if _, err := b.session.ChannelMessageEditEmbed(snapshot.ChannelID, snapshot.MessageID, embed); err != nil {
			b.logger.Warn("dbet: failed to edit betting card #%d: %v", matchID, err)
		}
	})
}

// closeDiscordBetCard removes the Team A/B stake buttons on a dedicated betting
// channel card when betting closes, finishes, or is cancelled.
func (b *Bot) closeDiscordBetCard(ctx context.Context, matchID int, statusLine string, keepPoolButton bool) {
	if b.session == nil || b.cfg.BettingChannelID == "" {
		return
	}

	card := b.getBetCard(matchID)
	chID, msgID := "", ""
	if card != nil {
		chID, msgID = card.ChannelID, card.MessageID
	} else if b.services != nil && b.services.BettingService != nil {
		if ch, msg, ok, err := b.services.BettingService.DiscordBetCard(ctx, matchID); err == nil && ok && ch == b.cfg.BettingChannelID {
			chID, msgID = ch, msg
		}
	}
	if chID == "" || msgID == "" {
		return
	}

	var comps []discordgo.MessageComponent
	if keepPoolButton {
		comps = bettingCardPoolOnlyButtons(matchID)
	} else {
		comps = []discordgo.MessageComponent{}
		b.deleteBetCard(matchID)
	}

	edit := &discordgo.MessageEdit{
		Channel:    chID,
		ID:         msgID,
		Components: &comps,
	}
	if card != nil {
		pool, _ := b.services.BettingService.BetPool(ctx, matchID)
		embed := buildDiscordBetCardEmbed(matchID, card, pool, statusLine, colorBettingClosed)
		embeds := []*discordgo.MessageEmbed{embed}
		edit.Embeds = &embeds
	}
	if _, err := b.session.ChannelMessageEditComplex(edit); err != nil {
		b.logger.Warn("dbet: failed to close betting card #%d: %v", matchID, err)
	}
}

// RegisterBettingHandlers registers Discord betting button handlers and wires
// payout/refund callbacks so Discord receives settlement summaries even on recovery sweeps.
func (b *Bot) RegisterBettingHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onDiscordBetButton))
	if b.services != nil && b.services.BettingService != nil {
		b.services.BettingService.SetDiscordPayoutCallback(b.notifyDiscordBetPayout)
		b.services.BettingService.SetDiscordRefundCallback(b.notifyDiscordBetRefund)
	}
}

func (b *Bot) onDiscordBetButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	cid := i.MessageComponentData().CustomID
	if !strings.HasPrefix(cid, "dbet_") {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}

	switch {
	case strings.HasPrefix(cid, buttonDiscordBetOpenA+"_"):
		matchID := parseID(strings.TrimPrefix(cid, buttonDiscordBetOpenA+"_"))
		b.handleDiscordBetOpen(ctx, s, i.Interaction, matchID, domain.TeamA)
	case strings.HasPrefix(cid, buttonDiscordBetOpenB+"_"):
		matchID := parseID(strings.TrimPrefix(cid, buttonDiscordBetOpenB+"_"))
		b.handleDiscordBetOpen(ctx, s, i.Interaction, matchID, domain.TeamB)
	case strings.HasPrefix(cid, buttonDiscordBetPool+"_"):
		matchID := parseID(strings.TrimPrefix(cid, buttonDiscordBetPool+"_"))
		b.handleDiscordBetPool(ctx, s, i.Interaction, matchID)
	case strings.HasPrefix(cid, buttonDiscordBetStake+"_"):
		b.handleDiscordBetStake(ctx, s, i.Interaction, cid)
	case strings.HasPrefix(cid, buttonDiscordBetCancel+"_"):
		matchID := parseID(strings.TrimPrefix(cid, buttonDiscordBetCancel+"_"))
		b.handleDiscordBetCancel(ctx, s, i.Interaction, matchID)
	default:
		b.respondMessage(s, i.Interaction, "Неизвестная кнопка ставки.", true)
	}
}

func (b *Bot) handleDiscordBetOpen(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, matchID int, team string) {
	if matchID <= 0 {
		b.respondMessage(s, i, "Некорректная кнопка ставки.", true)
		return
	}
	playerID, _, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}

	econ, err := b.services.EconomyService.GetPlayerEconomy(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "dbet_open", "Не удалось загрузить баланс.", err)
		return
	}
	if econ.Points <= 0 {
		b.respondMessage(s, i,
			fmt.Sprintf("У вас **0 %s**. Заберите `/daily`, поиграйте микс или пообщайтесь в чате/войсе для опыта и монет!", domain.CurrencySymbol), true)
		return
	}

	pool, err := b.services.BettingService.BetPool(ctx, matchID)
	if err != nil {
		b.logger.Warn("dbet: failed to load pool for match #%d: %v", matchID, err)
	}

	teamCode := "a"
	if team == domain.TeamB {
		teamCode = "b"
	}

	stakes := b.cfg.StakeOptions()
	betMax := b.cfg.BetMax

	var regularBtns []discordgo.MessageComponent
	for _, st := range stakes {
		regularBtns = append(regularBtns, discordgo.Button{
			Label:    fmt.Sprintf("%d %s", st, domain.CurrencySymbol),
			Style:    discordgo.PrimaryButton,
			CustomID: fmt.Sprintf("%s_%s_%d_0_%d", buttonDiscordBetStake, teamCode, st, matchID),
			Disabled: econ.Points < st,
		})
	}
	maxStake := econ.Points
	if betMax > 0 && maxStake > betMax {
		maxStake = betMax
	}
	regularBtns = append(regularBtns, discordgo.Button{
		Label:    fmt.Sprintf("Макс (%d)", maxStake),
		Style:    discordgo.SuccessButton,
		CustomID: fmt.Sprintf("%s_%s_max_0_%d", buttonDiscordBetStake, teamCode, matchID),
	})

	rows := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: regularBtns},
	}

	insuranceHint := ""
	if econ.BetInsuranceCount > 0 {
		var insuredBtns []discordgo.MessageComponent
		for _, st := range stakes {
			if len(insuredBtns) >= 4 {
				break
			}
			insuredBtns = append(insuredBtns, discordgo.Button{
				Label:    fmt.Sprintf("🛡️ %d", st),
				Style:    discordgo.SecondaryButton,
				CustomID: fmt.Sprintf("%s_%s_%d_1_%d", buttonDiscordBetStake, teamCode, st, matchID),
				Disabled: econ.Points < st,
			})
		}
		insuredBtns = append(insuredBtns, discordgo.Button{
			Label:    fmt.Sprintf("🛡️ Макс (%d)", maxStake),
			Style:    discordgo.SecondaryButton,
			CustomID: fmt.Sprintf("%s_%s_max_1_%d", buttonDiscordBetStake, teamCode, matchID),
		})
		rows = append(rows, discordgo.ActionsRow{Components: insuredBtns})
		insuranceHint = fmt.Sprintf("\n🛡️ Страховок ставки в инвентаре: **%d** *(кнопки с 🛡️ вернут 50%% при проигрыше)*", econ.BetInsuranceCount)
	}

	desc := fmt.Sprintf(
		"Выберите сумму ставки на **%s** (матч **#%d**).\n"+
			"Ваш баланс: **%s**%s\n\n%s",
		team, matchID, domain.FormatCoins(econ.Points), insuranceHint, formatDiscordPoolBlock(pool),
	)

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{{
				Title:       fmt.Sprintf("Ставка на %s — Матч #%d", team, matchID),
				Description: desc,
				Color:       colorBettingGold,
				Footer:      &discordgo.MessageEmbedFooter{Text: "Одна ставка на матч • Игроки матча ставить не могут"},
			}},
			Components: rows,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) handleDiscordBetPool(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, matchID int) {
	if matchID <= 0 {
		b.respondMessage(s, i, "Некорректная кнопка пула.", true)
		return
	}
	pool, err := b.services.BettingService.BetPool(ctx, matchID)
	if err != nil {
		b.reportFailure(s, i, "dbet_pool", "Не удалось загрузить пул ставок.", err)
		return
	}

	balLine := ""
	var components []discordgo.MessageComponent
	if member := interactionMember(i); member != nil && member.User != nil {
		if pid, _, err := b.services.MatchService.GetPlayerByDiscordID(ctx, member.User.ID); err == nil {
			if pts, pErr := b.services.BettingService.GetPlayerPointsByID(ctx, pid); pErr == nil {
				balLine = fmt.Sprintf("\n\nВаш баланс: **%s**", domain.FormatCoins(pts))
			}
			if openBet, bErr := b.services.BettingService.GetOpenBetForPlayer(ctx, matchID, pid); bErr == nil && openBet != nil {
				ins := ""
				if openBet.Insured {
					ins = " 🛡️"
				}
				balLine += fmt.Sprintf("\nВаша ставка: **%s** на **%s**%s", domain.FormatCoins(openBet.Amount), openBet.TeamChosen, ins)
				components = []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Отменить мою ставку",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("%s_%d", buttonDiscordBetCancel, matchID),
						},
					}},
				}
			}
		}
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{{
				Title:       fmt.Sprintf("Пул ставок — Матч #%d", matchID),
				Description: formatDiscordPoolBlock(pool) + balLine,
				Color:       colorBettingGold,
			}},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

// parseDiscordBetStake parses "dbet_stake_<a|b>_<amount|max>_<0|1>_<matchID>".
func parseDiscordBetStake(customID string, allowedStakes []int) (team string, stake int, isMax bool, insured bool, matchID int, ok bool) {
	rest := strings.TrimPrefix(customID, buttonDiscordBetStake+"_")
	parts := strings.Split(rest, "_")
	if len(parts) != 4 {
		return "", 0, false, false, 0, false
	}
	switch parts[0] {
	case "a":
		team = domain.TeamA
	case "b":
		team = domain.TeamB
	default:
		return "", 0, false, false, 0, false
	}

	if parts[1] == "max" {
		isMax = true
	} else {
		v, err := strconv.Atoi(parts[1])
		if err != nil || v <= 0 {
			return "", 0, false, false, 0, false
		}
		found := false
		for _, allowed := range allowedStakes {
			if allowed == v {
				found = true
				break
			}
		}
		if !found {
			return "", 0, false, false, 0, false
		}
		stake = v
	}

	switch parts[2] {
	case "0":
		insured = false
	case "1":
		insured = true
	default:
		return "", 0, false, false, 0, false
	}

	mid, err := strconv.Atoi(parts[3])
	if err != nil || mid <= 0 {
		return "", 0, false, false, 0, false
	}
	return team, stake, isMax, insured, mid, true
}

func (b *Bot) handleDiscordBetStake(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, customID string) {
	team, stake, isMax, insured, matchID, ok := parseDiscordBetStake(customID, b.cfg.StakeOptions())
	if !ok {
		b.respondMessage(s, i, "Некорректные параметры ставки.", true)
		return
	}

	playerID, _, bound := b.requireBoundPlayer(ctx, s, i)
	if !bound {
		return
	}

	if isMax {
		pts, err := b.services.BettingService.GetPlayerPointsByID(ctx, playerID)
		if err != nil {
			b.reportFailure(s, i, "dbet_max", "Не удалось проверить баланс.", err)
			return
		}
		stake = pts
		if b.cfg.BetMax > 0 && stake > b.cfg.BetMax {
			stake = b.cfg.BetMax
		}
		if stake <= 0 {
			b.respondMessage(s, i, fmt.Sprintf("Недостаточно %s для ставки.", domain.CurrencyName), true)
			return
		}
	} else if b.cfg.BetMax > 0 && stake > b.cfg.BetMax {
		stake = b.cfg.BetMax
	}

	_, newBalance, err := b.services.BettingService.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID:      matchID,
		PlayerID:     playerID,
		TeamChosen:   team,
		Amount:       stake,
		UseInsurance: insured,
	})
	if err != nil {
		b.respondMessage(s, i, discordBetErrorMessage(err), true)
		return
	}

	pool, _ := b.services.BettingService.BetPool(ctx, matchID)
	odds := pool.Odds(team)
	oddsLine := "Коэффициент сформируется после ставок на обе стороны."
	if odds > 0 {
		est := int(float64(stake) * odds)
		oddsLine = fmt.Sprintf("Текущий коэф.: **x%.2f** (≈ %s при победе)", odds, domain.FormatCoins(est))
	}
	insTag := ""
	if insured {
		insTag = " 🛡️ *(со страховкой 50%)*"
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{{
				Title: fmt.Sprintf("Ставка принята! — Матч #%d", matchID),
				Description: fmt.Sprintf(
					"Вы поставили **%s** на **%s**%s\n%s\nОстаток баланса: **%s**\n\n%s",
					domain.FormatCoins(stake), team, insTag, oddsLine, domain.FormatCoins(newBalance), formatDiscordPoolBlock(pool),
				),
				Color:  winColor,
				Footer: &discordgo.MessageEmbedFooter{Text: "Ошиблись? Пока окно ставок открыто, ставку можно отменить кнопкой ниже"},
			}},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Отменить ставку",
						Style:    discordgo.DangerButton,
						CustomID: fmt.Sprintf("%s_%d", buttonDiscordBetCancel, matchID),
					},
				}},
			},
		},
	})

	b.refreshDiscordBetCard(matchID)
}

func (b *Bot) handleDiscordBetCancel(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, matchID int) {
	if matchID <= 0 {
		b.respondMessage(s, i, "Некорректная кнопка отмены ставки.", true)
		return
	}
	playerID, _, bound := b.requireBoundPlayer(ctx, s, i)
	if !bound {
		return
	}

	refunded, restoredIns, newBalance, err := b.services.BettingService.CancelDiscordBet(ctx, matchID, playerID)
	if err != nil {
		b.respondMessage(s, i, discordBetErrorMessage(err), true)
		return
	}

	insNote := ""
	if restoredIns {
		insNote = " и купон страховки 🛡️"
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{{
				Title: fmt.Sprintf("Ставка отменена — Матч #%d", matchID),
				Description: fmt.Sprintf(
					"Возвращено **%s**%s.\nВаш баланс: **%s**.\n\nТеперь вы можете поставить заново, пока открыто окно ставок.",
					domain.FormatCoins(refunded), insNote, domain.FormatCoins(newBalance),
				),
				Color: colorBettingClosed,
			}},
			Components: []discordgo.MessageComponent{},
		},
	})

	b.refreshDiscordBetCard(matchID)
}

func discordBetErrorMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrBettingClosed):
		return "Окно ставок на этот матч уже закрыто."
	case errors.Is(err, domain.ErrAlreadyBet):
		return "Вы уже сделали ставку на этот матч (максимум 1 ставка на матч)."
	case errors.Is(err, domain.ErrBettingOnOwnMatch):
		return "Вы играете в этом матче — ставить на него нельзя."
	case errors.Is(err, domain.ErrInsufficientPoints):
		return fmt.Sprintf("Недостаточно %s для такой ставки.", domain.CurrencyName)
	case errors.Is(err, domain.ErrNoBetInsurance):
		return "У вас нет купона страховки ставки. Купите его в `/shop`."
	case errors.Is(err, domain.ErrBetNotFound):
		return "У вас нет активной ставки на этот матч (или она уже рассчитана)."
	case errors.Is(err, domain.ErrMatchNotFound):
		return "Матч не найден."
	default:
		return "Не удалось принять ставку. Попробуйте ещё раз."
	}
}

func formatDiscordPoolBlock(pool models.BetPool) string {
	oddsA, oddsB := "—", "—"
	if o := pool.Odds(domain.TeamA); o > 0 {
		oddsA = fmt.Sprintf("x%.2f", o)
	}
	if o := pool.Odds(domain.TeamB); o > 0 {
		oddsB = fmt.Sprintf("x%.2f", o)
	}
	return fmt.Sprintf(
		"**Общий банк:** %s\n• **Team A:** %s (%d ст., коэф. `%s`)\n• **Team B:** %s (%d ст., коэф. `%s`)",
		domain.FormatCoins(pool.Total()),
		domain.FormatCoins(pool.AmountA), pool.CountA, oddsA,
		domain.FormatCoins(pool.AmountB), pool.CountB, oddsB,
	)
}

// settleAndAnnounceMatchEconomy settles all bets for a finished match, closes
// the dedicated betting card, and posts ONE combined result embed to
// BETTING_CHANNEL_ID (or fallback channel) listing both match player coin
// rewards and betting payouts.
func (b *Bot) settleAndAnnounceMatchEconomy(ctx context.Context, matchID int, winner string, matchRewards []models.MatchCoinReward) {
	payoutRes, err := b.services.BettingService.ProcessPayoutDetailed(ctx, matchID, winner)
	if err != nil {
		b.logger.Error("betting: payout for match #%d (%s) failed, bets left unsettled: %v", matchID, winner, err)
	}

	b.closeDiscordBetCard(ctx, matchID, fmt.Sprintf("🏁 Завершён — Победила %s", winner), false)

	// When BETTING_CHANNEL_ID is configured, ALWAYS post the match result + who got
	// what coins (match rewards + bet payouts). Without BETTING_CHANNEL_ID, only
	// post a separate betting summary if bets were actually placed.
	if b.cfg.BettingChannelID == "" && !payoutRes.HadBets() {
		return
	}
	b.postBettingResultEmbed(ctx, payoutRes, matchRewards)
}

// notifyDiscordBetPayout handles delayed settlements swept by SettlePending after a crash.
func (b *Bot) notifyDiscordBetPayout(res application.PayoutResult) {
	if b.session == nil || !res.HadBets() {
		return
	}
	b.goBackground("discord-bet-payout-announce", backgroundTimeout, func(ctx context.Context) {
		b.closeDiscordBetCard(ctx, res.MatchID, fmt.Sprintf("🏁 Завершён — Победила %s", res.WinningTeam), false)
		b.postBettingResultEmbed(ctx, res, nil)
	})
}

func (b *Bot) postBettingResultEmbed(ctx context.Context, res application.PayoutResult, matchRewards []models.MatchCoinReward) {
	if b.session == nil {
		return
	}
	channelID := b.resolveMatchAnnounceChannel(ctx, res.MatchID)
	if channelID == "" {
		return
	}

	idSet := make(map[int]struct{}, len(res.PlayerPayouts)+len(res.InsuranceRefunds)+len(matchRewards))
	for pid := range res.PlayerPayouts {
		idSet[pid] = struct{}{}
	}
	for pid := range res.InsuranceRefunds {
		idSet[pid] = struct{}{}
	}
	for _, r := range matchRewards {
		idSet[r.PlayerID] = struct{}{}
	}
	ids := make([]int, 0, len(idSet))
	for pid := range idSet {
		ids = append(ids, pid)
	}
	names, _ := b.services.MatchService.GetPlayerNamesByIDs(ctx, ids)
	nameOf := func(pid int) string {
		if n := names[pid]; n != "" {
			return n
		}
		return fmt.Sprintf("ID %d", pid)
	}

	var fields []*discordgo.MessageEmbedField

	// 1. Match participant coin rewards (who got how much from playing the match).
	if len(matchRewards) > 0 {
		var winLines, loseLines []string
		for _, r := range matchRewards {
			if r.Coins <= 0 {
				continue
			}
			badge := ""
			if r.IsMVP {
				badge = " 🏆 MVP"
			} else if r.IsSVP {
				badge = " 🛡️ SVPG"
			}
			line := fmt.Sprintf("• **%s**: +%s%s", nameOf(r.PlayerID), domain.FormatCoins(r.Coins), badge)
			if r.Won {
				winLines = append(winLines, line)
			} else {
				loseLines = append(loseLines, line)
			}
		}
		if len(winLines) > 0 || len(loseLines) > 0 {
			var sb strings.Builder
			if len(winLines) > 0 {
				sb.WriteString("**Победители:**\n")
				sb.WriteString(strings.Join(winLines, "\n"))
			}
			if len(loseLines) > 0 {
				if sb.Len() > 0 {
					sb.WriteString("\n\n")
				}
				sb.WriteString("**Проигравшие (участие):**\n")
				sb.WriteString(strings.Join(loseLines, "\n"))
			}
			fields = append(fields, &discordgo.MessageEmbedField{
				Name:   fmt.Sprintf("Награды участникам матча (%s)", domain.CurrencyName),
				Value:  sb.String(),
				Inline: false,
			})
		}
	}

	// 2. Spectator betting payouts & 50% insurance refunds.
	var betSB strings.Builder
	if !res.HadBets() {
		betSB.WriteString("На этот матч ставок не было.")
	} else {
		fmt.Fprintf(&betSB, "Общий банк: **%s** (%d ст.)\n", domain.FormatCoins(res.TotalPool), res.BetCount)
		if len(res.PlayerPayouts) == 0 {
			betSB.WriteString("На победителя никто не поставил — банк сгорел.")
		} else {
			winIDs := make([]int, 0, len(res.PlayerPayouts))
			for pid := range res.PlayerPayouts {
				winIDs = append(winIDs, pid)
			}
			sort.Slice(winIDs, func(i, j int) bool { return res.PlayerPayouts[winIDs[i]] > res.PlayerPayouts[winIDs[j]] })
			betSB.WriteString("\n**Выплаты победителям:**\n")
			for _, pid := range winIDs {
				fmt.Fprintf(&betSB, "• **%s**: +%s\n", nameOf(pid), domain.FormatCoins(res.PlayerPayouts[pid]))
			}
		}
		if len(res.InsuranceRefunds) > 0 {
			insIDs := make([]int, 0, len(res.InsuranceRefunds))
			for pid := range res.InsuranceRefunds {
				insIDs = append(insIDs, pid)
			}
			sort.Ints(insIDs)
			betSB.WriteString("\n**🛡️ Возвраты по страховке (50%):**\n")
			for _, pid := range insIDs {
				fmt.Fprintf(&betSB, "• **%s**: +%s\n", nameOf(pid), domain.FormatCoins(res.InsuranceRefunds[pid]))
			}
		}
	}
	fields = append(fields, &discordgo.MessageEmbedField{
		Name:   "Итоги ставок",
		Value:  betSB.String(),
		Inline: false,
	})

	embed := &discordgo.MessageEmbed{
		Title:  fmt.Sprintf("Итоги матча #%d — Победила %s", res.MatchID, res.WinningTeam),
		Color:  colorBettingGold,
		Fields: fields,
		Footer: &discordgo.MessageEmbedFooter{Text: "Баланс и история: /wallet • Магазин: /shop"},
	}
	if _, err := b.session.ChannelMessageSendEmbed(channelID, embed); err != nil {
		b.logger.Warn("dbet: failed to post match economy result for #%d in %s: %v", res.MatchID, channelID, err)
	}
}

func (b *Bot) notifyDiscordBetRefund(matchID int, refundedCount int) {
	if b.session == nil {
		return
	}
	b.goBackground("discord-bet-refund-announce", backgroundTimeout, func(ctx context.Context) {
		b.closeDiscordBetCard(ctx, matchID, "❌ Матч отменён", false)
		if refundedCount <= 0 && b.cfg.BettingChannelID == "" {
			return
		}
		channelID := b.resolveMatchAnnounceChannel(ctx, matchID)
		if channelID == "" {
			return
		}
		desc := "Матч отменён судьёй. Ставок на него не было."
		if refundedCount > 0 {
			desc = fmt.Sprintf("Матч отменён судьёй. Все ставки (**%d**) и страховки возвращены на баланс.", refundedCount)
		}
		embed := &discordgo.MessageEmbed{
			Title:       fmt.Sprintf("Матч #%d отменён — Возврат ставок", matchID),
			Description: desc,
			Color:       colorBettingClosed,
		}
		if _, err := b.session.ChannelMessageSendEmbed(channelID, embed); err != nil {
			b.logger.Warn("dbet: failed to post refund summary for match #%d in %s: %v", matchID, channelID, err)
		}
	})
}

func (b *Bot) resolveMatchAnnounceChannel(ctx context.Context, matchID int) string {
	if b.cfg.BettingChannelID != "" {
		return b.cfg.BettingChannelID
	}
	if ch, _, ok, err := b.services.BettingService.DiscordBetCard(ctx, matchID); err == nil && ok {
		return ch
	}
	if m, err := b.services.Lobby.GetMatch(ctx, matchID); err == nil && m != nil && m.ThreadID != "" {
		return m.ThreadID
	}
	return b.allowedChannelID
}
