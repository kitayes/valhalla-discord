package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

const (
	sbConfirmPrefix  = "sb_confirm_"
	sbFlipPrefix     = "sb_flip_"
	sbCancelPrefix   = "sb_cancel_"
	sbEditKDAPrefix  = "sb_edit_kda_"
	sbAssignPrefix   = "sb_assign_"
	sbModalKDAPrefix = "sb_modal_kda_"

	sbInputKills   = "kills"
	sbInputDeaths  = "deaths"
	sbInputAssists = "assists"
	sbInputMedal   = "medal"

	maxEmbedFieldValueLen = 1024
	maxSelectLabelRunes   = 100
	maxModalTitleRunes    = 45
	maxDiscordOptions     = 25
	maxScoreboardStatVal  = 60
)

type pendingScoreboardReview struct {
	mu         sync.Mutex
	LobbyMatch *models.LobbyMatch
	Pending    *application.PendingScoreboard
	ChannelID  string
	MessageID  string
	at         time.Time
}

// RegisterScoreboardReviewHandlers registers interactive component callbacks
// for referee scoreboard confirmation, winner flipping, KDA editing, and
// unresolved player assignment.
func (b *Bot) RegisterScoreboardReviewHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onScoreboardReviewComponent))
}

func (b *Bot) setPendingReview(matchID int, review *pendingScoreboardReview) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	b.evictStaleCachesLocked(time.Now())
	b.pendingReviews[matchID] = review
}

func (b *Bot) getPendingReview(matchID int) *pendingScoreboardReview {
	b.cacheMu.RLock()
	defer b.cacheMu.RUnlock()
	return b.pendingReviews[matchID]
}

func (b *Bot) deletePendingReview(matchID int) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	delete(b.pendingReviews, matchID)
}

func (b *Bot) handleLobbyMatchScreenshot(
	ctx context.Context,
	s *discordgo.Session,
	m *discordgo.MessageCreate,
	lobbyMatch *models.LobbyMatch,
	attachments []*discordgo.MessageAttachment,
) {
	b.startTyping(s, m.ChannelID)
	progressMsg := b.sendChannelMessage(s, m.ChannelID, "Анализирую скриншот результатов...")

	pending, err := b.services.MatchService.ParseScreenshotForLobby(ctx, attachments[0].URL, lobbyMatch)

	if progressMsg != nil {
		if delErr := s.ChannelMessageDelete(m.ChannelID, progressMsg.ID); delErr != nil {
			b.logger.Warn("discord: failed to delete review progress message in %s: %v", m.ChannelID, delErr)
		}
	}

	if err != nil {
		if errors.Is(err, domain.ErrDuplicateMatch) {
			b.sendChannelMessage(s, m.ChannelID, "Этот скриншот уже был обработан ранее.")
			return
		}
		b.logger.Error("scoreboard review: parse failed for match #%d: %v", lobbyMatch.ID, err)
		b.sendChannelMessage(s, m.ChannelID,
			"Не удалось разобрать скриншот. Загрузите более чёткий скрин или закройте матч вручную кнопками **Team A/B WIN**.")
		return
	}

	breakdowns := b.previewBreakdowns(ctx, lobbyMatch, pending.Result)
	embed := buildScoreboardPreviewEmbed(lobbyMatch.ID, pending.Result, breakdowns)
	components := buildScoreboardPreviewComponents(lobbyMatch.ID, pending.Result)

	note := ""
	if len(attachments) > 1 {
		note = "Для проверки результата взят первый скриншот из сообщения."
	}

	sent, sendErr := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:    note,
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
	if sendErr != nil {
		b.logger.Error("scoreboard review: failed to post preview for match #%d: %v", lobbyMatch.ID, sendErr)
		b.sendChannelMessage(s, m.ChannelID, "Не удалось отправить превью результатов в чат.")
		return
	}

	if prev := b.getPendingReview(lobbyMatch.ID); prev != nil {
		prev.mu.Lock()
		prevCh, prevMsg := prev.ChannelID, prev.MessageID
		prev.mu.Unlock()
		if prevCh != "" && prevMsg != "" {
			superseded := "Превью заменено новым скриншотом."
			emptyEmbeds := []*discordgo.MessageEmbed{}
			emptyComps := []discordgo.MessageComponent{}
			if _, editErr := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
				Channel:    prevCh,
				ID:         prevMsg,
				Content:    &superseded,
				Embeds:     &emptyEmbeds,
				Components: &emptyComps,
			}); editErr != nil {
				b.logger.Warn("scoreboard review: failed to supersede old preview %s in %s: %v", prevMsg, prevCh, editErr)
			}
		}
	}

	b.setPendingReview(lobbyMatch.ID, &pendingScoreboardReview{
		LobbyMatch: lobbyMatch,
		Pending:    pending,
		ChannelID:  m.ChannelID,
		MessageID:  sent.ID,
		at:         time.Now(),
	})
}

