package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

const (
	buttonQuestClaim         = "quest_claim_"
	buttonQuestRefresh       = "quest_refresh_"
	selectSeasonHallOfFame   = "season_hof_select"
	buttonSeasonCloseConfirm = "season_close_confirm_"
	buttonSeasonCloseCancel  = "season_close_cancel_"

	colorQuestTeal   = 0x1ABC9C
	colorSeasonGold  = 0xF39C12
	seasonConfirmTTL = 5 * time.Minute
)

type pendingSeasonClose struct {
	adminID   string
	nextName  string
	resetMode string
	createdAt time.Time
}

type seasonCloseStore struct {
	mu    sync.Mutex
	seq   int64
	items map[string]pendingSeasonClose
}

func newSeasonCloseStore() *seasonCloseStore {
	return &seasonCloseStore{items: make(map[string]pendingSeasonClose)}
}

func (s *seasonCloseStore) put(adminID, nextName, resetMode string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.items {
		if now.Sub(v.createdAt) > seasonConfirmTTL {
			delete(s.items, k)
		}
	}
	s.seq++
	token := fmt.Sprintf("%d_%d", now.Unix(), s.seq)
	s.items[token] = pendingSeasonClose{
		adminID:   adminID,
		nextName:  nextName,
		resetMode: resetMode,
		createdAt: now,
	}
	return token
}

func (s *seasonCloseStore) pop(token string) (pendingSeasonClose, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[token]
	if !ok {
		return pendingSeasonClose{}, false
	}
	delete(s.items, token)
	if time.Since(item.createdAt) > seasonConfirmTTL {
		return pendingSeasonClose{}, false
	}
	return item, true
}

// RegisterQuestSeasonHandlers registers component handlers for /quests, /season, and /season_admin.
func (b *Bot) RegisterQuestSeasonHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onQuestSeasonComponent))
}

func (b *Bot) onQuestSeasonComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	cid := i.MessageComponentData().CustomID
	isQuest := strings.HasPrefix(cid, buttonQuestClaim) || strings.HasPrefix(cid, buttonQuestRefresh)
	isSeason := cid == selectSeasonHallOfFame ||
		strings.HasPrefix(cid, buttonSeasonCloseConfirm) ||
		strings.HasPrefix(cid, buttonSeasonCloseCancel)
	if !isQuest && !isSeason {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}

	switch {
	case strings.HasPrefix(cid, buttonQuestClaim):
		b.handleQuestClaimButton(ctx, s, i.Interaction, strings.TrimPrefix(cid, buttonQuestClaim))
	case strings.HasPrefix(cid, buttonQuestRefresh):
		b.handleQuestRefreshButton(ctx, s, i.Interaction, strings.TrimPrefix(cid, buttonQuestRefresh))
	case cid == selectSeasonHallOfFame:
		b.handleSeasonHallOfFameSelect(ctx, s, i.Interaction)
	case strings.HasPrefix(cid, buttonSeasonCloseConfirm):
		b.handleSeasonCloseConfirm(ctx, s, i.Interaction, strings.TrimPrefix(cid, buttonSeasonCloseConfirm))
	case strings.HasPrefix(cid, buttonSeasonCloseCancel):
		b.handleSeasonCloseCancel(s, i.Interaction, strings.TrimPrefix(cid, buttonSeasonCloseCancel))
	}
}

// =====================================================================
// /quests
// =====================================================================

func (b *Bot) handleQuests(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.services == nil || b.services.QuestService == nil {
		b.respondMessage(s, i, "Система квестов недоступна.", true)
		return
	}
	playerID, playerName, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}
	member := interactionMember(i)

	board, err := b.services.QuestService.GetQuestBoard(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "quests", "Не удалось загрузить квесты.", err)
		return
	}

	embed := buildQuestBoardEmbed(playerName, board, "")
	comps := buildQuestBoardComponents(member.User.ID, board)

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: comps,
		},
	})
}

func (b *Bot) handleQuestRefreshButton(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, ownerDiscordID string) {
	member := interactionMember(i)
	if member == nil || member.User == nil || member.User.ID != ownerDiscordID {
		b.respondMessage(s, i, "Это панель квестов другого игрока. Вызовите `/quests` для себя.", true)
		return
	}
	playerID, playerName, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}
	board, err := b.services.QuestService.GetQuestBoard(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "quests refresh", "Не удалось обновить квесты.", err)
		return
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{buildQuestBoardEmbed(playerName, board, "")},
			Components: buildQuestBoardComponents(ownerDiscordID, board),
		},
	})
}

