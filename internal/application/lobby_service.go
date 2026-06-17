package application

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"blackwatch/internal/models"
)

// LobbyService manages a pool of players ready to play today.
type LobbyService struct {
	mu            sync.RWMutex
	activePlayers map[int]models.Player // playerID -> Player
	logger        Logger
}

// NewLobbyService creates a new LobbyService.
func NewLobbyService(logger Logger) *LobbyService {
	return &LobbyService{
		activePlayers: make(map[int]models.Player),
		logger:        logger,
	}
}

// AddPlayer adds a player to the active lobby.
func (l *LobbyService) AddPlayer(player models.Player) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.activePlayers[player.ID] = player
	l.logger.Info("lobby: player %s (ID: %d) joined", player.Name, player.ID)
}

// RemovePlayer removes a player from the active lobby.
func (l *LobbyService) RemovePlayer(playerID int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p, ok := l.activePlayers[playerID]; ok {
		delete(l.activePlayers, playerID)
		l.logger.Info("lobby: player %s (ID: %d) left", p.Name, playerID)
	}
}

// GetActivePlayers returns a copy of all currently active players.
func (l *LobbyService) GetActivePlayers() []models.Player {
	l.mu.RLock()
	defer l.mu.RUnlock()

	players := make([]models.Player, 0, len(l.activePlayers))
	for _, p := range l.activePlayers {
		players = append(players, p)
	}
	return players
}

// IsPlayerActive checks if a given player is in the lobby.
func (l *LobbyService) IsPlayerActive(playerID int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.activePlayers[playerID]
	return ok
}

// ClearExpired removes players who have been idle for too long (not implemented as simple map-only).
// For now, this is a no-op. In a production system, you'd add timestamps and cleanup.
func (l *LobbyService) ClearExpired(maxAge time.Duration) {
	// Placeholder for future timestamp-based cleanup
}

// FormatPlayerList formats the active player list for display in an Embed.
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
