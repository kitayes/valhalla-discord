package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"blackwatch/internal/models"
)

// claim sends a screenshot and answers "who won" for a team's captain.
func claim(t *testing.T, svc *TelegramServiceImpl, team int, weWon bool) *GameEvent {
	t.Helper()
	ctx := context.Background()
	prompt, err := svc.AddGamePhoto(ctx, captainOf(team), "photo")
	if err != nil {
		t.Fatalf("AddGamePhoto(team %d): %v", team, err)
	}
	ev, err := svc.ClaimGame(ctx, captainOf(team), prompt.Match.ID, weWon)
	if err != nil {
		t.Fatalf("ClaimGame(team %d): %v", team, err)
	}
	return ev
}

func TestSeriesBestOfThree(t *testing.T) {
	svc, repo, prov, _ := resultFixture(t, models.StageStandalone, 0)
	ctx := context.Background()
	m := openMatchOf(t, repo, 1)
	a, b := *m.Team1ID, *m.Team2ID

	ev := claim(t, svc, a, true)
	if ev.Game.GameNo != 1 || ev.Game.Status != models.GamePending || ev.Series.BO != 3 {
		t.Fatalf("claimed game = %+v, series BO%d", ev.Game, ev.Series.BO)
	}
	ev, err := svc.ConfirmGame(ctx, captainOf(b), ev.Game.ID)
	if err != nil || ev.Series.Wins[a] != 1 || ev.Final != nil {
		t.Fatalf("after game 1: %+v, %v", ev, err)
	}
	if line := SeriesLine(ev.Match, ev.Series); !strings.Contains(line, "1:0") || !strings.Contains(line, "BO3 — до 2 побед") {
		t.Errorf("series line = %q", line)
	}

	ev = claim(t, svc, b, false) // the trailing side reports the other's win
	ev, err = svc.ConfirmGame(ctx, captainOf(a), ev.Game.ID)
	if err != nil {
		t.Fatalf("confirm game 2: %v", err)
	}
	if ev.Final == nil || ev.Final.Score != "2:0" || ev.Final.WinnerTeamID != a || len(ev.Final.PhotoFileIDs) != 2 {
		t.Fatalf("final report = %+v", ev.Final)
	}
	if prov.count("ReportMatch") != 1 {
		t.Errorf("Challonge written %d times", prov.count("ReportMatch"))
	}
	if done := matchState(t, repo, m.PlayOrder); done.State != models.BracketComplete {
		t.Errorf("match state after the series = %q", done.State)
	}
	if _, err := svc.AddGamePhoto(ctx, captainOf(b), "late"); err == nil {
		t.Error("a screenshot was taken for a finished match")
	}
}

func TestSeriesOneOpenGameAtATime(t *testing.T) {
	svc, repo, _, _ := resultFixture(t, models.StageStandalone, 0)
	m := openMatchOf(t, repo, 1)
	claim(t, svc, *m.Team1ID, true)
	if _, err := svc.AddGamePhoto(context.Background(), captainOf(*m.Team2ID), "x"); err == nil || !strings.Contains(err.Error(), "ждёт подтверждения") {
		t.Errorf("second open game: %v", err)
	}
}

func TestSeriesOnlyTheOpponentAnswers(t *testing.T) {
	svc, repo, _, _ := resultFixture(t, models.StageStandalone, 0)
	m := openMatchOf(t, repo, 1)
	ev := claim(t, svc, *m.Team1ID, true)
	if _, err := svc.ConfirmGame(context.Background(), captainOf(*m.Team1ID), ev.Game.ID); err == nil {
		t.Error("the reporting captain confirmed the team's own game")
	}
	other := openMatchOf(t, repo, 2)
	if _, err := svc.ConfirmGame(context.Background(), captainOf(*other.Team1ID), ev.Game.ID); err == nil {
		t.Error("a captain from another match confirmed the game")
	}
}

