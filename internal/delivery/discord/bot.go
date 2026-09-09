package discord

import (
	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/security"
	"blackwatch/pkg/config"
	"context"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Per-operation deadlines. discordgo hands us callbacks with no context of
// their own, so each entrypoint derives one from the bot's lifetime context.
const (
	// interactionTimeout covers a slash command or button press. Discord itself
	// expects the first response within 3s, so anything slower is already lost —
	// the deadline exists to release the DB connection, not to save the reply.
	interactionTimeout = 10 * time.Second
	// screenshotTimeout is far longer: it covers downloading the images and the
	// Gemini round-trip for each one.
	screenshotTimeout = 3 * time.Minute
	// backgroundTimeout covers detached follow-up work such as tier role syncing
	// or archiving a finished thread.
	backgroundTimeout = 2 * time.Minute
	// matchCacheTTL bounds how long a match's cached roster is kept when the
	// match is never closed and the thread never archived.
	matchCacheTTL = 12 * time.Hour
)

type Bot struct {
	session  *discordgo.Session
	services *application.Service
	logger   application.Logger
	commands []*discordgo.ApplicationCommand
	cfg      *config.Config

	// ctx is the bot's lifetime context, cancelled on shutdown. Handlers derive
	// their own bounded contexts from it.
	ctx    context.Context
	cancel context.CancelFunc

	adminIDs         map[string]struct{}
	allowedChannelID string
	rateLimiter      *security.RateLimiter
	// faqRateLimiter is separate from rateLimiter on purpose: a per-user bucket
	// keeps the limits it was created with, so sharing one limiter between
	// screenshot uploads and FAQ answers would apply whichever budget happened
	// to be requested first.
	faqRateLimiter *security.RateLimiter
	cmdRateLimiter *commandRateLimiter
	refereeRoleID  string
	tierRoleIDs    map[domain.Tier]string

	// discordgo dispatches handlers concurrently, so the caches are guarded.
	cacheMu           sync.RWMutex
	threadPlayerCache map[string]cachedThread // threadID -> player names
	matchPlayerCache  map[int]cachedMatch     // matchID -> player IDs (for requeue)
	clanCheckCache    map[string]clanVerdict  // userID -> last applied clan verdict
}

// cachedThread and cachedMatch carry an insertion time so entries can be evicted
// when a match is never archived — the only cleanup path used to be a successful
// thread archive, so anything else leaked for the lifetime of the process.
type cachedThread struct {
	names []string
	at    time.Time
}

type cachedMatch struct {
	playerIDs []int
	at        time.Time
}

func NewBot(cfg *config.Config, services *application.Service, logger application.Logger) *Bot {
	// Screenshot uploads: 30 in flight across the guild, refilling 5/second.
	// The per-user budget is passed at the call site in handleScreenshots.
	rateLimiter := security.NewRateLimiter(uploadBurstGlobal, uploadRefillGlobal)

	// FAQ answers are paid DeepSeek calls, so they get their own, much tighter
	// budget. Per-user limits are supplied by handleFAQAutoAnswer.
	faqRateLimiter := security.NewRateLimiter(faqBurstGlobal, faqRefillGlobal)

	ctx, cancel := context.WithCancel(context.Background())

	return &Bot{
		cfg:               cfg,
		services:          services,
		logger:            logger,
		ctx:               ctx,
		cancel:            cancel,
		allowedChannelID:  cfg.AllowedChannelID,
		rateLimiter:       rateLimiter,
		faqRateLimiter:    faqRateLimiter,
		cmdRateLimiter:    newCommandRateLimiter(),
		refereeRoleID:     cfg.RefereeRoleID,
		tierRoleIDs:       cfg.TierRoleIDs(),
		threadPlayerCache: make(map[string]cachedThread),
		matchPlayerCache:  make(map[int]cachedMatch),
		clanCheckCache:    make(map[string]clanVerdict),
	}
}

// opContext derives a bounded context for one unit of handler work.
func (b *Bot) opContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(b.ctx, timeout)
}