func (b *Bot) handleQuestClaimButton(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, ownerDiscordID string) {
	member := interactionMember(i)
	if member == nil || member.User == nil || member.User.ID != ownerDiscordID {
		b.respondMessage(s, i, "Забрать награды может только владелец этой панели (`/quests`).", true)
		return
	}
	playerID, playerName, ok := b.requireBoundPlayer(ctx, s, i)
	if !ok {
		return
	}

	claimRes, err := b.services.QuestService.ClaimAll(ctx, playerID)
	if err != nil {
		if errors.Is(err, domain.ErrNothingToClaim) {
			b.respondMessage(s, i, "Пока нет выполненных квестов для получения наград.", true)
			return
		}
		b.reportFailure(s, i, "quests claim", "Не удалось выдать награды за квесты.", err)
		return
	}

	board, err := b.services.QuestService.GetQuestBoard(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "quests reload", "Награды выданы, но не удалось обновить панель.", err)
		return
	}

	banner := fmt.Sprintf("🎉 **Получено за %d %s:** **+%s** • **+%d XP** (баланс: **%s**)",
		len(claimRes.ClaimedTitles), pluralQuestWord(len(claimRes.ClaimedTitles)),
		domain.FormatCoins(claimRes.TotalCoins), claimRes.TotalXP, domain.FormatCoins(claimRes.NewBalance))
	if claimRes.NewLevel > claimRes.OldLevel {
		banner += fmt.Sprintf("\n⬆️ **Новый уровень: %d!**", claimRes.NewLevel)
		if claimRes.LevelUpCoins > 0 {
			banner += fmt.Sprintf(" (+%s бонус уровня)", domain.FormatCoins(claimRes.LevelUpCoins))
		}
		lvlCh := i.ChannelID
		if b.cfg != nil && b.cfg.LevelAnnounceChannelID != "" {
			lvlCh = b.cfg.LevelAnnounceChannelID
		}
		b.announceLevelUp(s, lvlCh, ownerDiscordID, playerName, &models.XPGrantResult{
			PlayerID:     playerID,
			XPGained:     claimRes.TotalXP,
			OldLevel:     claimRes.OldLevel,
			NewLevel:     claimRes.NewLevel,
			CoinsAwarded: claimRes.LevelUpCoins,
			NewBalance:   claimRes.NewBalance,
		}, "квесты")
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{buildQuestBoardEmbed(playerName, board, banner)},
			Components: buildQuestBoardComponents(ownerDiscordID, board),
		},
	})
}

func buildQuestBoardEmbed(playerName string, board *models.QuestBoard, banner string) *discordgo.MessageEmbed {
	desc := fmt.Sprintf(
		"Ежедневные сбросятся <t:%d:R> • Еженедельные <t:%d:R>",
		board.DailyResetsAt.Unix(), board.WeeklyResetsAt.Unix(),
	)
	if banner != "" {
		desc = banner + "\n\n" + desc
	}

	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("📋 Квесты: %s", playerName),
		Description: desc,
		Color:       colorQuestTeal,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   fmt.Sprintf("☀️ Ежедневные (%d/%d)", board.DailyBonus.CompletedCount, board.DailyBonus.TotalCount),
				Value:  formatQuestSection(board.DailyQuests, board.DailyBonus),
				Inline: false,
			},
			{
				Name:   fmt.Sprintf("📆 Еженедельные (%d/%d)", board.WeeklyBonus.CompletedCount, board.WeeklyBonus.TotalCount),
				Value:  formatQuestSection(board.WeeklyQuests, board.WeeklyBonus),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Прогресс начисляется автоматически за матчи, ставки, /daily, чат и войс",
		},
	}
}

