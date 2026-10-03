package application

import (
	"context"
	"errors"
	"testing"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
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

// pickAll makes the eight picks of a draft whose captains are players 1 and 2,
// taking players 3..10 in order: A takes 3, B takes 4, A takes 5, ...
func pickAll(t *testing.T, l *LobbyService, draftID int) DraftView {
	t.Helper()
	var v DraftView
	for target := 3; target <= 10; target++ {
		picker := discordID(1)
		if target%2 == 0 {
			picker = discordID(2)
		}
		var err error
		v, err = l.PickPlayer(draftID, picker, discordID(target))
		if err != nil {
			t.Fatalf("pick of player %d: %v", target, err)
		}
	}
	return v
}

func TestPickPlayerAlternatesTurns(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))

	v, err := l.PickPlayer(d.ID, discordID(1), discordID(3))
	if err != nil {
		t.Fatalf("A's pick: %v", err)
	}
	if v.Turn != 1 || len(v.Sides[0].Picks) != 1 || v.Sides[0].Picks[0].ID != 3 {
		t.Errorf("after A's pick: %+v, want player 3 on A and B to move", v)
	}
	if l.IsPlayerActive(3) {
		t.Error("picked player is still in the lobby")
	}

	if _, err := l.PickPlayer(d.ID, discordID(1), discordID(4)); !errors.Is(err, domain.ErrNotYourTurn) {
		t.Errorf("A picking twice: err = %v, want ErrNotYourTurn", err)
	}
	if !l.IsPlayerActive(4) {
		t.Error("an out-of-turn pick took the player out of the lobby")
	}

	if _, err := l.PickPlayer(d.ID, discordID(2), discordID(4)); err != nil {
		t.Errorf("B's pick: %v", err)
	}
}

func TestPickPlayerRejectsNonCaptain(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))

	if _, err := l.PickPlayer(d.ID, discordID(5), discordID(3)); !errors.Is(err, domain.ErrNotCaptain) {
		t.Errorf("err = %v, want ErrNotCaptain", err)
	}
}

func TestPickPlayerRejectsPlayerNotInLobby(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))

	if _, err := l.PickPlayer(d.ID, discordID(1), "discord-nobody"); !errors.Is(err, domain.ErrNotInLobby) {
		t.Errorf("err = %v, want ErrNotInLobby", err)
	}
	// The failed pick must not hand the turn over.
	if _, err := l.PickPlayer(d.ID, discordID(1), discordID(3)); err != nil {
		t.Errorf("A's retry after a refused pick: %v", err)
	}
}

func TestParallelDraftsCannotTakeTheSamePlayer(t *testing.T) {
	l := newDraftLobby(t, 14)
	first, _ := l.StartDraft(discordID(1), discordID(2))
	second, _ := l.StartDraft(discordID(11), discordID(12))

	if _, err := l.PickPlayer(first.ID, discordID(1), discordID(5)); err != nil {
		t.Fatalf("first draft's pick: %v", err)
	}
	if _, err := l.PickPlayer(second.ID, discordID(11), discordID(5)); !errors.Is(err, domain.ErrNotInLobby) {
		t.Errorf("second draft took an already picked player: err = %v, want ErrNotInLobby", err)
	}
}

func TestDraftCompletesAfterEightPicks(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))

	v := pickAll(t, l, d.ID)

	if !v.Complete {
		t.Fatal("draft not complete after eight picks")
	}
	for side, want := range [2][]int{{3, 5, 7, 9}, {4, 6, 8, 10}} {
		got := v.Sides[side].Picks
		if len(got) != len(want) {
			t.Fatalf("side %d has %d picks, want %d", side, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i] {
				t.Errorf("side %d pick %d = %d, want %d", side, i, got[i].ID, want[i])
			}
		}
	}
	if _, err := l.PickPlayer(d.ID, discordID(1), discordID(11)); !errors.Is(err, domain.ErrDraftComplete) {
		t.Errorf("pick after completion: err = %v, want ErrDraftComplete", err)
	}
}

func TestPickPlayerUnknownDraft(t *testing.T) {
	l := newDraftLobby(t, 12)

	if _, err := l.PickPlayer(42, discordID(1), discordID(3)); !errors.Is(err, domain.ErrDraftNotFound) {
		t.Errorf("err = %v, want ErrDraftNotFound", err)
	}
}

