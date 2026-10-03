package application

import (
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

// DraftPicksPerTeam is how many players each captain drafts; with the captain
// that makes a side of five.
const DraftPicksPerTeam = 4

// draft is one 5v5 game being picked by two captains. It lives in
// LobbyService under l.mu, the same lock as the queue, which is what makes a
// pick atomic: a player leaves the lobby and joins a team in one step, so two
// drafts running side by side cannot take the same person.
type draft struct {
	id       int
	captains [2]lobbyEntry   // [0] captains team A, [1] team B
	teams    [2][]lobbyEntry // picks, captains excluded
	turn     int             // index of the captain to pick next
}

// DraftSide is one team of a draft.
type DraftSide struct {
	Captain models.Player
	Picks   []models.Player
}

// DraftView is a snapshot of a draft for rendering. Sides[0] is team A; Turn
// indexes Sides.
type DraftView struct {
	ID       int
	Sides    [2]DraftSide
	Turn     int
	Complete bool
}

func (d *draft) complete() bool {
	return len(d.teams[0]) == DraftPicksPerTeam && len(d.teams[1]) == DraftPicksPerTeam
}

func (d *draft) view() DraftView {
	v := DraftView{ID: d.id, Turn: d.turn, Complete: d.complete()}
	for side := range d.captains {
		v.Sides[side].Captain = d.captains[side].player
		picks := make([]models.Player, len(d.teams[side]))
		for i, e := range d.teams[side] {
			picks[i] = e.player
		}
		v.Sides[side].Picks = picks
	}
	return v
}

// StartDraft opens a draft with two captains, given by Discord ID. Both must
// be in the main queue; they leave it, and a coin flip decides who picks first.
func (l *LobbyService) StartDraft(captainA, captainB string) (DraftView, error) {
	if captainA == captainB {
		return DraftView{}, domain.ErrSameCaptain
	}
	v, notices, err := l.startDraftLocked(captainA, captainB)
	l.dispatch(notices)
	if err == nil {
		l.logger.Info("lobby: draft #%d started (%s vs %s)", v.ID, v.Sides[0].Captain.Name, v.Sides[1].Captain.Name)
	}
	return v, err
}

func (l *LobbyService) startDraftLocked(captainA, captainB string) (DraftView, []notice, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check both before taking either, so a refusal leaves the lobby untouched.
	if !l.inMainLocked(captainA) || !l.inMainLocked(captainB) {
		return DraftView{}, nil, domain.ErrNotInLobby
	}
	a, noticesA, _ := l.takeFromMainLocked(captainA)
	b, noticesB, _ := l.takeFromMainLocked(captainB)

	l.nextDraftID++
	d := &draft{id: l.nextDraftID, captains: [2]lobbyEntry{a, b}, turn: l.coin()}
	l.drafts[d.id] = d
	return d.view(), append(noticesA, noticesB...), nil
}

// inMainLocked reports whether the player with this Discord ID holds a main
// queue slot. Must be called with l.mu held.
func (l *LobbyService) inMainLocked(discordID string) bool {
	for _, e := range l.mainQueue {
		if e.discordID == discordID {
			return true
		}
	}
	return false
}

// takeFromMainLocked removes the main-queue player with this Discord ID and
// returns their entry, plus the waitlist-promotion notices the removal caused.
// Must be called with l.mu held.
func (l *LobbyService) takeFromMainLocked(discordID string) (lobbyEntry, []notice, bool) {
	for _, e := range l.mainQueue {
		if e.discordID == discordID {
			notices, _ := l.removePlayerLocked(e.player.ID)
			return e, notices, true
		}
	}
	return lobbyEntry{}, nil, false
}

// requeueLocked puts a drafted player back in the queue: the main queue while
// there is room, the waitlist after that. Must be called with l.mu held.
func (l *LobbyService) requeueLocked(e lobbyEntry, now time.Time) {
	e.lastActivity = now
	e.warned = false
	if len(l.mainQueue) < lobbyCapacity {
		l.mainQueue = append(l.mainQueue, e)
		return
	}
	l.waitlist = append(l.waitlist, e)
}

// PickPlayer moves the target from the lobby onto the picking captain's team
// and hands the turn over. Both players are given by Discord ID.
func (l *LobbyService) PickPlayer(draftID int, pickerDiscordID, targetDiscordID string) (DraftView, error) {
	v, notices, err := l.pickLocked(draftID, pickerDiscordID, targetDiscordID)
	l.dispatch(notices)
	return v, err
}

func (l *LobbyService) pickLocked(draftID int, picker, target string) (DraftView, []notice, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	d, ok := l.drafts[draftID]
	if !ok {
		return DraftView{}, nil, domain.ErrDraftNotFound
	}
	if d.complete() {
		return DraftView{}, nil, domain.ErrDraftComplete
	}
	if picker != d.captains[0].discordID && picker != d.captains[1].discordID {
		return DraftView{}, nil, domain.ErrNotCaptain
	}
	if picker != d.captains[d.turn].discordID {
		return DraftView{}, nil, domain.ErrNotYourTurn
	}
	e, notices, ok := l.takeFromMainLocked(target)
	if !ok {
		return DraftView{}, nil, domain.ErrNotInLobby
	}

	d.teams[d.turn] = append(d.teams[d.turn], e)
	d.turn = 1 - d.turn
	return d.view(), notices, nil
}

// has reports whether the player is a captain or a pick of this draft.
func (d *draft) has(playerID int) bool {
	for side := range d.captains {
		if d.captains[side].player.ID == playerID {
			return true
		}
		for _, e := range d.teams[side] {
			if e.player.ID == playerID {
				return true
			}
		}
	}
	return false
}

// inDraftLocked reports whether the player belongs to any open draft. Must be
// called with l.mu held.
func (l *LobbyService) inDraftLocked(playerID int) bool {
	for _, d := range l.drafts {
		if d.has(playerID) {
			return true
		}
	}
	return false
}