// guildID resolves the guild an operation belongs to: the one the interaction
// came from, else the configured GUILD_ID.
//
// There is deliberately no built-in fallback any more. A hardcoded guild ID here
// meant a deployment that forgot GUILD_ID silently registered its slash commands
// into somebody else's server; config.Validate now refuses to start instead.
func (b *Bot) guildID(interactionGuildID string) string {
	if interactionGuildID != "" {
		return interactionGuildID
	}
	return b.cfg.GuildID
}

// goBackground runs follow-up work that outlives the interaction that triggered
// it. Such work must NOT inherit the handler's context: that one is cancelled by
// its deferred cancel as soon as the handler returns, which would kill the
// goroutine before it did anything. It gets a fresh deadline off the bot's
// lifetime context instead, so shutdown still stops it.
func (b *Bot) goBackground(name string, timeout time.Duration, fn func(ctx context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.logger.Error("PANIC in background task %q: %v\n%s", name, r, debug.Stack())
			}
		}()

		ctx, cancel := b.opContext(timeout)
		defer cancel()
		fn(ctx)
	}()
}

func (b *Bot) Init() error {
	var err error

	b.session, err = discordgo.New("Bot " + b.cfg.DiscordToken)
	if err != nil {
		b.logger.Error("error creating Discord session: %v", err)
		return err
	}

	b.adminIDs = make(map[string]struct{})
	for _, id := range b.cfg.AdminUserIDs {
		cleanID := strings.TrimSpace(id)
		if cleanID != "" {
			b.adminIDs[cleanID] = struct{}{}
		}
	}

	b.addCommands(
		b.newExportCommand(),
		b.newResetCommand(),
		b.newSetTimerCommand(),
		b.newWipeCommand(),
		b.newDeleteMatchCommand(),
		b.newSyncSheetCommand(),
		b.newResetPlayerCommand(),
		b.newWipePlayerCommand(),
		b.newRenamePlayerCommand(),
		b.newBindCommand(),
		b.newWhoamiCommand(),
		b.newBindPlayerCommand(),
		b.newUnbindPlayerCommand(),
		b.newUnlinkPlayerCommand(),
		b.newPlayersCommand(),
		b.newTopCommand(),
		b.newProfileCommand(),
		b.newHistoryCommand(),
		b.newLinkCommand(),
		b.newUnlinkCommand(),
		b.newTelegramProfileCommand(),
		b.newUpdateNickCommand(),
		b.newLobbyCommand(),
		b.newCreateMixCommand(),
		b.newCreateMatchCommand(),
		b.newFAQCommand(),
		b.newFAQReloadCommand(),
		b.newLobbyKickCommand(),
		b.newLobbyCloseCommand(),
		b.newLobbyOpenCommand(),
		b.newLobbyClearCommand(),
		b.newLobbyStatusCommand(),
		b.newQueueBanCommand(),
		b.newCancelMatchCommand(),
		b.newBalanceCommand(),
	)

	// Wrap interaction handler with panic recovery
	b.session.AddHandler(b.wrapRecover(b.onInteraction))

	// Message handler with its own recover (different signature)
	b.session.AddHandler(b.wrapRecoverMessage(b.onMessage))

	// Register lobby button/select handlers
	b.RegisterQueueHandlers()

	// Register match button handlers (Team A WIN / Team B WIN)
	b.RegisterMatchHandlers()

	// Register requeue button handler (🔁 Встать в очередь лобби)
	b.RegisterRequeueHandler()

	// Register danger button confirmation handlers (/wipe, /reset, /wipe_player, /reset_player)
	b.RegisterDangerButtonHandlers()

	// Register the "Привязать профиль" button that opens the registration modal
	b.RegisterBindModalHandlers()

	// Register clan tag tracking handlers
	b.AddClanTagHandlers(b.cfg)

	return nil
}

// Run opens the session and ties the bot's lifetime context to ctx, so
// cancelling the caller's context aborts in-flight handler work.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.session.Open(); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		b.cancel()
	}()

	b.sweepBetting()
	b.startLobbyMaintenance()

	b.logger.Info("Discord Bot Started. Cleaning up and registering slash commands...")

	_, err := b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, "", nil)
	if err != nil {
		b.logger.Warn("Failed to clear global commands: %v", err)
	} else {
		b.logger.Info("Global commands cleared")
	}

	targetGuild := b.guildID("")
	_, err = b.session.ApplicationCommandBulkOverwrite(b.session.State.User.ID, targetGuild, b.commands)
	if err != nil {
		b.logger.Error("Failed to register commands: %v", err)
	} else {
		b.logger.Info("Slash commands registered successfully for guild %s", targetGuild)
	}

	return nil
}