// A captain or pick who pressed "join the lobby" again could otherwise be
// drafted into a second game while the first is still being picked.
func TestDraftedPlayerCannotRejoinLobby(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))
	if _, err := l.PickPlayer(d.ID, discordID(1), discordID(3)); err != nil {
		t.Fatalf("pick: %v", err)
	}

	for _, id := range []int{1, 3} {
		_, err := l.TryAddPlayer(context.Background(), models.Player{ID: id, Name: name(id)}, discordID(id))
		if !errors.Is(err, domain.ErrInDraft) {
			t.Errorf("player %d rejoining: err = %v, want ErrInDraft", id, err)
		}
	}
}

func TestCancelDraftReturnsEveryoneToLobby(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))
	if _, err := l.PickPlayer(d.ID, discordID(1), discordID(3)); err != nil {
		t.Fatalf("pick: %v", err)
	}

	v, err := l.CancelDraft(d.ID)
	if err != nil {
		t.Fatalf("CancelDraft: %v", err)
	}
	if v.Sides[0].Captain.ID != 1 {
		t.Errorf("cancelled view lost its captains: %+v", v)
	}
	for _, id := range []int{1, 2, 3} {
		if !l.IsPlayerActive(id) {
			t.Errorf("player %d was not returned to the lobby", id)
		}
	}
	if got := len(l.GetActivePlayers()); got != 12 {
		t.Errorf("lobby holds %d players after cancel, want 12", got)
	}
	if _, err := l.PickPlayer(d.ID, discordID(2), discordID(4)); !errors.Is(err, domain.ErrDraftNotFound) {
		t.Errorf("pick in a cancelled draft: err = %v, want ErrDraftNotFound", err)
	}
}

// A complete draft whose match could not be created is cancelled, and its ten
// players must all get their place back.
func TestCancelCompleteDraftReturnsAllTen(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))
	pickAll(t, l, d.ID)

	if _, err := l.CancelDraft(d.ID); err != nil {
		t.Fatalf("CancelDraft: %v", err)
	}
	if got := len(l.GetActivePlayers()); got != 12 {
		t.Errorf("lobby holds %d players, want 12", got)
	}
}

// Players drafted into a lobby that was closed meanwhile are not pushed back
// into it.
func TestCancelDraftIntoClosedLobbyRequeuesNobody(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))
	l.CloseLobby()

	if _, err := l.CancelDraft(d.ID); err != nil {
		t.Fatalf("CancelDraft: %v", err)
	}
	if got := len(l.GetActivePlayers()); got != 0 {
		t.Errorf("closed lobby holds %d players after cancel, want 0", got)
	}
}

func TestFinishDraftRemovesItWithoutRequeueing(t *testing.T) {
	l := newDraftLobby(t, 12)
	d, _ := l.StartDraft(discordID(1), discordID(2))
	pickAll(t, l, d.ID)

	if err := l.FinishDraft(d.ID); err != nil {
		t.Fatalf("FinishDraft: %v", err)
	}
	if l.IsPlayerActive(1) || l.IsPlayerActive(3) {
		t.Error("finishing a draft put its players back in the lobby")
	}
	if _, err := l.CancelDraft(d.ID); !errors.Is(err, domain.ErrDraftNotFound) {
		t.Errorf("cancel after finish: err = %v, want ErrDraftNotFound", err)
	}
	// Out of the draft, the players may queue again once their match is over.
	if _, err := l.TryAddPlayer(context.Background(), models.Player{ID: 1, Name: name(1)}, discordID(1)); err != nil {
		t.Errorf("captain rejoining after the draft finished: %v", err)
	}
}

func TestFinishAndCancelUnknownDraft(t *testing.T) {
	l := newDraftLobby(t, 12)

	if err := l.FinishDraft(42); !errors.Is(err, domain.ErrDraftNotFound) {
		t.Errorf("FinishDraft: err = %v, want ErrDraftNotFound", err)
	}
	if _, err := l.CancelDraft(42); !errors.Is(err, domain.ErrDraftNotFound) {
		t.Errorf("CancelDraft: err = %v, want ErrDraftNotFound", err)
	}
}
