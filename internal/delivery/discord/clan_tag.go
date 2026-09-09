package discord

import (
	"context"
	"encoding/json"
	"time"

	"blackwatch/pkg/config"

	"github.com/bwmarrin/discordgo"
)

const (
	// clanCheckTTL is how long an applied clan verdict is trusted before the
	// member is looked at again.
	clanCheckTTL = 10 * time.Minute
	// clanCheckCacheMax triggers a sweep of stale verdicts.
	clanCheckCacheMax = 5000
)

// clanVerdict records the last clan-tag decision applied to a member.
type clanVerdict struct {
	hasClan bool
	at      time.Time
}

type memberUpdatePayload struct {
	User    userPayload `json:"user"`
	GuildID string      `json:"guild_id"`
}

type userPayload struct {
	ID            string       `json:"id"`
	Username      string       `json:"username"`
	Discriminator string       `json:"discriminator"`
	Avatar        string       `json:"avatar"`
	Bot           bool         `json:"bot"`
	Clan          *clanPayload `json:"clan"`
}

type clanPayload struct {
	IdentityGuildID string `json:"identity_guild_id"`
	IdentityEnabled bool   `json:"identity_enabled"`
	Tag             string `json:"tag"`
}

type presenceUpdatePayload struct {
	User    presenceUserPayload `json:"user"`
	GuildID string              `json:"guild_id"`
}

type presenceUserPayload struct {
	ID             string       `json:"id"`
	PrimaryGuildID string       `json:"primary_guild_id,omitempty"`
	Clan           *clanPayload `json:"clan,omitempty"`
}

// AddClanTagHandlers registers raw event handlers for tracking server clan tags.
func (b *Bot) AddClanTagHandlers(cfg *config.Config) {
	if !cfg.ClanTagEnabled || cfg.ClanTagRoleID == "" {
		b.logger.Info("Clan tag tracking is disabled or CLAN_TAG_ROLE_ID not set")
		return
	}

	targetGuildID := b.guildID("")

	b.logger.Info("Clan tag tracking enabled for guild %s with role %s", targetGuildID, cfg.ClanTagRoleID)

	// One handler for both event types. The context is derived only after the
	// event turns out to be interesting: this callback fires for every single
	// gateway event, and building a context with a timer for each of them (then
	// throwing it away) was pure overhead.
	b.session.AddHandler(func(s *discordgo.Session, event *discordgo.Event) {
		var userID string
		var hasOurClan bool

		switch event.Type {
		case "GUILD_MEMBER_UPDATE":
			var payload memberUpdatePayload
			if err := json.Unmarshal(event.RawData, &payload); err != nil {
				b.logger.Debug("clan_tag: failed to parse GUILD_MEMBER_UPDATE: %v", err)
				return
			}
			if payload.GuildID != targetGuildID {
				return
			}
			userID = payload.User.ID
			hasOurClan = payload.User.Clan != nil &&
				payload.User.Clan.IdentityGuildID == targetGuildID &&
				payload.User.Clan.IdentityEnabled

		case "PRESENCE_UPDATE":
			var payload presenceUpdatePayload
			if err := json.Unmarshal(event.RawData, &payload); err != nil {
				b.logger.Debug("clan_tag: failed to parse PRESENCE_UPDATE: %v", err)
				return
			}
			if payload.GuildID != targetGuildID {
				return
			}
			userID = payload.User.ID
			hasOurClan = payload.User.PrimaryGuildID == targetGuildID ||
				(payload.User.Clan != nil &&
					payload.User.Clan.IdentityGuildID == targetGuildID &&
					payload.User.Clan.IdentityEnabled)

		default:
			return
		}

		if userID == "" {
			return
		}

		// PRESENCE_UPDATE fires on every status change of every member, so the
		// same verdict would otherwise be re-checked (and hit the members API)
		// dozens of times per user per hour.
		if !b.clanCheckDue(userID, hasOurClan) {
			return
		}

		ctx, cancel := b.opContext(interactionTimeout)
		defer cancel()
		b.handleClanCheck(ctx, s, targetGuildID, cfg.ClanTagRoleID, userID, hasOurClan)
	})
}

// clanCheckDue reports whether a clan verdict for this user is worth acting on,
// suppressing repeats of a verdict already applied within clanCheckTTL.
func (b *Bot) clanCheckDue(userID string, hasClan bool) bool {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()

	now := time.Now()
	if seen, ok := b.clanCheckCache[userID]; ok &&
		seen.hasClan == hasClan && now.Sub(seen.at) < clanCheckTTL {
		return false
	}

	// Opportunistic sweep: this map is keyed by user and would otherwise grow
	// for the lifetime of the process.
	if len(b.clanCheckCache) > clanCheckCacheMax {
		for id, seen := range b.clanCheckCache {
			if now.Sub(seen.at) > clanCheckTTL {
				delete(b.clanCheckCache, id)
			}
		}
	}

	b.clanCheckCache[userID] = clanVerdict{hasClan: hasClan, at: now}
	return true
}

func (b *Bot) handleClanCheck(ctx context.Context, s *discordgo.Session, guildID, roleID, userID string, hasClan bool) {
	member, err := s.State.Member(guildID, userID)
	if err != nil {
		member, err = s.GuildMember(guildID, userID)
		if err != nil {
			b.logger.Debug("clan_tag: failed to get member %s: %v", userID, err)
			return
		}
	}

	hasRole := false
	for _, role := range member.Roles {
		if role == roleID {
			hasRole = true
			break
		}
	}

	if hasClan && !hasRole {
		err := s.GuildMemberRoleAdd(guildID, userID, roleID)
		if err != nil {
			b.logger.Error("clan_tag: failed to add role to user %s: %v", userID, err)
		} else {
			b.logger.Info("clan_tag: added role to user %s for clan tag", userID)
		}
	} else if !hasClan && hasRole {
		err := s.GuildMemberRoleRemove(guildID, userID, roleID)
		if err != nil {
			b.logger.Error("clan_tag: failed to remove role from user %s: %v", userID, err)
		} else {
			b.logger.Info("clan_tag: removed role from user %s (clan tag disabled)", userID)
		}
	}
}
