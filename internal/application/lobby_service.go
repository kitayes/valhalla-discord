package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

const (
	lobbyCapacity = 10
	// inactivityTimeout is how long a player may sit idle before being warned.
	inactivityTimeout = 45 * time.Minute
	// inactivityGrace is how much longer they get to react to that warning
	// before being removed from the queue.
	inactivityGrace = 2 * time.Minute
)

type LobbyMatchRepository interface {
	Create(ctx context.Context, req models.CreateLobbyMatchRequest) (int, error)
	GetByID(ctx context.Context, id int) (*models.LobbyMatch, error)
	AtomicSetWinner(ctx context.Context, matchID int, winner string) (bool, error)
	GetActiveByGuild(ctx context.Context, guildID string) (*models.LobbyMatch, error)
	GetPlayerMMRsBatch(ctx context.Context, playerIDs []int) (map[int]int, error)
	UpdatePlayerMMR(ctx context.Context, playerID, newMMR, matchID int) error
	GetMMRHistory(ctx context.Context, playerID, limit int) ([]models.MMRChange, error)
	SaveThreadID(ctx context.Context, matchID int, threadID string) error
	GetByThreadID(ctx context.Context, threadID string) (*models.LobbyMatch, error)
	GetPlayerNamesByMatchID(ctx context.Context, matchID int) ([]string, error)
	OpenBetting(ctx context.Context, matchID int, window time.Duration) error
	CloseBetting(ctx context.Context, matchID int) error
	CloseExpiredBetting(ctx context.Context) (int, error)
	CancelMatch(ctx context.Context, matchID int) ([]int, error)
	SaveMedals(ctx context.Context, matchID int, mvp, svp string) error
}

// QueueBanRepository is the subset of repository needed for queue bans.
type QueueBanRepository interface {
	BanPlayer(ctx context.Context, discordID, reason string, bannedUntil time.Time) error
	IsBanned(ctx context.Context, discordID string) (bool, error)
	GetBanInfo(ctx context.Context, discordID string) (string, time.Time, error)
	PurgeExpired(ctx context.Context) (int, error)
}

// LobbyNotifier is a callback to send a message to a Discord user (for inactivity/auto-promote alerts).
type LobbyNotifier func(userID, message string)

// LobbyPlace is where a player ended up after joining.
type LobbyPlace string

const (
	// PlaceMain means the player holds one of the lobbyCapacity active slots.
	PlaceMain LobbyPlace = "main"
	// PlaceWaitlist means the player is in the reserve and will be promoted
	// when a slot frees up.
	PlaceWaitlist LobbyPlace = "waitlist"
	// PlaceAlreadyQueued means the player already held a slot and the press was
	// taken as an activity confirmation. TryAddPlayer never returns it — it is
	// what the delivery layer records after handling domain.ErrAlreadyQueued.
	PlaceAlreadyQueued LobbyPlace = "already-queued"
)

// QueueBanError carries the ban details a delivery layer needs to tell the user
// when their block expires. It wraps domain.ErrQueueBanned so callers that only
// care about the category can use errors.Is.
type QueueBanError struct {
	Reason string
	Until  time.Time
}

func (e *QueueBanError) Error() string {
	return fmt.Sprintf("queue ban until %s: %s", e.Until.Format(time.RFC3339), e.Reason)
}

func (e *QueueBanError) Unwrap() error { return domain.ErrQueueBanned }

type lobbyEntry struct {
	player       models.Player
	discordID    string // for ban checks + inactivity alerts
	joinedAt     time.Time
	lastActivity time.Time
	warned       bool // an inactivity warning has already been sent
}

// notice is a message queued while the lobby mutex is held; it is delivered
// after the lock is released so a slow Discord call never blocks the queue.
type notice struct {
	discordID string
	message   string
}

// LobbyService manages the queue with capacity, waitlist, inactivity, and bans.
type LobbyService struct {
	mu        sync.RWMutex
	mainQueue []lobbyEntry // max capacity = lobbyCapacity
	waitlist  []lobbyEntry // overflow beyond 10
	isOpen    bool

	logger       Logger
	matchRepo    LobbyMatchRepository
	queueBanRepo QueueBanRepository
	sheetSyncer  func(mmrUpdates map[int]int)

	// callbackMu guards the hooks below. They are wired after construction —
	// the Telegram bot they call into cannot exist before the services do — and
	// by then the Discord handler goroutines are already reading them.
	callbackMu        sync.RWMutex
	notifier          LobbyNotifier
	matchLiveCallback func(matchID int, capA, capB string, window time.Duration)
}

