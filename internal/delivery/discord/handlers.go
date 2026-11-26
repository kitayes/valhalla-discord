package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleTop(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	sortBy := "mmr"
	options := i.ApplicationCommandData().Options
	if len(options) > 0 {
		sortBy = options[0].StringValue()
	}

	stats, err := b.services.MatchService.GetLeaderboard(ctx, sortBy)
	if err != nil {
		b.reportFailure(s, i, "top", "Не удалось получить лидерборд. Попробуйте позже.", err)
		return
	}

	players, pErr := b.services.MatchService.GetPlayerList(ctx)
	if pErr != nil {
		b.logger.Warn("top: failed to load players for MMR enrichment: %v", pErr)
	}

	mmrByID := make(map[int]int, len(players))
	for _, pl := range players {
		mmrByID[pl.ID] = pl.CurrentMMR
	}

	var sb strings.Builder
	title := "Таблица лидеров (по MMR)"

	if sortBy == "mmr" {
		if len(players) == 0 {
			b.respondMessage(s, i, "Игроков пока нет. Зарегистрируйтесь через `/bind`!", false)
			return
		}
		statsByID := make(map[int]*application.PlayerStats, len(stats))
		for _, st := range stats {
			statsByID[st.ID] = st
		}

		sort.Slice(players, func(a, c int) bool {
			if players[a].CurrentMMR != players[c].CurrentMMR {
				return players[a].CurrentMMR > players[c].CurrentMMR
			}
			ma, mc := 0, 0
			if st := statsByID[players[a].ID]; st != nil {
				ma = st.Matches
			}
			if st := statsByID[players[c].ID]; st != nil {
				mc = st.Matches
			}
			if ma != mc {
				return ma > mc
			}
			return players[a].ID < players[c].ID
		})

		topCount := topPlayersLimit
		if len(players) < topCount {
			topCount = len(players)
		}
		for idx, pl := range players[:topCount] {
			medal := getMedalEmoji(idx)
			tier := domain.DetermineTier(pl.CurrentMMR)
			fmt.Fprintf(&sb, "%s **%s** — **%d MMR** (%s)", medal, pl.Name, pl.CurrentMMR, tier)
			if st := statsByID[pl.ID]; st != nil && st.Matches > 0 {
				wr := calculateWinRate(st)
				kda := calculateKDA(st.Kills, st.Deaths, st.Assists)
				fmt.Fprintf(&sb, " | WR: `%.0f%%` (%dW/%dL) | KDA: `%.2f`", wr, st.Wins, st.Losses, kda)
				if st.MVP > 0 {
					fmt.Fprintf(&sb, " [MVP×%d]", st.MVP)
				}
			}
			sb.WriteByte('\n')
		}
	} else {
		if len(stats) == 0 {
			b.respondMessage(s, i, "Сезонной статистики по скриншотам пока нет. Посмотрите рейтинг через `/top sort:По MMR (Рейтинг)`!", false)
			return
		}
		if sortBy == "winrate" {
			title = "Таблица лидеров (по Винрейту)"
		} else {
			title = "Таблица лидеров (по KDA)"
		}
		topCount := topPlayersLimit
		if len(stats) < topCount {
			topCount = len(stats)
		}
		for idx, p := range stats[:topCount] {
			medal := getMedalEmoji(idx)
			wr := calculateWinRate(p)
			kda := calculateKDA(p.Kills, p.Deaths, p.Assists)
			mmrPart := ""
			if mmr, ok := mmrByID[p.ID]; ok {
				mmrPart = fmt.Sprintf(" | `%d MMR`", mmr)
			}
			fmt.Fprintf(&sb, "%s **%s** — WR: `%.0f%%` | KDA: `%.2f` (%d игр)%s",
				medal, p.Name, wr, kda, p.Matches, mmrPart)
			if p.MVP > 0 {
				fmt.Fprintf(&sb, " [MVP×%d]", p.MVP)
			}
			sb.WriteByte('\n')
		}
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: sb.String(),
		Color:       colorGold,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Valhalla Ranked Season • /rich — топ по ValCoins и уровню XP"},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

// resolveTargetPlayer works out whose profile a command is asking about.
//
// Nobody knows their own numeric id, so the id argument is gone: with no
// arguments the command answers about the caller, resolved through the Discord
// binding. A nickname still works for the many profiles that came out of
// screenshots and have nobody bound to them.
//
// Answers the interaction and reports false when the target cannot be resolved.
func (b *Bot) resolveTargetPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) (int, string, bool) {
	data := i.ApplicationCommandData()

	if opt, ok := commandOption(data, "nickname"); ok {
		nickname := strings.TrimSpace(opt.StringValue())
		id, name, err := b.services.MatchService.FindPlayerByName(ctx, nickname)
		if err != nil {
			b.respondMessage(s, i, fmt.Sprintf("Игрок **%s** не найден.", nickname), true)
			return 0, "", false
		}
		return id, name, true
	}

	if opt, ok := commandOption(data, "user"); ok {
		target := opt.UserValue(s)
		if target == nil {
			b.respondMessage(s, i, "Не удалось определить пользователя.", true)
			return 0, "", false
		}
		id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, target.ID)
		if err != nil {
			b.respondMessage(s, i, fmt.Sprintf("У <@%s> нет привязанного профиля.", target.ID), true)
			return 0, "", false
		}
		return id, name, true
	}

	return b.callerProfile(s, i)
}

// commandOption finds a named option, which is how optional arguments have to
// be read: Discord sends only the options the user actually filled in, so
// indexing into the slice by position lands on the wrong one.
func commandOption(data discordgo.ApplicationCommandInteractionData, name string) (*discordgo.ApplicationCommandInteractionDataOption, bool) {
	for _, opt := range data.Options {
		if opt.Name == name {
			return opt, true
		}
	}
	return nil, false
}

