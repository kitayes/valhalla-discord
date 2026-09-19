package discord

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

// =====================================================================
// Phase 5: Full Discord Matchmaking — Referee UI, Captains, 5-step team dist
// =====================================================================

const (
	selectMenuCaptainA = "create_match_captain_a"
	selectMenuCaptainB = "create_match_captain_b"
	selectMenuTeamA    = "create_match_team_a"
	selectMenuTeamB    = "create_match_team_b"

	buttonTeamAWin = "match_team_a_win"
	buttonTeamBWin = "match_team_b_win"

	matchEmbedColor = 0x9B59B6
	winColor        = 0x2ECC71
	loseColor       = 0xE74C3C
)

func (b *Bot) newCreateMatchCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "create_match",
		Description: "Создать матч из активного лобби (Только SUDЬЯ/админы)",
	}
}

func (b *Bot) newBalanceCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "balance",
		Description: "Авто-баланс 10 игроков из лобби на 2 команды по MMR (Только SUDЬЯ/админы)",
	}
}

func (b *Bot) RegisterMatchHandlers() {
	b.session.AddHandler(b.wrapRecover(b.onMatchSelectMenu))
	b.session.AddHandler(b.wrapRecover(b.onMatchResultButton))
}

func (b *Bot) RegisterRequeueHandler() {
	b.session.AddHandler(b.wrapRecover(b.onRequeueButton))
}

// =====================================================================
// Step 1: handleCreateMatch
// =====================================================================

func (b *Bot) handleCreateMatch(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if !b.requireReferee(s, i) {
		return
	}

	activePlayers := b.services.Lobby.GetActivePlayers()
	if len(activePlayers) < teamSize*2 {
		b.respondMessage(s, i, fmt.Sprintf("Нужно %d игроков в лобби, сейчас %d.", teamSize*2, len(activePlayers)), true)
		return
	}

	sort.Slice(activePlayers, func(a, b int) bool { return activePlayers[a].ID < activePlayers[b].ID })

	var playerOptions []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		playerOptions = append(playerOptions, discordgo.SelectMenuOption{Label: p.Name, Value: fmt.Sprintf("%d", p.ID)})
	}

	minOne := 1
	maxOne := 1
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "**Шаг 1/4: Выберите Капитана A**",
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{CustomID: selectMenuCaptainA, Placeholder: "Выберите Капитана A...", MinValues: &minOne, MaxValues: maxOne, Options: playerOptions},
				}},
			},
		},
	})
}

func (b *Bot) onMatchSelectMenu(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	customID := i.MessageComponentData().CustomID
	if customID != selectMenuCaptainA &&
		!strings.HasPrefix(customID, selectMenuCaptainB) &&
		!strings.HasPrefix(customID, selectMenuTeamA) &&
		!strings.HasPrefix(customID, selectMenuTeamB) {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	// The wizard runs entirely on component interactions, which never pass
	// through onInteraction — so every step has to carry its own gates. The last
	// step creates the match, empties the lobby and opens betting; only the WIN
	// button at the end of the same flow used to check anything.
	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}
	if !b.requireReferee(s, i.Interaction) {
		return
	}

	if customID == selectMenuCaptainA {
		b.handleSelectCaptainA(ctx, s, i)
	} else if strings.HasPrefix(customID, selectMenuCaptainB) {
		b.handleSelectCaptainB(ctx, s, i)
	} else if strings.HasPrefix(customID, selectMenuTeamA) {
		b.handleSelectTeamA(ctx, s, i)
	} else if strings.HasPrefix(customID, selectMenuTeamB) {
		b.handleSelectTeamB(ctx, s, i)
	}
}

func (b *Bot) handleSelectCaptainA(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.MessageComponentData().Values) == 0 {
		return
	}
	captainAID := i.MessageComponentData().Values[0]
	captainAName := b.getPlayerName(ctx, captainAID)
	activePlayers := b.services.Lobby.GetActivePlayers()

	var options []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		if fmt.Sprintf("%d", p.ID) == captainAID {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{Label: p.Name, Value: fmt.Sprintf("%d", p.ID)})
	}

	minOne := 1
	maxOne := 1
	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Капитан A: **%s**\n\n**Шаг 2/4: Выберите Капитана B**", captainAName),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{CustomID: fmt.Sprintf("%s_%s", selectMenuCaptainB, captainAID), Placeholder: "Выберите Капитана B...", MinValues: &minOne, MaxValues: maxOne, Options: options},
				}},
			},
		},
	})
}

