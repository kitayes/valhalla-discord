package discord

import (
	"fmt"
	"sort"
	"strings"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

// =====================================================================
// Phase 5: Full Discord Matchmaking — Referee UI, Captains, Team Dist, Atomic Closure
// =====================================================================

const (
	// create_match command
	selectMenuCaptainA = "create_match_captain_a"
	selectMenuCaptainB = "create_match_captain_b"
	selectMenuTeamA    = "create_match_team_a"
	selectMenuTeamB    = "create_match_team_b"

	// match closure buttons
	buttonTeamAWin = "match_team_a_win"
	buttonTeamBWin = "match_team_b_win"

	// embed colors
	matchEmbedColor = 0x9B59B6
	winColor        = 0x2ECC71
	loseColor       = 0xE74C3C
)

// =====================================================================
// Command Registration
// =====================================================================

func (b *Bot) newCreateMatchCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "create_match",
		Description: "Создать матч из активного лобби (Только SUDЬЯ / Рефери)",
	}
}

// MMR-based auto-balance
func (b *Bot) newBalanceCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "balance",
		Description: "Автоматически разделить 10 игроков из лобби на 2 команды с минимальной разницей MMR (Только SUDЬЯ)",
	}
}

// =====================================================================
// Component Handler Registration
// =====================================================================

func (b *Bot) RegisterMatchHandlers() {
	b.session.AddHandler(b.onMatchSelectMenu)
	b.session.AddHandler(b.onMatchWinButton)
}

// =====================================================================
// Step 1: handleCreateMatch — Referee initiates match creation
// =====================================================================

func (b *Bot) handleCreateMatch(s *discordgo.Session, i *discordgo.Interaction) {
	// Referee role check
	if !b.isReferee(i.Member) {
		b.respondMessage(s, i, "⛔ Только рефери (роль SUDЬЯ) могут создавать матчи.", true)
		return
	}

	activePlayers := b.services.Lobby.GetActivePlayers()
	if len(activePlayers) < 10 {
		b.respondMessage(s, i,
			fmt.Sprintf("⚠️ Недостаточно игроков в лобби. Нужно 10, сейчас %d.", len(activePlayers)),
			true,
		)
		return
	}

	// Sort by ID for consistent display
	sort.Slice(activePlayers, func(a, b int) bool {
		return activePlayers[a].ID < activePlayers[b].ID
	})

	// Build options for player select menus
	var playerOptions []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		playerOptions = append(playerOptions, discordgo.SelectMenuOption{
			Label: p.Name,
			Value: fmt.Sprintf("%d", p.ID),
		})
	}

	// Step 1: Select Captain A
	minOne := 1
	maxOne := 1
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "🛡️ **Шаг 1/4: Выберите Капитана A**",
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							CustomID:    selectMenuCaptainA,
							Placeholder: "Выберите Капитана A...",
							MinValues:   &minOne,
							MaxValues:   maxOne,
							Options:     playerOptions,
						},
					},
				},
			},
		},
	})
}

// =====================================================================
// Select Menu Router
// =====================================================================

func (b *Bot) onMatchSelectMenu(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	customID := data.CustomID

	if customID == selectMenuCaptainA {
		b.handleSelectCaptainA(s, i)
		return
	}
	if strings.HasPrefix(customID, selectMenuCaptainB) {
		b.handleSelectCaptainB(s, i)
		return
	}
	if strings.HasPrefix(customID, selectMenuTeamA) {
		b.handleSelectTeamA(s, i)
		return
	}
	if customID == selectMenuTeamB {
		b.handleSelectTeamB(s, i)
		return
	}
}

// =====================================================================
// Step 2: handleSelectCaptainA — Captain A selected, now pick Captain B
// =====================================================================

func (b *Bot) handleSelectCaptainA(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.MessageComponentData().Values) == 0 {
		return
	}

	captainAID := i.MessageComponentData().Values[0]
	captainAName := b.getPlayerName(captainAID)

	activePlayers := b.services.Lobby.GetActivePlayers()

	// Build player options excluding Captain A
	var options []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		if fmt.Sprintf("%d", p.ID) == captainAID {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: p.Name,
			Value: fmt.Sprintf("%d", p.ID),
		})
	}

	// Store captainAID in the custom_id for next step (using separate select menu)
	minOne := 1
	maxOne := 1
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("🛡️ Капитан A: **%s**\n\n**Шаг 2/4: Выберите Капитана B**", captainAName),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							CustomID:    fmt.Sprintf("%s_%s", selectMenuCaptainB, captainAID),
							Placeholder: "Выберите Капитана B...",
							MinValues:   &minOne,
							MaxValues:   maxOne,
							Options:     options,
						},
					},
				},
			},
		},
	})
}