func (b *Bot) handleProfile(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id, _, ok := b.resolveTargetPlayer(ctx, s, i)
	if !ok {
		return
	}

	p, err := b.services.MatchService.GetPlayerStatsByID(ctx, id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	wr := calculateWinRate(p)
	kda := calculateKDA(p.Kills, p.Deaths, p.Assists)
	color := colorGold
	if p.Matches > 0 {
		color = getColorByWinRate(wr)
	}

	embed := &discordgo.MessageEmbed{
		Title:  fmt.Sprintf("Профиль: %s (ID: %d)", p.Name, id),
		Color:  color,
		Footer: &discordgo.MessageEmbedFooter{Text: "/wallet — история монет • /history — история матчей • /shop — кастомизация"},
	}

	// 1. Ranked MMR & Tier right at the top.
	b.appendMMRHeaderField(ctx, embed, id)

	// 2. Seasonal match & KDA summary (compact + per-game averages).
	matchesVal := "Нет игр по скриншотам"
	kdaVal := "—"
	if p.Matches > 0 {
		matchesVal = fmt.Sprintf("**%d** (`%dW / %dL` • **%.1f%%**)", p.Matches, p.Wins, p.Losses, wr)
		avgK := float64(p.Kills) / float64(p.Matches)
		avgD := float64(p.Deaths) / float64(p.Matches)
		avgA := float64(p.Assists) / float64(p.Matches)
		kdaVal = fmt.Sprintf("**%.2f** (`%.1f / %.1f / %.1f` ср.)", kda, avgK, avgD, avgA)
	}

	medalsVal := fmt.Sprintf("MVP: **%d** | SVPG: **%d**", p.MVP, p.SVP)
	if lifetime, lErr := b.services.MatchService.GetLifetimeMedals(ctx, id); lErr != nil {
		b.logger.Warn("profile: failed to get lifetime medals for player %d: %v", id, lErr)
	} else if lifetime.MVP > p.MVP || lifetime.SVP > p.SVP {
		medalsVal += fmt.Sprintf("\n*(за всё время: %d / %d)*", lifetime.MVP, lifetime.SVP)
	}

	embed.Fields = append(embed.Fields,
		&discordgo.MessageEmbedField{Name: "Матчи (сезон)", Value: matchesVal, Inline: true},
		&discordgo.MessageEmbedField{Name: "KDA (сезон)", Value: kdaVal, Inline: true},
		&discordgo.MessageEmbedField{Name: "Медали", Value: medalsVal, Inline: true},
	)

	// 3. Economy, activity XP/level, custom title & frame color.
	b.appendEconomyFields(ctx, embed, id)

	// 4. Hall of Fame podium trophies from archived seasons.
	b.appendSeasonTrophiesField(ctx, embed, id)

	// 5. Recent MMR movement (last 5 matches with base + KDA + medal breakdown).
	b.appendMMRHistoryField(ctx, embed, id)

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

// mmrHistoryInProfile is how many recent rating changes the profile shows.
const mmrHistoryInProfile = 5

// appendEconomyFields applies custom profile title/color and adds ValCoins and XP level.
func (b *Bot) appendEconomyFields(ctx context.Context, embed *discordgo.MessageEmbed, playerID int) {
	if b.services == nil || b.services.EconomyService == nil {
		return
	}
	econ, err := b.services.EconomyService.GetPlayerEconomy(ctx, playerID)
	if err != nil {
		b.logger.Warn("profile: failed to read economy for player %d: %v", playerID, err)
		return
	}
	if econ.ProfileTitle != "" {
		embed.Description = fmt.Sprintf("🏷️ **«%s»**", econ.ProfileTitle)
	}
	if econ.ProfileColor > 0 {
		embed.Color = econ.ProfileColor
	}

	lvl, curXP, needXP := domain.LevelFromXP(econ.XP)
	bar := domain.FormatProgressBar(curXP, needXP, 10)
	embed.Fields = append(embed.Fields,
		&discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("Кошелёк (%s)", domain.CurrencyName),
			Value:  fmt.Sprintf("**%s** • 🛡️ Страховок: %d", domain.FormatCoins(econ.Points), econ.BetInsuranceCount),
			Inline: true,
		},
		&discordgo.MessageEmbedField{
			Name:   "Уровень и актив",
			Value:  fmt.Sprintf("**%d ур.** (`%s`)\n💬 %d • 🎙️ %s", lvl, bar, econ.MessagesCount, domain.FormatVoiceDuration(econ.VoiceSeconds)),
			Inline: true,
		},
	)
}

func (b *Bot) appendMMRHeaderField(ctx context.Context, embed *discordgo.MessageEmbed, playerID int) {
	mmrs, err := b.services.Lobby.GetPlayerMMRsBatch(ctx, []int{playerID})
	if err != nil {
		b.logger.Warn("profile: failed to read MMR for player %d: %v", playerID, err)
		return
	}
	mmr, ok := mmrs[playerID]
	if !ok {
		return
	}
	tier := domain.DetermineTier(mmr)
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Рейтинг",
		Value:  fmt.Sprintf("**%d MMR** — %s", mmr, domain.FormatTierDisplay(tier)),
		Inline: false,
	})
}

