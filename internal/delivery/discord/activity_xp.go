package discord

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

const (
	unboundCacheTTL      = 2 * time.Minute
	activityCleanupEvery = 10 * time.Minute
	colorLevelUpGold     = 0xF1C40F
)

type voiceParticipant struct {
	ChannelID string
	Muted     bool
	Deafened  bool
	Bot       bool
}

type cachedDiscordPlayer struct {
	playerID  int
	name      string
	bound     bool
	checkedAt time.Time
}

type dailyXPUsage struct {
	dayKey  string // UTC YYYY-MM-DD
	chatXP  int64
	voiceXP int64
}

type activityTracker struct {
	mu           sync.Mutex
	chatCooldown map[string]time.Time
	dailyXP      map[string]dailyXPUsage
	pendingMsgs  map[string]int64
	playerCache  map[string]cachedDiscordPlayer
	voiceStates  map[string]map[string]voiceParticipant // guildID -> userID -> state
	afkChannels  map[string]string                      // guildID -> afkChannelID
}

func newActivityTracker() *activityTracker {
	return &activityTracker{
		chatCooldown: make(map[string]time.Time),
		dailyXP:      make(map[string]dailyXPUsage),
		pendingMsgs:  make(map[string]int64),
		playerCache:  make(map[string]cachedDiscordPlayer),
		voiceStates:  make(map[string]map[string]voiceParticipant),
		afkChannels:  make(map[string]string),
	}
}

func (t *activityTracker) invalidatePlayer(discordID string) {
	if t == nil || discordID == "" {
		return
	}
	t.mu.Lock()
	delete(t.playerCache, discordID)
	t.mu.Unlock()
}

// recordChatMessage buffers every non-bot guild message for accurate Noctaly-style
// message counting, and returns (xpToAward, flushedMessages) if the 15s cooldown
// elapsed and the user still has daily chat XP budget remaining.
func (t *activityTracker) recordChatMessage(userID string, rawXP int64, now time.Time) (int64, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.pendingMsgs[userID]++

	if rawXP <= 0 {
		return 0, 0
	}
	if last, ok := t.chatCooldown[userID]; ok && now.Sub(last) < domain.ChatXPCooldown {
		return 0, 0
	}
	t.chatCooldown[userID] = now

	day := now.UTC().Format("2006-01-02")
	usage := t.dailyXP[userID]
	if usage.dayKey != day {
		usage = dailyXPUsage{dayKey: day}
	}
	rem := int64(domain.ChatDailyXPCap) - usage.chatXP
	if rem <= 0 {
		// Daily XP cap reached; pending messages will be flushed on the 1-min ticker.
		return 0, 0
	}
	award := rawXP
	if award > rem {
		award = rem
	}
	usage.chatXP += award
	t.dailyXP[userID] = usage

	msgs := t.pendingMsgs[userID]
	delete(t.pendingMsgs, userID)
	return award, msgs
}

// claimVoiceXP returns how much voice XP the user may earn on this tick within
// VoiceDailyXPCap (voice_seconds itself is always recorded even at cap).
func (t *activityTracker) claimVoiceXP(userID string, rawXP int64, now time.Time) int64 {
	if rawXP <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	day := now.UTC().Format("2006-01-02")
	usage := t.dailyXP[userID]
	if usage.dayKey != day {
		usage = dailyXPUsage{dayKey: day}
	}
	rem := int64(domain.VoiceDailyXPCap) - usage.voiceXP
	if rem <= 0 {
		return 0
	}
	award := rawXP
	if award > rem {
		award = rem
	}
	usage.voiceXP += award
	t.dailyXP[userID] = usage
	return award
}

// drainPendingMessages atomically extracts all buffered message counts for periodic DB flush.
func (t *activityTracker) drainPendingMessages() map[string]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pendingMsgs) == 0 {
		return nil
	}
	out := t.pendingMsgs
	t.pendingMsgs = make(map[string]int64)
	return out
}

