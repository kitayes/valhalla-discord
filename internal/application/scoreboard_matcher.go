package application

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"blackwatch/internal/ai"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

const (
	fuzzyMatchThreshold = 0.72
	sideOverlapFuzzyMin = 0.78
)

// RosterPlayer describes an expected participant of a lobby match along with
// previously learned scoreboard aliases.
type RosterPlayer struct {
	PlayerID int
	Name     string
	Aliases  []string
}

// LobbyRoster holds the two five-player teams from lobby_matches.
type LobbyRoster struct {
	TeamA []RosterPlayer
	TeamB []RosterPlayer
}

// MatchedRow represents one scoreboard line during review and after matching
// against the known lobby roster.
type MatchedRow struct {
	Index       int
	OCRName     string
	Result      string // "WIN" or "LOSE"
	Kills       int
	Deaths      int
	Assists     int
	Rating      float64
	Confidence  float64
	PlayerID    int
	PlayerName  string
	MatchMethod string // "exact", "alias", "fuzzy", "elimination", "referee", ""
}

// LearnedAlias is a candidate nickname-to-player mapping discovered by
// elimination or explicit referee selection.
type LearnedAlias struct {
	PlayerID int
	Alias    string
	Source   string // "elimination" or "referee"
}

// ScoreboardMatchResult is the complete state of matching a parsed scoreboard
// against a lobby match's two teams.
type ScoreboardMatchResult struct {
	Winner          string // domain.TeamA, domain.TeamB, or "" when ambiguous
	Rows            []MatchedRow
	MVPPlayerID     int
	SVPPlayerID     int
	UnresolvedRows  []int
	UnassignedTeamA []RosterPlayer
	UnassignedTeamB []RosterPlayer
	LearnedAliases  []LearnedAlias
	OCRWarnings     []string
	Warnings        []string
	HeaderWinKills  *int
	HeaderLoseKills *int
	RawMVP          string
	RawSVP          string
}

// MatchScoreboardToLobby links parsed scoreboard rows to the known 10 players
// in roster, separating WIN and LOSE sides first and using elimination only
// within a single side.
func MatchScoreboardToLobby(roster LobbyRoster, parsed *models.Match) *ScoreboardMatchResult {
	res := &ScoreboardMatchResult{}
	if parsed == nil {
		res.Warnings = []string{"пустой результат распознавания"}
		return res
	}

	res.HeaderWinKills = parsed.HeaderWinKills
	res.HeaderLoseKills = parsed.HeaderLoseKills
	res.RawMVP = strings.TrimSpace(parsed.MVP)
	res.RawSVP = strings.TrimSpace(parsed.SVP)
	res.OCRWarnings = extractNonValidationWarnings(parsed)

	res.Rows = make([]MatchedRow, len(parsed.Players))
	var winNames, loseNames []string
	for idx, p := range parsed.Players {
		side := "LOSE"
		if strings.EqualFold(strings.TrimSpace(p.Result), "WIN") {
			side = "WIN"
			winNames = append(winNames, p.PlayerName)
		} else {
			loseNames = append(loseNames, p.PlayerName)
		}
		res.Rows[idx] = MatchedRow{
			Index:      idx,
			OCRName:    strings.TrimSpace(p.PlayerName),
			Result:     side,
			Kills:      p.Kills,
			Deaths:     p.Deaths,
			Assists:    p.Assists,
			Rating:     p.Rating,
			Confidence: p.Confidence,
		}
	}

	scoreWinAsA := sideOverlapScore(winNames, roster.TeamA) + sideOverlapScore(loseNames, roster.TeamB)
	scoreWinAsB := sideOverlapScore(winNames, roster.TeamB) + sideOverlapScore(loseNames, roster.TeamA)

	switch {
	case scoreWinAsA > scoreWinAsB && scoreWinAsA > 0:
		res.Winner = domain.TeamA
	case scoreWinAsB > scoreWinAsA && scoreWinAsB > 0:
		res.Winner = domain.TeamB
	default:
		res.Winner = ""
	}

	assignSidesFromWinner(roster, res)
	resolveMedalsForResult(res)
	RevalidateReview(roster, res)
	return res
}

