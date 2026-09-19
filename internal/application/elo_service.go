package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"blackwatch/internal/domain"

	"github.com/bwmarrin/discordgo"
)

const (
	// mvpBonusMMR is extra MMR for the MVP — the best player on the winning team.
	mvpBonusMMR = 5
	// svpMitigationMMR softens the loss for the SVPG — the best player on the
	// losing team. It is a mitigation, so it is always positive: the previous
	// -5 on Team A punished the medal instead of rewarding it.
	svpMitigationMMR = 10
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
func (s *EloService) ProcessMatchResult(ctx context.Context, matchID int, winner string, mvpName, svpName string) (map[int]int, []TierChange, error) {
	match, err := s.lobbyMatch.GetByID(ctx, matchID)
	if err != nil {
		return nil, nil, fmt.Errorf("elo: failed to get match %d: %w", matchID, err)
	}

	allPlayerIDs := make([]int, 0, len(match.TeamAIDs)+len(match.TeamBIDs))
	allPlayerIDs = append(allPlayerIDs, match.TeamAIDs...)
	allPlayerIDs = append(allPlayerIDs, match.TeamBIDs...)

	mmrs, err := s.lobbyMatch.GetPlayerMMRsBatch(ctx, allPlayerIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("elo: failed to get MMRs: %w", err)
	}

	// The batch only returns rows that exist, so resolve the default once, here.
	// extractMMRs substituted BaseMMR for a missing player when averaging, while
	// the update loop below read the raw map and got 0 — the same player counted
	// as 1000 for the team average and then had their rating written as the
	// bottom of the scale.
	for _, pid := range allPlayerIDs {
		if _, ok := mmrs[pid]; !ok {
			s.logger.Warn("elo: player %d has no MMR row, defaulting to %d", pid, domain.BaseMMR)
			mmrs[pid] = domain.BaseMMR
		}
	}

	teamAMMRs := extractMMRs(match.TeamAIDs, mmrs)
	teamBMMRs := extractMMRs(match.TeamBIDs, mmrs)

	avgMMRTeamA := domain.CalculateAverageMMR(teamAMMRs)
	avgMMRTeamB := domain.CalculateAverageMMR(teamBMMRs)

	delta := domain.CalculateEloShift(avgMMRTeamA, avgMMRTeamB, winner)

	// Medals come off the match screenshot; fall back to whatever was stored on
	// the match when the caller has nothing more specific.
	if mvpName == "" {
		mvpName = match.MVP
	}
	if svpName == "" {
		svpName = match.SVP
	}

	s.logger.Info("elo: match #%d | Team A avg: %.0f | Team B avg: %.0f | winner: %s | delta: %.0f | MVP: %q | SVP: %q",
		matchID, avgMMRTeamA, avgMMRTeamB, winner, delta, mvpName, svpName)

	// One query for every participant instead of one per participant inside the
	// update loop.
	names, err := s.matchSvc.GetPlayerNamesByIDs(ctx, allPlayerIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("elo: failed to get player names: %w", err)
	}

	newMMRs := make(map[int]int)
	deltaA, deltaB := teamDeltas(delta)

	// Apply MMR with MVP/SVP bonuses.
	//
	// Each medal is only offered to the side that can hold it: the MVP is the
	// best player on the winning team, the SVPG the best on the losing one. The
	// names come out of the screenshot parser, so an unscoped match on the name
	// alone handed the MVP bonus to whoever happened to carry that nick — a
	// misread, or a namesake on the other side.
	teams := []struct {
		playerIDs []int
		delta     int
		won       bool
	}{
		{playerIDs: match.TeamAIDs, delta: deltaA, won: winner == domain.TeamA},
		{playerIDs: match.TeamBIDs, delta: deltaB, won: winner == domain.TeamB},
	}

	// A failed update is not survivable by skipping the player: the match is
	// already closed by the time we get here, so half-applied ratings can never
	// be replayed. Collect every failure and report it instead of returning nil.
	var updateErrs []error

	for _, team := range teams {
		awarded := 0
		for _, pid := range team.playerIDs {
			name := names[pid]
			bonus := sideMedalBonus(name, team.won, mvpName, svpName)
			if bonus != 0 {
				// One medal per side. Two players sharing a nick would otherwise
				// both collect it, and the pair of them would quietly out-earn
				// the actual awardee.
				if awarded > 0 {
					s.logger.Warn("elo: match #%d — %q matches an already awarded medal, skipping the duplicate",
						matchID, name)
					bonus = 0
				} else {
					awarded++
					s.logger.Info("elo: medal bonus %+d for %s (ID: %d)", bonus, name, pid)
				}
			}

			newMMR := domain.ClampMMR(mmrs[pid] + team.delta + bonus)
			// matchID goes with the change so a player can see which match cost
			// or earned them the points, and so a replay is recognised as one.
			if err := s.lobbyMatch.UpdatePlayerMMR(ctx, pid, newMMR, matchID); err != nil {
				s.logger.Error("elo: failed to update MMR for player %d: %v", pid, err)
				updateErrs = append(updateErrs, fmt.Errorf("player %d: %w", pid, err))
				continue
			}
			newMMRs[pid] = newMMR
		}
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
	if len(updateErrs) > 0 {
		return newMMRs, tierChanges, fmt.Errorf("elo: match #%d applied to %d of %d players: %w",
			matchID, len(newMMRs), len(allPlayerIDs), errors.Join(updateErrs...))
	}
	return newMMRs, tierChanges, nil
}

// SyncTierRoles updates Discord tier roles for all players affected by an MMR
// change. Roles the player should not have are removed first, then the role for
// their current tier is granted.
//
// Only roles that are actually configured are touched, and a role the player
// already holds is left alone — every removal is a Discord API call, and the
// bot is rate limited per guild.
func (s *EloService) SyncTierRoles(ctx context.Context, session *discordgo.Session, guildID string, tierRoleMap map[domain.Tier]string, newMMRs map[int]int) error {
	if len(tierRoleMap) == 0 {
		return nil
	}

	// Every failure is collected rather than only logged: the method already
	// declared an error return that no path could ever fill, so the caller's
	// `if err != nil` was decoration.
	var errs []error

	for playerID, mmr := range newMMRs {
		tier := domain.DetermineTier(mmr)
		discordID, err := s.matchSvc.GetDiscordIDByPlayerID(ctx, playerID)
		if err != nil {
			// Not an error: a player with no Discord account bound simply has
			// no roles to sync.
			s.logger.Warn("elo: cannot sync tier for player %d — %v", playerID, err)
			continue
		}

		wantRoleID, wantsRole := tierRoleMap[tier]
		if tier == domain.TierUnranked {
			wantsRole = false
		}

		member, err := session.GuildMember(guildID, discordID)
		if err != nil {
			s.logger.Warn("elo: cannot read member %s in guild %s — %v", discordID, guildID, err)
			errs = append(errs, fmt.Errorf("member %s: %w", discordID, err))
			continue
		}
		held := make(map[string]struct{}, len(member.Roles))
		for _, roleID := range member.Roles {
			held[roleID] = struct{}{}
		}

		for _, roleID := range tierRoleMap {
			if roleID == "" || (wantsRole && roleID == wantRoleID) {
				continue
			}
			if _, has := held[roleID]; !has {
				continue
			}
			if err := session.GuildMemberRoleRemove(guildID, discordID, roleID); err != nil {
				s.logger.Warn("elo: failed to remove role %s from %s: %v", roleID, discordID, err)
				errs = append(errs, fmt.Errorf("remove role %s from %s: %w", roleID, discordID, err))
			}
		}

		if !wantsRole || wantRoleID == "" {
			continue
		}
		if _, has := held[wantRoleID]; has {
			continue // already correct
		}
		if err := session.GuildMemberRoleAdd(guildID, discordID, wantRoleID); err != nil {
			s.logger.Error("elo: failed to add role %s (%s) to %s: %v", wantRoleID, tier, discordID, err)
			errs = append(errs, fmt.Errorf("add role %s to %s: %w", wantRoleID, discordID, err))
			continue
		}
		s.logger.Info("elo: player %d → %s (%d MMR) — role updated", playerID, tier, mmr)
	}
	return errors.Join(errs...)
}

// AnnounceTierChanges sends tier promotion/demotion embeds to a channel.
//
// channelID must be a channel — this used to be called with the guild ID, so
// every announcement failed with "Unknown Channel" and nobody ever saw a rank
// change posted.
func (s *EloService) AnnounceTierChanges(ctx context.Context, session *discordgo.Session, channelID string, changes []TierChange) {
	if channelID == "" {
		s.logger.Warn("tier announce: no channel configured, skipping %d change(s)", len(changes))
		return
	}

	for _, ch := range changes {
		name, err := s.matchSvc.GetPlayerNameByID(ctx, ch.PlayerID)
		if err != nil {
			name = fmt.Sprintf("ID:%d", ch.PlayerID)
		}
		discordID, err := s.matchSvc.GetDiscordIDByPlayerID(ctx, ch.PlayerID)
		if err != nil {
			s.logger.Warn("tier announce: no discord_id for player %d", ch.PlayerID)
			continue
		}
		isPromotion := tierRank(ch.NewTier) > tierRank(ch.OldTier)
		action := "[ПОВЫШЕНИЕ РАНГА]"
		color := 0x2ECC71
		if !isPromotion {
			action = "[ПОНИЖЕНИЕ РАНГА]"
			color = 0xE74C3C
		}

		embed := &discordgo.MessageEmbed{
			Title: action,
			Description: fmt.Sprintf(
				"Игрок <@%s> перешагнул отметку в **%d MMR** и получает ранг **%s**!\n\n"+
					"Было: %s → Стало: %s",
				discordID, ch.MMR, ch.NewTier,
				domain.FormatTierDisplay(ch.OldTier),
				domain.FormatTierDisplay(ch.NewTier),
			),
			Color: color,
		}

		_, err = session.ChannelMessageSendEmbed(channelID, embed)
		if err != nil {
			s.logger.Error("tier announce: failed to send embed for player %s: %v", name, err)
		} else {
			s.logger.Info("tier announce: %s %s → %s (%s)", name, ch.OldTier, ch.NewTier, action)
		}
	}
}

// sideMedalBonus returns the MMR adjustment for a player, offering only the
// medal their side of the match can hold: the MVP is the best player on the
// winning team, the SVPG the best on the losing one.
//
// The names are read off a screenshot by the AI parser, so they are not
// trustworthy enough to award on their own. Matching a player against both
// medals regardless of side meant one misread nick — or a namesake across the
// scoreboard — handed the MVP bonus to somebody on the losing team, which the
// medal cannot describe.
func sideMedalBonus(playerName string, won bool, mvpName, svpName string) int {
	if won {
		return medalBonus(playerName, mvpName, "")
	}
	return medalBonus(playerName, "", svpName)
}

// medalBonus returns the MMR adjustment a player earns from their medal.
//
// Both awards are positive — the SVPG one is a mitigation, not a punishment. A
// name that matches neither medal gets nothing; if the same name somehow matches
// both, the MVP award wins. Callers go through sideMedalBonus, which is what
// decides which medals are on offer at all.
func medalBonus(playerName, mvpName, svpName string) int {
	if playerName == "" {
		return 0
	}
	switch {
	case strings.EqualFold(playerName, mvpName):
		return mvpBonusMMR
	case strings.EqualFold(playerName, svpName):
		return svpMitigationMMR
	default:
		return 0
	}
}

// teamDeltas splits an Elo shift into the per-team MMR changes.
//
// CalculateEloShift already returns the change *from Team A's point of view*
// (negative when Team B won), so the two teams simply take opposite signs.
// Swapping them again for a Team B win — as this code used to — handed the MMR
// to the losing side on every Team B victory.
func teamDeltas(delta float64) (deltaA, deltaB int) {
	deltaA = int(delta)
	return deltaA, -deltaA
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
