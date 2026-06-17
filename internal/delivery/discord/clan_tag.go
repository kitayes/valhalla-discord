package discord

import (
	"encoding/json"

	"blackwatch/pkg/config"

	"github.com/bwmarrin/discordgo"
)

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

	targetGuildID := cfg.GuildID
	if targetGuildID == "" {
		targetGuildID = defaultGuildID
	}

	b.logger.Info("Clan tag tracking enabled for guild %s with role %s", targetGuildID, cfg.ClanTagRoleID)

	// Handle GUILD_MEMBER_UPDATE (raw)
	b.session.AddHandler(func(s *discordgo.Session, event *discordgo.Event) {
		if event.Type != "GUILD_MEMBER_UPDATE" {
			return
		}

		var payload memberUpdatePayload
		if err := json.Unmarshal(event.RawData, &payload); err != nil {
			b.logger.Debug("clan_tag: failed to parse GUILD_MEMBER_UPDATE: %v", err)
			return
		}

		if payload.GuildID != targetGuildID {
			return
		}

		b.handleClanCheck(s, targetGuildID, cfg.ClanTagRoleID, payload.User.ID,
			payload.User.Clan != nil && payload.User.Clan.IdentityGuildID == targetGuildID && payload.User.Clan.IdentityEnabled)
	})

	// Handle PRESENCE_UPDATE (raw)
	b.session.AddHandler(func(s *discordgo.Session, event *discordgo.Event) {
		if event.Type != "PRESENCE_UPDATE" {
			return
		}

		var payload presenceUpdatePayload
		if err := json.Unmarshal(event.RawData, &payload); err != nil {
			b.logger.Debug("clan_tag: failed to parse PRESENCE_UPDATE: %v", err)
			return
		}

		if payload.GuildID != targetGuildID {
			return
		}

		hasOurClan := false
		if payload.User.PrimaryGuildID == targetGuildID {
			hasOurClan = true
		}
		if payload.User.Clan != nil &&
			payload.User.Clan.IdentityGuildID == targetGuildID &&
			payload.User.Clan.IdentityEnabled {
			hasOurClan = true
		}

		b.handleClanCheck(s, targetGuildID, cfg.ClanTagRoleID, payload.User.ID, hasOurClan)
	})
}

func (b *Bot) handleClanCheck(s *discordgo.Session, guildID, roleID, userID string, hasClan bool) {
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
