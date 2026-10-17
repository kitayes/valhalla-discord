package application

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"blackwatch/internal/models"
)

// sm builds a cached match; 0 stands for an undecided slot.
func sm(round, play, t1, t2, winner int) models.BracketMatch {
	ptr := func(v int) *int {
		if v == 0 {
			return nil
		}
		return &v
	}
	state := models.BracketPending
	switch {
	case winner != 0:
		state = models.BracketComplete
	case t1 != 0 && t2 != 0:
		state = models.BracketOpen
	}
	return models.BracketMatch{ID: play, Round: round, PlayOrder: play, Team1ID: ptr(t1), Team2ID: ptr(t2), WinnerID: ptr(winner), State: state}
}

func teamRange(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func qualifier(typ string, advance, size int) *models.TelegramTournament {
	return &models.TelegramTournament{Stage: models.StageQualifier, TournamentType: typ, AdvanceCount: advance, BracketSize: size,
		FormatRounds: 1, FormatSemifinal: 3, FormatFinal: 5}
}

func TestCutoffSingle(t *testing.T) {
	cases := []struct {
		ub   int
		want Cutoff
	}{
		{4, Cutoff{UB: 1}},
		{5, Cutoff{UB: 2}},
		{3, Cutoff{All: true}},
	}
	for _, c := range cases {
		if got := (Layout{UBRounds: c.ub}).Cutoff(8, nil); got != c.want {
			t.Errorf("SE UBRounds=%d: got %+v, want %+v", c.ub, got, c.want)
		}
	}
}

func TestCutoffDouble(t *testing.T) {
	cases := []struct {
		ub   int
		want Cutoff
	}{
		{4, Cutoff{UB: 2, LB: 2}},
		{5, Cutoff{UB: 3, LB: 4}},
		{3, Cutoff{All: true}},
	}
	for _, c := range cases {
		l := Layout{Double: true, UBRounds: c.ub, LBRounds: 2*c.ub - 2}
		if got := l.Cutoff(8, nil); got != c.want {
			t.Errorf("DE UBRounds=%d: got %+v, want %+v", c.ub, got, c.want)
		}
	}
}

func TestNewLayoutDerivesSize(t *testing.T) {
	se := []models.BracketMatch{sm(1, 1, 1, 2, 1), sm(4, 15, 0, 0, 0)}
	if l := NewLayout(&models.TelegramTournament{TournamentType: models.TournamentTypeSingleElimination}, se); l.UBRounds != 4 || l.Double {
		t.Errorf("SE layout = %+v", l)
	}
	de := []models.BracketMatch{sm(1, 1, 1, 2, 1), sm(-6, 30, 0, 0, 0), sm(5, 31, 0, 0, 0)}
	if l := NewLayout(&models.TelegramTournament{TournamentType: models.TournamentTypeDoubleElimination}, de); l.UBRounds != 4 || l.LBRounds != 6 || !l.Double {
		t.Errorf("DE layout = %+v", l)
	}
	stored := &models.TelegramTournament{TournamentType: models.TournamentTypeDoubleElimination, BracketSize: 32}
	if l := NewLayout(stored, nil); l.UBRounds != 5 || l.LBRounds != 8 {
		t.Errorf("stored size layout = %+v", l)
	}
}

// Sixteen teams: round 1 is the round of 16. Its eight winners advance; a
// quarterfinal played early does not count.
func TestQualifierStatusSingle16(t *testing.T) {
	var ms []models.BracketMatch
	for i := 0; i < 8; i++ {
		a, b := 2*i+1, 2*i+2
		ms = append(ms, sm(1, i+1, a, b, a))
	}
	ms = append(ms, sm(2, 9, 1, 3, 1)) // quarterfinal, past the line
	out := QualifierStatus(qualifier(models.TournamentTypeSingleElimination, 8, 16), ms, teamRange(1, 16))
	if !out.Done || out.Err != nil {
		t.Fatalf("outcome = %+v", out)
	}
	want := []int{1, 3, 5, 7, 9, 11, 13, 15}
	if !reflect.DeepEqual(out.Qualified, want) {
		t.Errorf("qualified = %v, want %v", out.Qualified, want)
	}
}

func TestQualifierStatusPending(t *testing.T) {
	var ms []models.BracketMatch
	for i := 0; i < 7; i++ {
		a, b := 2*i+1, 2*i+2
		ms = append(ms, sm(1, i+1, a, b, a))
	}
	ms = append(ms, sm(1, 8, 15, 16, 0))
	out := QualifierStatus(qualifier(models.TournamentTypeSingleElimination, 8, 16), ms, teamRange(1, 16))
	if out.Done || len(out.Pending) != 1 || out.Pending[0].PlayOrder != 8 {
		t.Fatalf("outcome = %+v", out)
	}
}

// Twelve teams in a 16 bracket: four byes skip round 1, so its four winners
// plus the bye teams make the eight.
func TestQualifierStatusByes(t *testing.T) {
	ms := []models.BracketMatch{
		sm(1, 1, 5, 12, 5), sm(1, 2, 6, 11, 11), sm(1, 3, 7, 10, 7), sm(1, 4, 8, 9, 8),
		sm(2, 5, 1, 5, 0), sm(2, 6, 2, 11, 0),
	}
	out := QualifierStatus(qualifier(models.TournamentTypeSingleElimination, 8, 16), ms, teamRange(1, 12))
	if !out.Done || out.Err != nil {
		t.Fatalf("outcome = %+v", out)
	}
	want := []int{1, 2, 3, 4, 5, 7, 8, 11}
	if !reflect.DeepEqual(out.Qualified, want) {
		t.Errorf("qualified = %v, want %v", out.Qualified, want)
	}
}

// Sixteen teams double elimination: after upper round 2 and lower round -2,
// four upper and four lower teams are left.
func TestQualifierStatusDouble16(t *testing.T) {
	var ms []models.BracketMatch
	play := 0
	next := func() int { play++; return play }
	// Upper round 1: odd beats even.
	var ub1Losers []int
	var ub1Winners []int
	for i := 0; i < 8; i++ {
		a, b := 2*i+1, 2*i+2
		ms = append(ms, sm(1, next(), a, b, a))
		ub1Winners = append(ub1Winners, a)
		ub1Losers = append(ub1Losers, b)
	}
	// Upper round 2: first of each pair wins.
	var ub2Losers []int
	for i := 0; i < 4; i++ {
		a, b := ub1Winners[2*i], ub1Winners[2*i+1]
		ms = append(ms, sm(2, next(), a, b, a))
		ub2Losers = append(ub2Losers, b)
	}
	// Lower round -1: round-1 losers pair up, first wins.
	var lb1Winners []int
	for i := 0; i < 4; i++ {
		a, b := ub1Losers[2*i], ub1Losers[2*i+1]
		ms = append(ms, sm(-1, next(), a, b, a))
		lb1Winners = append(lb1Winners, a)
	}
	// Lower round -2: lower survivors meet upper round-2 losers, who win.
	for i := 0; i < 4; i++ {
		ms = append(ms, sm(-2, next(), lb1Winners[i], ub2Losers[i], ub2Losers[i]))
	}
	// Past the line, must be ignored.
	ms = append(ms, sm(3, next(), 1, 5, 1), sm(-3, next(), ub2Losers[0], ub2Losers[1], 0))

	out := QualifierStatus(qualifier(models.TournamentTypeDoubleElimination, 8, 16), ms, teamRange(1, 16))
	if !out.Done || out.Err != nil {
		t.Fatalf("outcome = %+v", out)
	}
	want := append([]int{1, 5, 9, 13}, ub2Losers...)
	sort.Ints(want)
	if !reflect.DeepEqual(out.Qualified, want) {
		t.Errorf("qualified = %v, want %v", out.Qualified, want)
	}
}

func TestQualifierStatusCountMismatch(t *testing.T) {
	var ms []models.BracketMatch
	for i := 0; i < 8; i++ {
		a, b := 2*i+1, 2*i+2
		ms = append(ms, sm(1, i+1, a, b, a))
	}
	// Team 17 is a participant the cache never mentions.
	out := QualifierStatus(qualifier(models.TournamentTypeSingleElimination, 8, 16), ms, teamRange(1, 17))
	if out.Err == nil {
		t.Fatalf("expected a count mismatch, got %+v", out)
	}
}

func TestQualifierStatusAllAdvance(t *testing.T) {
	out := QualifierStatus(qualifier(models.TournamentTypeSingleElimination, 8, 8), nil, teamRange(1, 6))
	if !out.Done || out.Err != nil || len(out.Qualified) != 6 {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestFilterStage(t *testing.T) {
	ms := []models.BracketMatch{sm(1, 1, 1, 2, 1), sm(2, 2, 1, 3, 0), sm(-1, 3, 2, 4, 0), sm(-3, 4, 0, 0, 0)}
	got := FilterStage(qualifier(models.TournamentTypeDoubleElimination, 8, 16), ms)
	var plays []int
	for _, m := range got {
		plays = append(plays, m.PlayOrder)
	}
	if !reflect.DeepEqual(plays, []int{1, 2, 3}) {
		t.Errorf("kept %v, want [1 2 3]", plays)
	}
	standalone := &models.TelegramTournament{Stage: models.StageStandalone}
	if len(FilterStage(standalone, ms)) != len(ms) {
		t.Error("standalone tournaments keep every match")
	}
}

func TestMatchFormat(t *testing.T) {
	q := qualifier(models.TournamentTypeSingleElimination, 8, 16)
	if got := MatchFormat(q, nil, 1); got != 5 {
		t.Errorf("qualifier decider = %d, want 5", got)
	}
	q32 := qualifier(models.TournamentTypeSingleElimination, 8, 32)
	if got := MatchFormat(q32, nil, 1); got != 1 {
		t.Errorf("qualifier early round = %d, want 1", got)
	}

	se := &models.TelegramTournament{TournamentType: models.TournamentTypeSingleElimination, BracketSize: 16, FormatRounds: 1, FormatSemifinal: 3, FormatFinal: 5}
	for round, want := range map[int]int{1: 1, 2: 1, 3: 3, 4: 5} {
		if got := MatchFormat(se, nil, round); got != want {
			t.Errorf("SE round %d = %d, want %d", round, got, want)
		}
	}

	de := &models.TelegramTournament{TournamentType: models.TournamentTypeDoubleElimination, BracketSize: 16, FormatRounds: 1, FormatSemifinal: 3, FormatFinal: 5}
	for round, want := range map[int]int{1: 1, 3: 1, 4: 3, 5: 5, 6: 5, -1: 1, -5: 1, -6: 3} {
		if got := MatchFormat(de, nil, round); got != want {
			t.Errorf("DE round %d = %d, want %d", round, got, want)
		}
	}

	zero := &models.TelegramTournament{TournamentType: models.TournamentTypeSingleElimination, BracketSize: 8}
	if MatchFormat(zero, nil, 1) != 1 || MatchFormat(zero, nil, 2) != 3 || MatchFormat(zero, nil, 3) != 3 {
		t.Error("zero settings must fall back to 1/3/3")
	}
	if got := FormatLabel(MatchFormat(nil, nil, 1)); got != "BO1" {
		t.Errorf("nil tournament label = %q", got)
	}
}

func TestRoundTitleDouble(t *testing.T) {
	de := &models.TelegramTournament{TournamentType: models.TournamentTypeDoubleElimination, BracketSize: 16}
	want := map[int]string{
		1:  "Верхняя сетка, Раунд 1",
		4:  "Финал верхней сетки",
		5:  "Гранд-финал",
		-2: "Нижняя сетка, Раунд 2",
		-6: "Финал нижней сетки",
	}
	for round, w := range want {
		if got := RoundTitle(de, nil, round); got != w {
			t.Errorf("round %d = %q, want %q", round, got, w)
		}
	}
	q := qualifier(models.TournamentTypeSingleElimination, 8, 16)
	if got := RoundTitle(q, nil, 1); !strings.Contains(got, "за выход в основу") {
		t.Errorf("qualifier decider title = %q", got)
	}
	se := &models.TelegramTournament{TournamentType: models.TournamentTypeSingleElimination, BracketSize: 8}
	if got := RoundTitle(se, nil, 3); got != "Финал" {
		t.Errorf("SE final = %q", got)
	}
}

func TestValidateScore(t *testing.T) {
	ok := [][3]int{{1, 1, 0}, {3, 2, 0}, {3, 2, 1}, {5, 3, 2}, {5, 3, 0}}
	for _, c := range ok {
		if err := ValidateScore(c[0], c[1], c[2]); err != nil {
			t.Errorf("BO%d %d:%d rejected: %v", c[0], c[1], c[2], err)
		}
	}
	bad := [][3]int{{1, 2, 0}, {3, 3, 0}, {3, 1, 0}, {3, 2, 2}, {5, 2, 1}}
	for _, c := range bad {
		if err := ValidateScore(c[0], c[1], c[2]); err == nil {
			t.Errorf("BO%d %d:%d accepted", c[0], c[1], c[2])
		}
	}
}

func TestPlacementsSingle8(t *testing.T) {
	ms := []models.BracketMatch{
		sm(1, 1, 1, 8, 1), sm(1, 2, 4, 5, 4), sm(1, 3, 2, 7, 2), sm(1, 4, 3, 6, 3),
		sm(2, 5, 1, 4, 1), sm(2, 6, 2, 3, 2),
		sm(3, 7, 1, 2, 1),
	}
	places, points := Placements(&models.TelegramTournament{TournamentType: models.TournamentTypeSingleElimination}, ms, teamRange(1, 8))
	want := map[int]int{1: 1, 2: 2, 3: 3, 4: 3, 5: 5, 6: 5, 7: 5, 8: 5}
	if !reflect.DeepEqual(places, want) {
		t.Errorf("places = %v, want %v", places, want)
	}
	if points[1] != 100 || points[2] != 70 || points[3] != 50 || points[8] != 20 {
		t.Errorf("points = %v", points)
	}
}

func TestPlacementsDouble8(t *testing.T) {
	ms := []models.BracketMatch{
		// Upper bracket.
		sm(1, 1, 1, 8, 1), sm(1, 2, 4, 5, 4), sm(1, 3, 2, 7, 2), sm(1, 4, 3, 6, 3),
		sm(2, 5, 1, 4, 1), sm(2, 6, 2, 3, 2),
		sm(3, 7, 1, 2, 1),
		// Lower bracket.
		sm(-1, 8, 8, 5, 5), sm(-1, 9, 7, 6, 6),
		sm(-2, 10, 5, 3, 3), sm(-2, 11, 6, 4, 4),
		sm(-3, 12, 3, 4, 3),
		sm(-4, 13, 2, 3, 2),
		// Grand final.
		sm(4, 14, 1, 2, 1),
	}
	places, _ := Placements(&models.TelegramTournament{TournamentType: models.TournamentTypeDoubleElimination}, ms, teamRange(1, 8))
	want := map[int]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 5, 7: 7, 8: 7}
	if !reflect.DeepEqual(places, want) {
		t.Errorf("places = %v, want %v", places, want)
	}
}

func TestPointsForPlace(t *testing.T) {
	for place, want := range map[int]int{1: 100, 2: 70, 3: 50, 4: 50, 5: 20, 8: 20, 9: 10, 16: 10, 17: 5} {
		if got := PointsForPlace(place); got != want {
			t.Errorf("place %d = %d, want %d", place, got, want)
		}
	}
}

// The schedule is shown in Kazakhstan time: a 15:00 UTC start is 20:00.
func TestFormatScheduledTimeKazakhstan(t *testing.T) {
	start := time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC)
	for round, want := range map[int]string{1: "20:00 (КЗ)", 2: "20:35 (КЗ)", -1: "20:35 (КЗ)"} {
		if got := FormatScheduledTime(start, round); got != want {
			t.Errorf("round %d = %q, want %q", round, got, want)
		}
	}
	if got := FormatScheduledTime(time.Time{}, 1); got != "По готовности" {
		t.Errorf("no start time = %q", got)
	}
}
