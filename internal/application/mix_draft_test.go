package application

import (
	"errors"
	"testing"

	"blackwatch/internal/domain"
)

// newDraftLobby is a lobby of n players whose drafts always let captain A
// pick first, so turn order is deterministic.
func newDraftLobby(t *testing.T, n int) *LobbyService {
	t.Helper()
	l, _ := newTestLobby()
	addPlayers(t, l, n)
	l.coin = func() int { return 0 }
	return l
}

func TestStartDraftTakesCaptainsOutOfLobby(t *testing.T) {
	l := newDraftLobby(t, 12)

	v, err := l.StartDraft(discordID(1), discordID(2))
	if err != nil {
		t.Fatalf("StartDraft: %v", err)
	}
	if v.Sides[0].Captain.ID != 1 || v.Sides[1].Captain.ID != 2 {
		t.Errorf("captains = (%d, %d), want (1, 2)", v.Sides[0].Captain.ID, v.Sides[1].Captain.ID)
	}
	if v.Turn != 0 || v.Complete || v.ID == 0 {
		t.Errorf("new draft = %+v, want turn 0, not complete, non-zero ID", v)
	}
	if l.IsPlayerActive(1) || l.IsPlayerActive(2) {
		t.Error("captains are still in the lobby")
	}
	if got := len(l.GetActivePlayers()); got != 10 {
		t.Errorf("lobby holds %d players, want 10", got)
	}
}

func TestStartDraftRejectsSameCaptain(t *testing.T) {
	l := newDraftLobby(t, 12)

	if _, err := l.StartDraft(discordID(1), discordID(1)); !errors.Is(err, domain.ErrSameCaptain) {
		t.Errorf("err = %v, want ErrSameCaptain", err)
	}
	if !l.IsPlayerActive(1) {
		t.Error("a refused draft took the captain out of the lobby")
	}
}

// Both captains are checked before either is taken, so a refusal leaves the
// lobby exactly as it was.
func TestStartDraftRejectsCaptainNotInLobby(t *testing.T) {
	l := newDraftLobby(t, 12)

	if _, err := l.StartDraft(discordID(1), "discord-nobody"); !errors.Is(err, domain.ErrNotInLobby) {
		t.Errorf("err = %v, want ErrNotInLobby", err)
	}
	if !l.IsPlayerActive(1) {
		t.Error("a refused draft took the valid captain out of the lobby")
	}
}

func TestStartDraftNumbersDraftsDistinctly(t *testing.T) {
	l := newDraftLobby(t, 12)

	a, err := l.StartDraft(discordID(1), discordID(2))
	if err != nil {
		t.Fatalf("first StartDraft: %v", err)
	}
	b, err := l.StartDraft(discordID(3), discordID(4))
	if err != nil {
		t.Fatalf("second StartDraft: %v", err)
	}
	if a.ID == b.ID {
		t.Errorf("both drafts got ID %d", a.ID)
	}
}