func (b *Bot) appendMMRHistoryField(ctx context.Context, embed *discordgo.MessageEmbed, playerID int) {
	history, err := b.services.Lobby.GetMMRHistory(ctx, playerID, mmrHistoryInProfile)
	if err != nil {
		b.logger.Warn("profile: failed to read MMR history for player %d: %v", playerID, err)
		return
	}
	if len(history) == 0 {
		return
	}

	var sb strings.Builder
	for _, c := range history {
		sb.WriteString(formatMMRHistoryLine(c))
		sb.WriteByte('\n')
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   fmt.Sprintf("Последние изменения MMR (%d)", len(history)),
		Value:  sb.String(),
		Inline: false,
	})
}

func formatMMRHistoryLine(c models.MMRChange) string {
	var details []string
	if c.KDAMod != 0 || c.MedalBonus != 0 {
		var parts []string
		if c.BaseDelta != 0 {
			parts = append(parts, fmt.Sprintf("%+d база", c.BaseDelta))
		}
		if c.KDAMod != 0 {
			parts = append(parts, fmt.Sprintf("%+d KDA", c.KDAMod))
		}
		if c.MedalBonus != 0 {
			parts = append(parts, fmt.Sprintf("%+d медаль", c.MedalBonus))
		}
		if len(parts) > 0 {
			details = append(details, strings.Join(parts, " "))
		}
	}
	if c.MatchID > 0 {
		details = append(details, fmt.Sprintf("матч #%d", c.MatchID))
	}

	line := fmt.Sprintf("`%+d` → %d", c.Delta, c.After)
	if len(details) > 0 {
		line += fmt.Sprintf(" (%s)", strings.Join(details, ", "))
	}
	return fmt.Sprintf("%s — %s", line, c.CreatedAt.Format("02.01"))
}

func (b *Bot) handlePlayersList(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	players, err := b.services.MatchService.GetPlayerList(ctx)
	if err != nil {
		b.reportFailure(s, i, "players", "Не удалось получить список игроков. Попробуйте позже.", err)
		return
	}

	var sb strings.Builder
	sb.WriteString("Список зарегистрированных игроков:\n\n")
	for _, p := range players {
		sb.WriteString(fmt.Sprintf("`[%d]` **%s**\n", p.ID, p.Name))
	}

	b.respondMessage(s, i, truncateMessage(sb.String()), false)
}

func (b *Bot) handleHistory(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id, name, ok := b.resolveTargetPlayer(ctx, s, i)
	if !ok {
		return
	}

	lines, err := b.services.MatchService.GetHistoryByID(ctx, id)
	if err != nil {
		b.reportFailure(s, i, "history", "Не удалось получить историю матчей.", err)
		return
	}

	if len(lines) == 0 {
		b.respondMessage(s, i, fmt.Sprintf("У игрока **%s** нет истории матчей.", name), false)
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("История матчей: %s", name),
		Description: strings.Join(lines, "\n"),
		Color:       colorBlue,
		Footer:      &discordgo.MessageEmbedFooter{Text: "ID Матча | Результат | K/D/A | Дата"},
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

// =====================================================================
// DANGEROUS COMMANDS with confirmation buttons
// =====================================================================

func (b *Bot) handleWipePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())
	name, _ := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if name == "" {
		name = fmt.Sprintf("ID:%d", id)
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("**Вы уверены, что хотите удалить игрока %s (ID: %d)?**\n\nЭто действие необратимо!", name, id),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Да, удалить",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("confirm_wipe_player_%d", id),
						},
						discordgo.Button{
							Label:    "Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_wipe_player",
						},
					},
				},
			},
		},
	})
}

func (b *Bot) handleResetPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	id := int(options[0].IntValue())
	name, _ := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if name == "" {
		name = fmt.Sprintf("ID:%d", id)
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("**Сбросить сезонную статистику игрока %s (ID: %d)?**", name, id),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Да, сбросить",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("confirm_reset_player_%d", id),
						},
						discordgo.Button{
							Label:    "Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_reset_player",
						},
					},
				},
			},
		},
	})
}

func (b *Bot) handleWipe(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "**Вы уверены, что хотите полностью очистить базу данных? Это действие необратимо!**\n\nВсе матчи, игроки и статистика будут удалены.",
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Да, очистить всё",
							Style:    discordgo.DangerButton,
							CustomID: "confirm_wipe",
						},
						discordgo.Button{
							Label:    "Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_wipe",
						},
					},
				},
			},
		},
	})
}

func (b *Bot) handleReset(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "**Сбросить сезонную статистику всех игроков?**",
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Да, сбросить сезон",
							Style:    discordgo.DangerButton,
							CustomID: "confirm_reset",
						},
						discordgo.Button{
							Label:    "Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_reset",
						},
					},
				},
			},
		},
	})
}

// =====================================================================
// Admin commands (safe, no confirmation needed or already deferred)
// =====================================================================

func (b *Bot) handleExport(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, false)

	data, err := b.services.MatchService.GetExcelReport(ctx)
	if err != nil {
		b.reportFailureEdit(s, i, "export", "Не удалось сформировать отчёт.", err)
		return
	}

	content := "Ваш отчет готов!"
	b.editResponse(s, i, &discordgo.WebhookEdit{
		Content: &content,
		Files: []*discordgo.File{
			{Name: "статистика.xlsx", Reader: bytes.NewReader(data)},
		},
	})
}

func (b *Bot) handleSetTimer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	options := i.ApplicationCommandData().Options
	dateStr := options[0].StringValue()

	err := b.services.MatchService.SetTimer(ctx, dateStr)
	if err != nil {
		b.reportFailure(s, i, "set_timer", "Не удалось установить дату начала сезона. Проверьте формат.", err)
	} else {
		b.respondMessage(s, i, fmt.Sprintf("Дата начала сезона установлена: %s", dateStr), false)
	}
}

