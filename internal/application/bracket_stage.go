package application

import (
	"fmt"
	"strings"
	"time"

	"blackwatch/internal/models"
)

// FormatRoundTitle provides capitalized, user-friendly round names for UI.
func FormatRoundTitle(round, total int) string {
	return FormatRoundTitleWithContext(round, total, "")
}

// FormatRoundTitleWithContext provides capitalized round names taking tournament type and bracket side into account.
func FormatRoundTitleWithContext(round, total int, tourneyType string) string {
	if tourneyType == models.TournamentTypeRoundRobin || tourneyType == models.TournamentTypeSwiss {
		r := round
		if r < 0 {
			r = -r
		}
		return fmt.Sprintf("Тур %d", r)
	}

	if round < 0 {
		return fmt.Sprintf("Нижняя сетка, Раунд %d", -round)
	}
	lbl := StageLabel(round, total)
	if lbl == "финал" {
		if tourneyType == models.TournamentTypeDoubleElimination {
			return "Гранд-Финал"
		}
		return "Финал"
	}
	if lbl == "полуфинал" {
		return "Полуфинал"
	}
	if strings.HasPrefix(lbl, "1/") {
		return lbl + " Финала"
	}
	return fmt.Sprintf("Раунд %d", round)
}

// RoundSortKey maps round numbers to a display order.
// For double elimination (hasNegative=true):
// Winners bracket rounds come first (1, 2, ... total-1).
// Losers bracket rounds come next (-1, -2, -3, ...).
// Grand final comes last (total, total+1).
func RoundSortKey(round, total int, hasNegative bool) int {
	// The third-place match (round 0) is shown with the final, not first.
	if round == 0 && !hasNegative {
		return total
	}
	if !hasNegative {
		return round
	}
	if round < 0 {
		return 1000 + (-round)
	}
	if round >= total && total > 1 {
		return 2000 + (round - total)
	}
	return round
}

// DisplayZone is the time zone the league's schedule is shown in: Kazakhstan,
// one zone (UTC+5) for the whole country since 2024. It is fixed rather than
// loaded from tzdata, which older images still ship with Almaty at UTC+6.
var DisplayZone = time.FixedZone("КЗ", 5*3600)

// FormatScheduledTime is the estimated start of a round in Kazakhstan time:
// rounds are planned 35 minutes apart from the tournament start.
func FormatScheduledTime(startsAt time.Time, round int) string {
	if startsAt.IsZero() {
		return "По готовности"
	}
	effectiveRound := round
	if round < 0 {
		effectiveRound = -round + 1
	}
	t := startsAt.Add(time.Duration((effectiveRound-1)*35) * time.Minute)
	return t.In(DisplayZone).Format("15:04") + " (КЗ)"
}

// StageLabel names a round by how far it is from the final: "финал",
// "полуфинал", "1/4". The bracket is single elimination, so the distance to
// the last round is the whole story.
func StageLabel(round, total int) string {
	if total <= 0 || round <= 0 {
		return fmt.Sprintf("раунд %d", round)
	}
	d := total - round
	switch {
	case d < 0:
		return fmt.Sprintf("раунд %d", round)
	case d == 0:
		return "финал"
	case d == 1:
		return "полуфинал"
	case d > 30:
		return fmt.Sprintf("раунд %d", round)
	default:
		return fmt.Sprintf("1/%d", 1<<uint(d))
	}
}

// RoundLabel is StageLabel with the round number in front, the wording the
// Telegram bracket messages have always used.
func RoundLabel(round, total int) string {
	return fmt.Sprintf("Раунд %d — %s", round, StageLabel(round, total))
}

// TotalRounds is the last round present in the cache, i.e. the final.
func TotalRounds(ms []models.BracketMatch) int {
	total := 0
	for _, m := range ms {
		if m.Round > total {
			total = m.Round
		}
	}
	return total
}

// eliminationMatch returns the completed match in which the team went out:
// its latest loss in play order, which in double elimination is the
// lower-bracket loss rather than the earlier upper one. Nil while the team is
// still in the bracket or has never lost.
func eliminationMatch(ms []models.BracketMatch, teamID int) *models.BracketMatch {
	total := TotalRounds(ms)
	hasNegative := false
	for _, m := range ms {
		hasNegative = hasNegative || m.Round < 0
	}
	var lost *models.BracketMatch
	best := 0
	for i := range ms {
		m := &ms[i]
		if m.State != models.BracketComplete || !m.Has(teamID) || m.WinnerID == nil || *m.WinnerID == teamID {
			continue
		}
		if k := RoundSortKey(m.Round, total, hasNegative); lost == nil || k >= best {
			lost, best = m, k
		}
	}
	return lost
}
