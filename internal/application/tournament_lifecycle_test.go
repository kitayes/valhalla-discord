package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"blackwatch/internal/models"
)

// Captains made by addBracketTeam have telegram id 100*teamID.
func captainOf(teamID int) int64 { return int64(100 * teamID) }

// A check-in or technical defeat from the last tournament used to stay on the
// team row: the Mini App showed "checked in" while the sweep saw nothing, the
// first press un-checked the team, and last time's disqualified teams could
// not check in at all.
func TestNewTournamentDropsPreviousCheckInsAndDisqualifications(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	alpha := addBracketTeam(repo, 1, "Alpha", 10) // checked in last time
	gamma := addBracketTeam(repo, 2, "Gamma", 10)
	gamma.IsCheckedIn = false
	gamma.Status = models.TeamStatusDisqualified

	if _, err := svc.CreateTournament(ctx, "Этап 2", "", nil, "", false, ""); err != nil {
		t.Fatal(err)
	}
	if alpha.IsCheckedIn || gamma.IsCheckedIn || gamma.Status != models.TeamStatusActive {
		t.Fatalf("after new tournament: alpha checked=%v, gamma checked=%v status=%q; want clean teams",
			alpha.IsCheckedIn, gamma.IsCheckedIn, gamma.Status)
	}
	if got := svc.ToggleCheckIn(ctx, captainOf(2)); !strings.Contains(got, "подтверждён") {
		t.Errorf("Gamma check-in = %q, want confirmation", got)
	}
	if got := svc.ToggleCheckIn(ctx, captainOf(1)); !strings.Contains(got, "подтверждён") {
		t.Errorf("Alpha first press = %q, want confirmation, not removal", got)
	}
	if dq, _ := svc.DisqualifyUnchecked(ctx); len(dq) != 0 {
		t.Errorf("disqualified %d checked-in teams", len(dq))
	}
}

// Check-in without pressing "Заявиться" left the team out of the bracket
// without a word; while registration is open it now enters the team.
func TestCheckInEntersTeamWhileRegistrationIsOpen(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	addBracketTeam(repo, 2, "Beta", 10).IsCheckedIn = false
	_ = repo.RegisterTeamForTournament(ctx, 1, 2) // Beta entered from the Mini App

	if got := svc.ToggleCheckIn(ctx, captainOf(1)); !strings.Contains(got, "подтверждён") {
		t.Fatalf("check-in = %q, want confirmation", got)
	}
	entry, _ := repo.GetTournamentTeam(ctx, 1, 1)
	if entry == nil || !entry.IsCheckedIn {
		t.Fatalf("Alpha entry = %+v, want entered and checked in", entry)
	}
	dq, _ := svc.DisqualifyUnchecked(ctx)
	if len(dq) != 1 || dq[0].Name != "Beta" {
		t.Errorf("disqualified %+v, want only Beta", dq)
	}
}

func TestCheckInRefusedForTeamOutsideBuiltBracket(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	repo.tournaments[0].Status = models.TournamentStatusActive
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false

	got := svc.ToggleCheckIn(ctx, captainOf(1))
	if !strings.Contains(got, "не заявлена") {
		t.Errorf("check-in = %q, want refusal: team is not in the bracket", got)
	}
	if entry, _ := repo.GetTournamentTeam(ctx, 1, 1); entry != nil || repo.teams[1].IsCheckedIn {
		t.Errorf("entry = %+v, team checked = %v; want nothing recorded", entry, repo.teams[1].IsCheckedIn)
	}
}

// The reminder's button wrote only the team row, so the captain who pressed it
// was still disqualified by the sweep.
func TestReminderCheckInIsSeenByTheSweep(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	_ = repo.RegisterTeamForTournament(ctx, 1, 1)

	resp, _ := svc.HandleRegAction(ctx, captainOf(1), "checkin", "")
	if !strings.Contains(resp, "подтверждён") {
		t.Fatalf("reminder check-in = %q, want confirmation", resp)
	}
	if dq, _ := svc.DisqualifyUnchecked(ctx); len(dq) != 0 {
		t.Errorf("sweep disqualified %s after reminder check-in", dq[0].Name)
	}
}