func (b *Bot) handleSyncSheet(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, false)

	url, err := b.services.MatchService.SyncToGoogleSheet(ctx)
	if err != nil {
		b.reportFailureEdit(s, i, "sync_sheet", "Не удалось синхронизировать таблицу.", err)
		return
	}

	b.editContent(s, i, fmt.Sprintf("Таблица успешно обновлена!\nСсылка: %s", url))
}

func (b *Bot) handleDeleteMatch(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	id := i.ApplicationCommandData().Options[0].IntValue()

	err := b.services.MatchService.DeleteMatch(ctx, int(id))
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Ошибка удаления: %v", err), true)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Матч #%d успешно удален из базы.", id), false)
}

func (b *Bot) handleRenamePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	id := int(opts[0].IntValue())
	newName := opts[1].StringValue()

	oldName, err := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	err = b.services.MatchService.RenamePlayer(ctx, id, newName)
	if err != nil {
		b.reportFailure(s, i, "rename_player", "Не удалось переименовать игрока.", err)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Игрок переименован:\n**%s** → **%s**", oldName, newName), false)
}

func (b *Bot) handleAliases(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if !b.requireReferee(s, i) {
		return
	}

	data := i.ApplicationCommandData()
	idOpt, ok := commandOption(data, "id")
	if !ok {
		b.respondMessage(s, i, "Укажите ID игрока.", true)
		return
	}
	playerID := int(idOpt.IntValue())
	if playerID <= 0 {
		b.respondMessage(s, i, "Некорректный ID игрока.", true)
		return
	}

	playerName, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", playerID), true)
		return
	}

	action := "list"
	if actOpt, ok := commandOption(data, "action"); ok {
		action = strings.ToLower(strings.TrimSpace(actOpt.StringValue()))
	}
	aliasArg := ""
	if alOpt, ok := commandOption(data, "alias"); ok {
		aliasArg = strings.TrimSpace(alOpt.StringValue())
	}

	switch action {
	case "add":
		if aliasArg == "" {
			b.respondMessage(s, i, "Укажите параметр `alias` для добавления.", true)
			return
		}
		if err := b.services.MatchService.AddPlayerAlias(ctx, playerID, aliasArg); err != nil {
			b.respondMessage(s, i, fmt.Sprintf("Не удалось добавить алиас: %v", err), true)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("Алиас **%s** добавлен игроку **%s** (ID: %d).", aliasArg, playerName, playerID), true)
		return

	case "delete":
		if aliasArg == "" {
			b.respondMessage(s, i, "Укажите параметр `alias` для удаления.", true)
			return
		}
		deleted, err := b.services.MatchService.DeletePlayerAlias(ctx, playerID, aliasArg)
		if err != nil {
			b.reportFailure(s, i, "aliases delete", "Не удалось удалить алиас.", err)
			return
		}
		if !deleted {
			b.respondMessage(s, i, fmt.Sprintf("Алиас **%s** не найден у игрока **%s** (ID: %d).", aliasArg, playerName, playerID), true)
			return
		}
		b.respondMessage(s, i, fmt.Sprintf("Алиас **%s** удалён у игрока **%s** (ID: %d).", aliasArg, playerName, playerID), true)
		return
	}

	aliases, err := b.services.MatchService.ListPlayerAliases(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "aliases list", "Не удалось получить список алиасов.", err)
		return
	}
	if len(aliases) == 0 {
		b.respondMessage(s, i, fmt.Sprintf("У игрока **%s** (ID: %d) нет сохранённых алиасов.", playerName, playerID), true)
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Алиасы игрока **%s** (ID: %d):\n", playerName, playerID))
	for idx, a := range aliases {
		sb.WriteString(fmt.Sprintf("%d. `%s` *(источник: %s, %s)*\n", idx+1, a.Alias, a.Source, a.CreatedAt.Format("02.01.2006")))
	}
	b.respondMessage(s, i, truncateMessage(sb.String()), true)
}

func (b *Bot) handleMergePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	data := i.ApplicationCommandData()
	srcOpt, okSrc := commandOption(data, "source_id")
	dstOpt, okDst := commandOption(data, "target_id")
	if !okSrc || !okDst {
		b.respondMessage(s, i, "Укажите оба ID: `source_id` и `target_id`.", true)
		return
	}
	sourceID := int(srcOpt.IntValue())
	targetID := int(dstOpt.IntValue())
	if sourceID <= 0 || targetID <= 0 || sourceID == targetID {
		b.respondMessage(s, i, "Укажите два разных корректных ID игроков.", true)
		return
	}

	sourceName, err := b.services.MatchService.GetPlayerNameByID(ctx, sourceID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Исходный игрок с ID %d не найден.", sourceID), true)
		return
	}
	targetName, err := b.services.MatchService.GetPlayerNameByID(ctx, targetID)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Целевой игрок с ID %d не найден.", targetID), true)
		return
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf(
				"**Объединить профили?**\nДубль: **%s** (ID: `%d`) → Основной: **%s** (ID: `%d`)\n\n"+
					"Все матчи, медали, история MMR и алиасы перейдут к **%s**, старый ник сохранится как алиас, а дубль `#%d` будет помечен удалённым.",
				sourceName, sourceID, targetName, targetID, targetName, sourceID,
			),
			Flags: discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Да, объединить",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("confirm_merge_player_%d_%d", sourceID, targetID),
						},
						discordgo.Button{
							Label:    "Отмена",
							Style:    discordgo.SuccessButton,
							CustomID: "cancel_merge_player",
						},
					},
				},
			},
		},
	})
}