func (b *Bot) handleSelectCaptainB(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.MessageComponentData().Values) == 0 {
		return
	}
	captainBID := i.MessageComponentData().Values[0]
	captainBName := b.getPlayerName(ctx, captainBID)
	captainAID, ok := parseCaptainBCustomID(i.MessageComponentData().CustomID)
	if !ok {
		b.logger.Error("match: malformed captain B custom ID %q", i.MessageComponentData().CustomID)
		return
	}
	captainAName := b.getPlayerName(ctx, captainAID)

	activePlayers := b.services.Lobby.GetActivePlayers()
	var options []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		pid := fmt.Sprintf("%d", p.ID)
		if pid == captainAID || pid == captainBID {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{Label: p.Name, Value: pid})
	}

	minFour := 4
	maxFour := 4
	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Капитан A: **%s**\nКапитан B: **%s**\n\n**Шаг 3/4: Выберите 4 игроков для Команды A**", captainAName, captainBName),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{CustomID: fmt.Sprintf("%s_%s_%s", selectMenuTeamA, captainAID, captainBID), Placeholder: "Выберите 4 игроков для Команды A...", MinValues: &minFour, MaxValues: maxFour, Options: options},
				}},
			},
		},
	})
}

func (b *Bot) handleSelectTeamA(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.MessageComponentData().Values) < 4 {
		return
	}
	teamAValues := i.MessageComponentData().Values
	captainAID, captainBID, ok := parseTeamACustomID(i.MessageComponentData().CustomID)
	if !ok {
		b.logger.Error("match: malformed team A custom ID %q", i.MessageComponentData().CustomID)
		return
	}
	captainAName := b.getPlayerName(ctx, captainAID)
	captainBName := b.getPlayerName(ctx, captainBID)

	teamAIDs := []int{parseID(captainAID)}
	var teamANames []string
	teamANames = append(teamANames, captainAName)
	for _, v := range teamAValues {
		teamAIDs = append(teamAIDs, parseID(v))
		teamANames = append(teamANames, b.getPlayerName(ctx, v))
	}

	teamACombined := strings.Join(teamAValues, "-")

	activePlayers := b.services.Lobby.GetActivePlayers()
	excludeSet := make(map[int]bool)
	for _, aid := range teamAIDs {
		excludeSet[aid] = true
	}
	excludeSet[parseID(captainBID)] = true

	var options []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		if excludeSet[p.ID] {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{Label: p.Name, Value: fmt.Sprintf("%d", p.ID)})
	}

	minFour := 4
	maxFour := 4
	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Капитан A: **%s**\nКапитан B: **%s**\nКоманда A: %s\n\n**Шаг 4/4: Выберите 4 игроков для Команды B**",
				captainAName, captainBName, strings.Join(teamANames[1:], ", ")),
			Flags: discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{CustomID: fmt.Sprintf("%s_%s_%s_%s", selectMenuTeamB, captainAID, captainBID, teamACombined), Placeholder: "Выберите 4 игроков для Команды B...", MinValues: &minFour, MaxValues: maxFour, Options: options},
				}},
			},
		},
	})
}