func TestSeriesDisputeAndReferee(t *testing.T) {
	svc, repo, _, _ := resultFixture(t, models.StageStandalone, 0)
	ctx := context.Background()
	m := openMatchOf(t, repo, 1)
	a, b := *m.Team1ID, *m.Team2ID
	ev := claim(t, svc, a, true)
	ev, err := svc.DisputeGame(ctx, captainOf(b), ev.Game.ID)
	if err != nil || ev.Game.Status != models.GameDisputed {
		t.Fatalf("dispute: %+v, %v", ev, err)
	}
	if _, err := svc.ConfirmGame(ctx, captainOf(b), ev.Game.ID); err == nil {
		t.Error("a disputed game was confirmed by a captain")
	}
	ev, err = svc.JudgeGame(ctx, ev.Game.ID, b)
	if err != nil || ev.Series.Wins[b] != 1 || ev.Series.Wins[a] != 0 || ev.Game.GameNo != 1 {
		t.Fatalf("referee gave game 1 to B: %+v, %v", ev, err)
	}

	ev = claim(t, svc, a, true)
	ev, err = svc.JudgeGame(ctx, ev.Game.ID, 0)
	if err != nil || ev.Game.Status != models.GameAnnulled || ev.Series.NextGameNo != 2 {
		t.Fatalf("annulled game 2: %+v, %v", ev, err)
	}
}

func TestSeriesAutoConfirm(t *testing.T) {
	svc, repo, _, _ := resultFixture(t, models.StageStandalone, 0)
	now := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	m := openMatchOf(t, repo, 1)
	claim(t, svc, *m.Team1ID, true)

	if evs, _ := svc.AutoConfirmExpiredGames(context.Background()); len(evs) != 0 {
		t.Fatalf("confirmed before the window passed: %+v", evs)
	}
	now = now.Add(GameConfirmWindow + time.Second)
	evs, err := svc.AutoConfirmExpiredGames(context.Background())
	if err != nil || len(evs) != 1 || evs[0].Series.Wins[*m.Team1ID] != 1 {
		t.Fatalf("auto-confirm = %+v, %v", evs, err)
	}
}

func TestSeriesBestOfOneDecidesAtOnce(t *testing.T) {
	svc, repo, _, _ := resultFixture(t, models.StageQualifier, 2)
	active, _ := repo.GetActiveTournament(context.Background())
	active.FormatFinal = 1
	m := openMatchOf(t, repo, 1)
	ev := claim(t, svc, *m.Team1ID, true)
	ev, err := svc.ConfirmGame(context.Background(), captainOf(*m.Team2ID), ev.Game.ID)
	if err != nil || ev.Final == nil || ev.Final.Score != "1:0" {
		t.Fatalf("BO1 series: %+v, %v", ev, err)
	}
}

func TestSeriesNeedsScreenshotAndCaptain(t *testing.T) {
	svc, repo, _, _ := resultFixture(t, models.StageStandalone, 0)
	ctx := context.Background()
	m := openMatchOf(t, repo, 1)
	if _, err := svc.ClaimGame(ctx, captainOf(*m.Team1ID), m.ID, true); err == nil {
		t.Error("a game without a screenshot was accepted")
	}
	// Non-captain member of team 1 (addBracketTeam gives telegram id 100*id+i).
	if _, err := svc.AddGamePhoto(ctx, int64(100**m.Team1ID+1), "x"); err == nil {
		t.Error("a non-captain's screenshot was accepted")
	}
}

func TestFormatLong(t *testing.T) {
	for bo, want := range map[int]string{1: "BO1 — до 1 победы", 3: "BO3 — до 2 побед", 5: "BO5 — до 3 побед"} {
		if got := FormatLong(bo); got != want {
			t.Errorf("FormatLong(%d) = %q, want %q", bo, got, want)
		}
	}
}

// A series decided while Challonge is down is saved but not in the bracket
// yet; the bot must not say it was written.
func TestSeriesDecidedWhileChallongeIsDownIsQueued(t *testing.T) {
	svc, repo, prov, _ := resultFixture(t, models.StageQualifier, 2)
	active, _ := repo.GetActiveTournament(context.Background())
	active.FormatFinal = 1
	m := openMatchOf(t, repo, 1)
	ev := claim(t, svc, *m.Team1ID, true)
	prov.fail["ReportMatch"] = errors.New("503")
	ev, err := svc.ConfirmGame(context.Background(), captainOf(*m.Team2ID), ev.Game.ID)
	if err != nil || ev.Final == nil {
		t.Fatalf("BO1 series while down: %+v, %v", ev, err)
	}
	if !ev.Queued {
		t.Error("series result reported as written while Challonge refused it")
	}
}
