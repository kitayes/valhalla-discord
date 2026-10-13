package models

// A qualifier is a single-elimination tournament that stops once `advance`
// teams are left: they go on to the main event and the rest of the bracket is
// never played. Challonge knows nothing about this, so it still opens the
// later matches; the bot hides them.

// QualifierCutoffRound is the first round a qualifier does not play: the one
// that starts with exactly `advance` teams. Zero when no clean cut exists — a
// non-power-of-two count, a field no larger than `advance`, or a bracket that
// is not single elimination.
func QualifierCutoffRound(ms []BracketMatch, advance int) int {
	if advance < 2 || advance&(advance-1) != 0 {
		return 0
	}
	total := 0
	perRound := map[int]int{}
	for _, m := range ms {
		if m.Round < 0 {
			return 0
		}
		perRound[m.Round]++
		if m.Round > total {
			total = m.Round
		}
	}
	k := 0
	for 1<<k < advance {
		k++
	}
	cut := total - k + 1
	if cut < 2 || perRound[cut] != advance/2 {
		return 0
	}
	return cut
}

// ApplyQualifierCutoff turns every open match from the cut-off round on back
// into pending, so nothing downstream offers it, pings it or accepts a report.
func ApplyQualifierCutoff(ms []BracketMatch, advance int) {
	cut := QualifierCutoffRound(ms, advance)
	if cut == 0 {
		return
	}
	for i := range ms {
		if ms[i].Round >= cut && ms[i].State == BracketOpen {
			ms[i].State = BracketPending
		}
	}
}

// QualifiedTeam is a team that made it through a qualifier.
type QualifiedTeam struct {
	ID   int
	Name string
}

// QualifiedTeams lists the teams that went through once every match before
// the cut-off is played and all cut-off slots are filled; done is false until
// then.
func QualifiedTeams(ms []BracketMatch, advance int) ([]QualifiedTeam, bool) {
	cut := QualifierCutoffRound(ms, advance)
	if cut == 0 {
		return nil, false
	}
	var out []QualifiedTeam
	for _, m := range ms {
		switch {
		case m.Round < cut && m.State != BracketComplete:
			return nil, false
		case m.Round == cut:
			if m.Team1ID == nil || m.Team2ID == nil {
				return nil, false
			}
			out = append(out, QualifiedTeam{*m.Team1ID, m.Team1Name}, QualifiedTeam{*m.Team2ID, m.Team2Name})
		}
	}
	return out, len(out) == advance
}
