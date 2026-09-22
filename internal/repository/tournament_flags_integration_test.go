//go:build integration

package repository

import (
	"context"
	"testing"

	"blackwatch/internal/models"
)

// The team row mirrors the team's entry in the active tournament. A new
// tournament used to inherit last time's check-in and technical defeat.
func TestIntegrationTournamentSwitchSyncsTeamFlags(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewTelegramPostgres(db)

	prev, err := repo.GetActiveTournament(ctx)
	if err != nil {
		t.Fatalf("GetActiveTournament: %v", err)
	}
	var created []int
	t.Cleanup(func() {
		if prev != nil {
			_ = repo.SetActiveTournament(ctx, prev.ID)
		}
		for _, id := range created {
			_, _ = db.Exec(`DELETE FROM telegram_tournaments WHERE id = $1`, id)
		}
	})

	checked, err := repo.CreateTeam(ctx, uniqueName(t, "checked"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	dq, err := repo.CreateTeam(ctx, uniqueName(t, "dq"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM telegram_teams WHERE id IN ($1, $2)`, checked.ID, dq.ID)
	})

	first, err := repo.CreateTournament(ctx, &models.TelegramTournament{Name: uniqueName(t, "t1"), Slug: uniqueName(t, "s1"), Status: models.TournamentStatusRegistration, IsActive: true})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	created = append(created, first.ID)
	for _, id := range []int{checked.ID, dq.ID} {
		if err := repo.RegisterTeamForTournament(ctx, first.ID, id); err != nil {
			t.Fatalf("RegisterTeamForTournament: %v", err)
		}
	}
	if err := repo.SetTournamentCheckIn(ctx, first.ID, checked.ID, true); err != nil {
		t.Fatalf("SetTournamentCheckIn: %v", err)
	}
	if err := repo.SetTournamentTeamStatus(ctx, first.ID, dq.ID, models.TeamStatusDisqualified); err != nil {
		t.Fatalf("SetTournamentTeamStatus: %v", err)
	}
	if err := repo.SetTeamStatus(ctx, dq.ID, models.TeamStatusDisqualified); err != nil {
		t.Fatalf("SetTeamStatus: %v", err)
	}

	second, err := repo.CreateTournament(ctx, &models.TelegramTournament{Name: uniqueName(t, "t2"), Slug: uniqueName(t, "s2"), Status: models.TournamentStatusRegistration, IsActive: true})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	created = append(created, second.ID)
	assertFlags := func(when string, teamID int, wantChecked bool, wantStatus string) {
		t.Helper()
		got, err := repo.GetTeamByID(ctx, teamID)
		if err != nil {
			t.Fatalf("GetTeamByID: %v", err)
		}
		if got.IsCheckedIn != wantChecked || got.Status != wantStatus {
			t.Errorf("%s: team %d checked=%v status=%q, want %v %q", when, teamID, got.IsCheckedIn, got.Status, wantChecked, wantStatus)
		}
	}
	assertFlags("new tournament", checked.ID, false, models.TeamStatusActive)
	assertFlags("new tournament", dq.ID, false, models.TeamStatusActive)

	// Switching back restores the first tournament's picture.
	if err := repo.SetActiveTournament(ctx, first.ID); err != nil {
		t.Fatalf("SetActiveTournament: %v", err)
	}
	assertFlags("reactivated", checked.ID, true, models.TeamStatusActive)
	assertFlags("reactivated", dq.ID, false, models.TeamStatusDisqualified)
}