func formatQuestSection(quests []models.QuestStatus, bonus models.QuestBonusStatus) string {
	var sb strings.Builder
	for _, q := range quests {
		icon := "▫️"
		statusTag := ""
		if q.Claimed {
			icon = "✅"
			statusTag = " *(получено)*"
		} else if q.Completed {
			icon = "🎁"
			statusTag = " **— готово!**"
		}
		shown := q.Progress
		if shown > q.Def.Target {
			shown = q.Def.Target
		}
		bar := domain.FormatProgressBar(int64(shown), int64(q.Def.Target), 8)
		sb.WriteString(fmt.Sprintf("%s **%s** — %s%s\n", icon, q.Def.Title, q.Def.Description, statusTag))
		sb.WriteString(fmt.Sprintf("`%s` **%d/%d** • +%s • +%d XP\n",
			bar, shown, q.Def.Target, domain.FormatCoins(q.Def.RewardCoins), q.Def.RewardXP))
	}

	bonusIcon := "🔒"
	bonusTag := fmt.Sprintf("(%d/%d)", bonus.CompletedCount, bonus.TotalCount)
	if bonus.Claimed {
		bonusIcon = "✅"
		bonusTag = "*(получено)*"
	} else if bonus.Eligible {
		bonusIcon = "🎁"
		bonusTag = "**— готово!**"
	}
	sb.WriteString(fmt.Sprintf("%s **Бонус за все %d/%d:** +%s • +%d XP %s",
		bonusIcon, bonus.TotalCount, bonus.TotalCount,
		domain.FormatCoins(bonus.RewardCoins), bonus.RewardXP, bonusTag))
	return sb.String()
}

func buildQuestBoardComponents(discordID string, board *models.QuestBoard) []discordgo.MessageComponent {
	claimLabel := "🎁 Нет наград к получению"
	claimStyle := discordgo.SecondaryButton
	claimDisabled := true
	if board.ClaimableCount > 0 {
		claimLabel = fmt.Sprintf("🎁 Забрать награды (+%d %s, +%d XP)",
			board.ClaimableCoins, domain.CurrencySymbol, board.ClaimableXP)
		claimStyle = discordgo.SuccessButton
		claimDisabled = false
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    claimLabel,
					Style:    claimStyle,
					CustomID: buttonQuestClaim + discordID,
					Disabled: claimDisabled,
				},
				discordgo.Button{
					Label:    "🔄 Обновить",
					Style:    discordgo.SecondaryButton,
					CustomID: buttonQuestRefresh + discordID,
				},
			},
		},
	}
}

func pluralQuestWord(n int) string {
	mod10 := n % 10
	mod100 := n % 100
	if mod10 == 1 && mod100 != 11 {
		return "награду"
	}
	if mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
		return "награды"
	}
	return "наград"
}

// =====================================================================
// /season (current & history / Hall of Fame)
// =====================================================================

func (b *Bot) handleSeason(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.services == nil || b.services.SeasonService == nil {
		b.respondMessage(s, i, "Система сезонов недоступна.", true)
		return
	}
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 || opts[0].Name == "current" {
		b.handleSeasonCurrent(ctx, s, i)
		return
	}
	if opts[0].Name == "history" {
		seasonNum := 0
		for _, sub := range opts[0].Options {
			if sub.Name == "number" {
				seasonNum = int(sub.IntValue())
			}
		}
		b.handleSeasonHistory(ctx, s, i, seasonNum)
		return
	}
	b.respondMessage(s, i, "Неизвестная подкоманда `/season`.", true)
}

