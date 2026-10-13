package models

import "testing"

func ip(v int) *int { return &v }

// bracket27 is the shape Challonge builds for 27 teams: 32 slots, 11 round-1
// matches (five byes), then 8, 4, 2, 1.
func bracket27() []BracketMatch {
	var ms []BracketMatch
	id := 1
	for round, n := range map[int]int{1: 11, 2: 8, 3: 4, 4: 2, 5: 1} {
		for i := 0; i < n; i++ {
			ms = append(ms, BracketMatch{ID: id, Round: round, State: BracketPending})
			id++
		}
	}
	return ms
}

func TestQualifierCutoffRound(t *testing.T) {
	ms := bracket27()
	cases := []struct{ advance, want int }{
		{8, 3},  // quarterfinal is not played
		{16, 2}, // round of 16 is not played
		{4, 4},
		{0, 0},  // not a qualifier
		{6, 0},  // not a power of two: no clean cut
		{32, 0}, // the whole field: nothing to play
		{1, 0},
	}
	for _, c := range cases {
		if got := QualifierCutoffRound(ms, c.advance); got != c.want {
			t.Errorf("advance %d: cutoff = %d, want %d", c.advance, got, c.want)
		}
	}
}

func TestQualifierCutoffRoundIgnoresOtherShapes(t *testing.T) {
	// Double elimination has negative rounds; the cut-off is single elimination only.
	ms := append(bracket27(), BracketMatch{ID: 99, Round: -1})
	if got := QualifierCutoffRound(ms, 8); got != 0 {
		t.Fatalf("double elimination: cutoff = %d, want 0", got)
	}
	if got := QualifierCutoffRound(nil, 8); got != 0 {
		t.Fatalf("empty bracket: cutoff = %d, want 0", got)
	}
}

func TestApplyQualifierCutoffClosesLaterRounds(t *testing.T) {
	ms := bracket27()
	for i := range ms {
		ms[i].Team1ID, ms[i].Team2ID = ip(100+i), ip(200+i)
		ms[i].State = BracketOpen
	}
	ApplyQualifierCutoff(ms, 8)
	for _, m := range ms {
		if m.Round >= 3 && m.Ready() {
			t.Errorf("round %d match %d is still playable", m.Round, m.ID)
		}
		if m.Round < 3 && !m.Ready() {
			t.Errorf("round %d match %d was closed", m.Round, m.ID)
		}
	}
}

func TestQualifiedTeams(t *testing.T) {
	ms := bracket27()
	if _, done := QualifiedTeams(ms, 8); done {
		t.Fatal("done before anything was played")
	}
	for i := range ms {
		if ms[i].Round < 3 {
			ms[i].State = BracketComplete
		}
	}
	// Rounds 1-2 complete but the quarterfinal slots are not filled yet.
	if _, done := QualifiedTeams(ms, 8); done {
		t.Fatal("done before the quarterfinal pairs were known")
	}
	team := 1
	for i := range ms {
		if ms[i].Round == 3 {
			ms[i].Team1ID, ms[i].Team1Name = ip(team), "T"
			ms[i].Team2ID, ms[i].Team2Name = ip(team+1), "T"
			ms[i].State = BracketOpen
			team += 2
		}
	}
	got, done := QualifiedTeams(ms, 8)
	if !done || len(got) != 8 {
		t.Fatalf("QualifiedTeams = %v, %v; want 8 teams, done", got, done)
	}
	// One round-2 match still being played: not done.
	for i := range ms {
		if ms[i].Round == 2 {
			ms[i].State = BracketOpen
			break
		}
	}
	if _, done := QualifiedTeams(ms, 8); done {
		t.Fatal("done while a round-2 match is open")
	}
}