// startLobbyMaintenance wires the inactivity notifier and drives the sweep.
//
// The whole inactivity timeline — warn at 45 minutes, remove two minutes later —
// shipped inert: UpdateActivity was called on every lobby button press, but
// nothing ever read it. SetNotifier had exactly one caller, in a test, so
// CheckInactivity returned on its first line even if something had called it.
func (b *Bot) startLobbyMaintenance() {
	b.services.Lobby.SetNotifier(func(userID, message string) {
		ch, err := b.session.UserChannelCreate(userID)
		if err != nil {
			b.logger.Warn("lobby: cannot open DM with %s: %v", userID, err)
			return
		}
		if _, err := b.session.ChannelMessageSend(ch.ID, message); err != nil {
			b.logger.Warn("lobby: cannot notify %s: %v", userID, err)
		}
	})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.logger.Error("PANIC in lobby maintenance: %v\n%s", r, debug.Stack())
			}
		}()
		b.services.Lobby.RunInactivitySweeper(b.ctx, lobbySweepInterval)
	}()
}

func (b *Bot) Stop() {
	b.cancel()
	if b.session != nil {
		if err := b.session.Close(); err != nil {
			b.logger.Warn("discord: failed to close session: %v", err)
		}
	}
	if b.rateLimiter != nil {
		b.rateLimiter.Stop()
	}
	if b.faqRateLimiter != nil {
		b.faqRateLimiter.Stop()
	}
	b.logger.Info("Discord bot stopped")
}

// isReferee checks if a user has the SUDЬЯ (Referee) role.
func (b *Bot) isReferee(member *discordgo.Member) bool {
	if member == nil || b.refereeRoleID == "" {
		return false
	}
	for _, roleID := range member.Roles {
		if roleID == b.refereeRoleID {
			return true
		}
	}
	return false
}

// canRefereeMatches gates everything in the match lifecycle: creating, balancing,
// closing and cancelling. Admins are included because they already outrank the
// role, and because the referee role can be left unconfigured.
//
// This is the single gate for the whole flow. The match commands used to sit
// inside the admin-only switch *and* re-check isReferee, so a referee without an
// entry in ADMIN_USER_IDS could not create the matches the command description
// promised them — while the WIN buttons, which run outside that switch, needed
// only the role. Same lifecycle, two different bars.
func (b *Bot) canRefereeMatches(member *discordgo.Member) bool {
	if member == nil || member.User == nil {
		return false
	}
	return b.isAdmin(member.User.ID) || b.isReferee(member)
}

// requireReferee answers the interaction and reports false when the caller may
// not drive the match lifecycle.
func (b *Bot) requireReferee(s *discordgo.Session, i *discordgo.Interaction) bool {
	if b.canRefereeMatches(interactionMember(i)) {
		return true
	}
	b.respondMessage(s, i, "⛔ Только рефери (роль SUDЬЯ) или админ могут управлять матчами.", true)
	return false
}