func (b *Bot) previewBreakdowns(
	ctx context.Context,
	lobbyMatch *models.LobbyMatch,
	res *application.ScoreboardMatchResult,
) map[int]application.MatchMMRBreakdown {
	if lobbyMatch == nil || res == nil || res.Winner == "" {
		return nil
	}
	kdaMap := buildKDAMap(res.Rows)
	breakdowns, err := b.services.EloService.PreviewMatchDeltas(
		ctx, lobbyMatch, res.Winner, res.MVPPlayerID, res.SVPPlayerID, kdaMap,
	)
	if err != nil {
		b.logger.Warn("scoreboard review: preview MMR calculation failed for match #%d: %v", lobbyMatch.ID, err)
		return nil
	}
	return breakdowns
}

func (b *Bot) onScoreboardReviewComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	customID := data.CustomID
	if !strings.HasPrefix(customID, "sb_") {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}
	if !b.requireReferee(s, i.Interaction) {
		return
	}

	switch {
	case strings.HasPrefix(customID, sbConfirmPrefix):
		matchID, ok := parseReviewMatchID(customID, sbConfirmPrefix)
		if !ok {
			b.respondMessage(s, i.Interaction, "Некорректная кнопка подтверждения.", true)
			return
		}
		b.handleScoreboardConfirm(ctx, s, i, matchID)

	case strings.HasPrefix(customID, sbFlipPrefix):
		matchID, ok := parseReviewMatchID(customID, sbFlipPrefix)
		if !ok {
			b.respondMessage(s, i.Interaction, "Некорректная кнопка переключения.", true)
			return
		}
		b.handleScoreboardFlip(ctx, s, i, matchID)

	case strings.HasPrefix(customID, sbCancelPrefix):
		matchID, ok := parseReviewMatchID(customID, sbCancelPrefix)
		if !ok {
			b.respondMessage(s, i.Interaction, "Некорректная кнопка отмены.", true)
			return
		}
		b.handleScoreboardCancel(s, i, matchID)

	case strings.HasPrefix(customID, sbEditKDAPrefix):
		matchID, ok := parseReviewMatchID(customID, sbEditKDAPrefix)
		if !ok {
			b.respondMessage(s, i.Interaction, "Некорректное меню редактирования.", true)
			return
		}
		b.handleScoreboardEditSelect(s, i, matchID, data.Values)

	case strings.HasPrefix(customID, sbAssignPrefix):
		matchID, rowIdx, ok := parseReviewMatchRowID(customID, sbAssignPrefix)
		if !ok {
			b.respondMessage(s, i.Interaction, "Некорректное меню привязки игрока.", true)
			return
		}
		b.handleScoreboardAssign(ctx, s, i, matchID, rowIdx, data.Values)
	}
}