// NewLobbyService creates a LobbyService. The repositories are required
// collaborators, not optional extras: every match-lifecycle method used to open
// with a nil check that could never fire.
func NewLobbyService(logger Logger, matchRepo LobbyMatchRepository, queueBanRepo QueueBanRepository) *LobbyService {
	return &LobbyService{
		mainQueue:    make([]lobbyEntry, 0, lobbyCapacity),
		waitlist:     make([]lobbyEntry, 0),
		logger:       logger,
		matchRepo:    matchRepo,
		queueBanRepo: queueBanRepo,
		isOpen:       true,
	}
}

func (l *LobbyService) SetSheetSyncer(syncer func(mmrUpdates map[int]int)) { l.sheetSyncer = syncer }

// SetNotifier installs the sink for inactivity and auto-promotion alerts.
func (l *LobbyService) SetNotifier(n LobbyNotifier) {
	l.callbackMu.Lock()
	defer l.callbackMu.Unlock()
	l.notifier = n
}

// SetMatchLiveCallback installs the hook fired when a match goes live.
func (l *LobbyService) SetMatchLiveCallback(fn func(matchID int, capA, capB string, window time.Duration)) {
	l.callbackMu.Lock()
	defer l.callbackMu.Unlock()
	l.matchLiveCallback = fn
}

func (l *LobbyService) NotifyMatchLive(matchID int, capA, capB string, window time.Duration) {
	l.callbackMu.RLock()
	fn := l.matchLiveCallback
	l.callbackMu.RUnlock()
	if fn != nil {
		fn(matchID, capA, capB, window)
	}
}

func (l *LobbyService) IsOpen() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.isOpen
}

func (l *LobbyService) OpenLobby() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.isOpen = true
	l.logger.Info("lobby: opened")
}

func (l *LobbyService) CloseLobby() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.isOpen = false
	l.mainQueue = l.mainQueue[:0]
	l.waitlist = l.waitlist[:0]
	l.logger.Info("lobby: closed — all players cleared")
}

// TryAddPlayer attempts to add a player to the queue.
//
// It returns the place the player took, or an error explaining why they were
// refused: domain.ErrQueueBanned (as *QueueBanError, carrying reason and
// expiry), domain.ErrLobbyClosed, or domain.ErrAlreadyQueued when the player
// already held a slot and nothing changed.
func (l *LobbyService) TryAddPlayer(ctx context.Context, player models.Player, discordID string) (LobbyPlace, error) {
	// Ban lookup hits the database — keep it outside the lobby lock.
	//
	// A failed lookup refuses the join. Letting it through on error meant one
	// dropped connection was enough for a banned player to re-enter the queue,
	// and nothing downstream re-checked.
	banned, err := l.queueBanRepo.IsBanned(ctx, discordID)
	if err != nil {
		return "", fmt.Errorf("lobby: cannot verify queue ban for %s: %w", discordID, err)
	}
	if banned {
		reason, until, err := l.queueBanRepo.GetBanInfo(ctx, discordID)
		if err != nil {
			return "", fmt.Errorf("lobby: cannot read queue ban for %s: %w", discordID, err)
		}
		return "", &QueueBanError{Reason: reason, Until: until}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Authoritative check: callers that test IsOpen() first race against
	// CloseLobby, and the requeue paths never tested it at all.
	if !l.isOpen {
		return "", domain.ErrLobbyClosed
	}

	now := time.Now()

	// Prevent duplicates across both queues
	for _, e := range l.mainQueue {
		if e.player.ID == player.ID {
			return "", domain.ErrAlreadyQueued
		}
	}
	for _, e := range l.waitlist {
		if e.player.ID == player.ID {
			return "", domain.ErrAlreadyQueued
		}
	}

	entry := lobbyEntry{player: player, discordID: discordID, joinedAt: now, lastActivity: now}

	if len(l.mainQueue) < lobbyCapacity {
		l.mainQueue = append(l.mainQueue, entry)
		l.logger.Info("lobby: player %s (ID: %d) joined main queue (%d/%d)", player.Name, player.ID, len(l.mainQueue), lobbyCapacity)
		return PlaceMain, nil
	}

	l.waitlist = append(l.waitlist, entry)
	l.logger.Info("lobby: player %s (ID: %d) added to waitlist (position %d)", player.Name, player.ID, len(l.waitlist))
	return PlaceWaitlist, nil
}

// WaitlistPosition reports the player's 1-based place in the reserve, or 0 when
// they are not on it.
func (l *LobbyService) WaitlistPosition(playerID int) int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i, e := range l.waitlist {
		if e.player.ID == playerID {
			return i + 1
		}
	}
	return 0
}