// guardComponent applies the licence gate to a button or select-menu press.
//
// onInteraction returns early for anything that is not an application command,
// and every component handler is registered separately — so the licence check
// covered slash commands only. The whole match lifecycle runs on components: the
// creation wizard is select menus, the WIN button closes the match and moves the
// betting pool, requeue refills the lobby. An expired licence stopped
// /create_match and left the buttons that finish it working.
//
// cmdRateLimiter is deliberately NOT applied here. It enforces a three-second
// gap and punishes a violation with a sixty-second lockout, which is a shape
// that only fits one-shot slash commands: the match wizard is four component
// interactions in a row, started by a slash command that has just taken the
// user's token, so gating components with it locked the referee out on the very
// first step and no match could be created at all. Rate limiting the buttons
// needs a limiter built for bursts; that is its own task.
//
// Returns false when the caller has already been answered and the handler must
// stop.
func (b *Bot) guardComponent(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) bool {
	if interactionMember(i) == nil {
		return false
	}
	if i.GuildID != "" && b.isLicenseExpired(ctx, i.GuildID) {
		b.sendLicenseExpiredMessage(s, i)
		return false
	}
	return true
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// ===== GLOBAL GUARDS =====

	// 1. Bot-is-bot protection: never respond to bots or yourself
	if checkBotIsBot(i) {
		return
	}

	// Autocomplete and modal submissions arrive here too, and both used to be
	// dropped by the check below — an autocomplete request that goes unanswered
	// shows the user a spinner that never resolves.
	switch i.Type {
	case discordgo.InteractionApplicationCommandAutocomplete:
		b.onAutocomplete(s, i)
		return
	case discordgo.InteractionModalSubmit:
		b.onModalSubmit(s, i)
		return
	}

	// Only handle application commands here (component interactions handled separately)
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	ctx, cancel := b.opContext(interactionTimeout)
	defer cancel()

	// 2. Rate limiting: 1 req/3s per user, 60s cooldown on violation
	userID := i.Member.User.ID
	if !b.cmdRateLimiter.allow(userID) {
		b.sendRateLimitMessage(s, i.Interaction)
		return
	}

	// 3. License check for guild-scoped matchmaking/betting operations
	guildID := i.GuildID
	if guildID != "" && b.isLicenseExpired(ctx, guildID) {
		b.sendLicenseExpiredMessage(s, i.Interaction)
		return
	}

	name := i.ApplicationCommandData().Name

	// Public commands (no role check needed)
	switch name {
	case "top":
		b.handleTop(ctx, s, i.Interaction)
		return
	case "profile":
		b.handleProfile(ctx, s, i.Interaction)
		return
	case "players":
		b.handlePlayersList(ctx, s, i.Interaction)
		return
	case "history":
		b.handleHistory(ctx, s, i.Interaction)
		return
	case "link":
		b.handleLink(ctx, s, i.Interaction)
		return
	case "unlink":
		b.handleUnlink(ctx, s, i.Interaction)
		return
	case "telegram_profile":
		b.handleTelegramProfile(ctx, s, i.Interaction)
		return
	case "faq":
		b.handleFAQ(ctx, s, i.Interaction)
		return
	case "bind":
		b.handleBind(ctx, s, i.Interaction)
		return
	case "whoami":
		b.handleWhoami(ctx, s, i.Interaction)
		return
	}

	// Match lifecycle: referee OR admin. Kept out of the admin-only switch below
	// so the referee role actually grants what the command descriptions promise,
	// and so these commands sit at the same bar as the WIN buttons that close
	// the matches they create.
	//
	// cancel_match is here deliberately, and it is a widening: it used to be
	// admin-only. A referee can now cancel a match, which refunds every stake on
	// it. That was a decision, not an oversight — a referee who can close a match
	// and move the whole pool through the WIN button gains nothing from being
	// barred from the strictly smaller action of handing the same stakes back.
	// Queue bans and the lobby controls stay admin-only.
	switch name {
	case "create_mix":
		b.handleCreateMix(ctx, s, i.Interaction)
		return
	case "create_match":
		b.handleCreateMatch(ctx, s, i.Interaction)
		return
	case "balance":
		b.handleBalance(ctx, s, i.Interaction)
		return
	case "cancel_match":
		b.handleCancelMatch(ctx, s, i.Interaction)
		return
	}

	// Admin-only commands
	if !b.isAdmin(i.Member.User.ID) {
		b.respondMessage(s, i.Interaction, "У вас нет прав.", true)
		return
	}

	switch name {
	case "faq_reload":
		b.handleFAQReload(ctx, s, i.Interaction)
	case "export":
		b.handleExport(ctx, s, i.Interaction)
	case "reset":
		b.handleReset(ctx, s, i.Interaction)
	case "set_timer":
		b.handleSetTimer(ctx, s, i.Interaction)
	case "reset_player":
		b.handleResetPlayer(ctx, s, i.Interaction)
	case "sync_sheet":
		b.handleSyncSheet(ctx, s, i.Interaction)
	case "delete_match":
		b.handleDeleteMatch(ctx, s, i.Interaction)
	case "wipe":
		b.handleWipe(ctx, s, i.Interaction)
	case "wipe_player":
		b.handleWipePlayer(ctx, s, i.Interaction)
	case "rename_player":
		b.handleRenamePlayer(ctx, s, i.Interaction)
	case "update_nick":
		b.handleUpdateNick(ctx, s, i.Interaction)
	case "lobby":
		b.handleLobbyPost(ctx, s, i.Interaction)
	case "lobby_kick":
		b.handleLobbyKick(ctx, s, i.Interaction)
	case "lobby_close":
		b.handleLobbyClose(ctx, s, i.Interaction)
	case "lobby_open":
		b.handleLobbyOpen(ctx, s, i.Interaction)
	case "lobby_clear":
		b.handleLobbyClear(ctx, s, i.Interaction)
	case "queue_ban":
		b.handleQueueBan(ctx, s, i.Interaction)
	case "lobby_status":
		b.handleLobbyStatus(ctx, s, i.Interaction)
	case "bind_player":
		b.handleBindPlayer(ctx, s, i.Interaction)
	case "unlink_player":
		b.handleUnlinkPlayer(ctx, s, i.Interaction)
	case "unbind_player":
		b.handleUnbindPlayer(ctx, s, i.Interaction)
	}
}