func (b *Bot) handleScoreboardFlip(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, matchID int) {
	review := b.getPendingReview(matchID)
	if review == nil {
		b.respondMessage(s, i.Interaction, "Превью матча не найдено или устарело. Загрузите скриншот заново.", true)
		return
	}

	review.mu.Lock()
	application.FlipReviewWinner(review.Pending.Roster, review.Pending.Result)
	breakdowns := b.previewBreakdowns(ctx, review.LobbyMatch, review.Pending.Result)
	embed := buildScoreboardPreviewEmbed(matchID, review.Pending.Result, breakdowns)
	components := buildScoreboardPreviewComponents(matchID, review.Pending.Result)
	review.mu.Unlock()

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

func (b *Bot) handleScoreboardCancel(s *discordgo.Session, i *discordgo.InteractionCreate, matchID int) {
	b.deletePendingReview(matchID)

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "Проверка результатов отменена — загрузите скриншот повторно.",
			Embeds:     []*discordgo.MessageEmbed{},
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) handleScoreboardAssign(
	ctx context.Context,
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	matchID, rowIdx int,
	values []string,
) {
	if len(values) != 1 {
		b.respondMessage(s, i.Interaction, "Выберите одного игрока из списка.", true)
		return
	}
	playerID := parseID(values[0])
	if playerID <= 0 {
		b.respondMessage(s, i.Interaction, "Некорректный ID игрока.", true)
		return
	}

	review := b.getPendingReview(matchID)
	if review == nil {
		b.respondMessage(s, i.Interaction, "Превью матча не найдено или устарело.", true)
		return
	}

	review.mu.Lock()
	res := review.Pending.Result
	roster := review.Pending.Roster
	if rowIdx < 0 || rowIdx >= len(res.Rows) {
		review.mu.Unlock()
		b.respondMessage(s, i.Interaction, "Строка таблицы не найдена.", true)
		return
	}

	targetPlayer, found := findRosterPlayer(roster, playerID)
	if !found {
		review.mu.Unlock()
		b.respondMessage(s, i.Interaction, "Выбранный игрок не найден в составе матча.", true)
		return
	}

	for idx, r := range res.Rows {
		if idx != rowIdx && r.PlayerID == playerID {
			review.mu.Unlock()
			b.respondMessage(s, i.Interaction, fmt.Sprintf("Игрок **%s** уже привязан к строке #%d.", targetPlayer.Name, idx+1), true)
			return
		}
	}

	res.Rows[rowIdx].PlayerID = targetPlayer.PlayerID
	res.Rows[rowIdx].PlayerName = targetPlayer.Name
	res.Rows[rowIdx].MatchMethod = "referee"

	allRoster := concatRoster(roster)
	if application.CanLearnAlias(res.Rows[rowIdx].OCRName, allRoster) {
		addUniqueAlias(&res.LearnedAliases, application.LearnedAlias{
			PlayerID: targetPlayer.PlayerID,
			Alias:    strings.TrimSpace(res.Rows[rowIdx].OCRName),
			Source:   "referee",
		})
	}

	application.ResolveRemainingEliminations(roster, res)
	refreshMissingMedals(res)
	application.RevalidateReview(roster, res)
	breakdowns := b.previewBreakdowns(ctx, review.LobbyMatch, res)
	embed := buildScoreboardPreviewEmbed(matchID, res, breakdowns)
	components := buildScoreboardPreviewComponents(matchID, res)
	review.mu.Unlock()

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

func (b *Bot) handleScoreboardEditSelect(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	matchID int,
	values []string,
) {
	if len(values) != 1 {
		b.respondMessage(s, i.Interaction, "Выберите строку для редактирования.", true)
		return
	}
	rowIdx, ok := parseNonNegInt(values[0])
	if !ok {
		b.respondMessage(s, i.Interaction, "Некорректный индекс строки.", true)
		return
	}

	review := b.getPendingReview(matchID)
	if review == nil {
		b.respondMessage(s, i.Interaction, "Превью матча не найдено или устарело.", true)
		return
	}

	review.mu.Lock()
	res := review.Pending.Result
	if rowIdx < 0 || rowIdx >= len(res.Rows) {
		review.mu.Unlock()
		b.respondMessage(s, i.Interaction, "Строка таблицы не найдена.", true)
		return
	}
	row := res.Rows[rowIdx]
	medalVal := ""
	if row.PlayerID > 0 {
		if row.Result == "WIN" && row.PlayerID == res.MVPPlayerID {
			medalVal = "MVP"
		} else if row.Result == "LOSE" && row.PlayerID == res.SVPPlayerID {
			medalVal = "SVP"
		}
	}
	review.mu.Unlock()

	displayName := row.PlayerName
	if displayName == "" {
		displayName = displayOCRName(row.OCRName)
	}
	title := runeTruncate(fmt.Sprintf("Строка #%d: %s", rowIdx+1, displayName), maxModalTitleRunes)

	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: fmt.Sprintf("%s%d_%d", sbModalKDAPrefix, matchID, rowIdx),
			Title:    title,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:  sbInputKills,
						Label:     "Kills (убийства)",
						Style:     discordgo.TextInputShort,
						Value:     strconv.Itoa(row.Kills),
						Required:  true,
						MaxLength: 3,
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:  sbInputDeaths,
						Label:     "Deaths (смерти)",
						Style:     discordgo.TextInputShort,
						Value:     strconv.Itoa(row.Deaths),
						Required:  true,
						MaxLength: 3,
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:  sbInputAssists,
						Label:     "Assists (помощи)",
						Style:     discordgo.TextInputShort,
						Value:     strconv.Itoa(row.Assists),
						Required:  true,
						MaxLength: 3,
					},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:    sbInputMedal,
						Label:       "Медаль (MVP / SVP / пусто)",
						Style:       discordgo.TextInputShort,
						Value:       medalVal,
						Placeholder: "MVP, SVP или оставьте пустым",
						Required:    false,
						MaxLength:   8,
					},
				}},
			},
		},
	})
}

