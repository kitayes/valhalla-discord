package repository

import (
	"sync"
	"valhalla/internal/models"
)

// PlayerCache provides thread-safe in-memory cache for player ID lookups
type PlayerCache struct {
	mu    sync.RWMutex
	cache map[string]int // normalized player name -> player ID
}

// NewPlayerCache creates a new player cache instance
func NewPlayerCache() *PlayerCache {
	return &PlayerCache{
		cache: make(map[string]int),
	}
}

// Get retrieves a player ID from cache by normalized name
func (c *PlayerCache) Get(name string) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, found := c.cache[name]
	return id, found
}

// Set stores a player ID in cache with normalized name as key
func (c *PlayerCache) Set(name string, id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[name] = id
}

// Delete removes a player from cache by normalized name
func (c *PlayerCache) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, name)
}

// Clear removes all entries from cache
func (c *PlayerCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]int)
}

// LoadAll populates cache with existing players (used for cache warming)
func (c *PlayerCache) LoadAll(players []models.Player) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range players {
		normalized := normalizeForComparison(p.Name)
		c.cache[normalized] = p.ID
	}
}

// Size returns the number of cached entries (for monitoring/debugging)
func (c *PlayerCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}