func (t *activityTracker) setGuildSnapshot(guildID, afkChannelID string, states []*discordgo.VoiceState, botLookup func(userID string) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if afkChannelID != "" {
		t.afkChannels[guildID] = afkChannelID
	}
	m := make(map[string]voiceParticipant, len(states))
	for _, vs := range states {
		if vs == nil || vs.UserID == "" || vs.ChannelID == "" {
			continue
		}
		isBot := false
		if vs.Member != nil && vs.Member.User != nil {
			isBot = vs.Member.User.Bot
		} else if botLookup != nil {
			isBot = botLookup(vs.UserID)
		}
		m[vs.UserID] = voiceParticipant{
			ChannelID: vs.ChannelID,
			Muted:     vs.SelfMute || vs.Mute || vs.Suppress,
			Deafened:  vs.SelfDeaf || vs.Deaf,
			Bot:       isBot,
		}
	}
	t.voiceStates[guildID] = m
}

func (t *activityTracker) updateVoiceState(vs *discordgo.VoiceState, isBot bool) {
	if vs == nil || vs.GuildID == "" || vs.UserID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if vs.ChannelID == "" {
		if g, ok := t.voiceStates[vs.GuildID]; ok {
			delete(g, vs.UserID)
		}
		return
	}
	g, ok := t.voiceStates[vs.GuildID]
	if !ok {
		g = make(map[string]voiceParticipant)
		t.voiceStates[vs.GuildID] = g
	}
	g[vs.UserID] = voiceParticipant{
		ChannelID: vs.ChannelID,
		Muted:     vs.SelfMute || vs.Mute || vs.Suppress,
		Deafened:  vs.SelfDeaf || vs.Deaf,
		Bot:       isBot,
	}
}

type eligibleVoiceUser struct {
	GuildID string
	UserID  string
	Muted   bool
}

// collectEligibleVoiceUsers returns all non-bot, non-deafened users sitting in
// a non-AFK voice channel with at least domain.VoiceMinUsers qualifying humans.
func (t *activityTracker) collectEligibleVoiceUsers() []eligibleVoiceUser {
	t.mu.Lock()
	defer t.mu.Unlock()

	var eligible []eligibleVoiceUser
	for guildID, users := range t.voiceStates {
		afkID := t.afkChannels[guildID]
		byChannel := make(map[string][]eligibleVoiceUser)
		for userID, st := range users {
			if st.Bot || st.Deafened || st.ChannelID == "" {
				continue
			}
			if afkID != "" && st.ChannelID == afkID {
				continue
			}
			byChannel[st.ChannelID] = append(byChannel[st.ChannelID], eligibleVoiceUser{
				GuildID: guildID,
				UserID:  userID,
				Muted:   st.Muted,
			})
		}
		for _, members := range byChannel {
			if len(members) < domain.VoiceMinUsers {
				continue
			}
			eligible = append(eligible, members...)
		}
	}
	return eligible
}

func (t *activityTracker) pruneCaches(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	today := now.UTC().Format("2006-01-02")
	for uid, last := range t.chatCooldown {
		if now.Sub(last) > 5*time.Minute {
			delete(t.chatCooldown, uid)
		}
	}
	for uid, usage := range t.dailyXP {
		if usage.dayKey != today {
			delete(t.dailyXP, uid)
		}
	}
	for uid, entry := range t.playerCache {
		if !entry.bound && now.Sub(entry.checkedAt) > unboundCacheTTL {
			delete(t.playerCache, uid)
		}
	}
}