func (b *Bot) handleSelectTeamB(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.MessageComponentData().Values) < 4 {
		return
	}
	teamBValues := i.MessageComponentData().Values
	captainAID, captainBID, teamAIDStrs, ok := parseTeamBCustomID(i.MessageComponentData().CustomID)
	if !ok {
		b.logger.Error("match: malformed team B custom ID %q", i.MessageComponentData().CustomID)
		return
	}

	captainAName := b.getPlayerName(ctx, captainAID)
	captainBName := b.getPlayerName(ctx, captainBID)

	teamAIDs := []int{parseID(captainAID)}
	var teamANames []string
	teamANames = append(teamANames, captainAName)
	for _, s := range teamAIDStrs {
		id := parseID(s)
		teamAIDs = append(teamAIDs, id)
		teamANames = append(teamANames, b.getPlayerName(ctx, s))
	}

	teamBIDs := []int{parseID(captainBID)}
	var teamBNames []string
	teamBNames = append(teamBNames, captainBName)
	for _, v := range teamBValues {
		id := parseID(v)
		teamBIDs = append(teamBIDs, id)
		teamBNames = append(teamBNames, b.getPlayerName(ctx, v))
	}

	guildID := b.guildID(i.GuildID)

	matchID, err := b.services.Lobby.CreateMatch(ctx, guildID, parseID(captainAID), parseID(captainBID), teamAIDs, teamBIDs)
	if err != nil {
		b.logger.Error("match: failed to create: %v", err)
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "Ошибка при создании матча: " + err.Error(), Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	embed := b.buildMatchEmbed(matchID, captainAName, captainBName, teamANames, teamBNames)
	b.publishCreatedMatch(ctx, s, i.Interaction, matchID, teamAIDs, teamBIDs, embed, captainAName, captainBName)
	b.logger.Info("match: #%d created | Team A: %s vs Team B: %s", matchID, strings.Join(teamANames, ", "), strings.Join(teamBNames, ", "))
}

// publishCreatedMatch is everything that has to happen once a match row exists:
// clear the drafted players out of the lobby, post the scoreboard with the WIN
// buttons, cache the roster and arm the betting window.
//
// handleSelectTeamB and handleBalance each had their own copy of this, including
// two byte-identical button blocks. The copy in handleBalance had already
// drifted — it never logged the created match.
func (b *Bot) publishCreatedMatch(
	ctx context.Context,
	s *discordgo.Session,
	i *discordgo.Interaction,
	matchID int,
	teamAIDs, teamBIDs []int,
	embed *discordgo.MessageEmbed,
	captainAName, captainBName string,
) {
	allIDs := concatIDs(teamAIDs, teamBIDs)
	for _, pid := range allIDs {
		b.services.Lobby.RemovePlayer(pid)
	}
	b.setMatchPlayers(matchID, allIDs)

	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: matchResultButtons(matchID),
		},
	})

	b.openBettingWindow(ctx, matchID)
	b.startBettingTimer(matchID)
	b.notifyTelegramMatchLive(matchID, captainAName, captainBName)
}

// matchResultButtons builds the referee's WIN buttons for a match.
func matchResultButtons(matchID int) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Team A WIN", Style: discordgo.SuccessButton, CustomID: fmt.Sprintf("%s_%d", buttonTeamAWin, matchID)},
			discordgo.Button{Label: "Team B WIN", Style: discordgo.DangerButton, CustomID: fmt.Sprintf("%s_%d", buttonTeamBWin, matchID)},
		}},
	}
}

// =====================================================================
// Match Closure
// =====================================================================

