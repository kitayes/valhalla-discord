package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

// nopLogger satisfies Logger without producing output during tests.
type nopLogger struct{}

func (nopLogger) Info(string, ...interface{})  {}
func (nopLogger) Error(string, ...interface{}) {}
func (nopLogger) Debug(string, ...interface{}) {}
func (nopLogger) Warn(string, ...interface{})  {}

// recorder captures notifications sent to players.
type recorder struct {
	mu       sync.Mutex
	messages map[string][]string
}

func newRecorder() *recorder {
	return &recorder{messages: make(map[string][]string)}
}

func (r *recorder) notify(userID, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages[userID] = append(r.messages[userID], message)
}

func (r *recorder) count(userID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages[userID])
}

func newTestLobby() (*LobbyService, *recorder) {
	l, rec, _ := newTestLobbyWithBanRepo(&banRepoStub{})
	return l, rec
}

// newTestLobbyWithBanRepo builds a lobby around a specific queue-ban stub. The
// ban repository is a required collaborator now, so every lobby needs one.
func newTestLobbyWithBanRepo(repo *banRepoStub) (*LobbyService, *recorder, *banRepoStub) {
	l := NewLobbyService(nopLogger{}, nil, repo)
	rec := newRecorder()
	l.SetNotifier(rec.notify)
	return l, rec, repo
}

func addPlayers(t *testing.T, l *LobbyService, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		place, err := l.TryAddPlayer(context.Background(), models.Player{ID: i, Name: name(i)}, discordID(i))
		if err != nil {
			t.Fatalf("player %d rejected: %v", i, err)
		}
		if place == "" {
			t.Fatalf("player %d got no place", i)
		}
	}
}

func name(id int) string      { return "player" + string(rune('A'+id-1)) }
func discordID(id int) string { return "discord-" + name(id) }

// backdate rewinds a player's last activity to simulate an idle player, in
// whichever queue they are sitting.
func backdate(l *LobbyService, playerID int, d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, queue := range [][]lobbyEntry{l.mainQueue, l.waitlist} {
		for i := range queue {
			if queue[i].player.ID == playerID {
				queue[i].lastActivity = time.Now().Add(-d)
				return
			}
		}
	}
}

// isWaitlisted reports whether a player is sitting in the reserve.
func isWaitlisted(l *LobbyService, playerID int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, e := range l.waitlist {
		if e.player.ID == playerID {
			return true
		}
	}
	return false
}

func TestTryAddPlayerFillsMainThenWaitlist(t *testing.T) {
	l, _ := newTestLobby()
	addPlayers(t, l, lobbyCapacity)

	if got := len(l.GetActivePlayers()); got != lobbyCapacity {
		t.Fatalf("main queue holds %d players, want %d", got, lobbyCapacity)
	}

	place, err := l.TryAddPlayer(context.Background(), models.Player{ID: 99, Name: "overflow"}, "discord-overflow")
	if err != nil {
		t.Fatalf("11th player rejected: %v", err)
	}
	if place != PlaceWaitlist {
		t.Errorf("11th player went to %q, want waitlist", place)
	}
	if l.IsPlayerActive(99) {
		t.Error("waitlisted player must not count as active")
	}
}

func TestTryAddPlayerRejectsDuplicates(t *testing.T) {
	l, _ := newTestLobby()
	addPlayers(t, l, 3)

	place, err := l.TryAddPlayer(context.Background(), models.Player{ID: 2, Name: name(2)}, discordID(2))
	if !errors.Is(err, domain.ErrAlreadyQueued) {
		t.Errorf("duplicate join returned (%q, %v), want ErrAlreadyQueued", place, err)
	}
	if got := len(l.GetActivePlayers()); got != 3 {
		t.Errorf("queue grew to %d on duplicate join", got)
	}
}

func TestTryAddPlayerRejectedWhenLobbyClosed(t *testing.T) {
	l, _ := newTestLobby()
	l.CloseLobby()

	place, err := l.TryAddPlayer(context.Background(), models.Player{ID: 1, Name: "a"}, "discord-a")
	if place != "" {
		t.Errorf("closed lobby accepted a player into %q", place)
	}
	if !errors.Is(err, domain.ErrLobbyClosed) {
		t.Errorf("closed lobby returned %v, want ErrLobbyClosed", err)
	}
}

func TestRemovePlayerPromotesFromWaitlist(t *testing.T) {
	l, rec := newTestLobby()
	addPlayers(t, l, lobbyCapacity)
	l.TryAddPlayer(context.Background(), models.Player{ID: 99, Name: "reserve"}, "discord-reserve")

	l.RemovePlayer(1)

	if !l.IsPlayerActive(99) {
		t.Error("waitlisted player was not promoted into the freed slot")
	}
	if got := len(l.GetActivePlayers()); got != lobbyCapacity {
		t.Errorf("main queue holds %d after promotion, want %d", got, lobbyCapacity)
	}
	if rec.count("discord-reserve") != 1 {
		t.Errorf("promoted player got %d notifications, want 1", rec.count("discord-reserve"))
	}
}

