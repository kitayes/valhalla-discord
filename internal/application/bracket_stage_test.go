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
	}{
		{3, 3, "финал"},
		{2, 3, "полуфинал"},
		{1, 3, "1/4"},
		{1, 4, "1/8"},
		{4, 3, "раунд 4"},
		{0, 0, "раунд 0"},
		{-1, 3, "раунд -1"},
	}
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

func TestFormatRoundTitleWithContext(t *testing.T) {
	cases := []struct {
		round, total int
		tType        string
		want         string
	}{
		{1, 3, models.TournamentTypeSingleElimination, "1/4 Финала"},
		{2, 3, models.TournamentTypeSingleElimination, "Полуфинал"},
		{3, 3, models.TournamentTypeSingleElimination, "Финал"},
		{-1, 3, models.TournamentTypeDoubleElimination, "Нижняя сетка, Раунд 1"},
		{-2, 3, models.TournamentTypeDoubleElimination, "Нижняя сетка, Раунд 2"},
		{3, 3, models.TournamentTypeDoubleElimination, "Гранд-Финал"},
		{1, 4, models.TournamentTypeRoundRobin, "Тур 1"},
		{2, 4, models.TournamentTypeSwiss, "Тур 2"},
	}
	for _, c := range cases {
		if got := FormatRoundTitleWithContext(c.round, c.total, c.tType); got != c.want {
			t.Errorf("FormatRoundTitleWithContext(%d, %d, %s) = %q, want %q", c.round, c.total, c.tType, got, c.want)
		}
	}
}

func TestRoundSortKey(t *testing.T) {
	// For Single Elimination (no negative)
	if got := RoundSortKey(1, 3, false); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
	// For Double Elimination (hasNegative=true)
	// Winners: 1, 2. Losers: -1, -2. Final: 3.
	if got := RoundSortKey(1, 3, true); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
	if got := RoundSortKey(2, 3, true); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
	if got := RoundSortKey(-1, 3, true); got != 1001 {
		t.Errorf("got %d, want 1001", got)
	}
	if got := RoundSortKey(-2, 3, true); got != 1002 {
		t.Errorf("got %d, want 1002", got)
	}
	if got := RoundSortKey(3, 3, true); got != 2000 {
		t.Errorf("got %d, want 2000", got)
	}
}
