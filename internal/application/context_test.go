package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

// banRepoStub records the context it was called with and can block until the
// caller's deadline expires.
type banRepoStub struct {
	gotCtx  context.Context
	block   bool
	banned  bool
	banErr  error
	callCnt int
}

func (r *banRepoStub) BanPlayer(ctx context.Context, discordID, reason string, until time.Time) error {
	r.gotCtx = ctx
	return nil
}

func (r *banRepoStub) IsBanned(ctx context.Context, discordID string) (bool, error) {
	r.gotCtx = ctx
	r.callCnt++
	if r.block {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return r.banned, r.banErr
}

func (r *banRepoStub) GetBanInfo(ctx context.Context, discordID string) (string, time.Time, error) {
	r.gotCtx = ctx
	return "smurfing", time.Now().Add(time.Hour), nil
}

func (r *banRepoStub) PurgeExpired(ctx context.Context) (int, error) {
	r.gotCtx = ctx
	return 0, nil
}

func TestTryAddPlayerPassesCallerContext(t *testing.T) {
	l, _, repo := newTestLobbyWithBanRepo(&banRepoStub{})

	type ctxKey string
	const key ctxKey = "request-id"
	ctx := context.WithValue(context.Background(), key, "abc123")

	if _, err := l.TryAddPlayer(ctx, models.Player{ID: 1, Name: "a"}, "discord-a"); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}

	if repo.gotCtx == nil {
		t.Fatal("repository was called without a context")
	}
	if got := repo.gotCtx.Value(key); got != "abc123" {
		t.Errorf("repository got a detached context (value %v), the caller's context must reach it", got)
	}
}

func TestTryAddPlayerHonoursCancellation(t *testing.T) {
	l, _, _ := newTestLobbyWithBanRepo(&banRepoStub{block: true})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		l.TryAddPlayer(ctx, models.Player{ID: 1, Name: "a"}, "discord-a") //nolint:errcheck // the deadline is what is under test
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TryAddPlayer ignored the deadline and blocked on the ban lookup")
	}

	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("expected the context to have expired, got %v", ctx.Err())
	}
}

// A failed ban lookup must refuse the join.
//
// This test previously asserted the opposite — that an errored lookup was
// treated as "not banned" and the player was admitted. That is fail-open on an
// access-control check: one dropped connection was enough to put a banned player
// back in the queue, and nothing downstream re-checked.
func TestTryAddPlayerRefusesWhenBanCheckFails(t *testing.T) {
	l, _, _ := newTestLobbyWithBanRepo(&banRepoStub{banErr: errors.New("db down")})

	place, err := l.TryAddPlayer(context.Background(), models.Player{ID: 1, Name: "a"}, "discord-a")

	if err == nil {
		t.Fatalf("player was admitted to %q despite an unreadable ban check", place)
	}
	if place != "" {
		t.Errorf("player got place %q on a failed ban check", place)
	}
	if l.IsPlayerActive(1) {
		t.Error("player entered the queue despite an unreadable ban check")
	}
}

func TestTryAddPlayerRejectsBannedPlayer(t *testing.T) {
	l, _, _ := newTestLobbyWithBanRepo(&banRepoStub{banned: true})

	place, err := l.TryAddPlayer(context.Background(), models.Player{ID: 1, Name: "a"}, "discord-a")

	if place != "" {
		t.Errorf("banned player was admitted to %q", place)
	}
	if !errors.Is(err, domain.ErrQueueBanned) {
		t.Fatalf("banned player got %v, want ErrQueueBanned", err)
	}

	// The delivery layer renders the expiry, so the details have to survive.
	var banErr *QueueBanError
	if !errors.As(err, &banErr) {
		t.Fatalf("error %v does not carry ban details", err)
	}
	if banErr.Reason != "smurfing" {
		t.Errorf("ban reason = %q, want %q", banErr.Reason, "smurfing")
	}
	if banErr.Until.IsZero() {
		t.Error("ban expiry is zero — the user would be told they are banned until 01.01.0001")
	}
}
