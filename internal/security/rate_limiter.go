package security

import (
	"sync"
	"time"

	"blackwatch/internal/domain"
)

const (
	cleanupInterval = 5 * time.Minute
	bucketIdleTTL   = 10 * time.Minute
)

type RateLimiter struct {
	mu            sync.RWMutex
	userLimits    map[string]*tokenBucket
	globalLimit   *tokenBucket
	cleanupTicker *time.Ticker
	done          chan struct{}
	stopOnce      sync.Once
}

type tokenBucket struct {
	mu sync.Mutex
	// tokens is fractional so that refill rates below one token per second
	// (and calls arriving more than once a second) are not silently lost.
	tokens         float64
	maxTokens      float64
	refillRate     float64 // tokens per second
	lastRefillTime time.Time
}

func newTokenBucket(maxTokens, refillRate int) *tokenBucket {
	return newTokenBucketRate(maxTokens, float64(refillRate))
}

func newTokenBucketRate(maxTokens int, refillRate float64) *tokenBucket {
	return &tokenBucket{
		tokens:         float64(maxTokens),
		maxTokens:      float64(maxTokens),
		refillRate:     refillRate,
		lastRefillTime: time.Now(),
	}
}

func (tb *tokenBucket) tryConsume() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill(time.Now())

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// refund returns a token taken by tryConsume when the caller could not complete
// the request after all, so a rejection elsewhere does not cost the user credit.
func (tb *tokenBucket) refund() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tokens = min(tb.tokens+1, tb.maxTokens)
}

// refill must be called with tb.mu held.
func (tb *tokenBucket) refill(now time.Time) {
	elapsed := now.Sub(tb.lastRefillTime).Seconds()
	if elapsed <= 0 {
		return
	}
	tb.tokens = min(tb.tokens+elapsed*tb.refillRate, tb.maxTokens)
	tb.lastRefillTime = now
}

// idleSince reports the last time this bucket was touched, taking the bucket
// lock so the cleanup goroutine does not race with tryConsume.
func (tb *tokenBucket) idleSince() time.Time {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.lastRefillTime
}

// NewRateLimiter builds a limiter with a shared global budget. Per-user budgets
// are supplied by each Allow/AllowRate call, not here: this constructor used to
// take maxTokensPerUser and refillRatePerUser as well and then never read them,
// so the numbers at the call site — and the comments explaining them — described
// limits that were never applied.
func NewRateLimiter(maxTokensGlobal, refillRateGlobal int) *RateLimiter {
	rl := &RateLimiter{
		userLimits:  make(map[string]*tokenBucket),
		globalLimit: newTokenBucket(maxTokensGlobal, refillRateGlobal),
		done:        make(chan struct{}),
	}

	rl.cleanupTicker = time.NewTicker(cleanupInterval)
	go rl.cleanupRoutine()

	return rl
}

func (rl *RateLimiter) Allow(userID string, maxTokensPerUser, refillRatePerUser int) error {
	return rl.AllowRate(userID, maxTokensPerUser, float64(refillRatePerUser))
}

// AllowRate is Allow with a fractional refill rate, for budgets slower than one
// token per second — a paid API answering one question every 30 seconds cannot
// be expressed as an integer rate.
//
// The per-user bucket is created on first use and keeps the limits it was
// created with, so a single RateLimiter must not be shared between callers that
// intend different budgets: the first caller's numbers would silently win.
func (rl *RateLimiter) AllowRate(userID string, maxTokensPerUser int, refillRatePerUser float64) error {
	rl.mu.Lock()
	userBucket, exists := rl.userLimits[userID]
	if !exists {
		userBucket = newTokenBucketRate(maxTokensPerUser, refillRatePerUser)
		rl.userLimits[userID] = userBucket
	}
	rl.mu.Unlock()

	// The per-user budget is checked first. Taking the global token up front
	// meant a single user hammering their own limit still drained the shared
	// budget, so their spam throttled everyone else too.
	if !userBucket.tryConsume() {
		return domain.ErrRateLimitExceeded
	}

	if !rl.globalLimit.tryConsume() {
		userBucket.refund()
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
		if now.Sub(bucket.idleSince()) > bucketIdleTTL {
			delete(rl.userLimits, userID)
		}
	}
}

// Refund returns n tokens taken by Allow/AllowRate to the user's bucket and the
// global one, for a caller that reserved several and then abandoned the whole
// batch. Unknown users and non-positive counts are no-ops.
func (rl *RateLimiter) Refund(userID string, n int) {
	if n <= 0 {
		return
	}
	rl.mu.RLock()
	userBucket := rl.userLimits[userID]
	rl.mu.RUnlock()

	for range n {
		if userBucket != nil {
			userBucket.refund()
		}
		rl.globalLimit.refund()
	}
}

// Stop shuts down the cleanup goroutine. It is safe to call more than once.
func (rl *RateLimiter) Stop() {
	rl.stopOnce.Do(func() { close(rl.done) })
}