// RemovePlayer removes a player from the main queue and auto-promotes the first
// waitlist player. It reports whether the player was actually queued.
func (l *LobbyService) RemovePlayer(playerID int) bool {
	l.mu.Lock()
	notices, removed := l.removePlayerLocked(playerID)
	l.mu.Unlock()

	l.dispatch(notices)
	return removed
}

// dispatch delivers queued notices. Must be called without holding l.mu.
func (l *LobbyService) dispatch(notices []notice) {
	l.callbackMu.RLock()
	notify := l.notifier
	l.callbackMu.RUnlock()
	if notify == nil {
		return
	}
	for _, n := range notices {
		if n.discordID != "" {
			notify(n.discordID, n.message)
		}
	}
}

// ClearAll clears both main queue and waitlist.
func (l *LobbyService) ClearAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mainQueue = l.mainQueue[:0]
	l.waitlist = l.waitlist[:0]
	l.logger.Info("lobby: all players cleared")
}

// GetActivePlayers returns all players in the main queue only.
func (l *LobbyService) GetActivePlayers() []models.Player {
	l.mu.RLock()
	defer l.mu.RUnlock()
	players := make([]models.Player, 0, len(l.mainQueue))
	for _, e := range l.mainQueue {
		players = append(players, e.player)
	}
	return players
}

// IsPlayerActive checks if a player is in the main queue.
func (l *LobbyService) IsPlayerActive(playerID int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, e := range l.mainQueue {
		if e.player.ID == playerID {
			return true
		}
	}
	return false
}

// UpdateActivity updates the last activity timestamp for a player and clears
// any pending inactivity warning (for inactivity tracking).
func (l *LobbyService) UpdateActivity(playerID int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for i := range l.mainQueue {
		if l.mainQueue[i].player.ID == playerID {
			l.mainQueue[i].lastActivity = now
			l.mainQueue[i].warned = false
			return
		}
	}
	for i := range l.waitlist {
		if l.waitlist[i].player.ID == playerID {
			l.waitlist[i].lastActivity = now
			l.waitlist[i].warned = false
			return
		}
	}
}

// CheckInactivity warns idle players and removes those who ignored the warning.
// It is driven by RunInactivitySweeper.
//
// Timeline for a player who goes quiet at T:
//
//	T + inactivityTimeout                   -> warning sent once (warned = true)
//	T + inactivityTimeout + inactivityGrace -> removed from the queue
//
// Any activity resets lastActivity and clears the warning.
func (l *LobbyService) CheckInactivity() {
	l.mu.Lock()

	now := time.Now()
	warnAfter := now.Add(-inactivityTimeout)
	kickAfter := now.Add(-(inactivityTimeout + inactivityGrace))

	var notices []notice
	var toRemove []lobbyEntry

	// Both queues are swept: a waitlisted player who went quiet used to sit in
	// the reserve indefinitely and then get auto-promoted into the main queue
	// while still AFK, taking a slot from someone who was actually there.
	for _, queue := range [][]lobbyEntry{l.mainQueue, l.waitlist} {
		for i := range queue {
			e := &queue[i]
			switch {
			case e.warned && e.lastActivity.Before(kickAfter):
				// Warned and still silent — remove.
				toRemove = append(toRemove, *e)
			case !e.warned && e.lastActivity.Before(warnAfter):
				e.warned = true
				notices = append(notices, notice{
					discordID: e.discordID,
					message: fmt.Sprintf(
						"⏰ **%s**, вы всё ещё тут? Подтвердите активность в течение %d минут, нажав кнопку входа в лобби, иначе вы будете удалены.",
						e.player.Name, int(inactivityGrace.Minutes())),
				})
			}
		}
	}

	for _, e := range toRemove {
		l.logger.Info("lobby: kicking inactive player %s (ID: %d)", e.player.Name, e.player.ID)
		notices = append(notices, notice{
			discordID: e.discordID,
			message: fmt.Sprintf("**%s**, вы были удалены из лобби за неактивность (%d+ мин).",
				e.player.Name, int(inactivityTimeout.Minutes())),
		})
		promoted, _ := l.removePlayerLocked(e.player.ID)
		notices = append(notices, promoted...)
	}

	l.mu.Unlock()

	l.dispatch(notices)
}