// =====================================================================
// Step 3: handleSelectCaptainB — Both captains selected, now pick Team A (4 players)
// =====================================================================

func (b *Bot) handleSelectCaptainB(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.MessageComponentData().Values) == 0 {
		return
	}

	captainBID := i.MessageComponentData().Values[0]
	captainBName := b.getPlayerName(captainBID)

	// Extract captainAID from the CustomID suffix
	customID := i.MessageComponentData().CustomID
	parts := strings.SplitN(customID, "_", 4) // create_match_captain_b_<captainAID>
	captainAID := parts[len(parts)-1]
	captainAName := b.getPlayerName(captainAID)

	activePlayers := b.services.Lobby.GetActivePlayers()

	// Build player options excluding both captains
	var options []discordgo.SelectMenuOption
	for _, p := range activePlayers {
		pid := fmt.Sprintf("%d", p.ID)
		if pid == captainAID || pid == captainBID {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: p.Name,
			Value: pid,
		})
	}

	minFour := 4
	maxFour := 4
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("🛡️ Капитан A: **%s**\n🛡️ Капитан B: **%s**\n\n**Шаг 3/4: Выберите 4 игроков для Команды A**",
				captainAName, captainBName),
			Flags: discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							CustomID:    fmt.Sprintf("%s_%s_%s", selectMenuTeamA, captainAID, captainBID),
							Placeholder: "Выберите 4 игроков для Команды A...",
							MinValues:   &minFour,
							MaxValues:   maxFour,
							Options:     options,
						},
					},
				},
			},
		},
	})
}

// =====================================================================
// Step 4: handleSelectTeamA — Team A selected, remaining players go to Team B
// =====================================================================

func (b *Bot) handleSelectTeamA(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.MessageComponentData().Values) < 4 {
		return
	}

	teamAValues := i.MessageComponentData().Values

	// Parse customID: create_match_team_a_<captainA>_<captainB>
	customID := i.MessageComponentData().CustomID
	parts := strings.SplitN(customID, "_", 5)
	captainAID := parts[len(parts)-2]
	captainBID := parts[len(parts)-1]
	captainAName := b.getPlayerName(captainAID)
	captainBName := b.getPlayerName(captainBID)

	// Team A = captainAID + selected 4 players
	teamAIDs := []int{parseID(captainAID)}
	var teamANames []string
	teamANames = append(teamANames, captainAName)
	for _, v := range teamAValues {
		teamAIDs = append(teamAIDs, parseID(v))
		teamANames = append(teamANames, b.getPlayerName(v))
	}

	// Team B = captainBID + all remaining active players
	activePlayers := b.services.Lobby.GetActivePlayers()
	teamASet := make(map[int]bool)
	for _, id := range teamAIDs {
		teamASet[id] = true
	}

	teamBIDs := []int{parseID(captainBID)}
	var teamBNames []string
	teamBNames = append(teamBNames, captainBName)

	for _, p := range activePlayers {
		if !teamASet[p.ID] && p.ID != parseID(captainBID) && p.ID != parseID(captainAID) {
			teamBIDs = append(teamBIDs, p.ID)
			teamBNames = append(teamBNames, p.Name)
		}
	}

	// Validate team sizes
	if len(teamBIDs) != 5 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("⚠️ Ошибка распределения команд. Команда A: %d, Команда B: %d. Попробуйте заново.", len(teamAIDs), len(teamBIDs)),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Create the match in the database
	guildID := i.GuildID
	if guildID == "" {
		guildID = defaultGuildID
	}

	matchID, err := b.services.Lobby.CreateMatch(guildID, parseID(captainAID), parseID(captainBID), teamAIDs, teamBIDs)
	if err != nil {
		b.logger.Error("match: failed to create: %v", err)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⚠️ Ошибка при создании матча: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Build public match embed
	embed := b.buildMatchEmbed(matchID, captainAName, captainBName, teamANames, teamBNames)

	// Send public message with Team Win buttons (visible only to referees)
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Team A WIN",
							Style:    discordgo.SuccessButton,
							CustomID: fmt.Sprintf("%s_%d", buttonTeamAWin, matchID),
							Emoji:    &discordgo.ComponentEmoji{Name: "🏆"},
						},
						discordgo.Button{
							Label:    "Team B WIN",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("%s_%d", buttonTeamBWin, matchID),
							Emoji:    &discordgo.ComponentEmoji{Name: "🏆"},
						},
					},
				},
			},
		},
	})

	b.logger.Info("match: #%d created | Team A: %s vs Team B: %s", matchID,
		strings.Join(teamANames, ", "), strings.Join(teamBNames, ", "))

	// Notify Telegram about the new match asynchronously
	go b.notifyTelegramMatchLive(matchID, captainAName, captainBName)
}