func (b *Bot) handleUpdateNick(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	id := int(opts[0].IntValue())
	newName := opts[1].StringValue()

	oldName, err := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if err != nil {
		b.respondMessage(s, i, fmt.Sprintf("Игрок с ID %d не найден.", id), true)
		return
	}

	b.deferResponse(s, i, false)

	err = b.services.MatchService.RenamePlayer(ctx, id, newName)
	if err != nil {
		b.reportFailureEdit(s, i, "rename_player", "Не удалось переименовать игрока.", err)
		return
	}

	b.goBackground("sync-sheet-after-rename", backgroundTimeout, func(bg context.Context) {
		if _, err := b.services.MatchService.SyncToGoogleSheet(bg); err != nil {
			b.logger.Error("update_nick: failed to sync sheet: %v", err)
		}
	})

	b.editContent(s, i, fmt.Sprintf("Никнейм обновлён!\n**%s** → **%s**\n\nКаскадное переименование: все прошлые матчи, база данных и Google Таблицы обновлены.", oldName, newName))
}

func (b *Bot) handleScreenshots(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate) {
	var imageAttachments []*discordgo.MessageAttachment
	for _, att := range m.Attachments {
		filename := strings.ToLower(att.Filename)
		if strings.HasSuffix(filename, ".png") ||
			strings.HasSuffix(filename, ".jpg") ||
			strings.HasSuffix(filename, ".jpeg") ||
			strings.HasSuffix(filename, ".webp") {
			imageAttachments = append(imageAttachments, att)
		}
	}

	if len(imageAttachments) == 0 {
		return
	}

	// One token per attachment, and the whole batch is refused together: taking
	// them one at a time left the tokens already consumed spent on a batch that
	// was then dropped, so the user paid for work that never ran.
	userID := m.Author.ID
	taken := 0
	for range imageAttachments {
		if err := b.rateLimiter.Allow(userID, uploadBurstPerUser, uploadRefillPerUser); err != nil {
			b.rateLimiter.Refund(userID, taken)
			b.logger.Warn("Rate limit exceeded for user %s (%d screenshot(s) requested)", userID, len(imageAttachments))
			b.sendChannelMessage(s, m.ChannelID,
				"Слишком много запросов. Пожалуйста, подождите немного перед следующей загрузкой.")
			return
		}
		taken++
	}

	lobbyMatch, lobbyErr := b.services.Lobby.GetMatchByThreadID(ctx, m.ChannelID)
	if lobbyErr != nil && !errors.Is(lobbyErr, domain.ErrMatchNotFound) {
		b.logger.Warn("discord: failed to resolve lobby match for thread %s: %v", m.ChannelID, lobbyErr)
	}
	if lobbyMatch != nil && lobbyMatch.Status == models.LobbyMatchStatusActive {
		b.handleLobbyMatchScreenshot(ctx, s, m, lobbyMatch, imageAttachments)
		return
	}

	b.startTyping(s, m.ChannelID)
	msg := b.sendChannelMessage(s, m.ChannelID,
		fmt.Sprintf("Анализирую %d скриншот(ов)...", len(imageAttachments)))

	expectedPlayers, _ := b.getThreadPlayers(m.ChannelID)

	type result struct {
		parsed *models.MatchResult
		err    error
		index  int
	}

	results := make([]result, len(imageAttachments))
	semaphore := make(chan struct{}, 3)

	var wg sync.WaitGroup
	for i, att := range imageAttachments {
		wg.Add(1)
		go func(idx int, attachment *discordgo.MessageAttachment) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			parsed, err := b.services.MatchService.ProcessImageFromURLWithPlayers(ctx, attachment.URL, expectedPlayers)
			results[idx] = result{parsed: parsed, err: err, index: idx}
		}(i, att)
	}
	wg.Wait()

	if msg != nil {
		if err := s.ChannelMessageDelete(m.ChannelID, msg.ID); err != nil {
			b.logger.Warn("discord: failed to delete progress message in %s: %v", m.ChannelID, err)
		}
	}

	var successCount, duplicateCount, errorCount int
	var messages []string

	for _, res := range results {
		if res.err != nil {
			// Matched on the sentinel, not on the text of the message: the
			// substring check silently stopped counting duplicates the moment
			// anyone reworded the error.
			if errors.Is(res.err, domain.ErrDuplicateMatch) {
				duplicateCount++
			} else {
				errorCount++
				messages = append(messages,
					fmt.Sprintf("[Ошибка] Скриншот %d: %v", res.index+1, res.err))
			}
			continue
		}

		if res.parsed == nil {
			errorCount++
			messages = append(messages,
				fmt.Sprintf("[Ошибка] Скриншот %d: пустой результат распознавания", res.index+1))
			continue
		}

		successCount++
		line := fmt.Sprintf("[Успех] Скриншот %d: Матч #%d записан", res.index+1, res.parsed.MatchID)
		if medals := formatMedals(res.parsed); medals != "" {
			line += " " + medals
		}
		messages = append(messages, line)

		b.attachMedalsToLobbyMatch(ctx, m.ChannelID, res.parsed)
	}

	summary := fmt.Sprintf("**Обработано: %d скриншотов**\nУспешно: %d\nДубликаты: %d\nОшибки: %d",
		len(imageAttachments), successCount, duplicateCount, errorCount)

	if len(messages) > 0 {
		summary += "\n\n" + strings.Join(messages, "\n")
	}

	s.ChannelMessageSend(m.ChannelID, summary)

	// Archive the thread if screenshots were processed in a known match thread
	if len(expectedPlayers) > 0 {
		b.archiveThreadIfMatchFinished(ctx, s, m.ChannelID)
	}
}

