package application

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"blackwatch/internal/models"
)

// resultFixture: a built 4-team bracket on the fake Challonge, every message
// captured by chat id.
func resultFixture(t *testing.T, stage string, advance int) (*TelegramServiceImpl, *fakeTelegramRepo, *fakeChallonge, map[int64][]string) {
	t.Helper()
	bsvc, repo, prov := newBracketSvc(t)
	active, _ := repo.GetActiveTournament(context.Background())
	active.Stage, active.AdvanceCount = stage, advance
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10)
		enter(repo, i, true)
	}
	if _, err := bsvc.Build(context.Background(), tourneyAt); err != nil {
		t.Fatalf("Build: %v", err)
	}
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	got := map[int64][]string{}
	svc.SetMatchNotifier(func(_ context.Context, chatID int64, text string, _ bool) {
		got[chatID] = append(got[chatID], text)
	})
	return svc, repo, prov, got
}

func openMatchOf(t *testing.T, repo *fakeTelegramRepo, team int) models.BracketMatch {
	t.Helper()
	ms, _ := repo.GetBracketMatches(context.Background())
	if m := openMatchIn(ms, team); m != nil {
		return *m
	}
	t.Fatalf("team %d has no open match", team)
	return models.BracketMatch{}
}

func lastText(got map[int64][]string, team int) string {
	msgs := got[captainOf(team)]
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1]
}

func TestRecordResultSingleElimination(t *testing.T) {
	svc, repo, prov, got := resultFixture(t, models.StageStandalone, 0)
	ctx := context.Background()
	semi := openMatchOf(t, repo, 1)
	winner, loser := *semi.Team1ID, *semi.Team2ID
	if err := svc.recordResult(ctx, semi.ID, winner, 2, 1, false); err != nil {
		t.Fatalf("recordResult: %v", err)
	}
	if n := prov.count("ReportMatch"); n != 1 {
		t.Errorf("Challonge written %d times, want once", n)
	}
	if !strings.Contains(lastText(got, winner), "Следующий матч") {
		t.Errorf("semifinal winner got %q", lastText(got, winner))
	}
	if !strings.Contains(lastText(got, loser), "выбывает из турнира") {
		t.Errorf("semifinal loser got %q", lastText(got, loser))
	}

	other := openMatchOf(t, repo, 2)
	if err := svc.recordResult(ctx, other.ID, *other.Team1ID, 2, 0, false); err != nil {
		t.Fatal(err)
	}
	final := openMatchOf(t, repo, winner)
	if err := svc.recordResult(ctx, final.ID, winner, 2, 0, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastText(got, winner), "Победа в финале") {
		t.Errorf("final winner got %q", lastText(got, winner))
	}
}

// The decider of a qualifier sends its winner to the main stage; it is not a
// final, whatever the bracket shape says.
func TestRecordResultQualifierDecider(t *testing.T) {
	svc, repo, _, got := resultFixture(t, models.StageQualifier, 2)
	m := openMatchOf(t, repo, 1)
	winner, loser := *m.Team1ID, *m.Team2ID
	if err := svc.recordResult(context.Background(), m.ID, winner, 1, 0, false); err != nil {
		t.Fatal(err)
	}
	if w := lastText(got, winner); !strings.Contains(w, "проходит в основной этап") || strings.Contains(w, "финале") {
		t.Errorf("decider winner got %q", w)
	}
	if !strings.Contains(lastText(got, loser), "выбывает из квалификации") {
		t.Errorf("decider loser got %q", lastText(got, loser))
	}
}

// In double elimination an upper-bracket loss moves the team down, it does
// not knock it out.
func TestNotifyResultUpperBracketLoss(t *testing.T) {
	svc, repo := newTelegramSvc()
	repo.tournaments[0].TournamentType = models.TournamentTypeDoubleElimination
	repo.tournaments[0].BracketSize = 4
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10)
	}
	got := map[int64][]string{}
	svc.SetMatchNotifier(func(_ context.Context, chatID int64, text string, _ bool) {
		got[chatID] = append(got[chatID], text)
	})
	played := sm(1, 1, 1, 2, 1)
	repo.bracket = []models.BracketMatch{played, sm(1, 2, 3, 4, 0), sm(2, 3, 1, 0, 0), sm(-1, 4, 2, 0, 0)}
	svc.notifyResult(context.Background(), played, 1, 2, 1, 0)
	if !strings.Contains(lastText(got, 2), "нижнюю сетку") {
		t.Errorf("upper-bracket loser got %q", lastText(got, 2))
	}
}