// ===== Concurrency-safe cache accessors =====

func (b *Bot) setThreadPlayers(threadID string, names []string) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	b.evictStaleCachesLocked(time.Now())
	b.threadPlayerCache[threadID] = cachedThread{names: names, at: time.Now()}
}

func (b *Bot) getThreadPlayers(threadID string) ([]string, bool) {
	b.cacheMu.RLock()
	defer b.cacheMu.RUnlock()
	entry, ok := b.threadPlayerCache[threadID]
	if !ok {
		return nil, false
	}
	return entry.names, true
}

func (b *Bot) deleteThreadPlayers(threadID string) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	delete(b.threadPlayerCache, threadID)
}

func (b *Bot) setMatchPlayers(matchID int, playerIDs []int) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	b.evictStaleCachesLocked(time.Now())
	b.matchPlayerCache[matchID] = cachedMatch{playerIDs: playerIDs, at: time.Now()}
}

func (b *Bot) getMatchPlayers(matchID int) ([]int, bool) {
	b.cacheMu.RLock()
	defer b.cacheMu.RUnlock()
	entry, ok := b.matchPlayerCache[matchID]
	if !ok {
		return nil, false
	}
	return entry.playerIDs, true
}

func (b *Bot) deleteMatchPlayers(matchID int) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	delete(b.matchPlayerCache, matchID)
}

// evictStaleCachesLocked drops entries older than matchCacheTTL. Both caches are
// normally cleared when a match thread is archived, but a match that is never
// closed, or an archive call that fails, would otherwise keep its entry forever.
// Must be called with cacheMu held.
func (b *Bot) evictStaleCachesLocked(now time.Time) {
	for id, entry := range b.threadPlayerCache {
		if now.Sub(entry.at) > matchCacheTTL {
			delete(b.threadPlayerCache, id)
		}
	}
	for id, entry := range b.matchPlayerCache {
		if now.Sub(entry.at) > matchCacheTTL {
			delete(b.matchPlayerCache, id)
		}
	}
}

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Bot-is-bot protection
	if m.Author.ID == s.State.User.ID {
		return
	}
	if m.Author.Bot {
		return
	}

	ctx, cancel := b.opContext(screenshotTimeout)
	defer cancel()

	// Allow screenshots in the configured channel OR in a known match thread.
	//
	// The thread check is deliberately the second operand: on a cache miss it
	// queries lobby_matches, and evaluating it up front ran that query for every
	// message posted anywhere in the guild.
	isAllowedChannel := b.allowedChannelID == "" || m.ChannelID == b.allowedChannelID
	if !isAllowedChannel && !b.isKnownMatchThread(ctx, m.ChannelID) {
		return
	}

	// Auto-answer FAQ in the FAQ channel (plain text messages, no attachments)
	if b.isFAQChannel(m.ChannelID) && b.services.FAQService != nil && len(m.Attachments) == 0 && len(strings.TrimSpace(m.Content)) > 3 {
		b.handleFAQAutoAnswer(ctx, s, m)
		return
	}

	if len(m.Attachments) > 0 {
		b.handleScreenshots(ctx, s, m)
	}
}
