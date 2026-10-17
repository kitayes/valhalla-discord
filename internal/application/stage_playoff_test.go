package application

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"blackwatch/internal/models"
)

// sixteenForSeven is a full 16-team qualifier sending 7 teams: the round of 16
// is played (PO 1-8, the lower number wins) and the quarterfinals are drawn
// (PO 9-12). The second quarterfinal, PO 10, is the playoff.
func sixteenForSeven(playoffWinner int) []models.BracketMatch {
	var ms []models.BracketMatch
	for i := 0; i < 8; i++ {
		a, b := 2*i+1, 2*i+2
		ms = append(ms, sm(1, i+1, a, b, a))
	}
	ms = append(ms,
		sm(2, 9, 1, 3, 0),
		sm(2, 10, 5, 7, playoffWinner),
		sm(2, 11, 9, 11, 0),
		sm(2, 12, 13, 15, 0),
		sm(3, 13, 0, 0, 0), sm(3, 14, 0, 0, 0), sm(4, 15, 0, 0, 0),
	)
	return ms
}

func TestCutoffSevenPlaysTheSecondMatchOfTheEight(t *testing.T) {
	ms := sixteenForSeven(0)
	got := (Layout{UBRounds: 4}).Cutoff(7, ms)
	want := Cutoff{UB: 1, Playoff: 2, PlayoffOrder: 10}
	if got != want {
		t.Fatalf("cutoff = %+v, want %+v", got, want)
	}
	// The cache keeps only the playoff of its round: read back, it must
	// still point at the same match.
	cached := FilterStage(qualifier(models.TournamentTypeSingleElimination, 7, 16), ms)
	if again := (Layout{UBRounds: 4}).Cutoff(7, cached); again != want {
		t.Errorf("cutoff from the cache = %+v, want %+v", again, want)
	}
}

func TestFilterStageKeepsOnlyThePlayoffPastTheEight(t *testing.T) {
	tour := qualifier(models.TournamentTypeSingleElimination, 7, 16)
	cached := FilterStage(tour, sixteenForSeven(0))
	var orders []int
	for _, m := range cached {
		orders = append(orders, m.PlayOrder)
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 10}
	if !reflect.DeepEqual(orders, want) {
		t.Fatalf("cached play orders = %v, want %v", orders, want)
	}
	if again := FilterStage(tour, cached); len(again) != len(cached) {
		t.Errorf("filtering the cache again kept %d of %d matches", len(again), len(cached))
	}
}

func TestQualifierStatusSevenWaitsForThePlayoff(t *testing.T) {
	tour := qualifier(models.TournamentTypeSingleElimination, 7, 16)
	out := QualifierStatus(tour, FilterStage(tour, sixteenForSeven(0)), teamRange(1, 16))
	if out.Done || len(out.Pending) != 1 || out.Pending[0].PlayOrder != 10 {
		t.Fatalf("outcome = %+v, want the playoff pending", out)
	}

	out = QualifierStatus(tour, FilterStage(tour, sixteenForSeven(7)), teamRange(1, 16))
	if !out.Done || out.Err != nil {
		t.Fatalf("outcome = %+v", out)
	}
	want := []int{1, 3, 7, 9, 11, 13, 15}
	if !reflect.DeepEqual(out.Qualified, want) {
		t.Errorf("qualified = %v, want %v", out.Qualified, want)
	}
}

// Twelve teams in a 16 bracket: round 1 has four matches, round 2 is the
// eight. Its second match is still the playoff.
func TestCutoffSevenWithByes(t *testing.T) {
	ms := []models.BracketMatch{
		sm(1, 1, 5, 12, 5), sm(1, 2, 6, 11, 11), sm(1, 3, 7, 10, 7), sm(1, 4, 8, 9, 8),
		sm(2, 5, 1, 5, 0), sm(2, 6, 4, 8, 0), sm(2, 7, 2, 7, 0), sm(2, 8, 3, 11, 0),
		sm(3, 9, 0, 0, 0), sm(3, 10, 0, 0, 0), sm(4, 11, 0, 0, 0),
	}
	if got := (Layout{UBRounds: 4}).Cutoff(7, ms); got.PlayoffOrder != 6 {
		t.Errorf("cutoff = %+v, want the playoff at PO 6", got)
	}
}

// Eight teams for seven places: the playoff is the second match of round 1
// and the other three are not played. Seven or fewer all go through.
func TestCutoffSevenFromEight(t *testing.T) {
	tour := qualifier(models.TournamentTypeSingleElimination, 7, 8)
	full := []models.BracketMatch{
		sm(1, 1, 1, 8, 0), sm(1, 2, 4, 5, 0), sm(1, 3, 2, 7, 0), sm(1, 4, 3, 6, 0),
		sm(2, 5, 0, 0, 0), sm(2, 6, 0, 0, 0), sm(3, 7, 0, 0, 0),
	}
	want := Cutoff{UB: 0, Playoff: 1, PlayoffOrder: 2}
	if got := (Layout{UBRounds: 3}).Cutoff(7, full); got != want {
		t.Fatalf("cutoff = %+v, want %+v", got, want)
	}
	cached := FilterStage(tour, full)
	if len(cached) != 1 || cached[0].PlayOrder != 2 {
		t.Fatalf("cache = %+v, want the playoff only", cached)
	}
	if got := (Layout{UBRounds: 3}).Cutoff(7, cached); got != want {
		t.Errorf("cutoff from the cache = %+v, want %+v", got, want)
	}
	if out := QualifierStatus(tour, cached, teamRange(1, 8)); out.Done || len(out.Pending) != 1 {
		t.Errorf("outcome = %+v, want the playoff pending", out)
	}

	seven := []models.BracketMatch{
		sm(1, 1, 4, 5, 0), sm(1, 2, 2, 7, 0), sm(1, 3, 3, 6, 0),
		sm(2, 4, 1, 0, 0), sm(2, 5, 0, 0, 0), sm(3, 6, 0, 0, 0),
	}
	if got := (Layout{UBRounds: 3}).Cutoff(7, seven); !got.All {
		t.Errorf("seven teams: cutoff = %+v, want everyone through", got)
	}
	if out := QualifierStatus(tour, seven, teamRange(1, 7)); !out.Done || len(out.Qualified) != 7 {
		t.Errorf("seven teams: outcome = %+v, want all seven qualified", out)
	}
}