// RegisterActivityHandlers hooks guild snapshot and voice state events for voice XP.
func (b *Bot) RegisterActivityHandlers() {
	b.session.AddHandler(func(s *discordgo.Session, g *discordgo.GuildCreate) {
		if g == nil || g.Guild == nil {
			return
		}
		if b.cfg.GuildID != "" && g.Guild.ID != b.cfg.GuildID {
			return
		}
		b.activity.setGuildSnapshot(g.Guild.ID, g.Guild.AfkChannelID, g.Guild.VoiceStates, func(uid string) bool {
			if member, err := s.State.Member(g.Guild.ID, uid); err == nil && member != nil && member.User != nil {
				return member.User.Bot
			}
			return false
		})
		b.logger.Debug("xp: snapshotted %d voice state(s) for guild %s", len(g.Guild.VoiceStates), g.Guild.ID)
	})

	b.session.AddHandler(func(s *discordgo.Session, v *discordgo.VoiceStateUpdate) {
		if v == nil || v.VoiceState == nil {
			return
		}
		if b.cfg.GuildID != "" && v.GuildID != b.cfg.GuildID {
			return
		}
		isBot := false
		if v.Member != nil && v.Member.User != nil {
			isBot = v.Member.User.Bot
		} else if member, err := s.State.Member(v.GuildID, v.UserID); err == nil && member != nil && member.User != nil {
			isBot = member.User.Bot
		}
		b.activity.updateVoiceState(v.VoiceState, isBot)
	})
}

// startVoiceXPTracker starts the 1-minute ticker that awards voice XP, flushes
// buffered chat message counts, and prunes stale caches.
func (b *Bot) startVoiceXPTracker() {
	if b.services == nil || b.services.EconomyService == nil {
		return
	}
	go func() {
		tick := time.NewTicker(domain.VoiceTickInterval)
		cleanupTicker := time.NewTicker(activityCleanupEvery)
		defer tick.Stop()
		defer cleanupTicker.Stop()

		for {
			select {
			case <-b.ctx.Done():
				return
			case now := <-tick.C:
				b.tickVoiceAndFlushMessages(now)
			case now := <-cleanupTicker.C:
				b.activity.pruneCaches(now)
			}
		}
	}()
}

func (b *Bot) tickVoiceAndFlushMessages(now time.Time) {
	eligible := b.activity.collectEligibleVoiceUsers()
	pendingMsgs := b.activity.drainPendingMessages()
	if len(eligible) == 0 && len(pendingMsgs) == 0 {
		return
	}

	b.goBackground("activity-tick", backgroundTimeout, func(ctx context.Context) {
		tickSeconds := int64(domain.VoiceTickInterval.Seconds())

		// 1. Voice XP + voice_seconds (plus any pending chat messages for the same user).
		for _, u := range eligible {
			playerID, playerName, ok := b.resolveBoundPlayerForActivity(ctx, u.UserID)
			if !ok {
				delete(pendingMsgs, u.UserID)
				continue
			}
			rawXP := int64(domain.VoiceXPPerMinUnmuted)
			if u.Muted {
				rawXP = int64(domain.VoiceXPPerMinMuted)
			}
			xpAward := b.activity.claimVoiceXP(u.UserID, rawXP, now)
			msgs := pendingMsgs[u.UserID]
			delete(pendingMsgs, u.UserID)

			res, err := b.services.EconomyService.GrantActivityXP(ctx, playerID, xpAward, tickSeconds, msgs)
			if err != nil {
				b.logger.Warn("xp: voice tick failed for player %d: %v", playerID, err)
				continue
			}
			if res != nil && res.LeveledUp() {
				b.announceLevelUp(b.session, b.voiceLevelAnnounceChannel(), u.UserID, playerName, res, "войс")
			}
		}

		// 2. Flush remaining buffered chat message counts (messages sent within cooldown or after daily XP cap).
		for userID, msgs := range pendingMsgs {
			if msgs <= 0 {
				continue
			}
			playerID, _, ok := b.resolveBoundPlayerForActivity(ctx, userID)
			if !ok {
				continue
			}
			if _, err := b.services.EconomyService.GrantActivityXP(ctx, playerID, 0, 0, msgs); err != nil {
				b.logger.Warn("xp: message flush failed for player %d: %v", playerID, err)
			}
		}
	})
}