func (b *Bot) handleScoreboardKDAModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}
	if !b.requireReferee(s, i.Interaction) {
		return
	}

	data := i.ModalSubmitData()
	matchID, rowIdx, ok := parseReviewMatchRowID(data.CustomID, sbModalKDAPrefix)
	if !ok {
		b.respondMessage(s, i.Interaction, "Некорректная форма редактирования.", true)
		return
	}

	review := b.getPendingReview(matchID)
	if review == nil {
		b.respondMessage(s, i.Interaction, "Превью матча не найдено или устарело. Загрузите скриншот заново.", true)
		return
	}

	k, errK := strconv.Atoi(strings.TrimSpace(modalValue(data, sbInputKills)))
	d, errD := strconv.Atoi(strings.TrimSpace(modalValue(data, sbInputDeaths)))
	a, errA := strconv.Atoi(strings.TrimSpace(modalValue(data, sbInputAssists)))
	if errK != nil || errD != nil || errA != nil ||
		k < 0 || k > maxScoreboardStatVal ||
		d < 0 || d > maxScoreboardStatVal ||
		a < 0 || a > maxScoreboardStatVal {
		b.respondMessage(s, i.Interaction, fmt.Sprintf("Введите целые числа от 0 до %d для K, D и A.", maxScoreboardStatVal), true)
		return
	}

	medal := strings.ToUpper(strings.TrimSpace(modalValue(data, sbInputMedal)))
	switch medal {
	case "", "MVP", "SVP", "SVPG":
	default:
		b.respondMessage(s, i.Interaction, "Поле медали должно быть пустым, MVP или SVP.", true)
		return
	}

	review.mu.Lock()
	res := review.Pending.Result
	if rowIdx < 0 || rowIdx >= len(res.Rows) {
		review.mu.Unlock()
		b.respondMessage(s, i.Interaction, "Строка таблицы не найдена.", true)
		return
	}

	row := &res.Rows[rowIdx]
	switch medal {
	case "MVP":
		if row.Result != "WIN" {
			review.mu.Unlock()
			b.respondMessage(s, i.Interaction, "Медаль MVP можно назначить только игроку победившей стороны.", true)
			return
		}
		if row.PlayerID <= 0 {
			review.mu.Unlock()
			b.respondMessage(s, i.Interaction, "Сначала сопоставьте этого игрока с участником лобби.", true)
			return
		}
		res.MVPPlayerID = row.PlayerID
	case "SVP", "SVPG":
		if row.Result != "LOSE" {
			review.mu.Unlock()
			b.respondMessage(s, i.Interaction, "Медаль SVPG можно назначить только игроку проигравшей стороны.", true)
			return
		}
		if row.PlayerID <= 0 {
			review.mu.Unlock()
			b.respondMessage(s, i.Interaction, "Сначала сопоставьте этого игрока с участником лобби.", true)
			return
		}
		res.SVPPlayerID = row.PlayerID
	case "":
		if row.PlayerID > 0 {
			if res.MVPPlayerID == row.PlayerID {
				res.MVPPlayerID = 0
				res.RawMVP = ""
			}
			if res.SVPPlayerID == row.PlayerID {
				res.SVPPlayerID = 0
				res.RawSVP = ""
			}
		}
	}

	row.Kills = k
	row.Deaths = d
	row.Assists = a
	row.Confidence = 1.0

	application.RevalidateReview(review.Pending.Roster, res)
	breakdowns := b.previewBreakdowns(ctx, review.LobbyMatch, res)
	embed := buildScoreboardPreviewEmbed(matchID, res, breakdowns)
	components := buildScoreboardPreviewComponents(matchID, res)
	channelID, messageID := review.ChannelID, review.MessageID
	review.mu.Unlock()

	if channelID != "" && messageID != "" {
		embeds := []*discordgo.MessageEmbed{embed}
		if _, editErr := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel:    channelID,
			ID:         messageID,
			Embeds:     &embeds,
			Components: &components,
		}); editErr != nil {
			b.logger.Warn("scoreboard review: failed to update preview message %s in %s: %v", messageID, channelID, editErr)
		}
	}

	b.respondMessage(s, i.Interaction, "Строка обновлена.", true)
}