// attachMedalsToLobbyMatch records the medals recognised on a screenshot onto
// the lobby match the thread belongs to, so the referee closing the match awards
// the MVP/SVPG bonus. Screenshots posted outside a match thread only count
// towards the global medal tallies.
func (b *Bot) attachMedalsToLobbyMatch(ctx context.Context, channelID string, parsed *models.MatchResult) {
	if parsed == nil || (parsed.MVP == "" && parsed.SVP == "") {
		return
	}

	match, err := b.services.Lobby.GetMatchByThreadID(ctx, channelID)
	if err != nil || match == nil {
		return // not a match thread — nothing to attach to
	}

	if err := b.services.Lobby.SaveMedals(ctx, match.ID, parsed.MVP, parsed.SVP); err != nil {
		b.logger.Error("match: failed to save medals for #%d: %v", match.ID, err)
		return
	}
	b.logger.Info("match: medals recorded for #%d — MVP: %q, SVP: %q", match.ID, parsed.MVP, parsed.SVP)
}

// formatMedals renders the recognised medals for the upload summary.
func formatMedals(parsed *models.MatchResult) string {
	var parts []string
	if parsed.MVP != "" {
		parts = append(parts, fmt.Sprintf("[MVP] **%s**", parsed.MVP))
	}
	if parsed.SVP != "" {
		parts = append(parts, fmt.Sprintf("[SVPG] **%s**", parsed.SVP))
	}
	return strings.Join(parts, " | ")
}

// callerProfile resolves the player the caller has claimed with /bind.
//
// It answers the interaction and reports false when there is no binding, so the
// commands that act on "your profile" have exactly one place that decides whose
// profile that is.
func (b *Bot) callerProfile(s *discordgo.Session, i *discordgo.Interaction) (int, string, bool) {
	member := interactionMember(i)
	if member == nil {
		b.respondMessage(s, i, "Эта команда работает только на сервере.", true)
		return 0, "", false
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, member.User.ID)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			b.respond(s, i, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content:    "Ваш Discord не привязан к профилю.\n\nНажмите кнопку и укажите свой ник в игре.",
					Components: bindPromptComponents(),
					Flags:      discordgo.MessageFlagsEphemeral,
				},
			})
			return 0, "", false
		}
		b.reportFailure(s, i, "caller profile", "Не удалось определить ваш профиль. Попробуйте позже.", err)
		return 0, "", false
	}
	return id, name, true
}

// =====================================================================
// FAQ Commands
// =====================================================================

func (b *Bot) handleFAQ(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.services.FAQService == nil {
		b.respondMessage(s, i, "FAQ-сервис не настроен. Укажите DEEPSEEK_KEY в .env", true)
		return
	}
	question := i.ApplicationCommandData().Options[0].StringValue()
	if len(strings.TrimSpace(question)) < 3 {
		b.respondMessage(s, i, "Задайте вопрос длиной хотя бы 3 символа.", true)
		return
	}

	b.deferResponse(s, i, false)

	answer := b.services.FAQService.AnswerQuestion(ctx, question)
	b.editContent(s, i, answer)
}

func (b *Bot) handleFAQReload(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if b.services.FAQService == nil {
		b.respondMessage(s, i, "FAQ-сервис не настроен.", true)
		return
	}
	if err := b.services.FAQService.ReloadFAQ(); err != nil {
		b.reportFailure(s, i, "faq_reload", "Не удалось перезагрузить базу знаний FAQ.", err)
		return
	}
	b.respondMessage(s, i, "База знаний FAQ перезагружена.", true)
}

// handleFAQAutoAnswer responds to plain text messages in the FAQ channel automatically.
//
// Every answer is a paid DeepSeek call, so it is rate limited per user: without
// it, anyone could run up the API bill just by typing in the channel.
func (b *Bot) handleFAQAutoAnswer(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.services.FAQService == nil {
		return
	}
	if strings.HasPrefix(m.Content, "/") || strings.HasPrefix(m.Content, "!") {
		return
	}

	if err := b.faqRateLimiter.AllowRate(m.Author.ID, faqBurstPerUser, faqRefillPerUser); err != nil {
		b.logger.Warn("faq: rate limit exceeded for user %s", m.Author.ID)
		return
	}

	b.startTyping(s, m.ChannelID)
	answer := b.services.FAQService.AnswerQuestion(ctx, m.Content)
	if _, err := s.ChannelMessageSend(m.ChannelID, truncateMessage(fmt.Sprintf("<@%s>, %s", m.Author.ID, answer))); err != nil {
		b.logger.Error("faq: failed to post auto answer: %v", err)
	}
}

// =====================================================================
// Confirmation button handlers (register in bot.go via RegisterDangerButtonHandlers)
// =====================================================================

// RegisterDangerButtonHandlers registers handlers for confirm/cancel buttons of dangerous commands.
func (b *Bot) RegisterDangerButtonHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onDangerButton))
}

