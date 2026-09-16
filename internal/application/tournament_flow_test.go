package application

import (
	"context"
	"fmt"
	"testing"

	"blackwatch/internal/models"
)

// This test fails if a completed match no longer opens its downstream match,
// if the incremental open-match merge loses part of the bracket, or if a
// result starts costing more than one write plus one filtered read.
func TestEightTeamTournamentCompletesToOneChampion(t *testing.T) {
	svc, repo, provider := newBracketSvc(t)
	for id := 1; id <= 8; id++ {
		addBracketTeam(repo, id, fmt.Sprintf("Team %d", id), 100-id)
	}

	ctx := context.Background()
	built, err := svc.Build(ctx, tourneyAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Round1) != 4 || len(built.Byes) != 0 {
		t.Fatalf("round1=%d byes=%d, want 4 and 0", len(built.Round1), len(built.Byes))
	}

	reported := 0
	for reported < 7 {
		matches, err := svc.Matches(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var open *models.BracketMatch
		for i := range matches {
			if matches[i].Ready() {
				match := matches[i]
				open = &match
				break
			}
		}
		if open == nil {
			t.Fatalf("bracket stalled after %d results: %+v", reported, matches)
		}
		if _, err := svc.ReportResult(ctx, open.ID, *open.Team1ID, 2, 0); err != nil {
			t.Fatalf("report match #%d after %d results: %v", open.PlayOrder, reported, err)
		}
		reported++
	}

	matches, err := svc.Matches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	complete := 0
	champions := map[int]bool{}
	for _, match := range matches {
		if match.State != models.BracketComplete {
			t.Errorf("match #%d ended in state %q", match.PlayOrder, match.State)
			continue
		}
		complete++
		if match.Round == 3 && match.WinnerID != nil {
			champions[*match.WinnerID] = true
		}
	}
	if complete != 7 || len(champions) != 1 {
		t.Fatalf("complete=%d champions=%v, want 7 and one champion", complete, champions)
	}
	if got := provider.count("ReportMatch"); got != 7 {
		t.Errorf("ReportMatch calls=%d, want 7", got)
	}
	if got := provider.count("ListOpenMatches"); got != 7 {
		t.Errorf("ListOpenMatches calls=%d, want 7", got)
	}
}
