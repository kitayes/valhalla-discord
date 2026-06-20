package discord

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type rateLimitEntry struct {
	lastRequest   time.Time
	cooldown      bool
	cooldownUntil time.Time
}

type commandRateLimiter struct {
	mu     sync.Mutex
	limits map[string]*rateLimitEntry
}

func newCommandRateLimiter() *commandRateLimiter {
	return &commandRateLimiter{
		limits: make(map[string]*rateLimitEntry),
	}
}

func (rl *commandRateLimiter) allow(userID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.limits[userID]
	now := time.Now()

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

func checkBotIsBot(i *discordgo.InteractionCreate) bool {
	if i.Member == nil || i.Member.User == nil {
		return true // no user = ignore
	}
	return i.Member.User.Bot
}

func (b *Bot) checkLicense(guildID string) bool {
	valid, err := b.services.LicenseService.IsLicenseValid(guildID)
	if err != nil {
		b.logger.Error("license check failed for guild %s: %v", guildID, err)
		return false
	}
	return !valid
}

func (b *Bot) sendLicenseExpiredMessage(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "BLACKWATCH LICENSE EXPIRED",
					Description: "Automation paused. Contact support.",
					Color:       0xFF0000,
				},
			},
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) sendRateLimitMessage(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Слишком быстро! Пожалуйста, подождите 3 секунды между командами."),
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
func (b *Bot) isKnownMatchThread(channelID string) bool {
	// Fast path: in-memory cache (populated by onCreateMixSelect / handleSelectTeamA / handleBalance)
	if _, ok := b.threadPlayerCache[channelID]; ok {
		return true
	}

	// DB fallback: query lobby_matches by thread_id
	match, err := b.services.Lobby.GetMatchByThreadID(channelID)
	if err != nil || match == nil {
		return false
	}

	// Cache the player names for subsequent screenshots
	names, err := b.services.Lobby.GetPlayerNamesByMatchID(match.ID)
	if err == nil {
		b.threadPlayerCache[channelID] = names
	}
	return true
}
