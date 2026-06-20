package application

import (
	"fmt"

	"blackwatch/internal/domain"

	"github.com/bwmarrin/discordgo"
)

const (
	mvpBonusMMR = 5  // extra MMR for MVP on winning team
	svpBonusMMR = -5 // reduced MMR loss for SVPG on losing team (applied as +10 relative to standard -delta)
)

// EloService handles Elo-MMR calculations and tier role updates.
type EloService struct {
	logger     Logger
	matchSvc   MatchService
	lobbyMatch LobbyMatchRepository
}

// TierChange represents a player's rank change event.
type TierChange struct {
	PlayerID int
	OldTier  domain.Tier
	NewTier  domain.Tier
	MMR      int
}

// NewEloService creates a new EloService.
func NewEloService(logger Logger, matchSvc MatchService, lobbyMatch LobbyMatchRepository) *EloService {
	return &EloService{
		logger:     logger,
		matchSvc:   matchSvc,
		lobbyMatch: lobbyMatch,
	}
}

// ProcessMatchResult calculates Elo-MMR shifts (including MVP/SVP bonuses) and updates all players.
// Returns new MMRs map and any tier changes.
func (s *EloService) ProcessMatchResult(matchID int, winner string, mvpName, svpName string) (map[int]int, []TierChange, error) {
	match, err := s.lobbyMatch.GetByID(matchID)
	if err != nil {
		return nil, nil, fmt.Errorf("elo: failed to get match %d: %w", matchID, err)
	}

	allPlayerIDs := append(match.TeamAIDs, match.TeamBIDs...)
	mmrs, err := s.lobbyMatch.GetPlayerMMRsBatch(allPlayerIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("elo: failed to get MMRs: %w", err)
	}

	teamAMMRs := extractMMRs(match.TeamAIDs, mmrs)
	teamBMMRs := extractMMRs(match.TeamBIDs, mmrs)

	avgMMRTeamA := domain.CalculateAverageMMR(teamAMMRs)
	avgMMRTeamB := domain.CalculateAverageMMR(teamBMMRs)

	delta := domain.CalculateEloShift(avgMMRTeamA, avgMMRTeamB, winner)

	s.logger.Info("elo: match #%d | Team A avg: %.0f | Team B avg: %.0f | winner: %s | delta: %.0f",
		matchID, avgMMRTeamA, avgMMRTeamB, winner, delta)

	newMMRs := make(map[int]int)
	deltaA := int(delta)
	deltaB := -int(delta)

	if winner == "Team B" {
		deltaA, deltaB = deltaB, deltaA
	}

	// Build a map of player name -> player ID for MVP/SVP lookup
	nameToID := make(map[string]int)
	for _, pid := range allPlayerIDs {
		name, err := s.matchSvc.GetPlayerNameByID(pid)
		if err == nil {
			nameToID[name] = pid
		}
	}

	// Apply MMR with MVP/SVP bonuses
	for _, pid := range match.TeamAIDs {
		currentMMR := mmrs[pid]
		bonus := 0
		name, _ := s.matchSvc.GetPlayerNameByID(pid)
		if name == mvpName {
			bonus = mvpBonusMMR
			s.logger.Info("elo: MVP bonus +%d for %s (ID: %d)", mvpBonusMMR, name, pid)
		}
		if name == svpName {
			bonus = svpBonusMMR
			s.logger.Info("elo: SVP bonus +10 for %s (ID: %d)", name, pid)
		}
		newMMR := domain.ClampMMR(currentMMR + deltaA + bonus)
		if err := s.lobbyMatch.UpdatePlayerMMR(pid, newMMR); err != nil {
			s.logger.Error("elo: failed to update MMR for player %d: %v", pid, err)
			continue
		}
		newMMRs[pid] = newMMR
	}

	for _, pid := range match.TeamBIDs {
		currentMMR := mmrs[pid]
		bonus := 0
		name, _ := s.matchSvc.GetPlayerNameByID(pid)
		if name == mvpName {
			bonus = mvpBonusMMR
			s.logger.Info("elo: MVP bonus +%d for %s (ID: %d)", mvpBonusMMR, name, pid)
		}
		if name == svpName {
			bonus = 10 // Reduce loss by 10 MMR
			s.logger.Info("elo: SVP mitigation +10 for %s (ID: %d)", name, pid)
		}
		newMMR := domain.ClampMMR(currentMMR + deltaB + bonus)
		if err := s.lobbyMatch.UpdatePlayerMMR(pid, newMMR); err != nil {
			s.logger.Error("elo: failed to update MMR for player %d: %v", pid, err)
			continue
		}
		newMMRs[pid] = newMMR
	}

	// Detect tier changes
	var tierChanges []TierChange
	for playerID, newMMR := range newMMRs {
		oldMMR := mmrs[playerID]
		oldTier := domain.DetermineTier(oldMMR)
		newTier := domain.DetermineTier(newMMR)
		if oldTier != newTier && oldTier != domain.TierUnranked && newTier != domain.TierUnranked {
			tierChanges = append(tierChanges, TierChange{
				PlayerID: playerID,
				OldTier:  oldTier,
				NewTier:  newTier,
				MMR:      newMMR,
			})
		}
	}

	s.logger.Info("elo: match #%d processed — %d players updated, %d tier changes", matchID, len(newMMRs), len(tierChanges))
	return newMMRs, tierChanges, nil
}