func TestReinstateClearsTournamentDisqualification(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	_ = repo.RegisterTeamForTournament(ctx, 1, 1)
	if dq, _ := svc.DisqualifyUnchecked(ctx); len(dq) != 1 {
		t.Fatalf("setup: disqualified %d, want 1", len(dq))
	}

	svc.AdminReinstateTeam(ctx, "Alpha")

	entry, _ := repo.GetTournamentTeam(ctx, 1, 1)
	if entry.Status == models.TeamStatusDisqualified || !entry.IsCheckedIn {
		t.Errorf("entry after reinstate = %+v, want checked in and not disqualified", entry)
	}
}

func TestUnregisterDropsCheckInMirror(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	svc.ToggleCheckIn(ctx, captainOf(1))

	if err := svc.UnregisterTeamFromTournament(ctx, captainOf(1), 1); err != nil {
		t.Fatal(err)
	}
	if repo.teams[1].IsCheckedIn {
		t.Error("team still shows checked in after its entry was withdrawn")
	}
}

func TestLeaveTeamBlockedAfterCheckIn(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	teamID := 1
	repo.addPlayer(555, &teamID, false, models.StateIdle)
	svc.ToggleCheckIn(ctx, captainOf(1))

	err := svc.LeaveTeam(ctx, 555)
	if err == nil || !strings.Contains(err.Error(), "Check-in") {
		t.Errorf("leave after check-in = %v, want refusal", err)
	}
}

// Postponing the start after the build made the scheduler think the bracket
// was for another tournament and rebuild it, results and all.
func TestPostponingKeepsTheBuiltBracket(t *testing.T) {
	bracket, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	ctx := context.Background()
	if _, err := bracket.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	svc := NewTelegramServiceImpl(repo, nopLogger{})
	later := tourneyAt.Add(30 * time.Minute)

	svc.SetTournamentTime(ctx, later)

	if !bracket.IsBuiltFor(ctx, later) {
		t.Error("bracket no longer counts as built after postponing; the scheduler would rebuild it")
	}
	if prov.count("CreateTournament") != 1 {
		t.Errorf("CreateTournament called %d times, want 1", prov.count("CreateTournament"))
	}
}

func TestBuildRefusesToWipePlayedResults(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	for i := range repo.bracket {
		if repo.bracket[i].Ready() {
			repo.bracket[i].State = models.BracketComplete
			repo.bracket[i].WinnerID = repo.bracket[i].Team1ID
			break
		}
	}

	_, err := svc.Build(ctx, tourneyAt)
	if !errors.Is(err, ErrHasResults) {
		t.Errorf("rebuild with a played match = %v, want ErrHasResults", err)
	}
	if prov.count("DeleteTournament") != 0 {
		t.Error("the played tournament was deleted from Challonge")
	}
}

func TestTournamentStartsOnlyAfterSuccessfulBuild(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	prov.fail["Start"] = errors.New("boom")
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err == nil {
		t.Fatal("build succeeded despite Start failure")
	}
	if st := repo.tournaments[0].Status; st != models.TournamentStatusRegistration {
		t.Errorf("status after failed build = %q, want registration so the start button stays", st)
	}
	delete(prov.fail, "Start")
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	if st := repo.tournaments[0].Status; st != models.TournamentStatusActive {
		t.Errorf("status after build = %q, want active", st)
	}
}

// A slug still held by an undeleted old tournament made every rebuild fail.
func TestRebuildPicksFreshSlugWhenOldTournamentStays(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	repo.tournaments[0].Slug = "cup"
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	prov.fail["DeleteTournament"] = errors.New("boom")
	built, err := svc.Build(ctx, tourneyAt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built.URL, "/cup_") {
		t.Errorf("URL after failed delete = %q, want a fresh cup_* slug", built.URL)
	}
}

func TestNormalizeChallongeSlug(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "",
		"valhalla_cup_2":                       "valhalla_cup_2",
		"  https://challonge.com/valhalla_2/ ": "valhalla_2",
		"https://sub.challonge.com/cup?x=1":    "cup",
	} {
		got, err := normalizeChallongeSlug(in)
		if err != nil || got != want {
			t.Errorf("normalizeChallongeSlug(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"valhalla-2", "кубок", "cup 2"} {
		if _, err := normalizeChallongeSlug(bad); err == nil {
			t.Errorf("normalizeChallongeSlug(%q) accepted an invalid slug", bad)
		}
	}
}