func (b *Bot) onMatchResultButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	customID := data.CustomID
	if !strings.HasPrefix(customID, buttonTeamAWin) && !strings.HasPrefix(customID, buttonTeamBWin) {
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

	winner := domain.TeamA
	prefix := buttonTeamAWin
	if strings.HasPrefix(customID, buttonTeamBWin) {
		winner, prefix = domain.TeamB, buttonTeamBWin
	}
	matchID := parseID(strings.TrimPrefix(customID, prefix+"_"))
	if matchID <= 0 {
		b.logger.Error("match: malformed win button custom ID %q", customID)
		b.respondMessage(s, i.Interaction, "Некорректная кнопка. Обновите сообщение матча.", true)
		return
	}

	success, err := b.services.Lobby.AtomicMatchClosure(ctx, matchID, winner)
	if err != nil {
		b.logger.Error("match: atomic closure failed for #%d: %v", matchID, err)
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "Ошибка системы при закрытии матча.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}
	if !success {
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "Этот матч уже обрабатывается или завершён.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	// Stop taking bets before settling them; the 5-minute timer may still be pending.
	b.closeBettingWindow(ctx, matchID)

	// The pool is settled first and independently of the rating. The two share
	// nothing but the winner, and hanging the payout off a successful Elo update
	// meant a failure there left every stake deducted on a match that can never
	// be replayed — AtomicSetWinner refuses a second attempt and /cancel_match
	// only takes ACTIVE matches.
	b.goBackground("telegram-payout", backgroundTimeout, func(bg context.Context) {
		b.processTelegramPayout(bg, matchID, winner)
	})

	// MVP/SVP are read off the match screenshot and stored on the match, so an
	// empty pair here means "use whatever was recognised".
	newMMRs, tierChanges, err := b.services.EloService.ProcessMatchResult(ctx, matchID, winner, "", "")
	if err != nil {
		// The winner is already committed and AtomicSetWinner will refuse a
		// second attempt, so the rating for this match cannot be replayed by
		// clicking again. Say so instead of rendering a clean "match finished"
		// embed over a rating that was never applied.
		b.logger.Error("elo: failed to process match #%d: %v", matchID, err)
		b.respondMessage(s, i.Interaction, fmt.Sprintf(
			"Матч #%d закрыт (%s), но рейтинг применён не полностью — требуется ручная проверка. "+
				"Ставки рассчитываются отдельно.", matchID, winner), false)
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

	match, err := b.services.Lobby.GetMatch(ctx, matchID)
	if err != nil {
		b.logger.Error("match: closed #%d but failed to reload it for the result embed: %v", matchID, err)
		b.respondMessage(s, i.Interaction, fmt.Sprintf("Матч #%d закрыт (%s), но карточку результата отрисовать не удалось.", matchID, winner), false)
		return
	}

	// The requeue button rides on the result card instead of arriving as its own
	// message. One match used to leave two messages in the channel, and the
	// second one carried nothing but a button.
	embed := b.buildMatchResultEmbed(ctx, match, winner)
	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{Label: "Встать в очередь лобби", Style: discordgo.SecondaryButton, CustomID: fmt.Sprintf("requeue_%d", matchID)},
				}},
			},
		},
	})

	if match.ThreadID != "" {
		threadID := match.ThreadID
		b.goBackground("archive-thread", backgroundTimeout, func(bg context.Context) {
			b.archiveThreadIfMatchFinished(bg, s, threadID)
		})
	}
}

// =====================================================================
// /cancel_match command
// =====================================================================

func (b *Bot) handleCancelMatch(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if !b.requireReferee(s, i) {
		return
	}
	matchID := int(i.ApplicationCommandData().Options[0].IntValue())

	playerIDs, err := b.services.Lobby.CancelMatch(ctx, matchID)
	if err != nil {
		b.logger.Error("cancel_match: failed to cancel #%d: %v", matchID, err)
		b.respondMessage(s, i, cancelFailureMessage(err, matchID), true)
		return
	}

	// Refund first: the match row is already committed as FINISHED, so from this
	// point neither this command nor the WIN button can be replayed to move the
	// stakes. Everything below is cosmetic by comparison. A failure here is
	// picked up by BettingService.SettlePending on the next sweep.
	refunded, refundErr := b.services.BettingService.RefundAllBets(ctx, matchID)
	if refundErr != nil {
		b.logger.Error("cancel_match: failed to refund bets for #%d, left for the sweep: %v", matchID, refundErr)
	} else {
		b.logger.Info("cancel_match: refunded %d bets for #%d", refunded, matchID)
	}

	refundNote := fmt.Sprintf("ставки возвращены (%d)", refunded)
	if refundErr != nil {
		refundNote = "**ставки вернуть сразу не удалось — возврат выполнится автоматически**"
	}

	// Count what actually happened. The previous version discarded the result of
	// every re-add and then reported all ten players as returned, whether the
	// lobby was closed, the player was queue-banned, or the lookup failed.
	names, err := b.services.MatchService.GetPlayerNamesByIDs(ctx, playerIDs)
	if err != nil {
		b.logger.Error("cancel_match: failed to resolve rosters for #%d: %v", matchID, err)
		names = map[int]string{}
	}

	requeued := 0
	for _, pid := range playerIDs {
		discordID, err := b.services.MatchService.GetDiscordIDByPlayerID(ctx, pid)
		if err != nil {
			b.logger.Warn("cancel_match: player %d not returned to lobby: %v", pid, err)
			continue
		}
		if _, err := b.services.Lobby.TryAddPlayer(ctx, models.Player{ID: pid, Name: names[pid]}, discordID); err != nil {
			b.logger.Warn("cancel_match: player %d not returned to lobby: %v", pid, err)
			continue
		}
		requeued++
	}

	if match, err := b.services.Lobby.GetMatch(ctx, matchID); err != nil {
		b.logger.Warn("cancel_match: cannot reload match #%d: %v", matchID, err)
	} else if match.ThreadID != "" {
		threadID := match.ThreadID
		if _, err := s.ChannelMessageSend(threadID, fmt.Sprintf("**Матч #%d отменён судьёй.** %s", matchID, refundNote)); err != nil {
			b.logger.Warn("cancel_match: failed to post to thread %s: %v", threadID, err)
		}
		b.goBackground("archive-thread", backgroundTimeout, func(bg context.Context) {
			b.archiveThreadIfMatchFinished(bg, s, threadID)
		})
	}

	b.respondMessage(s, i, fmt.Sprintf("Матч #%d отменён. Возвращено в лобби: %d из %d. %s",
		matchID, requeued, len(playerIDs), refundNote), false)
}