// =====================================================================
// Step 5: handleSelectTeamB — (Not used; Team B is auto-selected)
// =====================================================================

func (b *Bot) handleSelectTeamB(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Team B is auto-computed, this is a no-op fallback.
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Команда B автоматически сформирована из оставшихся игроков.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// =====================================================================
// Match Closure: Team A WIN / Team B WIN buttons (Referee only)
// =====================================================================

func (b *Bot) onMatchWinButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	customID := data.CustomID

	if !strings.HasPrefix(customID, buttonTeamAWin) && !strings.HasPrefix(customID, buttonTeamBWin) {
		return
	}

	// Referee role check
	if !b.isReferee(i.Member) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⛔ Только рефери (роль SUDЬЯ) могут завершать матчи.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Parse match ID from custom ID
	var winner string
	var matchID int
	if strings.HasPrefix(customID, buttonTeamAWin) {
		winner = "Team A"
		fmt.Sscanf(customID, buttonTeamAWin+"_%d", &matchID)
	} else {
		winner = "Team B"
		fmt.Sscanf(customID, buttonTeamBWin+"_%d", &matchID)
	}

	// Atomic state transition: ACTIVE → PROCESSING → FINISHED
	success, err := b.services.Lobby.AtomicMatchClosure(matchID, winner)
	if err != nil {
		b.logger.Error("match: atomic closure failed for #%d: %v", matchID, err)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⚠️ Ошибка системы при закрытии матча. Операция отменена.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if !success {
		// Double-click or already processed — silently ignore
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⏳ Этот матч уже обрабатывается или завершён.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Process Elo-MMR (MVP/SVP bonuses applied when AI processes screenshots)
	newMMRs, tierChanges, err := b.services.EloService.ProcessMatchResult(matchID, winner, "", "")
	if err != nil {
		b.logger.Error("elo: failed to process match #%d: %v", matchID, err)
	} else {
		if len(newMMRs) > 0 {
			b.logger.Info("elo: match #%d — %d players' MMR updated", matchID, len(newMMRs))
			go b.services.Lobby.SyncMMRToSheet(newMMRs)
		}
		// Announce tier promotions/demotions in guild channel
		for _, ch := range tierChanges {
			go b.services.EloService.AnnounceTierChanges(s, i.GuildID, []application.TierChange{ch})
		}
	}

	// Process Telegram bet payouts asynchronously
	go b.processTelegramPayout(matchID, winner)

	// Update the embed with the winner
	match, _ := b.services.Lobby.GetMatch(matchID)
	if match != nil {
		embed := b.buildMatchResultEmbed(match, winner)

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{embed},
				Components: []discordgo.MessageComponent{}, // Remove buttons
			},
		})

		// Archive the match thread if one exists
		if match.ThreadID != "" {
			go b.archiveThreadIfMatchFinished(s, match.ThreadID)
		}
	}
}

// =====================================================================
// Embed Builders
// =====================================================================

func (b *Bot) buildMatchEmbed(matchID int, captainA, captainB string, teamA, teamB []string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("⚔️ МАТЧ #%d АКТИВЕН", matchID),
		Description: fmt.Sprintf("Капитаны: **%s** vs **%s**\n\nГолосуйте за победителя!", captainA, captainB),
		Color:       matchEmbedColor,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🛡️ Team A",
				Value:  formatPlayerList(teamA),
				Inline: true,
			},
			{
				Name:   "⚔️ Team B",
				Value:  formatPlayerList(teamB),
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Только рефери (SUDЬЯ) могут завершить матч",
		},
	}
}