func (b *Bot) handleSeasonCurrent(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	ov, err := b.services.SeasonService.GetCurrentSeasonOverview(ctx, 5)
	if err != nil {
		b.reportFailure(s, i, "season current", "Не удалось загрузить текущий сезон.", err)
		return
	}

	var topText strings.Builder
	if len(ov.TopPlayers) == 0 {
		topText.WriteString("Пока нет сыгранных матчей в этом сезоне.")
	} else {
		for idx, p := range ov.TopPlayers {
			tier := domain.DetermineTier(p.MMR)
			record := ""
			if p.Matches > 0 {
				record = fmt.Sprintf(" • %dW/%dL", p.Wins, p.Losses)
			}
			topText.WriteString(fmt.Sprintf("%s **%s** — **%d MMR** (%s)%s\n",
				rankBadge(idx+1), p.Name, p.MMR, domain.FormatTierShort(tier), record))
		}
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("🏆 %s (Сезон #%d)", ov.Season.Name, ov.Season.Number),
		Description: fmt.Sprintf(
			"Старт: <t:%d:D> (<t:%d:R>)\nПризовой фонд Топ-3: 🥇 **%s** • 🥈 **%s** • 🥉 **%s**",
			ov.Season.StartedAt.Unix(), ov.Season.StartedAt.Unix(),
			domain.FormatCoins(domain.SeasonRewardRank1),
			domain.FormatCoins(domain.SeasonRewardRank2),
			domain.FormatCoins(domain.SeasonRewardRank3),
		),
		Color: colorSeasonGold,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Матчей сыграно", Value: fmt.Sprintf("**%d**", ov.MatchesPlayed), Inline: true},
			{Name: "Активных игроков", Value: fmt.Sprintf("**%d**", ov.ActivePlayers), Inline: true},
			{Name: "Лидеры сезона (Топ-5)", Value: strings.TrimSpace(topText.String()), Inline: false},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "/top — полный рейтинг • /season history — Зал Славы прошлых сезонов",
		},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (b *Bot) handleSeasonHistory(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, seasonNumber int) {
	hof, err := b.services.SeasonService.GetHallOfFame(ctx, seasonNumber, 10)
	if err != nil {
		if errors.Is(err, domain.ErrSeasonNotFound) {
			if seasonNumber > 0 {
				b.respondMessage(s, i, fmt.Sprintf("Архивный сезон #%d не найден.", seasonNumber), true)
			} else {
				b.respondMessage(s, i, "Пока нет завершённых сезонов — Зал Славы появится после закрытия первого сезона.", true)
			}
			return
		}
		b.reportFailure(s, i, "season history", "Не удалось загрузить Зал Славы.", err)
		return
	}

	embed := buildHallOfFameEmbed(hof.Season, hof.Standings)
	comps := buildHallOfFameSelector(hof.ArchivedSeasons, hof.Season.Number)

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: comps,
		},
	})
}

func (b *Bot) handleSeasonHallOfFameSelect(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		return
	}
	num, err := strconv.Atoi(vals[0])
	if err != nil || num <= 0 {
		b.respondMessage(s, i, "Некорректный номер сезона.", true)
		return
	}
	hof, err := b.services.SeasonService.GetHallOfFame(ctx, num, 10)
	if err != nil {
		b.reportFailure(s, i, "season hof select", "Не удалось загрузить сезон.", err)
		return
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{buildHallOfFameEmbed(hof.Season, hof.Standings)},
			Components: buildHallOfFameSelector(hof.ArchivedSeasons, hof.Season.Number),
		},
	})
}

func buildHallOfFameEmbed(season models.Season, standings []models.SeasonStanding) *discordgo.MessageEmbed {
	period := fmt.Sprintf("<t:%d:D>", season.StartedAt.Unix())
	if season.EndedAt != nil {
		period += fmt.Sprintf(" — <t:%d:D>", season.EndedAt.Unix())
	}

	var body strings.Builder
	if len(standings) == 0 {
		body.WriteString("В этом сезоне не было квалифицированных участников.")
	} else {
		for _, st := range standings {
			tierShort := domain.FormatTierShort(domain.Tier(st.Tier))
			stats := ""
			if st.Matches > 0 {
				stats = fmt.Sprintf(" • %dW/%dL • KDA %.2f", st.Wins, st.Losses, st.KDA)
			}
			medals := ""
			if st.MVPCount > 0 || st.SVPCount > 0 {
				medals = fmt.Sprintf(" • MVP %d / SVPG %d", st.MVPCount, st.SVPCount)
			}
			reward := ""
			if st.RewardCoins > 0 {
				reward = fmt.Sprintf(" • +%s", domain.FormatCoins(st.RewardCoins))
			}
			body.WriteString(fmt.Sprintf("%s **%s** — **%d MMR** (%s)%s%s%s\n",
				rankBadge(st.Rank), st.PlayerName, st.FinalMMR, tierShort, stats, medals, reward))
		}
	}

	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("🏛️ Зал Славы — %s (Сезон #%d)", season.Name, season.Number),
		Description: fmt.Sprintf("Период: %s • Матчей: **%d** • Сброс: %s", period, season.MatchesPlayed, domain.FormatSeasonResetMode(season.MMRResetMode)),
		Color:       colorSeasonGold,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Итоговый рейтинг", Value: strings.TrimSpace(body.String()), Inline: false},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: "Valhalla Hall of Fame"},
	}
}