// onChatActivity tracks every guild text message and awards chat XP respecting
// the 15s cooldown and daily chat XP cap.
func (b *Bot) onChatActivity(s *discordgo.Session, m *discordgo.MessageCreate) {
	if b.services == nil || b.services.EconomyService == nil {
		return
	}
	if m == nil || m.Author == nil || m.Author.Bot || m.GuildID == "" {
		return
	}
	if b.cfg.GuildID != "" && m.GuildID != b.cfg.GuildID {
		return
	}

	trimmed := strings.TrimSpace(m.Content)
	runes := utf8.RuneCountInString(trimmed)
	if runes < domain.ChatXPMinRunes && len(m.Attachments) > 0 {
		runes = domain.ChatXPMinRunes
	}
	rawXP := domain.ChatXPForMessage(runes)

	xpAward, msgs := b.activity.recordChatMessage(m.Author.ID, rawXP, time.Now())
	if xpAward <= 0 {
		// Message count is buffered and will flush with the next XP grant or 1-min ticker.
		return
	}

	userID := m.Author.ID
	channelID := m.ChannelID
	b.goBackground("chat-xp", backgroundTimeout, func(ctx context.Context) {
		playerID, playerName, ok := b.resolveBoundPlayerForActivity(ctx, userID)
		if !ok {
			return
		}
		res, err := b.services.EconomyService.GrantActivityXP(ctx, playerID, xpAward, 0, msgs)
		if err != nil {
			b.logger.Warn("xp: chat grant failed for player %d: %v", playerID, err)
			return
		}
		if res != nil && res.LeveledUp() {
			targetChannel := channelID
			if b.cfg.LevelAnnounceChannelID != "" {
				targetChannel = b.cfg.LevelAnnounceChannelID
			}
			b.announceLevelUp(s, targetChannel, userID, playerName, res, "чат")
		}
	})
}

func (b *Bot) resolveBoundPlayerForActivity(ctx context.Context, discordID string) (int, string, bool) {
	now := time.Now()
	b.activity.mu.Lock()
	if cached, ok := b.activity.playerCache[discordID]; ok {
		if cached.bound || now.Sub(cached.checkedAt) < unboundCacheTTL {
			b.activity.mu.Unlock()
			return cached.playerID, cached.name, cached.bound
		}
	}
	b.activity.mu.Unlock()

	id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, discordID)
	entry := cachedDiscordPlayer{checkedAt: now}
	if err == nil && id > 0 {
		entry.playerID = id
		entry.name = name
		entry.bound = true
	}

	b.activity.mu.Lock()
	b.activity.playerCache[discordID] = entry
	b.activity.mu.Unlock()
	return entry.playerID, entry.name, entry.bound
}

func (b *Bot) voiceLevelAnnounceChannel() string {
	if b.cfg.LevelAnnounceChannelID != "" {
		return b.cfg.LevelAnnounceChannelID
	}
	return b.allowedChannelID
}

func (b *Bot) announceLevelUp(s *discordgo.Session, channelID, discordID, playerName string, res *models.XPGrantResult, source string) {
	if s == nil || channelID == "" || res == nil || !res.LeveledUp() {
		return
	}
	desc := fmt.Sprintf("<@%s> (**%s**) достиг **%d уровня**!", discordID, playerName, res.NewLevel)
	if res.CoinsAwarded > 0 {
		desc += fmt.Sprintf("\nНаграда за уровень: **+%s** (баланс: **%s**)",
			domain.FormatCoins(res.CoinsAwarded), domain.FormatCoins(res.NewBalance))
	}
	embed := &discordgo.MessageEmbed{
		Title:       "Новый уровень!",
		Description: desc,
		Color:       colorLevelUpGold,
		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Источник XP: %s • Магазин: /shop", source)},
	}
	if _, err := s.ChannelMessageSendEmbed(channelID, embed); err != nil {
		b.logger.Warn("xp: failed to announce level-up for %s in channel %s: %v", playerName, channelID, err)
	}
}
