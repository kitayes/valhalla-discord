package telegram

import (
	"strings"
	"testing"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/models"
)

func bracketInt(v int) *int { return &v }

func TestRoundLabel(t *testing.T) {
	cases := []struct {
		round, total int
		want         string
	}{{3, 3, "Раунд 3 — финал"}, {2, 3, "Раунд 2 — полуфинал"}, {1, 3, "Раунд 1 — 1/4"}}
	for _, c := range cases {
		if got := application.RoundLabel(c.round, c.total); got != c.want {
			t.Errorf("RoundLabel(%d,%d) = %q, want %q", c.round, c.total, got, c.want)
		}
	}
}

func TestFormatBracketRound(t *testing.T) {
	ms := []models.BracketMatch{
		{PlayOrder: 1, Round: 1, Team1ID: bracketInt(1), Team2ID: bracketInt(4), Team1Name: "T1", Team2Name: "T4", State: models.BracketComplete, WinnerID: bracketInt(1), ScoresCSV: "2 - 0"},
		{PlayOrder: 2, Round: 1, Team1ID: bracketInt(2), Team2ID: bracketInt(3), Team1Name: "T2", Team2Name: "T3", State: models.BracketOpen},
		{PlayOrder: 3, Round: 2, Team1ID: bracketInt(1), Team1Name: "T1", State: models.BracketPending},
	}
	text := formatBracketRound("https://challonge.com/x", ms, 1)[0]
	for _, want := range []string{"https://challonge.com/x", "Раунд 1 — полуфинал", "#1 T1 vs T4 — 2 - 0, победа T1", "#2 T2 vs T3 — идёт"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if r2 := formatBracketRound("u", ms, 2)[0]; !strings.Contains(r2, "#3 T1 vs ? — ожидает соперника") {
		t.Errorf("pending line wrong:\n%s", r2)
	}
}

func TestFormatBracketRoundSplitsLongOutput(t *testing.T) {
	var ms []models.BracketMatch
	for i := 1; i <= 64; i++ {
		ms = append(ms, models.BracketMatch{PlayOrder: i, Round: 1, Team1ID: bracketInt(i), Team2ID: bracketInt(i + 100), Team1Name: strings.Repeat("A", 30), Team2Name: strings.Repeat("B", 30), State: models.BracketOpen})
	}
	parts := formatBracketRound("u", ms, 1)
	if len(parts) < 2 {
		t.Fatalf("64 long lines fit in one message")
	}
	for i, part := range parts {
		if len(part) > telegramMessageLimit {
			t.Errorf("part %d has %d bytes", i, len(part))
		}
	}
}

func TestDefaultBracketRound(t *testing.T) {
	ms := []models.BracketMatch{{Round: 1, State: models.BracketComplete}, {Round: 2, State: models.BracketOpen}, {Round: 3, State: models.BracketPending}}
	if got := defaultBracketRound(ms); got != 2 {
		t.Errorf("default round = %d, want 2", got)
	}
	if got := defaultBracketRound(nil); got != 1 {
		t.Errorf("empty default = %d, want 1", got)
	}
}

func TestParseSetWinner(t *testing.T) {
	po, team, w, l, err := parseSetWinner("12 Team Liquid 2:1", 1, 0)
	if err != nil || po != 12 || team != "Team Liquid" || w != 2 || l != 1 {
		t.Fatalf("parsed %d %q %d:%d err=%v", po, team, w, l, err)
	}
	for _, bad := range []string{"", "x Team", "7", "7 Team 1:2", "7 Team 2-2"} {
		if _, _, _, _, err := parseSetWinner(bad, 1, 0); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestBracketBuildDue(t *testing.T) {
	tourney := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{{tourney.Add(-90 * time.Minute), false}, {tourney.Add(-60 * time.Minute), true}, {tourney.Add(-5 * time.Minute), true}, {tourney.Add(time.Minute), false}} {
		if got := bracketBuildDue(tourney, tc.now); got != tc.want {
			t.Errorf("due(%s) = %v, want %v", tc.now, got, tc.want)
		}
	}
}