// FlipReviewWinner toggles the winning side (Team A <-> Team B, or "" -> Team A).
// When OCR misread VICTORY/DEFEAT in the header, flipping also checks whether
// inverting WIN/LOSE labels produces a better name match with the rosters so
// already-matched players are preserved.
func FlipReviewWinner(roster LobbyRoster, res *ScoreboardMatchResult) {
	if res == nil {
		return
	}

	var nextWinner string
	switch res.Winner {
	case domain.TeamA:
		nextWinner = domain.TeamB
	case domain.TeamB:
		nextWinner = domain.TeamA
	default:
		nextWinner = domain.TeamA
	}

	var winNames, loseNames []string
	for _, r := range res.Rows {
		if r.Result == "WIN" {
			winNames = append(winNames, r.OCRName)
		} else {
			loseNames = append(loseNames, r.OCRName)
		}
	}

	var keepScore, invertScore int
	if nextWinner == domain.TeamA {
		keepScore = sideOverlapScore(winNames, roster.TeamA) + sideOverlapScore(loseNames, roster.TeamB)
		invertScore = sideOverlapScore(loseNames, roster.TeamA) + sideOverlapScore(winNames, roster.TeamB)
	} else {
		keepScore = sideOverlapScore(winNames, roster.TeamB) + sideOverlapScore(loseNames, roster.TeamA)
		invertScore = sideOverlapScore(loseNames, roster.TeamB) + sideOverlapScore(winNames, roster.TeamA)
	}

	if invertScore > keepScore {
		for idx := range res.Rows {
			if res.Rows[idx].Result == "WIN" {
				res.Rows[idx].Result = "LOSE"
			} else {
				res.Rows[idx].Result = "WIN"
			}
		}
		res.HeaderWinKills, res.HeaderLoseKills = res.HeaderLoseKills, res.HeaderWinKills
		res.RawMVP, res.RawSVP = res.RawSVP, res.RawMVP
	}

	res.Winner = nextWinner
	for idx := range res.Rows {
		res.Rows[idx].PlayerID = 0
		res.Rows[idx].PlayerName = ""
		res.Rows[idx].MatchMethod = ""
	}
	res.LearnedAliases = nil
	res.MVPPlayerID = 0
	res.SVPPlayerID = 0

	assignSidesFromWinner(roster, res)
	resolveMedalsForResult(res)
	RevalidateReview(roster, res)
}

// RevalidateReview recomputes unresolved rows, unassigned players, and all
// scoreboard + matching warnings after any referee edit.
func RevalidateReview(roster LobbyRoster, res *ScoreboardMatchResult) {
	if res == nil {
		return
	}

	assignedIDs := make(map[int]bool, len(res.Rows))
	var unresolved []int
	for idx, row := range res.Rows {
		if row.PlayerID > 0 {
			assignedIDs[row.PlayerID] = true
		} else {
			unresolved = append(unresolved, idx)
		}
	}
	res.UnresolvedRows = unresolved
	res.UnassignedTeamA = filterUnassigned(roster.TeamA, assignedIDs)
	res.UnassignedTeamB = filterUnassigned(roster.TeamB, assignedIDs)

	tmp := &models.Match{
		HeaderWinKills:  res.HeaderWinKills,
		HeaderLoseKills: res.HeaderLoseKills,
		Players:         make([]models.PlayerResult, len(res.Rows)),
	}
	for idx, row := range res.Rows {
		name := row.PlayerName
		if name == "" {
			name = row.OCRName
		}
		tmp.Players[idx] = models.PlayerResult{
			PlayerID:   row.PlayerID,
			PlayerName: name,
			Result:     row.Result,
			Kills:      row.Kills,
			Deaths:     row.Deaths,
			Assists:    row.Assists,
			Rating:     row.Rating,
			Confidence: row.Confidence,
		}
		if res.MVPPlayerID > 0 && row.PlayerID == res.MVPPlayerID && row.Result == "WIN" {
			tmp.MVP = name
		}
		if res.SVPPlayerID > 0 && row.PlayerID == res.SVPPlayerID && row.Result == "LOSE" {
			tmp.SVP = name
		}
	}
	if tmp.MVP == "" {
		tmp.MVP = res.RawMVP
	}
	if tmp.SVP == "" {
		tmp.SVP = res.RawSVP
	}

	var combined []string
	seen := make(map[string]struct{})
	add := func(w string) {
		clean := strings.TrimSpace(w)
		if clean == "" {
			return
		}
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		combined = append(combined, clean)
	}

	for _, w := range res.OCRWarnings {
		add(w)
	}
	for _, w := range ai.ValidateScoreboard(tmp) {
		add(w)
	}
	if res.Winner == "" {
		add("не удалось определить победившую сторону (нажмите «Сменить победителя»)")
	}
	if len(res.UnresolvedRows) > 0 {
		add(fmt.Sprintf("не сопоставлено игроков: %d (выберите вручную из списка)", len(res.UnresolvedRows)))
	}

	res.Warnings = combined
}