// SyncTierRoles updates Discord tier roles for all players affected by an MMR change.
func (s *EloService) SyncTierRoles(session *discordgo.Session, guildID string, tierRoleMap map[domain.Tier]string, newMMRs map[int]int) error {
	for playerID, mmr := range newMMRs {
		tier := domain.DetermineTier(mmr)
		discordID, err := s.matchSvc.GetDiscordIDByPlayerID(playerID)
		if err != nil {
			s.logger.Warn("elo: cannot sync tier for player %d — %v", playerID, err)
			continue
		}
		for _, roleID := range tierRoleMap {
			session.GuildMemberRoleRemove(guildID, discordID, roleID)
		}
		newRoleID, ok := tierRoleMap[tier]
		if !ok || tier == domain.TierUnranked {
			continue
		}
		if err := session.GuildMemberRoleAdd(guildID, discordID, newRoleID); err != nil {
			s.logger.Error("elo: failed to add role %s (%s) to %s: %v", newRoleID, tier, discordID, err)
			continue
		}
		s.logger.Info("elo: player %d → %s (%d MMR) — role updated", playerID, tier, mmr)
	}
	return nil
}

// AnnounceTierChanges sends beautiful tier promotion/demotion embeds to the channel.
func (s *EloService) AnnounceTierChanges(session *discordgo.Session, guildID string, changes []TierChange) {
	for _, ch := range changes {
		name, err := s.matchSvc.GetPlayerNameByID(ch.PlayerID)
		if err != nil {
			name = fmt.Sprintf("ID:%d", ch.PlayerID)
		}
		discordID, err := s.matchSvc.GetDiscordIDByPlayerID(ch.PlayerID)
		if err != nil {
			s.logger.Warn("tier announce: no discord_id for player %d", ch.PlayerID)
			continue
		}

		isPromotion := tierRank(ch.NewTier) > tierRank(ch.OldTier)
		emoji := "🎉"
		action := "ПОВЫШЕНИЕ РАНГА!"
		color := 0x2ECC71
		if !isPromotion {
			emoji = "📉"
			action = "ПОНИЖЕНИЕ РАНГА"
			color = 0xE74C3C
		}

		embed := &discordgo.MessageEmbed{
			Title: fmt.Sprintf("%s %s", emoji, action),
			Description: fmt.Sprintf(
				"Игрок <@%s> перешагнул отметку в **%d MMR** и получает ранг **%s**!\n\n"+
					"Было: %s → Стало: %s",
				discordID, ch.MMR, ch.NewTier,
				domain.FormatTierDisplay(ch.OldTier),
				domain.FormatTierDisplay(ch.NewTier),
			),
			Color: color,
		}

		_, err = session.ChannelMessageSendEmbed(guildID, embed)
		if err != nil {
			s.logger.Error("tier announce: failed to send embed for player %s: %v", name, err)
		} else {
			s.logger.Info("tier announce: %s %d → %s (%s)", name, ch.OldTier, ch.NewTier, action)
		}
	}
}

// tierRank returns a numeric rank for a tier (higher = better).
func tierRank(t domain.Tier) int {
	switch t {
	case domain.TierObsidian:
		return 4
	case domain.TierOnyx:
		return 3
	case domain.TierCarbon:
		return 2
	case domain.TierGraphite:
		return 1
	default:
		return 0
	}
}

func extractMMRs(playerIDs []int, mmrMap map[int]int) []int {
	mmrs := make([]int, 0, len(playerIDs))
	for _, pid := range playerIDs {
		mmr, ok := mmrMap[pid]
		if !ok {
			mmr = domain.BaseMMR
		}
		mmrs = append(mmrs, mmr)
	}
	return mmrs
}
