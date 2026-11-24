package application

import (
	"testing"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

func sampleRoster() LobbyRoster {
	return LobbyRoster{
		TeamA: []RosterPlayer{
			{PlayerID: 1, Name: "Bjorn", Aliases: []string{"BjornSmurf"}},
			{PlayerID: 2, Name: "Thorin"},
			{PlayerID: 3, Name: "Freya"},
			{PlayerID: 4, Name: "Бьорн"},
			{PlayerID: 5, Name: "Kael"},
		},
		TeamB: []RosterPlayer{
			{PlayerID: 6, Name: "Gunnar"},
			{PlayerID: 7, Name: "Loki"},
			{PlayerID: 8, Name: "Astrid"},
			{PlayerID: 9, Name: "Soren"},
			{PlayerID: 10, Name: "Ragnar"},
		},
	}
}

func makeTenRowMatch(winNames, loseNames []string) *models.Match {
	winKills := 15
	loseKills := 10
	players := make([]models.PlayerResult, 0, 10)
	for i, n := range winNames {
		players = append(players, models.PlayerResult{
			PlayerName: n,
			Result:     "WIN",
			Kills:      3,
			Deaths:     2,
			Assists:    4,
			Rating:     float64(10 - i),
			Confidence: 0.95,
		})
	}
	for i, n := range loseNames {
		players = append(players, models.PlayerResult{
			PlayerName: n,
			Result:     "LOSE",
			Kills:      2,
			Deaths:     3,
			Assists:    3,
			Rating:     float64(9 - i),
			Confidence: 0.95,
		})
	}
	return &models.Match{
		Players:         players,
		HeaderWinKills:  &winKills,
		HeaderLoseKills: &loseKills,
	}
}

func TestMatchScoreboardToLobbyAllExactAndTeamBWin(t *testing.T) {
	roster := sampleRoster()
	// Team B won here: WIN rows hold Team B names, LOSE rows hold Team A names.
	parsed := makeTenRowMatch(
		[]string{"Gunnar", "Loki", "Astrid", "Soren", "Ragnar"},
		[]string{"Bjorn", "Thorin", "Freya", "Бьорн", "Kael"},
	)
	parsed.MVP = "Loki"
	parsed.SVP = "Thorin"

	res := MatchScoreboardToLobby(roster, parsed)
	if res.Winner != domain.TeamB {
		t.Fatalf("Winner = %q, want %q", res.Winner, domain.TeamB)
	}
	if len(res.UnresolvedRows) != 0 {
		t.Fatalf("UnresolvedRows = %v, want empty", res.UnresolvedRows)
	}
	if res.MVPPlayerID != 7 {
		t.Errorf("MVPPlayerID = %d, want 7 (Loki)", res.MVPPlayerID)
	}
	if res.SVPPlayerID != 2 {
		t.Errorf("SVPPlayerID = %d, want 2 (Thorin)", res.SVPPlayerID)
	}
}

func TestMatchScoreboardToLobbyElimination1Plus1AndAliasPoisoningGuard(t *testing.T) {
	// Gunnar (Team B) plays on Bjorn's account; Bjorn (Team A) plays on SomeNick.
	roster := sampleRoster()
	parsed := makeTenRowMatch(
		[]string{"SomeNick", "Thorin", "Freya", "Бьорн", "Kael"},
		[]string{"Bjorn", "Loki", "Astrid", "Soren", "Ragnar"},
	)

	res := MatchScoreboardToLobby(roster, parsed)
	if res.Winner != domain.TeamA {
		t.Fatalf("Winner = %q, want %q", res.Winner, domain.TeamA)
	}
	if len(res.UnresolvedRows) != 0 {
		t.Fatalf("expected 1+1 elimination to resolve all rows, got unresolved: %v", res.UnresolvedRows)
	}

	if res.Rows[0].PlayerID != 1 || res.Rows[0].MatchMethod != "elimination" {
		t.Errorf("row 0 (SomeNick) = (%d, %q), want (1/Bjorn, elimination)", res.Rows[0].PlayerID, res.Rows[0].MatchMethod)
	}
	if res.Rows[5].PlayerID != 6 || res.Rows[5].MatchMethod != "elimination" {
		t.Errorf("row 5 (Bjorn on Team B) = (%d, %q), want (6/Gunnar, elimination)", res.Rows[5].PlayerID, res.Rows[5].MatchMethod)
	}

	// Only SomeNick -> Bjorn should be learned; "Bjorn" belongs to player 1 so must NOT be learned for Gunnar!
	if len(res.LearnedAliases) != 1 {
		t.Fatalf("LearnedAliases = %+v, want exactly 1 (SomeNick -> Bjorn)", res.LearnedAliases)
	}
	if res.LearnedAliases[0].PlayerID != 1 || res.LearnedAliases[0].Alias != "SomeNick" {
		t.Errorf("learned alias = %+v, want PlayerID=1 Alias=SomeNick", res.LearnedAliases[0])
	}
}

func TestMatchScoreboardToLobby2Plus0StaysUnresolved(t *testing.T) {
	roster := sampleRoster()
	parsed := makeTenRowMatch(
		[]string{"UnknownOne", "UnknownTwo", "Freya", "Бьорн", "Kael"},
		[]string{"Gunnar", "Loki", "Astrid", "Soren", "Ragnar"},
	)

	res := MatchScoreboardToLobby(roster, parsed)
	if res.Winner != domain.TeamA {
		t.Fatalf("Winner = %q, want %q", res.Winner, domain.TeamA)
	}
	if len(res.UnresolvedRows) != 2 {
		t.Fatalf("len(UnresolvedRows) = %d, want 2: %v", len(res.UnresolvedRows), res.UnresolvedRows)
	}
	if len(res.UnassignedTeamA) != 2 {
		t.Errorf("len(UnassignedTeamA) = %d, want 2", len(res.UnassignedTeamA))
	}
	if len(res.LearnedAliases) != 0 {
		t.Errorf("2+0 must not guess aliases, got %+v", res.LearnedAliases)
	}
}

func TestMatchScoreboardToLobbyAliasAndCyrillicNFKCFuzzy(t *testing.T) {
	roster := sampleRoster()
	// Bjorn uses alias "BjornSmurf"; Бьорн has a Cyrillic 1-letter OCR typo "Бьерн"; Kael uses NFKC math bold "𝐊𝐚𝐞𝐥".
	parsed := makeTenRowMatch(
		[]string{"BjornSmurf", "Thorin", "Freya", "Бьерн", "𝐊𝐚𝐞𝐥"},
		[]string{"Gunnar", "Loki", "Astrid", "Soren", "Ragnar"},
	)

	res := MatchScoreboardToLobby(roster, parsed)
	if res.Winner != domain.TeamA {
		t.Fatalf("Winner = %q, want %q", res.Winner, domain.TeamA)
	}
	if len(res.UnresolvedRows) != 0 {
		t.Fatalf("UnresolvedRows = %v, want empty", res.UnresolvedRows)
	}
	if res.Rows[0].MatchMethod != "alias" || res.Rows[0].PlayerID != 1 {
		t.Errorf("row 0 = (%d, %q), want (1, alias)", res.Rows[0].PlayerID, res.Rows[0].MatchMethod)
	}
	if res.Rows[3].MatchMethod != "fuzzy" || res.Rows[3].PlayerID != 4 {
		t.Errorf("row 3 = (%d, %q), want (4, fuzzy)", res.Rows[3].PlayerID, res.Rows[3].MatchMethod)
	}
	if res.Rows[4].MatchMethod != "exact" || res.Rows[4].PlayerID != 5 {
		t.Errorf("row 4 (NFKC Kael) = (%d, %q), want (5, exact)", res.Rows[4].PlayerID, res.Rows[4].MatchMethod)
	}
}

func TestMatchScoreboardToLobbyTiedOverlapYieldsAmbiguousWinner(t *testing.T) {
	roster := sampleRoster()
	parsed := makeTenRowMatch(
		[]string{"Unknown1", "Unknown2", "Unknown3", "Unknown4", "Unknown5"},
		[]string{"Unknown6", "Unknown7", "Unknown8", "Unknown9", "Unknown10"},
	)
	res := MatchScoreboardToLobby(roster, parsed)
	if res.Winner != "" {
		t.Errorf("Winner = %q, want empty on tied/zero overlap", res.Winner)
	}
	if len(res.UnresolvedRows) != 10 {
		t.Errorf("UnresolvedRows = %d, want 10", len(res.UnresolvedRows))
	}
}

func TestMatchScoreboardToLobbyMedalsFallbackToHighestRating(t *testing.T) {
	roster := sampleRoster()
	parsed := makeTenRowMatch(
		[]string{"Bjorn", "Thorin", "Freya", "Бьорн", "Kael"},
		[]string{"Gunnar", "Loki", "Astrid", "Soren", "Ragnar"},
	)
	// No explicit MVP/SVP names, so highest rating on each side should win:
	// Team A row 2 (Freya, rating 12.5), Team B row 8 (Soren, rating 10.2).
	parsed.MVP = ""
	parsed.SVP = ""
	parsed.Players[2].Rating = 12.5
	parsed.Players[8].Rating = 10.2

	res := MatchScoreboardToLobby(roster, parsed)
	if res.MVPPlayerID != 3 {
		t.Errorf("MVPPlayerID fallback = %d, want 3 (Freya)", res.MVPPlayerID)
	}
	if res.SVPPlayerID != 9 {
		t.Errorf("SVPPlayerID fallback = %d, want 9 (Soren)", res.SVPPlayerID)
	}
}

func TestFlipReviewWinnerPreservesNameAlignmentWhenHeaderInverted(t *testing.T) {
	roster := sampleRoster()
	// Suppose OCR read Team A nicks under LOSE and Team B nicks under WIN because
	// header VICTORY/DEFEAT was misread backwards.
	parsed := makeTenRowMatch(
		[]string{"Gunnar", "Loki", "Astrid", "Soren", "Ragnar"},
		[]string{"Bjorn", "Thorin", "Freya", "Бьорн", "Kael"},
	)
	res := MatchScoreboardToLobby(roster, parsed)
	if res.Winner != domain.TeamB {
		t.Fatalf("initial Winner = %q, want Team B", res.Winner)
	}

	FlipReviewWinner(roster, res)
	if res.Winner != domain.TeamA {
		t.Fatalf("flipped Winner = %q, want Team A", res.Winner)
	}
	if len(res.UnresolvedRows) != 0 {
		t.Fatalf("flip broke name resolution: unresolved=%v", res.UnresolvedRows)
	}
	// Bjorn's row (previously LOSE) should now be WIN!
	if res.Rows[5].PlayerID != 1 || res.Rows[5].Result != "WIN" {
		t.Errorf("Bjorn row after flip = (id=%d, result=%s), want (1, WIN)", res.Rows[5].PlayerID, res.Rows[5].Result)
	}
}