func (b *Bot) onDangerButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if !strings.HasPrefix(data.CustomID, "confirm_") && !strings.HasPrefix(data.CustomID, "cancel_") {
		return
	}

	// The confirmation buttons live on an ephemeral message, but these actions
	// wipe the database — re-check the caller instead of trusting that only the
	// original admin can reach them.
	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}
	member := interactionMember(i.Interaction)
	if member == nil || !b.isAdmin(member.User.ID) {
		b.respondMessage(s, i.Interaction, "У вас нет прав.", true)
		return
	}

	switch {
	case strings.HasPrefix(data.CustomID, "confirm_wipe_player_"):
		id, ok := parseIDFromPrefix(data.CustomID, "confirm_wipe_player_")
		if !ok {
			b.rejectMalformedDangerButton(s, i.Interaction, data.CustomID)
			return
		}
		b.executeWipePlayer(ctx, s, i.Interaction, id)
	case strings.HasPrefix(data.CustomID, "confirm_merge_player_"):
		srcID, dstID, ok := parseTwoIDsFromPrefix(data.CustomID, "confirm_merge_player_")
		if !ok {
			b.rejectMalformedDangerButton(s, i.Interaction, data.CustomID)
			return
		}
		b.executeMergePlayer(ctx, s, i.Interaction, srcID, dstID)
	case strings.HasPrefix(data.CustomID, "confirm_revert_match_"):
		matchID, ok := parseIDFromPrefix(data.CustomID, "confirm_revert_match_")
		if !ok {
			b.rejectMalformedDangerButton(s, i.Interaction, data.CustomID)
			return
		}
		b.executeRevertMatch(ctx, s, i.Interaction, matchID)
	case strings.HasPrefix(data.CustomID, "confirm_reset_player_"):
		id, ok := parseIDFromPrefix(data.CustomID, "confirm_reset_player_")
		if !ok {
			b.rejectMalformedDangerButton(s, i.Interaction, data.CustomID)
			return
		}
		b.executeResetPlayer(ctx, s, i.Interaction, id)
	case data.CustomID == "confirm_wipe":
		b.executeWipe(ctx, s, i.Interaction)
	case data.CustomID == "confirm_reset":
		b.executeReset(ctx, s, i.Interaction)
	case data.CustomID == "cancel_wipe_player",
		data.CustomID == "cancel_merge_player",
		data.CustomID == "cancel_reset_player",
		data.CustomID == "cancel_wipe",
		data.CustomID == "cancel_reset":
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "Операция отменена.",
				Components: []discordgo.MessageComponent{},
			},
		})
	}
}

func (b *Bot) executeWipePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, playerID int) {
	if err := b.services.MatchService.WipePlayerByID(ctx, playerID); err != nil {
		b.logger.Error("wipe_player: failed to wipe player %d: %v", playerID, err)
		b.updateWithError(s, i, fmt.Sprintf("Не удалось удалить игрока с ID %d.", playerID))
		return
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("Игрок с ID **%d** и вся его статистика полностью удалены.", playerID),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeMergePlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, sourceID, targetID int) {
	summary, err := b.services.MatchService.MergePlayers(ctx, sourceID, targetID)
	if err != nil {
		b.logger.Error("merge_player: failed to merge %d -> %d: %v", sourceID, targetID, err)
		b.updateWithError(s, i, fmt.Sprintf("Не удалось объединить профили: %v", err))
		return
	}

	aliasNote := ""
	if summary.LearnedAlias != "" {
		aliasNote = fmt.Sprintf("\nСохранён алиас: `%s`", summary.LearnedAlias)
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf(
				"Профили объединены: **%s** (ID: `%d`) → **%s** (ID: `%d`).\n"+
					"Перенесено: матчей `%d`, медалей `%d`, записей MMR `%d`.%s",
				summary.SourceName, summary.SourceID, summary.TargetName, summary.TargetID,
				summary.MovedResults, summary.MovedMedals, summary.MovedHistory, aliasNote,
			),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeRevertMatch(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, matchID int) {
	summary, err := b.services.EconomyService.RevertFinishedMatch(ctx, matchID)
	if err != nil {
		msg := "Не удалось откатить матч."
		if errors.Is(err, domain.ErrMatchNotFound) {
			msg = fmt.Sprintf("Матч #%d не найден.", matchID)
		} else if errors.Is(err, domain.ErrMatchNotRevertible) {
			msg = fmt.Sprintf("Матч #%d уже не закрыт с победителем — откатывать нечего.", matchID)
		}
		b.logger.Error("revert_match: failed for #%d: %v", matchID, err)
		b.updateWithError(s, i, msg)
		return
	}

	if len(summary.RestoredMMRs) > 0 {
		guildID := b.guildID(i.GuildID)
		restored := summary.RestoredMMRs
		b.goBackground("sync-reverted-mmr", backgroundTimeout, func(bg context.Context) {
			b.syncTierRoles(bg, s, guildID, restored)
			b.services.Lobby.SyncMMRToSheet(restored)
		})
	}

	if summary.ThreadID != "" {
		archived := false
		if _, err := s.ChannelEditComplex(summary.ThreadID, &discordgo.ChannelEdit{Archived: &archived}); err != nil {
			b.logger.Warn("revert_match: failed to unarchive thread %s for #%d: %v", summary.ThreadID, matchID, err)
		}
	}

	if b.cfg.BettingChannelID != "" {
		chID := b.cfg.BettingChannelID
		b.goBackground("announce-revert-match", backgroundTimeout, func(context.Context) {
			_, _ = s.ChannelMessageSendEmbed(chID, &discordgo.MessageEmbed{
				Title: fmt.Sprintf("↩️ Матч #%d откачен администратором", matchID),
				Description: fmt.Sprintf(
					"Предыдущий результат (**%s**) отменён.\n• Откачен MMR у **%d** игроков\n• Откачено монет за матч: **%s**\n• Пересчитано/возвращено ставок: **%d**\n\nМатч снова в статусе `ACTIVE` — судья может закрыть его с верным победителем.",
					summary.OldWinner, summary.RevertedMMRCount, domain.FormatCoins(summary.RevertedMatchCoins), summary.RevertedBetsCount,
				),
				Color: colorBettingClosed,
			})
		})
	}

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf(
				"↩️ **Матч #%d откачен** (был победитель: `%s`).\n"+
					"• Откачен MMR у **%d** игроков (ранговые роли и таблица синхронизируются)\n"+
					"• Возвращено монет за матч: **%s**\n"+
					"• Возвращено ставок: **%d**\n"+
					"• Скрыто OCR-таблиц: **%d**\n\n"+
					"Матч снова `ACTIVE` — можно закрыть его верной кнопкой WIN или заново скинуть скриншот в ветку.",
				summary.MatchID, summary.OldWinner,
				summary.RevertedMMRCount, domain.FormatCoins(summary.RevertedMatchCoins),
				summary.RevertedBetsCount, summary.DeletedScoreboards,
			),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeResetPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction, playerID int) {
	// The reset is keyed by name, so the name must be the real one. Falling back
	// to a literal "Unknown" on a failed lookup pointed a destructive operation
	// at whichever player happened to carry that nickname.
	name, err := b.services.MatchService.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		b.logger.Error("reset_player: cannot resolve player %d: %v", playerID, err)
		b.updateWithError(s, i, fmt.Sprintf("Не удалось найти игрока с ID %d. Сброс отменён.", playerID))
		return
	}

	if err := b.services.MatchService.ResetPlayer(ctx, name, "now"); err != nil {
		b.logger.Error("reset_player: failed to reset %s (ID: %d): %v", name, playerID, err)
		b.updateWithError(s, i, fmt.Sprintf("Не удалось сбросить статистику игрока **%s**.", name))
		return
	}
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("Статистика игрока **%s** (ID: %d) сброшена.", name, playerID),
			Components: []discordgo.MessageComponent{},
		},
	})
}

