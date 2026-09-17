package application

import (
	"fmt"

	"blackwatch/internal/models"
)

// StageLabel names a round by how far it is from the final: "финал",
// "полуфинал", "1/4". The bracket is single elimination, so the distance to
// the last round is the whole story.
func StageLabel(round, total int) string {
	switch d := total - round; d {
	case 0:
		return "финал"
	case 1:
		return "полуфинал"
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

// eliminationMatch returns the completed match this team lost, or nil while it
// is still in the bracket. Callers pass the cache in round order, which
// GetBracketMatches guarantees, so the last such match is the elimination.
func eliminationMatch(ms []models.BracketMatch, teamID int) *models.BracketMatch {
	var lost *models.BracketMatch
	for i := range ms {
		m := &ms[i]
		if m.State == models.BracketComplete && m.Has(teamID) && m.WinnerID != nil && *m.WinnerID != teamID {
			lost = m
		}
	}
	return lost
}