func (b *Bot) handleScoreboardConfirm(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, matchID int) {
	review := b.getPendingReview(matchID)
	if review == nil {
		b.respondMessage(s, i.Interaction, "Превью матча не найдено или устарело. Загрузите скриншот заново.", true)
		return
	}

	review.mu.Lock()
	res := review.Pending.Result
	if res == nil || res.Winner == "" {
		review.mu.Unlock()
		b.respondMessage(s, i.Interaction, "Сначала определите победившую команду кнопкой «Сменить победителя».", true)
		return
	}
	if len(res.UnresolvedRows) > 0 {
		review.mu.Unlock()
		b.respondMessage(s, i.Interaction, "Сначала сопоставьте всех неопознанных игроков.", true)
		return
	}

	winner := res.Winner
	fileHash := review.Pending.FileHash
	rowsCopy := append([]application.MatchedRow(nil), res.Rows...)
	aliasesCopy := append([]application.LearnedAlias(nil), res.LearnedAliases...)
	mvpID := res.MVPPlayerID
	svpID := res.SVPPlayerID
	lobbyMatch := review.LobbyMatch
	resSnapshot := *res
	resSnapshot.Rows = rowsCopy
	review.mu.Unlock()

	// Atomically close the lobby match first so concurrent Confirm or Team WIN
	// button clicks cannot double-commit stats or ratings.
	success, err := b.services.Lobby.AtomicMatchClosure(ctx, matchID, winner)
	if err != nil {
		b.logger.Error("scoreboard confirm: atomic closure failed for #%d: %v", matchID, err)
		b.respondMessage(s, i.Interaction, "Ошибка системы при закрытии матча.", true)
		return
	}
	if !success {
		b.deletePendingReview(matchID)
		b.respondMessage(s, i.Interaction, "Этот матч уже обрабатывается или завершён.", true)
		return
	}

	b.closeBettingWindow(ctx, matchID)
	b.goBackground("telegram-payout", backgroundTimeout, func(bg context.Context) {
		b.processTelegramPayout(bg, matchID, winner)
	})

	var commitWarning string
	if _, commitErr := b.services.MatchService.CommitLobbyScoreboard(ctx, application.CommitScoreboardRequest{
		LobbyMatchID:   matchID,
		FileHash:       fileHash,
		Winner:         winner,
		Rows:           rowsCopy,
		MVPPlayerID:    mvpID,
		SVPPlayerID:    svpID,
		LearnedAliases: aliasesCopy,
	}); commitErr != nil {
		if errors.Is(commitErr, domain.ErrDuplicateMatch) {
			b.logger.Warn("scoreboard confirm: match #%d scoreboard already persisted: %v", matchID, commitErr)
		} else {
			b.logger.Error("scoreboard confirm: failed to persist scoreboard for #%d: %v", matchID, commitErr)
			commitWarning = "\n⚠️ Статистика K/D/A не сохранилась в архив — проверьте логи."
		}
	}

	mvpName := playerNameForID(rowsCopy, mvpID)
	svpName := playerNameForID(rowsCopy, svpID)
	if err := b.services.Lobby.SaveMedals(ctx, matchID, mvpName, svpName); err != nil {
		b.logger.Error("scoreboard confirm: failed to save medals for #%d: %v", matchID, err)
	}

	kdaMap := buildKDAMap(rowsCopy)
	finalBreakdowns, previewErr := b.services.EloService.PreviewMatchDeltas(ctx, lobbyMatch, winner, mvpID, svpID, kdaMap)
	if previewErr != nil {
		b.logger.Warn("scoreboard confirm: failed to compute final breakdown preview for #%d: %v", matchID, previewErr)
	}

	newMMRs, tierChanges, eloErr := b.services.EloService.ProcessMatchResultWithKDA(
		ctx, matchID, winner, mvpID, svpID, mvpName, svpName, kdaMap,
	)
	b.deletePendingReview(matchID)

	if eloErr != nil {
		b.logger.Error("scoreboard confirm: elo failed for #%d: %v", matchID, eloErr)
		b.respondMessage(s, i.Interaction, fmt.Sprintf(
			"Матч #%d закрыт (%s), но рейтинг применён не полностью — требуется ручная проверка. Ставки рассчитываются отдельно.%s",
			matchID, winner, commitWarning), false)
		return
	}

	if len(newMMRs) > 0 {
		b.goBackground("sync-mmr-sheet", backgroundTimeout, func(context.Context) {
			b.services.Lobby.SyncMMRToSheet(newMMRs)
		})
		guildID := i.GuildID
		b.goBackground("sync-tier-roles", backgroundTimeout, func(bg context.Context) {
			b.syncTierRoles(bg, s, guildID, newMMRs)
		})
	}
	if len(tierChanges) > 0 {
		channelID := b.tierAnnounceChannel(i.ChannelID)
		b.goBackground("announce-tier-changes", backgroundTimeout, func(bg context.Context) {
			b.services.EloService.AnnounceTierChanges(bg, s, channelID, tierChanges)
		})
	}

	finalEmbed := buildConfirmedScoreboardEmbed(matchID, &resSnapshot, finalBreakdowns, commitWarning)
	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{finalEmbed},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{Label: "Встать в очередь лобби", Style: discordgo.SecondaryButton, CustomID: fmt.Sprintf("requeue_%d", matchID)},
				}},
			},
		},
	})

	if lobbyMatch != nil && lobbyMatch.ThreadID != "" {
		threadID := lobbyMatch.ThreadID
		b.goBackground("archive-thread", backgroundTimeout, func(bg context.Context) {
			b.archiveThreadIfMatchFinished(bg, s, threadID)
		})
	}
}

