package application

import (
	"testing"

	"blackwatch/internal/models"
)

func stageInt(v int) *int { return &v }

// StageLabel names a round by distance from the final. It is the half of the
// old delivery-layer roundLabel that the sheet export needs on its own: the
// sheet already carries the round number in its own column.
func TestStageLabel(t *testing.T) {
	cases := []struct {
		round, total int
		want         string
	}{{3, 3, "финал"}, {2, 3, "полуфинал"}, {1, 3, "1/4"}, {1, 4, "1/8"}}
	for _, c := range cases {
		if got := StageLabel(c.round, c.total); got != c.want {
			t.Errorf("StageLabel(%d,%d) = %q, want %q", c.round, c.total, got, c.want)
		}
	}
}

func TestRoundLabelKeepsBotWording(t *testing.T) {
	if got := RoundLabel(2, 3); got != "Раунд 2 — полуфинал" {
		t.Errorf("RoundLabel(2,3) = %q, want %q", got, "Раунд 2 — полуфинал")
	}
}

func TestTotalRounds(t *testing.T) {
	ms := []models.BracketMatch{{Round: 1}, {Round: 3}, {Round: 2}}
	if got := TotalRounds(ms); got != 3 {
		t.Errorf("TotalRounds = %d, want 3", got)
	}
	if got := TotalRounds(nil); got != 0 {
		t.Errorf("TotalRounds(nil) = %d, want 0", got)
	}
}

// eliminationMatch is the single definition of "where did this team go out":
// the last completed match it lost. Reinstate and the sheet export share it.
func TestEliminationMatchReturnsLastLoss(t *testing.T) {
	ms := []models.BracketMatch{
		{ID: 1, Round: 1, Team1ID: stageInt(7), Team2ID: stageInt(8), WinnerID: stageInt(7), State: models.BracketComplete},
		{ID: 2, Round: 2, Team1ID: stageInt(7), Team2ID: stageInt(9), WinnerID: stageInt(9), State: models.BracketComplete},
	}
	got := eliminationMatch(ms, 7)
	if got == nil {
		t.Fatal("eliminationMatch = nil, want the round-2 loss")
	}
	if got.ID != 2 {
		t.Errorf("eliminated in match %d, want 2", got.ID)
	}
}

func TestEliminationMatchNilWhileStillAlive(t *testing.T) {
	ms := []models.BracketMatch{
		{ID: 1, Round: 1, Team1ID: stageInt(7), Team2ID: stageInt(8), WinnerID: stageInt(7), State: models.BracketComplete},
		{ID: 2, Round: 2, Team1ID: stageInt(7), Team2ID: stageInt(9), State: models.BracketOpen},
	}
	if got := eliminationMatch(ms, 7); got != nil {
		t.Errorf("eliminationMatch = %+v, want nil for a team still in the bracket", got)
	}
}
