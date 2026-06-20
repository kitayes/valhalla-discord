package application

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"blackwatch/internal/models"
)

type LobbyMatchRepository interface {
	Create(req models.CreateLobbyMatchRequest) (int, error)
	GetByID(id int) (*models.LobbyMatch, error)
	AtomicSetWinner(matchID int, winner string) (bool, error)
	GetActiveByGuild(guildID string) (*models.LobbyMatch, error)
	GetPlayerMMRsBatch(playerIDs []int) (map[int]int, error)
	UpdatePlayerMMR(playerID, newMMR int) error
	SaveThreadID(matchID int, threadID string) error
	GetByThreadID(threadID string) (*models.LobbyMatch, error)
	GetPlayerNamesByMatchID(matchID int) ([]string, error)
}

// lobbyEntry tracks a player in the lobby with their join time.
type lobbyEntry struct {
	player   models.Player
	joinedAt time.Time
}

// LobbyService manages the pool of active players and match lifecycle.
type LobbyService struct {
	mu            sync.RWMutex
	activePlayers map[int]lobbyEntry // playerID -> entry
	logger        Logger
	matchRepo     LobbyMatchRepository
	sheetSyncer   func(mmrUpdates map[int]int)
}

// NewLobbyService creates a new LobbyService.
func NewLobbyService(logger Logger) *LobbyService {
	return &LobbyService{
		activePlayers: make(map[int]lobbyEntry),
		logger:        logger,
	}
}

// SetMatchRepository sets the lobby match repository (called after construction).
func (l *LobbyService) SetMatchRepository(repo LobbyMatchRepository) {
	l.matchRepo = repo
}

// SetSheetSyncer sets the callback to sync MMR updates to Google Sheets.
func (l *LobbyService) SetSheetSyncer(syncer func(mmrUpdates map[int]int)) {
	l.sheetSyncer = syncer
}

// AddPlayer adds a player to the active lobby with the current timestamp.
func (l *LobbyService) AddPlayer(player models.Player) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.activePlayers[player.ID] = lobbyEntry{
		player:   player,
		joinedAt: time.Now(),
	}
	l.logger.Info("lobby: player %s (ID: %d) joined", player.Name, player.ID)
}

// RemovePlayer removes a player from the active lobby.
func (l *LobbyService) RemovePlayer(playerID int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, ok := l.activePlayers[playerID]; ok {
		delete(l.activePlayers, playerID)
		l.logger.Info("lobby: player %s (ID: %d) left", entry.player.Name, playerID)
	}
}

// GetActivePlayers returns a copy of all currently active players.
func (l *LobbyService) GetActivePlayers() []models.Player {
	l.mu.RLock()
	defer l.mu.RUnlock()

	players := make([]models.Player, 0, len(l.activePlayers))
	for _, entry := range l.activePlayers {
		players = append(players, entry.player)
	}
	return players
}

func (l *LobbyService) IsPlayerActive(playerID int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.activePlayers[playerID]
	return ok
}

// ClearExpired removes players who have been in the lobby longer than maxAge without a match.
// Called periodically (e.g. every 15 minutes) to evict stale entries.
func (l *LobbyService) ClearExpired(maxAge time.Duration) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	evicted := 0
	for id, entry := range l.activePlayers {
		if now.Sub(entry.joinedAt) > maxAge {
			l.logger.Info("lobby: player %s (ID: %d) expired after %s",
				entry.player.Name, id, now.Sub(entry.joinedAt).Round(time.Minute))
			delete(l.activePlayers, id)
			evicted++
		}
	}
	return evicted
}

func (l *LobbyService) FormatPlayerList() string {
	players := l.GetActivePlayers()
	if len(players) == 0 {
		return "Пока никто не отметился. Нажмите кнопку, чтобы войти в лобби."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Активные участники лиги сегодня (%d):**\n\n", len(players)))
	for _, p := range players {
		sb.WriteString(fmt.Sprintf("• **%s** (ID: %d)\n", p.Name, p.ID))
	}
	return sb.String()
}

func (l *LobbyService) CreateMatch(guildID string, captainAID, captainBID int, teamAIDs, teamBIDs []int) (int, error) {
	if l.matchRepo == nil {
		return 0, fmt.Errorf("lobby: match repository not initialized")
	}

	req := models.CreateLobbyMatchRequest{
		GuildID:    guildID,
		CaptainAID: captainAID,
		CaptainBID: captainBID,
		TeamAIDs:   teamAIDs,
		TeamBIDs:   teamBIDs,
	}

	matchID, err := l.matchRepo.Create(req)
	if err != nil {
		return 0, fmt.Errorf("lobby: failed to create match: %w", err)
	}

	l.logger.Info("lobby: match #%d created (guild: %s, captains: %d vs %d)", matchID, guildID, captainAID, captainBID)
	return matchID, nil
}

// GetMatch retrieves a lobby match by ID.
func (l *LobbyService) GetMatch(matchID int) (*models.LobbyMatch, error) {
	if l.matchRepo == nil {
		return nil, fmt.Errorf("lobby: match repository not initialized")
	}
	return l.matchRepo.GetByID(matchID)
}

// GetMatchByThreadID retrieves a lobby match by its Discord thread ID.
func (l *LobbyService) GetMatchByThreadID(threadID string) (*models.LobbyMatch, error) {
	if l.matchRepo == nil {
		return nil, fmt.Errorf("lobby: match repository not initialized")
	}
	return l.matchRepo.GetByThreadID(threadID)
}

// GetPlayerNamesByMatchID returns the display names of all players in a match.
func (l *LobbyService) GetPlayerNamesByMatchID(matchID int) ([]string, error) {
	if l.matchRepo == nil {
		return nil, fmt.Errorf("lobby: match repository not initialized")
	}
	return l.matchRepo.GetPlayerNamesByMatchID(matchID)
}

// AtomicMatchClosure performs the atomic state transition ACTIVE → PROCESSING → FINISHED.
// Returns true if the transition was successful (prevents double-clicks).
func (l *LobbyService) AtomicMatchClosure(matchID int, winner string) (bool, error) {
	if l.matchRepo == nil {
		return false, fmt.Errorf("lobby: match repository not initialized")
	}

	success, err := l.matchRepo.AtomicSetWinner(matchID, winner)
	if err != nil {
		return false, err
	}
	if success {
		l.logger.Info("lobby: match #%d closed — winner: %s", matchID, winner)
	}
	return success, nil
}

// GetPlayerMMRsBatch returns MMRs for a batch of player IDs. Exposed for balance/handlers.
func (l *LobbyService) GetPlayerMMRsBatch(playerIDs []int) (map[int]int, error) {
	if l.matchRepo == nil {
		return nil, fmt.Errorf("lobby: match repository not initialized")
	}
	return l.matchRepo.GetPlayerMMRsBatch(playerIDs)
}

// SyncMMRToSheet triggers an asynchronous sync of MMR updates to Google Sheets.
func (l *LobbyService) SyncMMRToSheet(mmrUpdates map[int]int) {
	if l.sheetSyncer != nil {
		l.sheetSyncer(mmrUpdates)
	}
}
