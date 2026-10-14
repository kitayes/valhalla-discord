package application

import (
	"fmt"
	"math"
	"math/bits"
	"sort"

	"blackwatch/internal/models"
)

// Layout is the shape of an elimination bracket. Challonge numbers upper
// (or only) bracket rounds 1..UBRounds, the grand final after them, and lower
// bracket rounds -1..-LBRounds.
type Layout struct {
	Double   bool // double elimination
	UBRounds int  // the last upper round is the final (SE) or the upper final (DE)
	LBRounds int  // 2*UBRounds-2 for double elimination, 0 otherwise
	Other    bool // round robin or swiss: rounds are just tours
}

// NewLayout reads the bracket shape from the tournament and, when its size was
// not recorded at build time, from the cached matches. A qualifier's cache
// stops at the advance line, so the stored size is the reliable source.
func NewLayout(t *models.TelegramTournament, ms []models.BracketMatch) Layout {
	typ := ""
	size := 0
	if t != nil {
		typ = t.TournamentType
		size = t.BracketSize
	}
	if typ == models.TournamentTypeRoundRobin || typ == models.TournamentTypeSwiss {
		return Layout{Other: true}
	}
	minRound, maxRound := 0, 0
	for _, m := range ms {
		if m.Round < minRound {
			minRound = m.Round
		}
		if m.Round > maxRound {
			maxRound = m.Round
		}
	}
	l := Layout{Double: typ == models.TournamentTypeDoubleElimination || minRound < 0}
	switch {
	case size > 1:
		l.UBRounds = log2(size)
	case l.Double && minRound < 0:
		l.UBRounds = (-minRound + 2) / 2
	default:
		l.UBRounds = maxRound
	}
	if l.Double && l.UBRounds > 1 {
		l.LBRounds = 2*l.UBRounds - 2
	}
	return l
}

// Cutoff is where a qualifier stops: upper rounds 1..UB and lower rounds
// -1..-LB are played, everything after them belongs to the main stage. All
// means the field is no bigger than the advance count and everyone goes on.
type Cutoff struct {
	All    bool
	UB, LB int
}

// Cutoff computes the advance line for `advance` teams. Single elimination
// keeps the rounds that leave `advance` teams; double elimination keeps the
// upper rounds that leave advance/2 teams there plus the lower rounds that
// absorb those rounds' losers and leave advance/2 more.
func (l Layout) Cutoff(advance int) Cutoff {
	if l.Other || advance <= 0 {
		return Cutoff{All: true}
	}
	if l.Double {
		u := l.UBRounds + 1 - log2(advance)
		if u <= 1 {
			return Cutoff{All: true}
		}
		return Cutoff{UB: u, LB: 2*u - 2}
	}
	c := l.UBRounds - log2(advance)
	if c <= 0 {
		return Cutoff{All: true}
	}
	return Cutoff{UB: c}
}

// InStage reports whether a round is played inside the qualifier.
func (c Cutoff) InStage(round int) bool {
	if c.All {
		return true
	}
	if round > 0 {
		return round <= c.UB
	}
	return round < 0 && -round <= c.LB
}

// Decider reports whether a round is the last one before the advance line.
func (c Cutoff) Decider(round int) bool {
	if c.All {
		return false
	}
	return round == c.UB || (c.LB > 0 && round == -c.LB)
}

// QualifierOutcome is where a qualifier stands.
type QualifierOutcome struct {
	Done      bool                  // every match before the advance line is played
	Qualified []int                 // teams still alive, sorted; final only when Done
	Pending   []models.BracketMatch // unplayed matches before the line
	Err       error                 // the alive count does not match the advance count
}

// QualifierStatus works out a qualifier from its full match cache.
// participants are the team ids that were seeded into the bracket.
func QualifierStatus(t *models.TelegramTournament, ms []models.BracketMatch, participants []int) QualifierOutcome {
	l := NewLayout(t, ms)
	cut := l.Cutoff(t.AdvanceCount)
	if cut.All {
		out := append([]int(nil), participants...)
		sort.Ints(out)
		return QualifierOutcome{Done: true, Qualified: out}
	}
	need := 1
	if l.Double {
		need = 2
	}
	losses := map[int]int{}
	var out QualifierOutcome
	for _, m := range ms {
		if !cut.InStage(m.Round) {
			continue
		}
		if m.State == models.BracketComplete {
			if m.WinnerID != nil && m.Team1ID != nil && m.Team2ID != nil {
				if loser := m.Opponent(*m.WinnerID); loser != nil {
					losses[*loser]++
				}
			}
			continue
		}
		if m.Team1ID != nil || m.Team2ID != nil {
			out.Pending = append(out.Pending, m)
		}
	}
	for _, id := range participants {
		if losses[id] < need {
			out.Qualified = append(out.Qualified, id)
		}
	}
	sort.Ints(out.Qualified)
	out.Done = len(out.Pending) == 0
	if out.Done && len(out.Qualified) != t.AdvanceCount {
		out.Err = fmt.Errorf("после раундов отсечения в турнире остаётся %d команд, а должно выйти %d — завершите квалификацию вручную", len(out.Qualified), t.AdvanceCount)
	}
	return out
}

// FilterStage drops a qualifier's matches past the advance line: they belong
// to no one, and kept in the cache they showed as live and reached captains.
func FilterStage(t *models.TelegramTournament, ms []models.BracketMatch) []models.BracketMatch {
	if !t.Qualifier() {
		return ms
	}
	cut := NewLayout(t, ms).Cutoff(t.AdvanceCount)
	if cut.All {
		return ms
	}
	out := make([]models.BracketMatch, 0, len(ms))
	for _, m := range ms {
		if cut.InStage(m.Round) {
			out = append(out, m)
		}
	}
	return out
}