func TestPlayoffRoundIsADecider(t *testing.T) {
	tour := qualifier(models.TournamentTypeSingleElimination, 7, 16)
	cached := FilterStage(tour, sixteenForSeven(0))
	if bo := MatchFormat(tour, cached, 2); bo != 5 {
		t.Errorf("playoff format = BO%d, want the decider's BO5", bo)
	}
	if title := RoundTitle(tour, cached, 2); !strings.Contains(title, "Добивочный") {
		t.Errorf("playoff title = %q", title)
	}
	if title := RoundTitle(tour, cached, 1); !strings.Contains(title, "за выход в основу") {
		t.Errorf("round of 16 title = %q", title)
	}
}

func TestNormalizeSettingsAdvanceCounts(t *testing.T) {
	cases := []struct {
		typ     string
		advance int
		ok      bool
	}{
		{models.TournamentTypeSingleElimination, 8, true},
		{models.TournamentTypeSingleElimination, 7, true},
		{models.TournamentTypeSingleElimination, 3, true},
		{models.TournamentTypeSingleElimination, 15, true},
		{models.TournamentTypeSingleElimination, 6, false},
		{models.TournamentTypeSingleElimination, 1, false},
		{models.TournamentTypeDoubleElimination, 8, true},
		{models.TournamentTypeDoubleElimination, 7, false},
	}
	for _, c := range cases {
		set := TournamentSettings{Name: "Q", Stage: models.StageQualifier, TournamentType: c.typ, AdvanceCount: c.advance}
		if err := normalizeSettings(&set); (err == nil) != c.ok {
			t.Errorf("%s, advance %d: err = %v, want ok=%v", c.typ, c.advance, err, c.ok)
		}
	}
}

// playoffNotifySvc is a 16-team qualifier for 7 with the given cache.
func playoffNotifySvc(t *testing.T, cache []models.BracketMatch) (*TelegramServiceImpl, map[int64][]string) {
	t.Helper()
	svc, repo := newTelegramSvc()
	tour := repo.tournaments[0]
	tour.Stage, tour.AdvanceCount = models.StageQualifier, 7
	tour.TournamentType, tour.BracketSize = models.TournamentTypeSingleElimination, 16
	for i := 1; i <= 16; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10)
	}
	repo.bracket = cache
	got := map[int64][]string{}
	svc.SetMatchNotifier(func(_ context.Context, chatID int64, text string, _ bool) {
		got[chatID] = append(got[chatID], text)
	})
	return svc, got
}

func TestNotifyResultBeforeThePlayoff(t *testing.T) {
	tour := qualifier(models.TournamentTypeSingleElimination, 7, 16)
	cache := FilterStage(tour, sixteenForSeven(0))

	// Team 5 sits in the playoff: it is told it plays one more.
	svc, got := playoffNotifySvc(t, cache)
	svc.notifyResult(context.Background(), cache[2], 5, 6, 1, 0)
	if w := lastText(got, 5); !strings.Contains(w, "добивочный матч") || strings.Contains(w, "проходит в основной этап") {
		t.Errorf("winner into the playoff got %q", w)
	}
	// Team 1 is not in it and the playoff pair is known: it is through.
	svc.notifyResult(context.Background(), cache[0], 1, 2, 1, 0)
	if w := lastText(got, 1); !strings.Contains(w, "проходит в основной этап") {
		t.Errorf("winner past the playoff got %q", w)
	}

	// The playoff pair is not known yet: nobody is promised a place.
	open := FilterStage(tour, sixteenForSeven(0))
	for i := range open {
		if open[i].PlayOrder == 10 {
			open[i].Team2ID, open[i].State = nil, models.BracketPending
		}
	}
	svc, got = playoffNotifySvc(t, open)
	svc.notifyResult(context.Background(), open[0], 1, 2, 1, 0)
	if w := lastText(got, 1); strings.Contains(w, "проходит в основной этап") || !strings.Contains(w, "добивочный") {
		t.Errorf("winner before the playoff pair is known got %q", w)
	}
}

func TestNotifyResultOfThePlayoff(t *testing.T) {
	tour := qualifier(models.TournamentTypeSingleElimination, 7, 16)
	cache := FilterStage(tour, sixteenForSeven(7))
	svc, got := playoffNotifySvc(t, cache)
	playoff := cache[len(cache)-1]
	svc.notifyResult(context.Background(), playoff, 7, 5, 3, 1)
	if w := lastText(got, 7); !strings.Contains(w, "проходит в основной этап") {
		t.Errorf("playoff winner got %q", w)
	}
	if l := lastText(got, 5); !strings.Contains(l, "выбывает из квалификации") {
		t.Errorf("playoff loser got %q", l)
	}
}
