package security

import (
	"errors"
	"testing"

	"blackwatch/internal/domain"
)

func TestRefundReturnsTokensForAnAbandonedBatch(t *testing.T) {
	// The screenshot handler reserves one token per attachment and drops the
	// whole batch if any of them is refused. Without the refund the tokens
	// already taken stayed spent on work that never ran.
	rl := NewRateLimiter(100, 100)
	defer rl.Stop()

	const burst = 3
	for n := range burst {
		if err := rl.Allow("user-1", burst, 0); err != nil {
			t.Fatalf("token %d refused: %v", n+1, err)
		}
	}
	if err := rl.Allow("user-1", burst, 0); !errors.Is(err, domain.ErrRateLimitExceeded) {
		t.Fatalf("bucket past its burst returned %v, want ErrRateLimitExceeded", err)
	}

	rl.Refund("user-1", burst)

	for n := range burst {
		if err := rl.Allow("user-1", burst, 0); err != nil {
			t.Fatalf("token %d after refund was refused: %v", n+1, err)
		}
	}
}

func TestRefundIsCappedAtTheBurst(t *testing.T) {
	// Refunding more than was taken must not mint credit.
	rl := NewRateLimiter(100, 100)
	defer rl.Stop()

	const burst = 2
	if err := rl.Allow("user-2", burst, 0); err != nil {
		t.Fatalf("first token refused: %v", err)
	}
	rl.Refund("user-2", 10)

	for n := range burst {
		if err := rl.Allow("user-2", burst, 0); err != nil {
			t.Fatalf("token %d refused: %v", n+1, err)
		}
	}
	if err := rl.Allow("user-2", burst, 0); !errors.Is(err, domain.ErrRateLimitExceeded) {
		t.Errorf("refund handed out more than the burst: %v", err)
	}
}

func TestRefundIgnoresUnknownUsersAndNonPositiveCounts(t *testing.T) {
	rl := NewRateLimiter(100, 100)
	defer rl.Stop()

	rl.Refund("never-seen", 3) // must not panic or create a bucket
	rl.Refund("never-seen", 0)
	rl.Refund("never-seen", -1)
}

func TestPerUserBudgetDoesNotDrainTheGlobalOne(t *testing.T) {
	// A single user hammering their own limit must not throttle everyone else:
	// the per-user bucket is checked first and the global token is handed back
	// when the user's own budget refuses.
	rl := NewRateLimiter(4, 0)
	defer rl.Stop()

	const burst = 1
	if err := rl.Allow("noisy", burst, 0); err != nil {
		t.Fatalf("first request refused: %v", err)
	}
	for n := range 5 {
		if err := rl.Allow("noisy", burst, 0); !errors.Is(err, domain.ErrRateLimitExceeded) {
			t.Fatalf("spam request %d returned %v, want ErrRateLimitExceeded", n+1, err)
		}
	}

	// Three global tokens are left; a different user must still get through.
	for n := range 3 {
		if err := rl.Allow("quiet", burst+3, 0); err != nil {
			t.Fatalf("bystander request %d refused after another user's spam: %v", n+1, err)
		}
	}
}