func (b *Bot) buildMatchResultEmbed(match *models.LobbyMatch, winner string) *discordgo.MessageEmbed {
	color := winColor
	if winner == "Team B" {
		color = loseColor
	}

	// Get player names for both teams
	teamANames := b.getPlayerNames(match.TeamAIDs)
	teamBNames := b.getPlayerNames(match.TeamBIDs)

	winTeamNames := teamANames
	winTeamName := "Team A"
	if winner == "Team B" {
		winTeamNames = teamBNames
		winTeamName = "Team B"
	}

	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("🏁 МАТЧ #%d ЗАВЕРШЁН", match.ID),
		Description: fmt.Sprintf("**Победила: %s!**\n\n%s", winTeamName, formatPlayerList(winTeamNames)),
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🛡️ Team A",
				Value:  formatPlayerList(teamANames),
				Inline: true,
			},
			{
				Name:   "⚔️ Team B",
				Value:  formatPlayerList(teamBNames),
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Матч #%d | Elo-MMR обновлён", match.ID),
		},
	}
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

func (b *Bot) getPlayerName(idStr string) string {
	var id int
	fmt.Sscanf(idStr, "%d", &id)
	name, err := b.services.MatchService.GetPlayerNameByID(id)
	if err != nil {
		return fmt.Sprintf("ID:%d", id)
	}
	return name
}

func (b *Bot) getPlayerNames(ids []int) []string {
	names := make([]string, len(ids))
	for i, id := range ids {
		name, err := b.services.MatchService.GetPlayerNameByID(id)
		if err != nil {
			name = fmt.Sprintf("ID:%d", id)
		}
		names[i] = name
	}
	return names
}

func parseID(s string) int {
	var id int
	fmt.Sscanf(s, "%d", &id)
	return id
}

// =====================================================================
// Cross-Platform: Telegram Notification (called asynchronously)
// =====================================================================

func (b *Bot) notifyTelegramMatchLive(matchID int, captainA, captainB string) {
	if b.services.BettingService == nil {
		return
	}
	b.logger.Info("telegram: notifying match #%d is live (captains: %s vs %s)", matchID, captainA, captainB)
	// Telegram bot will pick this up via a shared channel or direct API call.
	// For now, the Telegram bot polls for ACTIVE matches.
}

func (b *Bot) processTelegramPayout(matchID int, winningTeam string) {
	if b.services.BettingService == nil {
		return
	}
	payouts, err := b.services.BettingService.ProcessPayout(matchID, winningTeam)
	if err != nil {
		b.logger.Error("betting: payout failed for match #%d: %v", matchID, err)
		return
	}
	if payouts != nil {
		b.logger.Info("betting: match #%d — %d winners paid out", matchID, len(payouts))
	} else {
		b.logger.Info("betting: match #%d — no bets to pay out", matchID)
	}
}

// =====================================================================
// MMR Formatting for Tier Display
// =====================================================================

func (b *Bot) formatMMRField(playerIDs []int, mmrMap map[int]int) string {
	var sb strings.Builder
	for _, pid := range playerIDs {
		mmr, ok := mmrMap[pid]
		if !ok {
			mmr = domain.BaseMMR
		}
		name, err := b.services.MatchService.GetPlayerNameByID(pid)
		if err != nil {
			name = fmt.Sprintf("ID:%d", pid)
		}
		tier := domain.DetermineTier(mmr)
		sb.WriteString(fmt.Sprintf("• **%s** — %d MMR (%s)\n", name, mmr, tier))
	}
	return sb.String()
}

// =====================================================================
// 2.1 Auto-Balance: greedy algorithm to split 10 players into 2 teams
//     minimizing average MMR difference
// =====================================================================