// cancelFailureMessage renders a cancellation refusal. "There is no such match"
// and "that match is already over" send the referee to different places, so they
// do not share one message.
func cancelFailureMessage(err error, matchID int) string {
	switch {
	case errors.Is(err, domain.ErrMatchNotFound):
		return fmt.Sprintf("Матч #%d не найден. Проверьте номер в карточке матча.", matchID)
	case errors.Is(err, domain.ErrMatchNotActive):
		return fmt.Sprintf("Матч #%d уже завершён или отменён — отменять нечего.", matchID)
	default:
		return fmt.Sprintf("Не удалось отменить матч #%d. Попробуйте ещё раз.", matchID)
	}
}

// =====================================================================
// Requeue button
// =====================================================================

func (b *Bot) onRequeueButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if !strings.HasPrefix(data.CustomID, "requeue_") {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	if !b.guardComponent(ctx, s, i.Interaction) {
		return
	}

	matchID := parseID(strings.TrimPrefix(data.CustomID, "requeue_"))
	if matchID <= 0 {
		b.logger.Error("requeue: malformed custom ID %q", data.CustomID)
		return
	}

	member := interactionMember(i.Interaction)
	if member == nil {
		return
	}
	userID := member.User.ID
	player, err := b.findPlayerByDiscordID(ctx, userID)
	if err != nil {
		b.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content:    "Ваш Discord не привязан к профилю игрока. Нажмите кнопку и укажите свой ник в игре.",
				Components: bindPromptComponents(),
				Flags:      discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Verify if the player was a participant of the match
	isParticipant := false
	if pids, ok := b.getMatchPlayers(matchID); ok {
		for _, pid := range pids {
			if pid == player.ID {
				isParticipant = true
				break
			}
		}
	} else {
		// Fallback: check database if cache expired. A lookup failure is not the
		// same answer as "you did not play in it" — say so rather than accusing
		// the user.
		match, err := b.services.Lobby.GetMatch(ctx, matchID)
		if err != nil {
			b.logger.Error("requeue: failed to load match #%d: %v", matchID, err)
			b.respondMessage(s, i.Interaction, "Не удалось проверить состав матча. Попробуйте ещё раз.", true)
			return
		}
		allIDs := concatIDs(match.TeamAIDs, match.TeamBIDs)
		for _, pid := range allIDs {
			if pid == player.ID {
				isParticipant = true
				break
			}
		}
	}

	if !isParticipant {
		b.respondMessage(s, i.Interaction, "Вы не участвовали в этом матче.", true)
		return
	}

	place, err := b.services.Lobby.TryAddPlayer(ctx, player, userID)
	if err != nil && !errors.Is(err, domain.ErrAlreadyQueued) {
		b.logger.Warn("requeue: player %d refused: %v", player.ID, err)
		b.respondMessage(s, i.Interaction, joinMessage(err), true)
		return
	}

	content := "Вы снова в лобби!"
	if place == application.PlaceWaitlist {
		content = "Основное лобби заполнено. Вы добавлены в резерв."
	}

	b.logger.Info("requeue: player %s (ID: %d) rejoined lobby (%s)", player.Name, player.ID, place)
	b.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral},
	})
}

// =====================================================================
// Helpers
// =====================================================================

func formatPlayerList(names []string) string {
	var sb strings.Builder
	for i, name := range names {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, name))
	}
	return sb.String()
}