// RunInactivitySweeper drives CheckInactivity until ctx is cancelled. Without a
// caller the whole inactivity timeline was inert: activity was recorded on every
// lobby button press and never read.
func (l *LobbyService) RunInactivitySweeper(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.CheckInactivity()
			if n, err := l.queueBanRepo.PurgeExpired(ctx); err != nil {
				l.logger.Warn("lobby: failed to purge expired queue bans: %v", err)
			} else if n > 0 {
				l.logger.Info("lobby: purged %d expired queue ban(s)", n)
			}
		}
	}
}

// removePlayerLocked removes a player from either queue, promoting the first
// waitlisted player when a main slot frees up. Must be called with l.mu held.
// Returns the notices to deliver once the caller has released the lock, and
// whether the player was queued at all.
func (l *LobbyService) removePlayerLocked(playerID int) ([]notice, bool) {
	for i, e := range l.mainQueue {
		if e.player.ID == playerID {
			l.mainQueue = append(l.mainQueue[:i], l.mainQueue[i+1:]...)
			l.logger.Info("lobby: player %s (ID: %d) left main queue", e.player.Name, playerID)

			if len(l.waitlist) > 0 && len(l.mainQueue) < lobbyCapacity {
				promoted := l.waitlist[0]
				l.waitlist = l.waitlist[1:]
				now := time.Now()
				promoted.joinedAt = now
				promoted.lastActivity = now
				promoted.warned = false
				l.mainQueue = append(l.mainQueue, promoted)
				l.logger.Info("lobby: waitlist player %s (ID: %d) auto-promoted to main",
					promoted.player.Name, promoted.player.ID)
				return []notice{{
					discordID: promoted.discordID,
					message: fmt.Sprintf("**%s**, вы переведены из резерва в основной состав лобби! Место освободилось.",
						promoted.player.Name),
				}}, true
			}
			return nil, true
		}
	}

	for i, e := range l.waitlist {
		if e.player.ID == playerID {
			l.waitlist = append(l.waitlist[:i], l.waitlist[i+1:]...)
			l.logger.Info("lobby: player %s (ID: %d) left waitlist", e.player.Name, playerID)
			return nil, true
		}
	}
	return nil, false
}

// QueueBan bans a player from the lobby.
func (l *LobbyService) QueueBan(ctx context.Context, discordID, reason string, duration time.Duration) error {
	until := time.Now().Add(duration)
	return l.queueBanRepo.BanPlayer(ctx, discordID, reason, until)
}

// FormatPlayerList formats the main queue.
func (l *LobbyService) FormatPlayerList() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var sb strings.Builder
	if len(l.mainQueue) == 0 {
		sb.WriteString("Пока никто не отметился. Нажмите кнопку, чтобы войти в лобби.")
	} else {
		sb.WriteString(fmt.Sprintf("**Активные участники (%d/%d):**\n\n", len(l.mainQueue), lobbyCapacity))
		for i, e := range l.mainQueue {
			sb.WriteString(fmt.Sprintf("%d. **%s** (ID: %d)\n", i+1, e.player.Name, e.player.ID))
		}
	}
	if len(l.waitlist) > 0 {
		sb.WriteString(fmt.Sprintf("\n**Резерв (%d):**\n", len(l.waitlist)))
		for i, e := range l.waitlist {
			sb.WriteString(fmt.Sprintf("%d. **%s** (ID: %d)\n", i+1, e.player.Name, e.player.ID))
		}
	}
	return sb.String()
}