func buildHallOfFameSelector(archived []models.Season, selectedNum int) []discordgo.MessageComponent {
	if len(archived) <= 1 {
		return nil
	}
	limit := len(archived)
	if limit > 25 {
		limit = 25
	}
	options := make([]discordgo.SelectMenuOption, 0, limit)
	for _, s := range archived[:limit] {
		desc := fmt.Sprintf("Матчей: %d", s.MatchesPlayed)
		if s.EndedAt != nil {
			desc = fmt.Sprintf("Завершён %s • Матчей: %d", s.EndedAt.UTC().Format("02.01.2006"), s.MatchesPlayed)
		}
		options = append(options, discordgo.SelectMenuOption{
			Label:       fmt.Sprintf("#%d — %s", s.Number, s.Name),
			Value:       strconv.Itoa(s.Number),
			Description: desc,
			Default:     s.Number == selectedNum,
		})
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    selectSeasonHallOfFame,
					Placeholder: "Выберите архивный сезон...",
					Options:     options,
				},
			},
		},
	}
}

// =====================================================================
// /season_admin (close & rename)
// =====================================================================

func (b *Bot) handleSeasonAdmin(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.services == nil || b.services.SeasonService == nil {
		b.respondMessage(s, i, "Система сезонов недоступна.", true)
		return
	}
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		b.respondMessage(s, i, "Укажите подкоманду: `close` или `rename`.", true)
		return
	}

	switch opts[0].Name {
	case "rename":
		newName := opts[0].Options[0].StringValue()
		updated, err := b.services.SeasonService.RenameActiveSeason(ctx, newName)
		if err != nil {
			b.reportFailure(s, i, "season_admin rename", "Некорректное название сезона (1–60 символов).", err)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("✅ Текущий сезон #%d переименован в **«%s»**.", updated.Number, updated.Name), false)

	case "close":
		nextName := ""
		resetMode := domain.SeasonResetModeFull
		for _, sub := range opts[0].Options {
			switch sub.Name {
			case "next_name":
				nextName = strings.TrimSpace(sub.StringValue())
			case "reset_mode":
				resetMode = sub.StringValue()
			}
		}
		if nextName != "" {
			if _, err := domain.ValidateSeasonName(nextName); err != nil {
				b.respondMessage(s, i, "Название следующего сезона должно быть от 1 до 60 символов.", true)
				return
			}
		}

		ov, err := b.services.SeasonService.GetCurrentSeasonOverview(ctx, 3)
		if err != nil {
			b.reportFailure(s, i, "season_admin close", "Не удалось загрузить текущий сезон.", err)
			return
		}

		member := interactionMember(i)
		token := b.seasonCloses.put(member.User.ID, nextName, resetMode)

		nextDisplay := nextName
		if nextDisplay == "" {
			nextDisplay = fmt.Sprintf("Сезон %d", ov.Season.Number+1)
		}

		var podiumPreview strings.Builder
		if len(ov.TopPlayers) == 0 {
			podiumPreview.WriteString("• Квалифицированных игроков нет (призы не начислятся)")
		} else {
			for idx, p := range ov.TopPlayers {
				rank := idx + 1
				podiumPreview.WriteString(fmt.Sprintf("%s **%s** (%d MMR) → **+%s**\n",
					rankBadge(rank), p.Name, p.MMR, domain.FormatCoins(domain.SeasonPodiumReward(rank))))
			}
		}

		msg := fmt.Sprintf(
			"⚠️ **Подтвердите закрытие сезона #%d «%s»**\n\n"+
				"• **Следующий сезон:** %s\n"+
				"• **Сброс MMR:** %s\n"+
				"• **Матчей в сезоне:** %d • **Участников в архиве:** %d\n\n"+
				"**Подиум и награды Топ-3:**\n%s\n"+
				"Сезонная статистика (`/top`, `/profile`) обнулится, Зал Славы сохранится навсегда, а ранговые роли синхронизируются.",
			ov.Season.Number, ov.Season.Name,
			nextDisplay,
			domain.FormatSeasonResetMode(resetMode),
			ov.MatchesPlayed, ov.ActivePlayers,
			strings.TrimSpace(podiumPreview.String()),
		)

		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: msg,
				Flags:   discordgo.MessageFlagsEphemeral,
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.Button{
								Label:    "✅ Подтвердить закрытие сезона",
								Style:    discordgo.DangerButton,
								CustomID: buttonSeasonCloseConfirm + token,
							},
							discordgo.Button{
								Label:    "Отмена",
								Style:    discordgo.SecondaryButton,
								CustomID: buttonSeasonCloseCancel + token,
							},
						},
					},
				},
			},
		})
	}
}

