package security

import (
	"sync"
	"time"
	"blackwatch/internal/domain"
)

type RateLimiter struct {
	mu            sync.RWMutex
	userLimits    map[string]*tokenBucket
	globalLimit   *tokenBucket
	cleanupTicker *time.Ticker
	done          chan bool
}

type tokenBucket struct {
	tokens         int
	maxTokens      int
	refillRate     int
	lastRefillTime time.Time
	mu             sync.Mutex
}

func newTokenBucket(maxTokens, refillRate int) *tokenBucket {
	return &tokenBucket{
		tokens:         maxTokens,
		maxTokens:      maxTokens,
		refillRate:     refillRate,
		lastRefillTime: time.Now(),
	}
}

func (tb *tokenBucket) tryConsume() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens > 0 {
		tb.tokens--
		return true
	}
	return false
}

func (tb *tokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefillTime)
	tokensToAdd := int(elapsed.Seconds()) * tb.refillRate

	if tokensToAdd > 0 {
		tb.tokens = min(tb.tokens+tokensToAdd, tb.maxTokens)
		tb.lastRefillTime = now
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func NewRateLimiter(maxTokensPerUser, refillRatePerUser, maxTokensGlobal, refillRateGlobal int) *RateLimiter {
	rl := &RateLimiter{
		userLimits:  make(map[string]*tokenBucket),
		globalLimit: newTokenBucket(maxTokensGlobal, refillRateGlobal),
		done:        make(chan bool),
	}

	rl.cleanupTicker = time.NewTicker(5 * time.Minute)
	go rl.cleanupRoutine()

	return rl
}

func (rl *RateLimiter) Allow(userID string, maxTokensPerUser, refillRatePerUser int) error {
	if !rl.globalLimit.tryConsume() {
		return domain.ErrRateLimitExceeded
	}

	rl.mu.Lock()
	userBucket, exists := rl.userLimits[userID]
	if !exists {
		userBucket = newTokenBucket(maxTokensPerUser, refillRatePerUser)
		rl.userLimits[userID] = userBucket
	}
	rl.mu.Unlock()

	if !userBucket.tryConsume() {
		return domain.ErrRateLimitExceeded
	}

	return nil
}

func (rl *RateLimiter) cleanupRoutine() {
	for {
		select {
		case <-rl.cleanupTicker.C:
			rl.cleanup()
		case <-rl.done:
			rl.cleanupTicker.Stop()
			return
		}
	}
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for userID, bucket := range rl.userLimits {
		if now.Sub(bucket.lastRefillTime) > 10*time.Minute {
			delete(rl.userLimits, userID)
		}
	}
}

func (rl *RateLimiter) Stop() {
	close(rl.done)
}
