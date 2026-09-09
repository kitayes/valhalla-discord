package discord

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"
	"time"

	"blackwatch/internal/domain"

	"github.com/bwmarrin/discordgo"
)

type rateLimitEntry struct {
	lastRequest   time.Time
	cooldown      bool
	cooldownUntil time.Time
}

// commandCooldownTTL is how long an idle user entry is kept before being
// evicted, so the map does not grow for the lifetime of the process.
const commandCooldownTTL = 10 * time.Minute

type commandRateLimiter struct {
	mu          sync.Mutex
	limits      map[string]*rateLimitEntry
	lastCleanup time.Time
}

func newCommandRateLimiter() *commandRateLimiter {
	return &commandRateLimiter{
		limits:      make(map[string]*rateLimitEntry),
		lastCleanup: time.Now(),
	}
}

func (rl *commandRateLimiter) allow(userID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.evictStaleLocked(now)

	entry, exists := rl.limits[userID]

	if exists && entry.cooldown {
		if now.Before(entry.cooldownUntil) {
			return false // still in cooldown window
		}
		entry.cooldown = false
		entry.lastRequest = time.Time{}
	}

	if exists {
		elapsed := now.Sub(entry.lastRequest)
		if elapsed < 3*time.Second {
			entry.cooldown = true
			entry.cooldownUntil = now.Add(60 * time.Second)
			return false
		}
	} else {
		entry = &rateLimitEntry{}
		rl.limits[userID] = entry
	}

	entry.lastRequest = now
	return true
}

// evictStaleLocked drops entries nobody has touched recently. Must be called
// with rl.mu held.
func (rl *commandRateLimiter) evictStaleLocked(now time.Time) {
	if now.Sub(rl.lastCleanup) < commandCooldownTTL {
		return
	}
	rl.lastCleanup = now
	for id, e := range rl.limits {
		if now.Sub(e.lastRequest) > commandCooldownTTL && now.After(e.cooldownUntil) {
			delete(rl.limits, id)
		}
	}
}

func (b *Bot) wrapRecover(handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				b.logger.Error("PANIC recovered: %v\nStack:\n%s", r, string(stack))

				if i.Interaction != nil {
					respErr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "System error. Operation cancelled.",
							Flags:   discordgo.MessageFlagsEphemeral,
						},
					})
					if respErr != nil {
						b.logger.Error("Failed to send panic response: %v", respErr)
					}
				}
			}
		}()
		handler(s, i)
	}
}

// wrapRecoverMessage is the MessageCreate counterpart of wrapRecover. Screenshot
// parsing walks AI output and attachment metadata, so a panic there must not be
// allowed to reach discordgo — it dispatches handlers on their own goroutines,
// where an unrecovered panic kills the process rather than the handler.
func (b *Bot) wrapRecoverMessage(handler func(s *discordgo.Session, m *discordgo.MessageCreate)) func(s *discordgo.Session, m *discordgo.MessageCreate) {
	return func(s *discordgo.Session, m *discordgo.MessageCreate) {
		defer func() {
			if r := recover(); r != nil {
				b.logger.Error("PANIC recovered in message handler: %v\nStack:\n%s", r, string(debug.Stack()))
			}
		}()
		handler(s, m)
	}
}

func checkBotIsBot(i *discordgo.InteractionCreate) bool {
	if i.Member == nil || i.Member.User == nil {
		return true // no user = ignore
	}
	return i.Member.User.Bot
}

// interactionMember returns the guild member behind an interaction, or nil when
// there is none. Interactions delivered outside a guild carry User instead of
// Member, so reaching for i.Member.User unconditionally panics — and since the
// component handlers run outside the recovering wrapper, that took the whole
// process down.
func interactionMember(i *discordgo.Interaction) *discordgo.Member {
	if i == nil || i.Member == nil || i.Member.User == nil {
		return nil
	}
	if i.Member.User.Bot {
		return nil
	}
	return i.Member
}

// isLicenseExpired reports whether the guild's automation must be blocked.
//
// It is fail-closed: an unreadable licence blocks. The previous version was
// named checkLicense, returned the negation of what the name suggested, and
// answered "not expired" when the database was unreachable — that is, a Postgres
// outage handed every guild unlimited access.
//
// The gate sits in front of every command, the read-only ones included, so a
// database outage makes the bot mute rather than half-working. That is the
// accepted trade-off: /profile and /top read from the same database that just
// went down, so letting them through would only trade a clear "licence
// unavailable" for a confusing failure one call later.
func (b *Bot) isLicenseExpired(ctx context.Context, guildID string) bool {
	valid, err := b.services.LicenseService.IsLicenseValid(ctx, guildID)
	if err != nil {
		b.logger.Error("license check failed for guild %s, blocking: %v", guildID, err)
		return true
	}
	return !valid
}

func (b *Bot) sendLicenseExpiredMessage(s *discordgo.Session, i *discordgo.Interaction) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "BLACKWATCH LICENSE EXPIRED",
					Description: "Automation paused. Contact support.",
					Color:       colorRed,
				},
			},
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) sendRateLimitMessage(s *discordgo.Session, i *discordgo.Interaction) {
	b.respond(s, i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Слишком быстро! Пожалуйста, подождите 3 секунды между командами.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// isFAQChannel checks if the channel ID is the designated FAQ support channel.
func (b *Bot) isFAQChannel(channelID string) bool {
	return b.cfg.FAQChannelID != "" && channelID == b.cfg.FAQChannelID
}

// isKnownMatchThread checks if the channel ID belongs to a known match thread
// (in-memory cache or DB lookup via lobby_matches.thread_id).
//
// A lookup failure is logged rather than folded into "not a match thread": both
// answers dropped the screenshot, but only one of them is a bug worth seeing.
func (b *Bot) isKnownMatchThread(ctx context.Context, channelID string) bool {
	// Fast path: in-memory cache (populated by onCreateMixSelect / handleSelectTeamA / handleBalance)
	if _, ok := b.getThreadPlayers(channelID); ok {
		return true
	}

	// DB fallback: query lobby_matches by thread_id
	match, err := b.services.Lobby.GetMatchByThreadID(ctx, channelID)
	if err != nil {
		if !errors.Is(err, domain.ErrMatchNotFound) {
			b.logger.Error("discord: failed to resolve match thread %s: %v", channelID, err)
		}
		return false
	}
	if match == nil {
		return false
	}

	// Cache the player names for subsequent screenshots
	names, err := b.services.Lobby.GetPlayerNamesByMatchID(ctx, match.ID)
	if err != nil {
		b.logger.Error("discord: failed to load roster for match #%d: %v", match.ID, err)
		return true
	}
	b.setThreadPlayers(channelID, names)
	return true
}