func assignSidesFromWinner(roster LobbyRoster, res *ScoreboardMatchResult) {
	if res.Winner == "" {
		return
	}

	var winTeam, loseTeam []RosterPlayer
	if res.Winner == domain.TeamA {
		winTeam = roster.TeamA
		loseTeam = roster.TeamB
	} else {
		winTeam = roster.TeamB
		loseTeam = roster.TeamA
	}

	allRoster := make([]RosterPlayer, 0, len(roster.TeamA)+len(roster.TeamB))
	allRoster = append(allRoster, roster.TeamA...)
	allRoster = append(allRoster, roster.TeamB...)

	var winRows, loseRows []*MatchedRow
	for idx := range res.Rows {
		if res.Rows[idx].Result == "WIN" {
			winRows = append(winRows, &res.Rows[idx])
		} else {
			loseRows = append(loseRows, &res.Rows[idx])
		}
	}

	winAliases := assignOneSide(winRows, winTeam, allRoster)
	loseAliases := assignOneSide(loseRows, loseTeam, allRoster)
	res.LearnedAliases = append(res.LearnedAliases, winAliases...)
	res.LearnedAliases = append(res.LearnedAliases, loseAliases...)
}

func assignOneSide(sideRows []*MatchedRow, team []RosterPlayer, allRoster []RosterPlayer) []LearnedAlias {
	if len(sideRows) == 0 || len(team) == 0 {
		return nil
	}

	rowAssigned := make([]bool, len(sideRows))
	playerAssigned := make([]bool, len(team))

	// Pass 1: exact normalized name.
	for ri, row := range sideRows {
		normRow := normalizedLower(row.OCRName)
		if normRow == "" {
			continue
		}
		for pi, p := range team {
			if playerAssigned[pi] {
				continue
			}
			if normalizedLower(p.Name) == normRow {
				bindRow(row, p, "exact")
				rowAssigned[ri] = true
				playerAssigned[pi] = true
				break
			}
		}
	}

	// Pass 2: learned aliases.
	for ri, row := range sideRows {
		if rowAssigned[ri] {
			continue
		}
		normRow := normalizedLower(row.OCRName)
		if normRow == "" {
			continue
		}
		for pi, p := range team {
			if playerAssigned[pi] {
				continue
			}
			if hasNormalizedAlias(p, normRow) {
				bindRow(row, p, "alias")
				rowAssigned[ri] = true
				playerAssigned[pi] = true
				break
			}
		}
	}

	// Pass 3: greedy 1-to-1 fuzzy similarity.
	type candidate struct {
		ri  int
		pi  int
		sim float64
	}
	var candidates []candidate
	for ri, row := range sideRows {
		if rowAssigned[ri] || normalizedLower(row.OCRName) == "" {
			continue
		}
		for pi, p := range team {
			if playerAssigned[pi] {
				continue
			}
			best := bestSimilarity(row.OCRName, p)
			if best >= fuzzyMatchThreshold {
				candidates = append(candidates, candidate{ri: ri, pi: pi, sim: best})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sim != candidates[j].sim {
			return candidates[i].sim > candidates[j].sim
		}
		if candidates[i].ri != candidates[j].ri {
			return candidates[i].ri < candidates[j].ri
		}
		return team[candidates[i].pi].PlayerID < team[candidates[j].pi].PlayerID
	})
	for _, c := range candidates {
		if rowAssigned[c.ri] || playerAssigned[c.pi] {
			continue
		}
		bindRow(sideRows[c.ri], team[c.pi], "fuzzy")
		rowAssigned[c.ri] = true
		playerAssigned[c.pi] = true
	}

	// Pass 4: 1+1 elimination inside the side.
	var freeRows, freePlayers []int
	for ri := range sideRows {
		if !rowAssigned[ri] {
			freeRows = append(freeRows, ri)
		}
	}
	for pi := range team {
		if !playerAssigned[pi] {
			freePlayers = append(freePlayers, pi)
		}
	}

	var learned []LearnedAlias
	if len(freeRows) == 1 && len(freePlayers) == 1 {
		row := sideRows[freeRows[0]]
		player := team[freePlayers[0]]
		bindRow(row, player, "elimination")
		if CanLearnAlias(row.OCRName, allRoster) {
			learned = append(learned, LearnedAlias{
				PlayerID: player.PlayerID,
				Alias:    strings.TrimSpace(row.OCRName),
				Source:   "elimination",
			})
		}
	}
	return learned
}

// CanLearnAlias verifies that alias is safe to record: non-empty, contains at
// least one letter, at least 2 runes, and does not collide with any lobby
// player's canonical name or existing alias.
func CanLearnAlias(alias string, allRoster []RosterPlayer) bool {
	clean := strings.TrimSpace(alias)
	if utf8.RuneCountInString(clean) < 2 {
		return false
	}
	hasLetter := false
	for _, r := range clean {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return false
	}

	norm := normalizedLower(clean)
	if norm == "" {
		return false
	}
	for _, p := range allRoster {
		if normalizedLower(p.Name) == norm {
			return false
		}
		if hasNormalizedAlias(p, norm) {
			return false
		}
	}
	return true
}

func resolveMedalsForResult(res *ScoreboardMatchResult) {
	if res == nil {
		return
	}
	res.MVPPlayerID = resolveSideMedal(res.Rows, "WIN", res.RawMVP)
	res.SVPPlayerID = resolveSideMedal(res.Rows, "LOSE", res.RawSVP)
}

func resolveSideMedal(rows []MatchedRow, side, medalName string) int {
	normMedal := normalizedLower(medalName)
	if normMedal != "" {
		bestID := 0
		bestSim := 0.0
		for _, r := range rows {
			if r.Result != side || r.PlayerID <= 0 {
				continue
			}
			if normalizedLower(r.OCRName) == normMedal || normalizedLower(r.PlayerName) == normMedal {
				return r.PlayerID
			}
			sim := max(ai.SimilarityScore(r.OCRName, medalName), ai.SimilarityScore(r.PlayerName, medalName))
			if sim >= sideOverlapFuzzyMin && sim > bestSim {
				bestSim = sim
				bestID = r.PlayerID
			}
		}
		if bestID > 0 {
			return bestID
		}
	}

	// Fallback: highest MLBB post-match rating on this side.
	bestID := 0
	bestRating := 0.0
	for _, r := range rows {
		if r.Result != side || r.PlayerID <= 0 || r.Rating <= 0 {
			continue
		}
		if r.Rating > bestRating {
			bestRating = r.Rating
			bestID = r.PlayerID
		}
	}
	return bestID
}

func sideOverlapScore(rowNames []string, team []RosterPlayer) int {
	if len(rowNames) == 0 || len(team) == 0 {
		return 0
	}
	used := make([]bool, len(team))
	score := 0
	for _, raw := range rowNames {
		normRow := normalizedLower(raw)
		if normRow == "" {
			continue
		}
		bestIdx := -1
		bestPts := 0
		for pi, p := range team {
			if used[pi] {
				continue
			}
			pts := 0
			switch {
			case normalizedLower(p.Name) == normRow:
				pts = 3
			case hasNormalizedAlias(p, normRow):
				pts = 2
			case bestSimilarity(raw, p) >= sideOverlapFuzzyMin:
				pts = 1
			}
			if pts > bestPts {
				bestPts = pts
				bestIdx = pi
			}
		}
		if bestIdx >= 0 {
			used[bestIdx] = true
			score += bestPts
		}
	}
	return score
}

func extractNonValidationWarnings(parsed *models.Match) []string {
	if parsed == nil || len(parsed.Warnings) == 0 {
		return nil
	}
	valSet := make(map[string]struct{})
	for _, w := range ai.ValidateScoreboard(parsed) {
		valSet[strings.TrimSpace(w)] = struct{}{}
	}
	var out []string
	for _, w := range parsed.Warnings {
		clean := strings.TrimSpace(w)
		if clean == "" {
			continue
		}
		if _, isVal := valSet[clean]; isVal {
			continue
		}
		out = append(out, clean)
	}
	return out
}

func bindRow(row *MatchedRow, player RosterPlayer, method string) {
	row.PlayerID = player.PlayerID
	row.PlayerName = player.Name
	row.MatchMethod = method
}

func filterUnassigned(team []RosterPlayer, assigned map[int]bool) []RosterPlayer {
	var out []RosterPlayer
	for _, p := range team {
		if !assigned[p.PlayerID] {
			out = append(out, p)
		}
	}
	return out
}

func normalizedLower(s string) string {
	return strings.ToLower(ai.NormalizeName(s))
}

func hasNormalizedAlias(p RosterPlayer, normTarget string) bool {
	if normTarget == "" {
		return false
	}
	for _, alias := range p.Aliases {
		if normalizedLower(alias) == normTarget {
			return true
		}
	}
	return false
}

func bestSimilarity(ocrName string, p RosterPlayer) float64 {
	best := ai.SimilarityScore(ocrName, p.Name)
	for _, alias := range p.Aliases {
		if sim := ai.SimilarityScore(ocrName, alias); sim > best {
			best = sim
		}
	}
	return best
}