// updateWithError replaces a confirmation message with a failure notice and
// strips its buttons.
//
// The text is written for the operator, not copied from the error: err.Error()
// on these paths is a raw driver message and went straight into a Discord
// channel. The detail belongs in the log.
func (b *Bot) updateWithError(s *discordgo.Session, i *discordgo.Interaction, msg string) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    truncateMessage(msg),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) executeWipe(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, true)
	if err := b.services.MatchService.WipeAllData(ctx); err != nil {
		b.logger.Error("wipe: failed to wipe all data: %v", err)
		b.editContent(s, i, "Ошибка при очистке. Подробности в логах.")
		return
	}
	b.editContent(s, i, "База данных полностью очищена, Google Таблица сброшена.")
}

func (b *Bot) executeReset(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	b.deferResponse(s, i, true)
	if err := b.services.MatchService.ResetGlobal(ctx); err != nil {
		b.logger.Error("reset: failed to reset season: %v", err)
		b.editContent(s, i, "Не удалось сбросить сезон. Подробности в логах.")
		return
	}
	b.editContent(s, i, "Сезонная статистика всех игроков сброшена.")
}

// parseIDFromPrefix extracts an int from a custom ID like "confirm_wipe_player_42".
//
// It reports failure instead of falling back to 0: these IDs address destructive
// operations, and a swallowed parse error turned a malformed button into
// "player 0 deleted successfully" over a no-op.
func parseIDFromPrefix(customID, prefix string) (int, bool) {
	id, err := strconv.Atoi(strings.TrimPrefix(customID, prefix))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func parseTwoIDsFromPrefix(customID, prefix string) (int, int, bool) {
	rest, found := strings.CutPrefix(customID, prefix)
	if !found {
		return 0, 0, false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, errA := strconv.Atoi(parts[0])
	b, errB := strconv.Atoi(parts[1])
	if errA != nil || errB != nil || a <= 0 || b <= 0 || a == b {
		return 0, 0, false
	}
	return a, b, true
}

// rejectMalformedDangerButton refuses a confirmation whose target cannot be read.
func (b *Bot) rejectMalformedDangerButton(s *discordgo.Session, i *discordgo.Interaction, customID string) {
	b.logger.Error("discord: malformed danger button custom ID %q", customID)
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "Некорректная кнопка подтверждения. Повторите команду.",
			Components: []discordgo.MessageComponent{},
		},
	})
}

// archiveThreadIfMatchFinished checks if the match linked to this thread is finished and archives it.
func (b *Bot) archiveThreadIfMatchFinished(ctx context.Context, s *discordgo.Session, threadID string) {
	match, err := b.services.Lobby.GetMatchByThreadID(ctx, threadID)
	if err != nil {
		// A mix thread has no match row behind it; that is not a failure.
		if !errors.Is(err, domain.ErrMatchNotFound) {
			b.logger.Error("discord: cannot resolve match for thread %s, not archiving: %v", threadID, err)
		}
		return
	}
	if match == nil || match.Status != models.LobbyMatchStatusFinished {
		return
	}

	// Send final message before archiving
	names, err := b.services.Lobby.GetPlayerNamesByMatchID(ctx, match.ID)
	if err != nil {
		b.logger.Warn("discord: cannot list participants of match #%d: %v", match.ID, err)
	}
	winMsg := fmt.Sprintf("**Матч #%d завершён.** Ветка будет архивирована.\nУчастники: %s",
		match.ID, strings.Join(names, ", "))
	if _, err := s.ChannelMessageSend(threadID, winMsg); err != nil {
		b.logger.Warn("discord: cannot post closing note to thread %s: %v", threadID, err)
	}

	// Archive the thread
	archived := true
	_, err = s.ChannelEdit(threadID, &discordgo.ChannelEdit{Archived: &archived})
	if err != nil {
		b.logger.Warn("match: failed to archive thread %s: %v", threadID, err)
	} else {
		b.logger.Info("match: thread %s archived after match #%d finished", threadID, match.ID)
		// Clean up memory cache
		b.deleteThreadPlayers(threadID)
		b.deleteMatchPlayers(match.ID)
	}
}