// handleBalance auto-balances 10 players from the lobby into 2 teams.
func (b *Bot) handleBalance(s *discordgo.Session, i *discordgo.Interaction) {
	if !b.isReferee(i.Member) {
		b.respondMessage(s, i, "⛔ Только рефери (роль SUDЬЯ) могут использовать авто-баланс.", true)
		return
	}

	activePlayers := b.services.Lobby.GetActivePlayers()
	if len(activePlayers) < 10 {
		b.respondMessage(s, i,
			fmt.Sprintf("⚠️ Нужно 10 игроков в лобби, сейчас %d.", len(activePlayers)),
			true,
		)
		return
	}

	// Get MMRs for all active players
	playerIDs := make([]int, len(activePlayers))
	for idx, p := range activePlayers {
		playerIDs[idx] = p.ID
	}

	mmrMap, err := b.services.Lobby.GetPlayerMMRsBatch(playerIDs)
	if err != nil {
		b.logger.Error("balance: failed to get MMRs: %v", err)
		b.respondMessage(s, i, "⚠️ Ошибка получения MMR игроков.", true)
		return
	}

	// Greedy MMR-balance: sort by MMR descending, alternate between teams
	sort.Slice(activePlayers, func(a, b int) bool {
		mmrA := mmrMap[activePlayers[a].ID]
		mmrB := mmrMap[activePlayers[b].ID]
		return mmrA > mmrB // descending MMR
	})

	var teamAIDs, teamBIDs []int
	var teamANames, teamBNames []string
	sumA, sumB := 0, 0

	for _, p := range activePlayers {
		mmr := mmrMap[p.ID]
		// Assign to the team with lower total MMR to balance
		if sumA <= sumB {
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

	// Choose captains as the highest MMR player on each team
	captainAID := teamAIDs[0]
	captainBID := teamBIDs[0]

	guildID := i.GuildID
	if guildID == "" {
		guildID = defaultGuildID
	}

	matchID, err := b.services.Lobby.CreateMatch(guildID, captainAID, captainBID, teamAIDs, teamBIDs)
	if err != nil {
		b.logger.Error("balance: failed to create match: %v", err)
		b.respondMessage(s, i, "⚠️ Ошибка создания матча: "+err.Error(), true)
		return
	}

	embed := b.buildMatchEmbed(matchID, teamANames[0], teamBNames[0], teamANames, teamBNames)
	embed.Footer = &discordgo.MessageEmbedFooter{
		Text: fmt.Sprintf("Авто-баланс | Team A avg: %.0f MMR | Team B avg: %.0f MMR", avgA, avgB),
	}

	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Team A WIN",
							Style:    discordgo.SuccessButton,
							CustomID: fmt.Sprintf("%s_%d", buttonTeamAWin, matchID),
							Emoji:    &discordgo.ComponentEmoji{Name: "🏆"},
						},
						discordgo.Button{
							Label:    "Team B WIN",
							Style:    discordgo.DangerButton,
							CustomID: fmt.Sprintf("%s_%d", buttonTeamBWin, matchID),
							Emoji:    &discordgo.ComponentEmoji{Name: "🏆"},
						},
					},
				},
			},
		},
	})

	b.logger.Info("balance: match #%d auto-balanced | Team A: %d (avg %.0f) vs Team B: %d (avg %.0f)",
		matchID, len(teamAIDs), avgA, len(teamBIDs), avgB)
}

// =====================================================================
// 2.2 Dynamic Betting Odds based on MMR difference
// =====================================================================

// CalculateBettingOdds returns payout multipliers for each team based on MMR difference.
// Higher MMR team gets lower multiplier (safer bet), lower MMR team gets higher multiplier.
func CalculateBettingOdds(avgMMRTeamA, avgMMRTeamB float64) (multiplierA, multiplierB float64) {
	// Base multiplier is 1.5x for equal teams
	baseMultiplier := 1.5

	// Calculate MMR ratio
	mmrDiff := avgMMRTeamA - avgMMRTeamB // positive = Team A stronger
	adjustment := mmrDiff / 400.0        // 400 MMR difference = 1.0 adjustment

	multiplierA = baseMultiplier - adjustment*0.5
	multiplierB = baseMultiplier + adjustment*0.5

	// Clamp to sensible range
	if multiplierA < 1.1 {
		multiplierA = 1.1
	}
	if multiplierA > 3.0 {
		multiplierA = 3.0
	}
	if multiplierB < 1.1 {
		multiplierB = 1.1
	}
	if multiplierB > 3.0 {
		multiplierB = 3.0
	}

	return multiplierA, multiplierB
}