func TestCheckInactivityWarnsOnceThenKicks(t *testing.T) {
	l, rec := newTestLobby()
	addPlayers(t, l, 2)

	// Idle past the warning threshold but still inside the grace period.
	backdate(l, 1, inactivityTimeout+time.Second)
	l.CheckInactivity()

	if rec.count(discordID(1)) != 1 {
		t.Fatalf("expected exactly one warning, got %d", rec.count(discordID(1)))
	}
	if !l.IsPlayerActive(1) {
		t.Fatal("player kicked before the grace period elapsed")
	}

	// Running again without new activity must not re-warn.
	l.CheckInactivity()
	if rec.count(discordID(1)) != 1 {
		t.Errorf("warning repeated: %d notifications", rec.count(discordID(1)))
	}
	if !l.IsPlayerActive(1) {
		t.Fatal("player kicked while still inside the grace period")
	}

	// Past warning + grace: the player must actually be removed.
	backdate(l, 1, inactivityTimeout+inactivityGrace+time.Second)
	l.CheckInactivity()

	if l.IsPlayerActive(1) {
		t.Error("inactive player was never kicked")
	}
	if rec.count(discordID(1)) != 2 {
		t.Errorf("expected a warning and a kick notice, got %d", rec.count(discordID(1)))
	}
	if !l.IsPlayerActive(2) {
		t.Error("active player was removed alongside the idle one")
	}
}

func TestUpdateActivityCancelsPendingWarning(t *testing.T) {
	l, rec := newTestLobby()
	addPlayers(t, l, 1)

	backdate(l, 1, inactivityTimeout+time.Second)
	l.CheckInactivity()
	if rec.count(discordID(1)) != 1 {
		t.Fatalf("expected a warning, got %d", rec.count(discordID(1)))
	}

	// The player reacts; the kick must be cancelled.
	l.UpdateActivity(1)
	l.CheckInactivity()

	if !l.IsPlayerActive(1) {
		t.Error("player was kicked despite confirming activity")
	}
	if rec.count(discordID(1)) != 1 {
		t.Errorf("no further notices expected, got %d", rec.count(discordID(1)))
	}
}

func TestCheckInactivityPromotesReplacement(t *testing.T) {
	l, rec := newTestLobby()
	addPlayers(t, l, lobbyCapacity)
	l.TryAddPlayer(context.Background(), models.Player{ID: 99, Name: "reserve"}, "discord-reserve")

	backdate(l, 1, inactivityTimeout+inactivityGrace+time.Second)
	l.CheckInactivity() // warns
	backdate(l, 1, inactivityTimeout+inactivityGrace+time.Second)
	l.CheckInactivity() // kicks

	if l.IsPlayerActive(1) {
		t.Fatal("idle player was not kicked")
	}
	if !l.IsPlayerActive(99) {
		t.Error("reserve player did not take the freed slot")
	}
	if rec.count("discord-reserve") == 0 {
		t.Error("promoted player was not notified")
	}
}

// An idle player in the reserve must be swept too. Otherwise they linger there
// indefinitely and get auto-promoted into the main queue while still AFK,
// taking the slot from someone who is actually around.
func TestCheckInactivityKicksIdleWaitlistPlayer(t *testing.T) {
	l, rec := newTestLobby()
	addPlayers(t, l, lobbyCapacity)
	l.TryAddPlayer(context.Background(), models.Player{ID: 99, Name: "reserve"}, "discord-reserve")

	if !isWaitlisted(l, 99) {
		t.Fatal("setup: player 99 is not in the waitlist")
	}

	backdate(l, 99, inactivityTimeout+time.Second)
	l.CheckInactivity() // warns

	if rec.count("discord-reserve") != 1 {
		t.Fatalf("expected one warning for the reserve player, got %d", rec.count("discord-reserve"))
	}
	if !isWaitlisted(l, 99) {
		t.Fatal("reserve player dropped before the grace period elapsed")
	}

	backdate(l, 99, inactivityTimeout+inactivityGrace+time.Second)
	l.CheckInactivity() // kicks

	if isWaitlisted(l, 99) {
		t.Error("idle reserve player was never removed from the waitlist")
	}
	if l.IsPlayerActive(99) {
		t.Error("idle reserve player was promoted instead of removed")
	}
}

func TestConcurrentQueueAccessIsRaceFree(t *testing.T) {
	l, _ := newTestLobby()

	var wg sync.WaitGroup
	for i := 1; i <= 40; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			l.TryAddPlayer(context.Background(), models.Player{ID: id, Name: "p"}, "discord")
			l.UpdateActivity(id)
			l.GetActivePlayers()
			l.FormatPlayerList()
			l.IsPlayerActive(id)
			l.RemovePlayer(id)
		}(i)
	}
	wg.Wait()

	if got := len(l.GetActivePlayers()); got != 0 {
		t.Errorf("queue should be empty after every player left, got %d", got)
	}
}