func buildScoreboardPreviewEmbed(
	matchID int,
	res *application.ScoreboardMatchResult,
	breakdowns map[int]application.MatchMMRBreakdown,
) *discordgo.MessageEmbed {
	if res == nil {
		return &discordgo.MessageEmbed{
			Title:       fmt.Sprintf("ПРЕВЬЮ МАТЧА #%d", matchID),
			Description: "Нет данных для отображения.",
			Color:       colorRed,
		}
	}

	winnerLabel := "Не определён ⚠️"
	color := colorGold
	switch res.Winner {
	case domain.TeamA:
		winnerLabel = "Team A 🏆"
		color = colorGreen
	case domain.TeamB:
		winnerLabel = "Team B 🏆"
		color = colorBlue
	}

	fields := buildTeamPreviewFields(res, breakdowns)
	if len(res.Warnings) > 0 {
		var warnSB strings.Builder
		for _, w := range res.Warnings {
			warnSB.WriteString("• ")
			warnSB.WriteString(w)
			warnSB.WriteByte('\n')
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "⚠️ Проверьте данные",
			Value:  truncateText(strings.TrimSpace(warnSB.String()), maxEmbedFieldValueLen),
			Inline: false,
		})
	}

	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("ПРЕВЬЮ МАТЧА #%d — Победитель: %s", matchID, winnerLabel),
		Description: "Проверьте ники, K/D/A и начисление MMR. Нажмите **Подтвердить и закрыть** для записи матча.",
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Только рефери (SUDЬЯ) могут подтверждать или править результаты"},
	}
}

func buildConfirmedScoreboardEmbed(
	matchID int,
	res *application.ScoreboardMatchResult,
	breakdowns map[int]application.MatchMMRBreakdown,
	extraNote string,
) *discordgo.MessageEmbed {
	color := winColor
	if res != nil && res.Winner == domain.TeamB {
		color = loseColor
	}
	winner := ""
	if res != nil {
		winner = res.Winner
	}
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("МАТЧ #%d ЗАВЕРШЁН", matchID),
		Description: fmt.Sprintf("**Победила: %s!**%s", winner, extraNote),
		Color:       color,
		Fields:      buildTeamPreviewFields(res, breakdowns),
	}
}

func buildTeamPreviewFields(
	res *application.ScoreboardMatchResult,
	breakdowns map[int]application.MatchMMRBreakdown,
) []*discordgo.MessageEmbedField {
	if res == nil {
		return nil
	}

	var leftLabel, rightLabel string
	var leftRows, rightRows []application.MatchedRow

	switch res.Winner {
	case domain.TeamA:
		leftLabel = "Team A (WIN)"
		rightLabel = "Team B (LOSE)"
		leftRows, rightRows = splitRowsByResult(res.Rows)
	case domain.TeamB:
		leftLabel = "Team A (LOSE)"
		rightLabel = "Team B (WIN)"
		rightRows, leftRows = splitRowsByResult(res.Rows)
	default:
		leftLabel = "Сторона WIN"
		rightLabel = "Сторона LOSE"
		leftRows, rightRows = splitRowsByResult(res.Rows)
	}

	return []*discordgo.MessageEmbedField{
		{
			Name:   leftLabel,
			Value:  formatPreviewSide(leftRows, res.MVPPlayerID, res.SVPPlayerID, breakdowns),
			Inline: false,
		},
		{
			Name:   rightLabel,
			Value:  formatPreviewSide(rightRows, res.MVPPlayerID, res.SVPPlayerID, breakdowns),
			Inline: false,
		},
	}
}

func splitRowsByResult(rows []application.MatchedRow) (winRows, loseRows []application.MatchedRow) {
	for _, r := range rows {
		if r.Result == "WIN" {
			winRows = append(winRows, r)
		} else {
			loseRows = append(loseRows, r)
		}
	}
	return winRows, loseRows
}