// GetStatusDetail returns detailed lobby status with MMR.
func (l *LobbyService) GetStatusDetail(mmrMap map[int]int) string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== ЛОББИ (%d/%d) ===\n\n", len(l.mainQueue), lobbyCapacity))

	if len(l.mainQueue) == 0 {
		sb.WriteString("Пусто.\n")
	} else {
		totalMMR := 0
		for _, e := range l.mainQueue {
			mmr := mmrMap[e.player.ID]
			totalMMR += mmr
			sb.WriteString(fmt.Sprintf("• **%s** — %d MMR\n", e.player.Name, mmr))
		}
		avg := totalMMR / len(l.mainQueue)
		sb.WriteString(fmt.Sprintf("\nСредний MMR: **%d**\n", avg))
	}

	if len(l.waitlist) > 0 {
		sb.WriteString(fmt.Sprintf("\n--- РЕЗЕРВ (%d) ---\n", len(l.waitlist)))
		for i, e := range l.waitlist {
			mmr := mmrMap[e.player.ID]
			sb.WriteString(fmt.Sprintf("%d. %s — %d MMR\n", i+1, e.player.Name, mmr))
		}
	}

	return sb.String()
}

// =====================================================================
// Match Lifecycle Methods
// =====================================================================

func (l *LobbyService) CreateMatch(ctx context.Context, guildID string, captainAID, captainBID int, teamAIDs, teamBIDs []int) (int, error) {
	req := models.CreateLobbyMatchRequest{GuildID: guildID, CaptainAID: captainAID, CaptainBID: captainBID, TeamAIDs: teamAIDs, TeamBIDs: teamBIDs}
	matchID, err := l.matchRepo.Create(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("lobby: failed to create match: %w", err)
	}
	l.logger.Info("lobby: match #%d created (guild: %s, captains: %d vs %d)", matchID, guildID, captainAID, captainBID)
	return matchID, nil
}

func (l *LobbyService) GetMatch(ctx context.Context, matchID int) (*models.LobbyMatch, error) {
	return l.matchRepo.GetByID(ctx, matchID)
}

func (l *LobbyService) GetMatchByThreadID(ctx context.Context, threadID string) (*models.LobbyMatch, error) {
	return l.matchRepo.GetByThreadID(ctx, threadID)
}

func (l *LobbyService) GetPlayerNamesByMatchID(ctx context.Context, matchID int) ([]string, error) {
	return l.matchRepo.GetPlayerNamesByMatchID(ctx, matchID)
}

func (l *LobbyService) AtomicMatchClosure(ctx context.Context, matchID int, winner string) (bool, error) {
	success, err := l.matchRepo.AtomicSetWinner(ctx, matchID, winner)
	if err != nil {
		return false, err
	}
	if success {
		l.logger.Info("lobby: match #%d closed — winner: %s", matchID, winner)
	}
	return success, nil
}

// GetMMRHistory returns a player's recent rating changes, newest first.
func (l *LobbyService) GetMMRHistory(ctx context.Context, playerID, limit int) ([]models.MMRChange, error) {
	return l.matchRepo.GetMMRHistory(ctx, playerID, limit)
}

func (l *LobbyService) GetPlayerMMRsBatch(ctx context.Context, playerIDs []int) (map[int]int, error) {
	return l.matchRepo.GetPlayerMMRsBatch(ctx, playerIDs)
}

func (l *LobbyService) OpenBetting(ctx context.Context, matchID int, window time.Duration) error {
	return l.matchRepo.OpenBetting(ctx, matchID, window)
}

// CloseExpiredBetting shuts every betting window whose deadline has passed.
func (l *LobbyService) CloseExpiredBetting(ctx context.Context) (int, error) {
	return l.matchRepo.CloseExpiredBetting(ctx)
}

func (l *LobbyService) CloseBetting(ctx context.Context, matchID int) error {
	return l.matchRepo.CloseBetting(ctx, matchID)
}

// CancelMatch cancels an active match and returns the 10 player IDs for re-lobbying.
func (l *LobbyService) CancelMatch(ctx context.Context, matchID int) ([]int, error) {
	return l.matchRepo.CancelMatch(ctx, matchID)
}

// SaveMedals stores the MVP/SVPG names recognised on a match screenshot.
func (l *LobbyService) SaveMedals(ctx context.Context, matchID int, mvp, svp string) error {
	return l.matchRepo.SaveMedals(ctx, matchID, mvp, svp)
}

func (l *LobbyService) SyncMMRToSheet(mmrUpdates map[int]int) {
	if l.sheetSyncer != nil {
		l.sheetSyncer(mmrUpdates)
	}
}