func (b *Bot) handleSeasonCloseCancel(s *discordgo.Session, i *discordgo.Interaction, token string) {
	b.seasonCloses.pop(token)
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "Закрытие сезона отменено.",
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) handleSeasonCloseConfirm(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, token string) {
	member := interactionMember(i)
	if member == nil || member.User == nil || !b.isAdmin(member.User.ID) {
		b.respondMessage(s, i, "Только админ может закрыть сезон.", true)
		return
	}
	req, ok := b.seasonCloses.pop(token)
	if !ok {
		b.respond(s, i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "Запрос на закрытие сезона истёк или уже был обработан. Запустите `/season_admin close` снова.",
				Components: []discordgo.MessageComponent{},
			},
		})
		return
	}
	if req.adminID != member.User.ID {
		b.respondMessage(s, i, "Подтвердить закрытие может только админ, который его запустил.", true)
		return
	}

	res, err := b.services.SeasonService.CloseCurrentSeason(ctx, req.nextName, req.resetMode, member.User.ID)
	if err != nil {
		b.reportFailure(s, i, "season_admin close confirm", "Не удалось закрыть сезон.", err)
		return
	}

	// Re-sync Discord tier roles in the background for every player whose MMR changed.
	if len(res.NewMMRs) > 0 && len(b.tierRoleIDs) > 0 {
		guildID := b.guildID(i.GuildID)
		newMMRs := res.NewMMRs
		b.goBackground("season-tier-sync", backgroundTimeout, func(bg context.Context) {
			b.syncTierRoles(bg, s, guildID, newMMRs)
		})
	}

	// Announce the archived Hall of Fame in the channel.
	announceChannel := i.ChannelID
	if announceChannel != "" {
		topStandings := res.Standings
		if len(topStandings) > 10 {
			topStandings = topStandings[:10]
		}
		hofEmbed := buildHallOfFameEmbed(res.ClosedSeason, topStandings)
		hofEmbed.Title = fmt.Sprintf("🏁 Сезон #%d «%s» завершён! Стартовал «%s»",
			res.ClosedSeason.Number, res.ClosedSeason.Name, res.NextSeason.Name)
		if _, sendErr := s.ChannelMessageSendEmbed(announceChannel, hofEmbed); sendErr != nil {
			b.logger.Warn("season: failed to post Hall of Fame announcement: %v", sendErr)
		}
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf(
				"✅ Сезон **#%d «%s»** закрыт и сохранён в Зал Славы!\n"+
					"• В архиве игроков: **%d** • Выдано призов: **%s**\n"+
					"• Сброшен MMR у **%d** игроков (%s)\n"+
					"• Открыт новый сезон: **#%d «%s»**",
				res.ClosedSeason.Number, res.ClosedSeason.Name,
				len(res.Standings), domain.FormatCoins(res.TotalRewardCoins),
				res.MMRResetCount, domain.FormatSeasonResetMode(res.ClosedSeason.MMRResetMode),
				res.NextSeason.Number, res.NextSeason.Name,
			),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func rankBadge(rank int) string {
	switch rank {
	case 1:
		return "🥇"
	case 2:
		return "🥈"
	case 3:
		return "🥉"
	default:
		return fmt.Sprintf("`#%d`", rank)
	}
}

// appendSeasonTrophiesField adds a compact trophy showcase to /profile for players
// who placed Top-3 in archived seasons.
func (b *Bot) appendSeasonTrophiesField(ctx context.Context, embed *discordgo.MessageEmbed, playerID int) {
	if b.services == nil || b.services.SeasonService == nil {
		return
	}
	trophies, err := b.services.SeasonService.GetPlayerTrophies(ctx, playerID)
	if err != nil {
		b.logger.Warn("profile: failed to load season trophies for player %d: %v", playerID, err)
		return
	}
	if len(trophies) == 0 {
		return
	}

	parts := make([]string, 0, len(trophies))
	for _, tr := range trophies {
		parts = append(parts, fmt.Sprintf("%s **S%d** (%d MMR)", rankBadge(tr.Rank), tr.SeasonNumber, tr.FinalMMR))
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "🏆 Трофеи сезонов",
		Value:  strings.Join(parts, " • "),
		Inline: false,
	})
}