// MatchFormat is the best-of for a round: 1, 3 or 5.
func MatchFormat(t *models.TelegramTournament, ms []models.BracketMatch, round int) int {
	rounds, semi, final := models.DefaultFormatRounds, models.DefaultFormatSemifinal, models.DefaultFormatFinal
	if t != nil {
		if t.FormatRounds > 0 {
			rounds = t.FormatRounds
		}
		if t.FormatSemifinal > 0 {
			semi = t.FormatSemifinal
		}
		if t.FormatFinal > 0 {
			final = t.FormatFinal
		}
	}
	l := NewLayout(t, ms)
	switch {
	case l.Other:
		return rounds
	case round == 0:
		// The third-place match is played like a semifinal.
		return semi
	case t.Qualifier():
		if l.Cutoff(t.AdvanceCount).Decider(round) {
			return final
		}
		return rounds
	case l.Double:
		if round > l.UBRounds {
			return final
		}
		if round == l.UBRounds || (round < 0 && -round == l.LBRounds) {
			return semi
		}
		return rounds
	default:
		if round == l.UBRounds {
			return final
		}
		if round == l.UBRounds-1 {
			return semi
		}
		return rounds
	}
}

// FormatLabel renders a best-of as shown in the UI: "BO3".
func FormatLabel(bo int) string {
	return fmt.Sprintf("BO%d", bo)
}

// ValidateScore checks a result against the best-of: the winner took exactly
// the games needed, the loser fewer.
func ValidateScore(bo, winner, loser int) error {
	need := (bo + 1) / 2
	if winner != need || loser < 0 || loser >= need {
		return fmt.Errorf("матч играется в формате BO%d: у победителя должно быть %d, у проигравшего — от 0 до %d", bo, need, need-1)
	}
	return nil
}

// RoundTitle names a round for the UI, lower bracket and qualifier included.
func RoundTitle(t *models.TelegramTournament, ms []models.BracketMatch, round int) string {
	l := NewLayout(t, ms)
	if l.Other {
		r := round
		if r < 0 {
			r = -r
		}
		return fmt.Sprintf("Тур %d", r)
	}
	var title string
	switch {
	case round == 0:
		// Round 0 is where a third-place match lands when Challonge numbers
		// it apart from the final.
		title = "Матч за 3-е место"
	case !l.Double:
		typ := ""
		if t != nil {
			typ = t.TournamentType
		}
		title = FormatRoundTitleWithContext(round, l.UBRounds, typ)
	case round > l.UBRounds:
		title = "Гранд-финал"
	case round == l.UBRounds:
		title = "Финал верхней сетки"
	case round > 0:
		title = fmt.Sprintf("Верхняя сетка, Раунд %d", round)
	case -round == l.LBRounds:
		title = "Финал нижней сетки"
	default:
		title = fmt.Sprintf("Нижняя сетка, Раунд %d", -round)
	}
	if t.Qualifier() && l.Cutoff(t.AdvanceCount).Decider(round) {
		title += " — за выход в основу"
	}
	return title
}

// Placements ranks the teams of a finished elimination bracket. A team's place
// is one more than the number of teams that went out after it: single
// elimination gives 1, 2, 3-4, 5-8...; double elimination 1, 2, 3, 4, 5-6,
// 7-8... Teams that played no match get no place.
func Placements(t *models.TelegramTournament, ms []models.BracketMatch, teamIDs []int) (map[int]int, map[int]int) {
	l := NewLayout(t, ms)
	need := 1
	if l.Double {
		need = 2
	}
	total := TotalRounds(ms)
	hasNegative := false
	for _, m := range ms {
		hasNegative = hasNegative || m.Round < 0
	}
	sorted := append([]models.BracketMatch(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ki, kj := RoundSortKey(sorted[i].Round, total, hasNegative), RoundSortKey(sorted[j].Round, total, hasNegative)
		if ki != kj {
			return ki < kj
		}
		return sorted[i].PlayOrder < sorted[j].PlayOrder
	})

	played := map[int]bool{}
	losses := map[int]int{}
	outAt := map[int]int{} // team -> sort key of the round it was eliminated in
	for _, m := range sorted {
		if m.Team1ID != nil {
			played[*m.Team1ID] = true
		}
		if m.Team2ID != nil {
			played[*m.Team2ID] = true
		}
		if m.State != models.BracketComplete || m.WinnerID == nil || m.Team1ID == nil || m.Team2ID == nil {
			continue
		}
		loser := m.Opponent(*m.WinnerID)
		if loser == nil {
			continue
		}
		losses[*loser]++
		if losses[*loser] == need {
			outAt[*loser] = RoundSortKey(m.Round, total, hasNegative)
		}
	}

	key := func(id int) int {
		if k, ok := outAt[id]; ok {
			return k
		}
		return math.MaxInt
	}
	places, points := map[int]int{}, map[int]int{}
	for _, id := range teamIDs {
		if !played[id] {
			continue
		}
		place := 1
		for _, other := range teamIDs {
			if other != id && played[other] && key(other) > key(id) {
				place++
			}
		}
		places[id] = place
		points[id] = PointsForPlace(place)
	}
	return places, points
}

// PointsForPlace is the league reward for a final place.
func PointsForPlace(place int) int {
	switch {
	case place == 1:
		return 100
	case place == 2:
		return 70
	case place <= 4:
		return 50
	case place <= 8:
		return 20
	case place <= 16:
		return 10
	default:
		return 5
	}
}

// NextPow2 is the smallest power of two not below n: the Challonge bracket
// size for n participants.
func NextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}

func log2(n int) int {
	if n <= 1 {
		return 0
	}
	return bits.Len(uint(n)) - 1
}
