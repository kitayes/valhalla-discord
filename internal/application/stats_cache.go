package application

import (
	"sync"
	"time"
)

type StatsCache struct {
	stats       []*PlayerStats
	lastUpdated time.Time
	ttl         time.Duration
	mu          sync.RWMutex
	hits        int64
	misses      int64
}

func NewStatsCache(ttl time.Duration) *StatsCache {
	return &StatsCache{
		ttl: ttl,
	}
}

func (c *StatsCache) Get() ([]*PlayerStats, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.stats == nil || time.Since(c.lastUpdated) > c.ttl {
		c.misses++
		return nil, false
	}

	c.hits++
	// Return pointer directly - caller should treat as read-only
	return c.stats, true
}

func (c *StatsCache) Set(stats []*PlayerStats) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Store pointer directly instead of copying
	c.stats = stats
	c.lastUpdated = time.Now()
}

func (c *StatsCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats = nil
}

// GetMetrics returns cache hit/miss statistics
func (c *StatsCache) GetMetrics() (hits, misses int64, size int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses, len(c.stats)
}
