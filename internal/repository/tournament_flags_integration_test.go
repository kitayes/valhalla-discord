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
	if err := repo.SetTournamentCheckIn(ctx, first.ID, checked.ID, true, 42); err != nil {
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

// Stage settings round-trip, entries carry their source and the check-in
// remembers who made it.
func TestIntegrationTournamentStagesAndEntries(t *testing.T) {
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
	team, err := repo.CreateTeam(ctx, uniqueName(t, "stage"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM telegram_teams WHERE id = $1`, team.ID) })

	qual, err := repo.CreateTournament(ctx, &models.TelegramTournament{
		Name: uniqueName(t, "q"), Slug: uniqueName(t, "q"), Status: models.TournamentStatusRegistration,
		Stage: models.StageQualifier, AdvanceCount: 8, FormatRounds: 1, FormatSemifinal: 3, FormatFinal: 5,
	})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	created = append(created, qual.ID)
	if qual.Stage != models.StageQualifier || qual.AdvanceCount != 8 || qual.FormatFinal != 5 {
		t.Fatalf("created = %+v", qual)
	}
	qual.BracketSize = 16
	if err := repo.UpdateTournament(ctx, qual); err != nil {
		t.Fatalf("UpdateTournament: %v", err)
	}
	got, err := repo.GetTournamentByID(ctx, qual.ID)
	if err != nil || got.BracketSize != 16 || got.Stage != models.StageQualifier {
		t.Fatalf("GetTournamentByID = %+v, %v", got, err)
	}

	if err := repo.RegisterTeamForTournament(ctx, qual.ID, team.ID); err != nil {
		t.Fatalf("RegisterTeamForTournament: %v", err)
	}
	if err := repo.SetTournamentCheckIn(ctx, qual.ID, team.ID, true, 777); err != nil {
		t.Fatalf("SetTournamentCheckIn: %v", err)
	}
	entry, err := repo.GetTournamentTeam(ctx, qual.ID, team.ID)
	if err != nil {
		t.Fatalf("GetTournamentTeam: %v", err)
	}
	if !entry.IsCheckedIn || entry.CheckedInBy == nil || *entry.CheckedInBy != 777 || entry.CheckedInAt == nil || entry.Source != models.SourceRegistered {
		t.Fatalf("entry = %+v", entry)
	}
	if err := repo.SetTournamentTeamStatus(ctx, qual.ID, team.ID, models.EntryQualified); err != nil {
		t.Fatalf("SetTournamentTeamStatus: %v", err)
	}
	// A late check-in change must not undo the qualification.
	if err := repo.SetTournamentCheckIn(ctx, qual.ID, team.ID, false, 0); err != nil {
		t.Fatalf("SetTournamentCheckIn: %v", err)
	}
	entry, _ = repo.GetTournamentTeam(ctx, qual.ID, team.ID)
	if entry.Status != models.EntryQualified {
		t.Fatalf("status after check-in change = %q, want qualified", entry.Status)
	}
	has, err := repo.HasQualifiedEntry(ctx, team.ID, 0)
	if err != nil || !has {
		t.Fatalf("HasQualifiedEntry = %v, %v", has, err)
	}
	if has, _ := repo.HasQualifiedEntry(ctx, team.ID, qual.ID); has {
		t.Fatal("HasQualifiedEntry must skip the excepted tournament")
	}

	main, err := repo.CreateTournament(ctx, &models.TelegramTournament{
		Name: uniqueName(t, "m"), Slug: uniqueName(t, "m"), Status: models.TournamentStatusRegistration, Stage: models.StageMain,
	})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	created = append(created, main.ID)
	if err := repo.EnterTeam(ctx, main.ID, team.ID, models.SourceQualified, &qual.ID); err != nil {
		t.Fatalf("EnterTeam: %v", err)
	}
	entries, err := repo.GetTournamentEntries(ctx, main.ID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("GetTournamentEntries = %+v, %v", entries, err)
	}
	if e := entries[0]; e.Source != models.SourceQualified || e.QualifiedFrom == nil || *e.QualifiedFrom != qual.ID || e.TeamName != team.Name {
		t.Fatalf("entry = %+v", e)
	}
	if main.FormatRounds != models.DefaultFormatRounds || main.FormatFinal != models.DefaultFormatFinal {
		t.Fatalf("defaults not applied: %+v", main)
	}
}
