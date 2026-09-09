package application

import (
	"sync"
	"sync/atomic"
	"time"
)

type StatsCache struct {
	stats       []*PlayerStats
	lastUpdated time.Time
	ttl         time.Duration
	mu          sync.RWMutex
	// Counters are atomic: they used to be incremented from Get under a read
	// lock, which allows concurrent writers and is a data race.
	hits   atomic.Int64
	misses atomic.Int64
}

func NewStatsCache(ttl time.Duration) *StatsCache {
	return &StatsCache{
		ttl: ttl,
	}
}

// Get returns a copy of the cached stats.
//
// Callers sort the result (by KDA, by win rate, for the sheet export), and
// handing out the cached slice let those sorts reorder the shared backing array
// while another request was reading it — a race that also produced leaderboards
// mixing two orderings. The PlayerStats values themselves are copied too, since
// the cache holds pointers.
func (c *StatsCache) Get() ([]*PlayerStats, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.stats == nil || time.Since(c.lastUpdated) > c.ttl {
		c.misses.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	return clonePlayerStats(c.stats), true
}

// Set stores a private copy of stats so later mutations by the caller cannot
// reach into the cache.
func (c *StatsCache) Set(stats []*PlayerStats) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats = clonePlayerStats(stats)
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
	return c.hits.Load(), c.misses.Load(), len(c.stats)
}

func clonePlayerStats(stats []*PlayerStats) []*PlayerStats {
	if stats == nil {
		return nil
	}
	out := make([]*PlayerStats, len(stats))
	for i, s := range stats {
		if s == nil {
			continue
		}
		cp := *s
		out[i] = &cp
	}
	return out
}