// unknownPlayerName is shown when a player row cannot be read. An empty string
// rendered as "1. ****" in the embed and hid the failure entirely.
const unknownPlayerName = "неизвестный игрок"

func (b *Bot) getPlayerName(ctx context.Context, idStr string) string {
	id := parseID(idStr)
	name, err := b.services.MatchService.GetPlayerNameByID(ctx, id)
	if err != nil {
		b.logger.Warn("discord: cannot resolve name for player %d: %v", id, err)
		return unknownPlayerName
	}
	return name
}

// getPlayerNames resolves a roster in one query, preserving the order of ids.
func (b *Bot) getPlayerNames(ctx context.Context, ids []int) []string {
	lookup, err := b.services.MatchService.GetPlayerNamesByIDs(ctx, ids)
	if err != nil {
		b.logger.Error("discord: failed to resolve %d player names: %v", len(ids), err)
		lookup = map[int]string{}
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		if name, ok := lookup[id]; ok {
			names[i] = name
			continue
		}
		names[i] = unknownPlayerName
	}
	return names
}

// parseID converts a select-menu value to a player ID, returning 0 for anything
// that is not a plain number. Sscanf would accept "42abc" and stop at the first
// non-digit, quietly turning a malformed ID into a valid-looking one.
func parseID(s string) int {
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return id
}

// =====================================================================
// Custom ID encoding for the match creation wizard
//
// Each step carries the previous selections in the next component's custom ID.
// The menu prefixes themselves contain underscores ("create_match_captain_b"),
// so splitting the whole ID on "_" and counting fields from either end picked up
// fragments of the prefix — captain IDs arrived as "b_42" or even "a", parsed to
// 0, and CreateMatch then failed the players(id) foreign key. Trimming the known
// prefix first makes the split unambiguous.
// =====================================================================

// parseCaptainBCustomID extracts captain A's ID from the captain B menu.
func parseCaptainBCustomID(customID string) (captainAID string, ok bool) {
	rest, found := strings.CutPrefix(customID, selectMenuCaptainB+"_")
	if !found || rest == "" {
		return "", false
	}
	return rest, true
}