func formatPreviewSide(
	rows []application.MatchedRow,
	mvpID, svpID int,
	breakdowns map[int]application.MatchMMRBreakdown,
) string {
	if len(rows) == 0 {
		return "—"
	}
	var sb strings.Builder
	for i, row := range rows {
		medal := ""
		if row.PlayerID > 0 {
			if row.Result == "WIN" && row.PlayerID == mvpID {
				medal = " **[MVP]**"
			} else if row.Result == "LOSE" && row.PlayerID == svpID {
				medal = " **[SVPG]**"
			}
		}

		if row.PlayerID <= 0 {
			sb.WriteString(fmt.Sprintf("%d. ❓ **%s** — `%d/%d/%d` (не опознан)\n",
				i+1, displayOCRName(row.OCRName), row.Kills, row.Deaths, row.Assists))
			continue
		}

		deltaPart := ""
		if bd, ok := breakdowns[row.PlayerID]; ok && bd.TotalDelta != 0 {
			deltaPart = formatBreakdownDelta(bd)
		}

		sb.WriteString(fmt.Sprintf("%d. **%s** — `%d/%d/%d`%s%s\n",
			i+1, row.PlayerName, row.Kills, row.Deaths, row.Assists, medal, deltaPart))
	}
	return truncateText(strings.TrimSpace(sb.String()), maxEmbedFieldValueLen)
}

func formatBreakdownDelta(bd application.MatchMMRBreakdown) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("%+d", bd.BaseDelta))
	if bd.KDAMod != 0 {
		parts = append(parts, fmt.Sprintf("%+d KDA", bd.KDAMod))
	}
	if bd.MedalBonus != 0 {
		parts = append(parts, fmt.Sprintf("%+d медаль", bd.MedalBonus))
	}
	return fmt.Sprintf(" (`%+d`: %s)", bd.TotalDelta, strings.Join(parts, " "))
}

func buildScoreboardPreviewComponents(matchID int, res *application.ScoreboardMatchResult) []discordgo.MessageComponent {
	confirmDisabled := res == nil || res.Winner == "" || len(res.UnresolvedRows) > 0

	rows := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Подтвердить и закрыть",
				Style:    discordgo.SuccessButton,
				CustomID: fmt.Sprintf("%s%d", sbConfirmPrefix, matchID),
				Disabled: confirmDisabled,
			},
			discordgo.Button{
				Label:    "Сменить победителя",
				Style:    discordgo.SecondaryButton,
				CustomID: fmt.Sprintf("%s%d", sbFlipPrefix, matchID),
			},
			discordgo.Button{
				Label:    "Отклонить",
				Style:    discordgo.DangerButton,
				CustomID: fmt.Sprintf("%s%d", sbCancelPrefix, matchID),
			},
		}},
	}

	if res != nil && len(res.Rows) > 0 {
		editOpts := make([]discordgo.SelectMenuOption, 0, min(len(res.Rows), maxDiscordOptions))
		for idx, row := range res.Rows {
			if len(editOpts) >= maxDiscordOptions {
				break
			}
			name := row.PlayerName
			if name == "" {
				name = "❓ " + displayOCRName(row.OCRName)
			}
			medal := ""
			if row.PlayerID > 0 {
				if row.Result == "WIN" && row.PlayerID == res.MVPPlayerID {
					medal = " | MVP"
				} else if row.Result == "LOSE" && row.PlayerID == res.SVPPlayerID {
					medal = " | SVPG"
				}
			}
			editOpts = append(editOpts, discordgo.SelectMenuOption{
				Label:       runeTruncate(fmt.Sprintf("#%d %s", idx+1, name), maxSelectLabelRunes),
				Description: runeTruncate(fmt.Sprintf("%s | %d/%d/%d%s", row.Result, row.Kills, row.Deaths, row.Assists, medal), maxSelectLabelRunes),
				Value:       strconv.Itoa(idx),
			})
		}
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				MenuType:    discordgo.StringSelectMenu,
				CustomID:    fmt.Sprintf("%s%d", sbEditKDAPrefix, matchID),
				Placeholder: "Исправить K/D/A или медаль...",
				Options:     editOpts,
			},
		}})
	}

	if res != nil && len(res.UnresolvedRows) > 0 {
		targetIdx := res.UnresolvedRows[0]
		if targetIdx >= 0 && targetIdx < len(res.Rows) {
			targetRow := res.Rows[targetIdx]
			candidates := candidatePlayersForRow(res, targetRow)
			if len(candidates) > 0 {
				assignOpts := make([]discordgo.SelectMenuOption, 0, min(len(candidates), maxDiscordOptions))
				for _, p := range candidates {
					if len(assignOpts) >= maxDiscordOptions {
						break
					}
					assignOpts = append(assignOpts, discordgo.SelectMenuOption{
						Label: runeTruncate(p.Name, maxSelectLabelRunes),
						Value: strconv.Itoa(p.PlayerID),
					})
				}
				rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						MenuType:    discordgo.StringSelectMenu,
						CustomID:    fmt.Sprintf("%s%d_%d", sbAssignPrefix, matchID, targetIdx),
						Placeholder: runeTruncate(fmt.Sprintf("Кто строка #%d (%s)?", targetIdx+1, displayOCRName(targetRow.OCRName)), maxSelectLabelRunes),
						Options:     assignOpts,
					},
				}})
			}
		}
	}

	return rows
}

func candidatePlayersForRow(res *application.ScoreboardMatchResult, row application.MatchedRow) []application.RosterPlayer {
	var candidates []application.RosterPlayer
	switch res.Winner {
	case domain.TeamA:
		if row.Result == "WIN" {
			candidates = res.UnassignedTeamA
		} else {
			candidates = res.UnassignedTeamB
		}
	case domain.TeamB:
		if row.Result == "WIN" {
			candidates = res.UnassignedTeamB
		} else {
			candidates = res.UnassignedTeamA
		}
	}
	if len(candidates) == 0 {
		candidates = make([]application.RosterPlayer, 0, len(res.UnassignedTeamA)+len(res.UnassignedTeamB))
		candidates = append(candidates, res.UnassignedTeamA...)
		candidates = append(candidates, res.UnassignedTeamB...)
	}
	return candidates
}

func buildKDAMap(rows []application.MatchedRow) map[int]domain.PlayerKDA {
	out := make(map[int]domain.PlayerKDA, len(rows))
	for _, r := range rows {
		if r.PlayerID <= 0 {
			continue
		}
		out[r.PlayerID] = domain.PlayerKDA{
			PlayerID: r.PlayerID,
			Kills:    r.Kills,
			Deaths:   r.Deaths,
			Assists:  r.Assists,
		}
	}
	return out
}

func parseReviewMatchID(customID, prefix string) (int, bool) {
	rest, found := strings.CutPrefix(customID, prefix)
	if !found {
		return 0, false
	}
	id := parseID(rest)
	return id, id > 0
}

func parseReviewMatchRowID(customID, prefix string) (matchID, rowIdx int, ok bool) {
	rest, found := strings.CutPrefix(customID, prefix)
	if !found {
		return 0, 0, false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	matchID = parseID(parts[0])
	rowIdx, rowOK := parseNonNegInt(parts[1])
	if matchID <= 0 || !rowOK {
		return 0, 0, false
	}
	return matchID, rowIdx, true
}

func parseNonNegInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func findRosterPlayer(roster application.LobbyRoster, playerID int) (application.RosterPlayer, bool) {
	for _, p := range roster.TeamA {
		if p.PlayerID == playerID {
			return p, true
		}
	}
	for _, p := range roster.TeamB {
		if p.PlayerID == playerID {
			return p, true
		}
	}
	return application.RosterPlayer{}, false
}

func concatRoster(roster application.LobbyRoster) []application.RosterPlayer {
	all := make([]application.RosterPlayer, 0, len(roster.TeamA)+len(roster.TeamB))
	all = append(all, roster.TeamA...)
	all = append(all, roster.TeamB...)
	return all
}

func addUniqueAlias(list *[]application.LearnedAlias, item application.LearnedAlias) {
	for _, existing := range *list {
		if existing.PlayerID == item.PlayerID && strings.EqualFold(existing.Alias, item.Alias) {
			return
		}
	}
	*list = append(*list, item)
}

func refreshMissingMedals(res *application.ScoreboardMatchResult) {
	if res == nil {
		return
	}
	if res.MVPPlayerID == 0 {
		bestID := 0
		bestRating := 0.0
		for _, r := range res.Rows {
			if r.Result != "WIN" || r.PlayerID <= 0 {
				continue
			}
			if res.RawMVP != "" && (strings.EqualFold(r.OCRName, res.RawMVP) || strings.EqualFold(r.PlayerName, res.RawMVP)) {
				bestID = r.PlayerID
				break
			}
			if r.Rating > bestRating {
				bestRating = r.Rating
				bestID = r.PlayerID
			}
		}
		res.MVPPlayerID = bestID
	}
	if res.SVPPlayerID == 0 {
		bestID := 0
		bestRating := 0.0
		for _, r := range res.Rows {
			if r.Result != "LOSE" || r.PlayerID <= 0 {
				continue
			}
			if res.RawSVP != "" && (strings.EqualFold(r.OCRName, res.RawSVP) || strings.EqualFold(r.PlayerName, res.RawSVP)) {
				bestID = r.PlayerID
				break
			}
			if r.Rating > bestRating {
				bestRating = r.Rating
				bestID = r.PlayerID
			}
		}
		res.SVPPlayerID = bestID
	}
}

func playerNameForID(rows []application.MatchedRow, playerID int) string {
	if playerID <= 0 {
		return ""
	}
	for _, r := range rows {
		if r.PlayerID == playerID {
			return r.PlayerName
		}
	}
	return ""
}

func displayOCRName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return "без ника"
	}
	return clean
}

func runeTruncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