// parseTeamACustomID extracts both captain IDs from the team A menu.
func parseTeamACustomID(customID string) (captainAID, captainBID string, ok bool) {
	rest, found := strings.CutPrefix(customID, selectMenuTeamA+"_")
	if !found {
		return "", "", false
	}
	parts := strings.SplitN(rest, "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// parseTeamBCustomID extracts the captains and team A's roster from the team B menu.
func parseTeamBCustomID(customID string) (captainAID, captainBID string, teamAIDs []string, ok bool) {
	rest, found := strings.CutPrefix(customID, selectMenuTeamB+"_")
	if !found {
		return "", "", nil, false
	}
	parts := strings.SplitN(rest, "_", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", nil, false
	}
	return parts[0], parts[1], strings.Split(parts[2], "-"), true
}

// notifyTelegramMatchLive fires the Telegram announcement without blocking the
// interaction. It runs through goBackground like every other detached call: a
// bare `go` here meant a panic inside the Telegram callback took the process
// down instead of one announcement.
func (b *Bot) notifyTelegramMatchLive(matchID int, captainA, captainB string) {
	b.goBackground("telegram-match-live", backgroundTimeout, func(context.Context) {
		b.services.Lobby.NotifyMatchLive(matchID, captainA, captainB, bettingWindow)
	})
}

// syncTierRoles grants each player the Discord role matching their new tier.
// A no-op unless at least one TIER_ROLE_* is configured.
func (b *Bot) syncTierRoles(ctx context.Context, s *discordgo.Session, guildID string, newMMRs map[int]int) {
	if len(b.tierRoleIDs) == 0 {
		return
	}
	guildID = b.guildID(guildID)
	if err := b.services.EloService.SyncTierRoles(ctx, s, guildID, b.tierRoleIDs, newMMRs); err != nil {
		b.logger.Error("match: failed to sync tier roles: %v", err)
	}
}

// tierAnnounceChannel picks where rank changes are posted, falling back to the
// channel the match was closed in.
func (b *Bot) tierAnnounceChannel(fallback string) string {
	if b.cfg.TierAnnounceChannelID != "" {
		return b.cfg.TierAnnounceChannelID
	}
	return fallback
}

// openBettingWindow arms the betting window for a freshly created match.
// Without it lobby_matches.betting_open stays at its FALSE default and every
// bet is rejected as "betting closed".
func (b *Bot) openBettingWindow(ctx context.Context, matchID int) {
	if err := b.services.Lobby.OpenBetting(ctx, matchID, bettingWindow); err != nil {
		b.logger.Error("match: failed to open betting for #%d: %v", matchID, err)
		return
	}
	b.logger.Info("match: betting window opened for #%d (%s)", matchID, bettingWindow)
}

// sweepBetting reconciles the two things that outlive the interaction which
// started them: betting windows left open by a restart, and settlements that
// were begun and never finished.
//
// It runs once at startup and then on every tick, so a lost timer goroutine
// cannot leave a match accepting bets forever and a dropped payout cannot leave
// stakes deducted with nothing to move them.
func (b *Bot) sweepBetting() {
	// The recover sits inside one sweep, not around the loop. Wrapping the
	// goroutine instead meant a single panic killed the ticker for the lifetime
	// of the process — and this sweep is the safety net that moves money nobody
	// else will move.
	sweep := func() {
		defer func() {
			if r := recover(); r != nil {
				b.logger.Error("PANIC in betting sweep: %v\n%s", r, debug.Stack())
			}
		}()

		ctx, cancel := b.opContext(backgroundTimeout)
		defer cancel()

		if n, err := b.services.Lobby.CloseExpiredBetting(ctx); err != nil {
			b.logger.Error("match: failed to close expired betting windows: %v", err)
		} else if n > 0 {
			b.logger.Info("match: closed %d expired betting window(s)", n)
		}

		if n, err := b.services.BettingService.SettlePending(ctx); err != nil {
			b.logger.Error("betting: pending settlement sweep failed: %v", err)
		} else if n > 0 {
			b.logger.Warn("betting: swept %d match(es) whose settlement had not completed", n)
		}
	}

	go func() {
		sweep()

		ticker := time.NewTicker(bettingSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-b.ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
}

// startBettingTimer closes the betting window once bettingWindow elapses.
// It waits on the bot's lifetime context rather than sleeping blindly, so a
// shutdown does not leave the goroutine parked for five minutes.
func (b *Bot) startBettingTimer(matchID int) {
	go func() {
		timer := time.NewTimer(bettingWindow)
		defer timer.Stop()

		select {
		case <-b.ctx.Done():
			return
		case <-timer.C:
		}

		ctx, cancel := b.opContext(backgroundTimeout)
		defer cancel()
		b.closeBettingWindow(ctx, matchID)
	}()
}

func (b *Bot) closeBettingWindow(ctx context.Context, matchID int) {
	if err := b.services.Lobby.CloseBetting(ctx, matchID); err != nil {
		b.logger.Error("match: failed to close betting for #%d: %v", matchID, err)
	}
}

// concatIDs joins two ID slices into a freshly allocated one. Plain
// append(a, b...) would write into a's backing array when it has spare
// capacity, silently corrupting the caller's slice.
func concatIDs(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// processTelegramPayout settles the bets on a finished match.
//
// A failure here means bettors were never credited. The bets stay unsettled
// (settled_at IS NULL), which is what BettingService.SettlePending looks for on
// the sweep driven by sweepBetting — so the money still moves, just late.
func (b *Bot) processTelegramPayout(ctx context.Context, matchID int, winningTeam string) {
	payouts, err := b.services.BettingService.ProcessPayout(ctx, matchID, winningTeam)
	if err != nil {
		b.logger.Error("betting: payout for match #%d (%s) failed, bets left unsettled: %v", matchID, winningTeam, err)
		return
	}
	b.logger.Info("betting: match #%d paid out to %d bettors", matchID, len(payouts))
}

// =====================================================================
// Embed Builders
// =====================================================================

func (b *Bot) buildMatchEmbed(matchID int, captainA, captainB string, teamA, teamB []string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: fmt.Sprintf("МАТЧ #%d АКТИВЕН", matchID), Description: fmt.Sprintf("Капитаны: **%s** vs **%s**", captainA, captainB),
		Color: matchEmbedColor,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Team A", Value: formatPlayerList(teamA), Inline: true},
			{Name: "Team B", Value: formatPlayerList(teamB), Inline: true},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: "Только рефери (SUDЬЯ) могут завершить матч"},
	}
}

func (b *Bot) buildMatchResultEmbed(ctx context.Context, match *models.LobbyMatch, winner string) *discordgo.MessageEmbed {
	color := winColor
	if winner == domain.TeamB {
		color = loseColor
	}
	return &discordgo.MessageEmbed{
		Title: fmt.Sprintf("МАТЧ #%d ЗАВЕРШЁН", match.ID), Description: fmt.Sprintf("**Победила: %s!**", winner),
		Color: color,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Team A", Value: formatPlayerList(b.getPlayerNames(ctx, match.TeamAIDs)), Inline: true},
			{Name: "Team B", Value: formatPlayerList(b.getPlayerNames(ctx, match.TeamBIDs)), Inline: true},
		},
	}
}

// =====================================================================
// 2.1 Auto-Balance
// =====================================================================

func (b *Bot) handleBalance(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	if !b.requireReferee(s, i) {
		return
	}
	activePlayers := b.services.Lobby.GetActivePlayers()
	if len(activePlayers) < teamSize*2 {
		b.respondMessage(s, i, fmt.Sprintf("Нужно %d игроков в лобби, сейчас %d.", teamSize*2, len(activePlayers)), true)
		return
	}
	playerIDs := make([]int, len(activePlayers))
	for idx, p := range activePlayers {
		playerIDs[idx] = p.ID
	}
	mmrMap, err := b.services.Lobby.GetPlayerMMRsBatch(ctx, playerIDs)
	if err != nil {
		b.logger.Error("balance: failed to get MMRs: %v", err)
		b.respondMessage(s, i, "Ошибка получения MMR.", true)
		return
	}

	// Sort first, then take the top ten. The other order truncated by join
	// position while the comment claimed the ten highest-MMR players were being
	// drafted — harmless only because lobbyCapacity happens to equal ten.
	sort.Slice(activePlayers, func(a, b int) bool { return mmrMap[activePlayers[a].ID] > mmrMap[activePlayers[b].ID] })
	if len(activePlayers) > teamSize*2 {
		activePlayers = activePlayers[:teamSize*2]
	}
	teamAIDs := make([]int, 0, teamSize)
	teamBIDs := make([]int, 0, teamSize)
	teamANames := make([]string, 0, teamSize)
	teamBNames := make([]string, 0, teamSize)
	sumA, sumB := 0, 0
	for _, p := range activePlayers {
		mmr := mmrMap[p.ID]
		// The greedy pick must respect the roster size: without the capacity
		// check a lopsided MMR spread kept feeding the lighter team and produced
		// matches like 6v4.
		takeA := sumA <= sumB
		if len(teamAIDs) >= teamSize {
			takeA = false
		} else if len(teamBIDs) >= teamSize {
			takeA = true
		}

		if takeA {
			teamAIDs = append(teamAIDs, p.ID)
			teamANames = append(teamANames, p.Name)
			sumA += mmr
		} else {
			teamBIDs = append(teamBIDs, p.ID)
			teamBNames = append(teamBNames, p.Name)
			sumB += mmr
		}
	}
	avgA := float64(sumA) / float64(len(teamAIDs))
	avgB := float64(sumB) / float64(len(teamBIDs))
	guildID := b.guildID(i.GuildID)
	matchID, err := b.services.Lobby.CreateMatch(ctx, guildID, teamAIDs[0], teamBIDs[0], teamAIDs, teamBIDs)
	if err != nil {
		b.logger.Error("balance: failed to create match: %v", err)
		b.respondMessage(s, i, "Ошибка создания матча", true)
		return
	}

	embed := b.buildMatchEmbed(matchID, teamANames[0], teamBNames[0], teamANames, teamBNames)
	embed.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Авто-баланс | Team A avg: %.0f | Team B avg: %.0f", avgA, avgB)}
	b.publishCreatedMatch(ctx, s, i, matchID, teamAIDs, teamBIDs, embed, teamANames[0], teamBNames[0])
	b.logger.Info("balance: match #%d auto-balanced | Team A avg: %.0f | Team B avg: %.0f", matchID, avgA, avgB)
}
