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

	if _, err := svc.CreateTournament(ctx, TournamentSettings{Name: "Этап 2"}); err != nil {
		t.Fatal(err)
	}
	if alpha.IsCheckedIn || gamma.IsCheckedIn || gamma.Status != models.TeamStatusActive {
		t.Fatalf("after new tournament: alpha checked=%v, gamma checked=%v status=%q; want clean teams",
			alpha.IsCheckedIn, gamma.IsCheckedIn, gamma.Status)
	}
	if got := svc.SetTeamCheckIn(ctx, captainOf(2), true).Message; !strings.Contains(got, "подтверждён") {
		t.Errorf("Gamma check-in = %q, want confirmation", got)
	}
	if got := svc.SetTeamCheckIn(ctx, captainOf(1), true).Message; !strings.Contains(got, "подтверждён") {
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

	if got := svc.SetTeamCheckIn(ctx, captainOf(1), true).Message; !strings.Contains(got, "подтверждён") {
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

	got := svc.SetTeamCheckIn(ctx, captainOf(1), true).Message
	if !strings.Contains(got, "закрыт") {
		t.Errorf("check-in = %q, want refusal: the bracket is built", got)
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
	svc.SetTeamCheckIn(ctx, captainOf(1), true)

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
	svc.SetTeamCheckIn(ctx, captainOf(1), true)

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

// qualifierFixture: tournament 1 is a running 16-team qualifier sending 8 on;
// every team is entered with a Challonge participant id, the bracket holds
// round 1 plus the quarterfinals Challonge opened after it.
func qualifierFixture(t *testing.T, playAll bool) (*TelegramServiceImpl, *fakeTelegramRepo) {
	t.Helper()
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	q := repo.tournaments[0]
	q.Stage, q.AdvanceCount, q.BracketSize, q.Status = models.StageQualifier, 8, 16, models.TournamentStatusActive
	for i := 1; i <= 16; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%02d", i), 10)
		_ = repo.RegisterTeamForTournament(ctx, 1, i)
		_ = repo.SetTournamentTeamParticipantID(ctx, 1, i, int64(5000+i))
	}
	var ms []models.BracketMatch
	for i := 0; i < 8; i++ {
		a, b := 2*i+1, 2*i+2
		w := a
		if !playAll && i == 7 {
			w = 0
		}
		m := sm(1, i+1, a, b, w)
		m.ChallongeMatchID = int64(i + 1)
		ms = append(ms, m)
	}
	for i := 0; i < 3; i++ {
		m := sm(2, 9+i, 4*i+1, 4*i+3, 0)
		m.ChallongeMatchID = int64(9 + i)
		ms = append(ms, m)
	}
	repo.bracket = ms
	repo.settings[settingChallongeID] = "777"
	repo.settings["tournament_time"] = "2026-10-12T15:00:00Z"
	svc.tournamentTime = time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	return svc, repo
}

func TestFinishQualifierMarksQualified(t *testing.T) {
	svc, repo := qualifierFixture(t, true)
	ctx := context.Background()

	res, err := svc.FinishTournament(ctx, 1)
	if err != nil {
		t.Fatalf("FinishTournament: %v", err)
	}
	if len(res.Qualified) != 8 {
		t.Fatalf("qualified %d teams, want 8", len(res.Qualified))
	}
	for i := 1; i <= 16; i++ {
		e, _ := repo.GetTournamentTeam(ctx, 1, i)
		wantQualified := i%2 == 1
		if (e.Status == models.EntryQualified) != wantQualified {
			t.Errorf("team %d status %q, qualified want %v", i, e.Status, wantQualified)
		}
	}
	for _, m := range repo.bracket {
		if m.Round != 1 {
			t.Errorf("round %d match left in the cache after the qualifier closed", m.Round)
		}
	}
	if q := repo.tournaments[0]; q.Status != models.TournamentStatusCompleted || q.IsActive {
		t.Errorf("tournament status=%q active=%v, want completed and inactive", q.Status, q.IsActive)
	}
	if repo.settings[settingChallongeID] != "" || !svc.GetTournamentTime(ctx).IsZero() {
		t.Error("the finished qualifier must be detached from the background worker")
	}
}

func TestFinishQualifierPendingFails(t *testing.T) {
	svc, repo := qualifierFixture(t, false)
	_, err := svc.FinishTournament(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "#8") {
		t.Fatalf("err = %v, want the unplayed match #8 named", err)
	}
	if repo.tournaments[0].Status == models.TournamentStatusCompleted {
		t.Error("an unfinished qualifier was closed")
	}
}

func TestTryFinishQualifierWaitsForTheLine(t *testing.T) {
	svc, _ := qualifierFixture(t, false)
	if res, err := svc.TryFinishQualifier(context.Background()); res != nil || err != nil {
		t.Fatalf("TryFinishQualifier = %+v, %v; want nothing while round 1 runs", res, err)
	}
	svc, _ = qualifierFixture(t, true)
	res, err := svc.TryFinishQualifier(context.Background())
	if err != nil || res == nil || len(res.Qualified) != 8 {
		t.Fatalf("TryFinishQualifier = %+v, %v; want the qualifier closed", res, err)
	}
}

func TestImportQualifiedAndInvite(t *testing.T) {
	svc, repo := qualifierFixture(t, true)
	ctx := context.Background()
	if _, err := svc.FinishTournament(ctx, 1); err != nil {
		t.Fatal(err)
	}
	addBracketTeam(repo, 40, "Guest", 10)
	main, err := svc.CreateTournament(ctx, TournamentSettings{Name: "Основной этап", Stage: models.StageMain})
	if err != nil {
		t.Fatal(err)
	}
	n, err := svc.ImportQualified(ctx, main.ID, []int{1})
	if err != nil || n != 8 {
		t.Fatalf("ImportQualified = %d, %v; want 8", n, err)
	}
	e, _ := repo.GetTournamentTeam(ctx, main.ID, 3)
	if e == nil || e.Source != models.SourceQualified || e.QualifiedFrom == nil || *e.QualifiedFrom != 1 {
		t.Errorf("imported entry = %+v", e)
	}
	if e, _ := repo.GetTournamentTeam(ctx, main.ID, 2); e != nil {
		t.Errorf("eliminated team 2 was imported: %+v", e)
	}
	if err := svc.InviteTeam(ctx, main.ID, 40); err != nil {
		t.Fatalf("InviteTeam: %v", err)
	}
	if e, _ := repo.GetTournamentTeam(ctx, main.ID, 40); e == nil || e.Source != models.SourceInvited {
		t.Errorf("invited entry = %+v", e)
	}
	if err := svc.InviteTeam(ctx, 1, 40); err == nil {
		t.Error("inviting into a qualifier must be refused")
	}
}

func TestImportRefusesUnfinishedQualifier(t *testing.T) {
	svc, repo := qualifierFixture(t, false)
	ctx := context.Background()
	repo.tournaments[0].IsActive = false
	main, err := svc.CreateTournament(ctx, TournamentSettings{Name: "Основной этап", Stage: models.StageMain})
	if err != nil {
		t.Fatal(err)
	}
	// CreateTournament marks the previous active tournament completed; put
	// the qualifier back to running.
	repo.tournaments[0].Status = models.TournamentStatusActive
	if _, err := svc.ImportQualified(ctx, main.ID, []int{1}); err == nil {
		t.Error("import from a running qualifier must be refused")
	}
}

func TestMainStageRefusesSelfRegistration(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	repo.tournaments[0].Stage = models.StageMain
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	if err := svc.RegisterTeamForTournament(ctx, captainOf(1), 1); !errors.Is(err, errMainStageClosed) {
		t.Errorf("RegisterTeamForTournament = %v, want main stage closed", err)
	}
	svc.SetTeamCheckIn(ctx, captainOf(1), true)
	if e, _ := repo.GetTournamentTeam(ctx, 1, 1); e != nil {
		t.Errorf("check-in entered the team into the main stage: %+v", e)
	}
}

func TestQualifiedTeamBlockedFromSecondQualifier(t *testing.T) {
	svc, repo := qualifierFixture(t, true)
	ctx := context.Background()
	if _, err := svc.FinishTournament(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateTournament(ctx, TournamentSettings{Name: "Квала 2", Stage: models.StageQualifier, AdvanceCount: 8}); err != nil {
		t.Fatal(err)
	}
	second := repo.tournaments[len(repo.tournaments)-1].ID
	if err := svc.RegisterTeamForTournament(ctx, captainOf(1), second); !errors.Is(err, errAlreadyQualified) {
		t.Errorf("qualified team 1: %v, want already qualified", err)
	}
	if err := svc.RegisterTeamForTournament(ctx, captainOf(2), second); err != nil {
		t.Errorf("eliminated team 2: %v, want allowed", err)
	}
}

func TestTournamentSettingsValidation(t *testing.T) {
	bad := []TournamentSettings{
		{Name: ""},
		{Name: "Q", Stage: models.StageQualifier, AdvanceCount: 6},
		{Name: "Q", Stage: models.StageQualifier, AdvanceCount: 0},
		{Name: "Q", Stage: models.StageQualifier, AdvanceCount: 2, TournamentType: models.TournamentTypeDoubleElimination},
		{Name: "Q", Stage: models.StageQualifier, AdvanceCount: 8, TournamentType: models.TournamentTypeSwiss},
		{Name: "T", FormatFinal: 4},
		{Name: "T", Stage: "playoff"},
	}
	for _, set := range bad {
		s := set
		if err := normalizeSettings(&s); err == nil {
			t.Errorf("%+v accepted", set)
		}
	}
	ok := TournamentSettings{Name: " Квала ", Stage: models.StageQualifier, AdvanceCount: 8, TournamentType: models.TournamentTypeDoubleElimination, FormatFinal: 5}
	if err := normalizeSettings(&ok); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	if ok.Name != "Квала" || ok.FormatRounds != 1 || ok.FormatSemifinal != 3 || ok.FormatFinal != 5 {
		t.Errorf("normalized = %+v", ok)
	}
	plain := TournamentSettings{Name: "T", AdvanceCount: 8}
	_ = normalizeSettings(&plain)
	if plain.AdvanceCount != 0 || plain.Stage != models.StageStandalone {
		t.Errorf("standalone keeps no advance count: %+v", plain)
	}
}

// Marking a running tournament as a qualifier after the fact (the first
// qualifier ran before stages existed) records its size and cuts the cache.
func TestUpdateSettingsTurnsRunningTournamentIntoQualifier(t *testing.T) {
	svc, repo := qualifierFixture(t, true)
	ctx := context.Background()
	q := repo.tournaments[0]
	q.Stage, q.AdvanceCount, q.BracketSize = models.StageStandalone, 0, 0
	// The full bracket: add the remaining rounds so the size reads 16.
	repo.bracket = append(repo.bracket, sm(3, 13, 0, 0, 0), sm(4, 15, 0, 0, 0))

	got, err := svc.UpdateTournamentSettings(ctx, 1, TournamentSettings{Name: q.Name, Stage: models.StageQualifier, AdvanceCount: 8})
	if err != nil {
		t.Fatalf("UpdateTournamentSettings: %v", err)
	}
	if got.BracketSize != 16 {
		t.Errorf("BracketSize = %d, want 16", got.BracketSize)
	}
	for _, m := range repo.bracket {
		if m.Round != 1 {
			t.Errorf("round %d still cached", m.Round)
		}
	}
}

func TestCheckInClosesAtStart(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	addBracketTeam(repo, 2, "Beta", 10).IsCheckedIn = false
	start := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)
	svc.SetTournamentTime(ctx, start)
	svc.now = func() time.Time { return start.Add(-time.Minute) }
	if res := svc.SetTeamCheckIn(ctx, captainOf(2), true); !res.OK {
		t.Fatalf("check-in before the start = %+v", res)
	}

	svc.now = func() time.Time { return start.Add(time.Minute) }
	if res := svc.SetTeamCheckIn(ctx, captainOf(1), true); res.OK || !strings.Contains(res.Message, "закрыт") {
		t.Errorf("check-in after the start = %+v, want closed", res)
	}
	if res := svc.SetTeamCheckIn(ctx, captainOf(2), false); res.OK || !res.CheckedIn {
		t.Errorf("un-check after the start = %+v, want refused and still checked in", res)
	}
	// Repeating the current state is not a change and still answers.
	if res := svc.SetTeamCheckIn(ctx, captainOf(2), true); !res.OK || !res.CheckedIn {
		t.Errorf("repeated check-in after the start = %+v", res)
	}
}

// The bracket counts only the main roster; check-in used to count
// substitutes too and let in a team the bracket then dropped as incomplete.
func TestCheckInCountsMainRosterOnly(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	for _, m := range repo.members {
		if m.TeamID != nil && *m.TeamID == 1 && m.ID == 14 {
			m.IsSubstitute = true
		}
	}
	res := svc.SetTeamCheckIn(ctx, captainOf(1), true)
	if res.OK || !strings.Contains(res.Message, "4 из 5") {
		t.Errorf("check-in with 4 main + 1 substitute = %+v, want refusal", res)
	}
}

func TestCheckInRecordsActorAndPublishes(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	var events []map[string]any
	svc.SetEventHook(func(event string, payload map[string]any) {
		if event == "checkin_update" {
			events = append(events, payload)
		}
	})
	if res := svc.SetTeamCheckIn(ctx, captainOf(1), true); !res.OK {
		t.Fatalf("check-in = %+v", res)
	}
	e, _ := repo.GetTournamentTeam(ctx, 1, 1)
	if e.CheckedInBy == nil || *e.CheckedInBy != captainOf(1) || e.CheckedInAt == nil {
		t.Errorf("entry audit = by %v at %v, want the captain", e.CheckedInBy, e.CheckedInAt)
	}
	if len(events) != 1 || events[0]["is_checked_in"] != true {
		t.Errorf("events = %v, want one checkin_update", events)
	}
	svc.SetTeamCheckIn(ctx, captainOf(1), true)
	if len(events) != 1 {
		t.Error("a repeated check-in published again")
	}
}

// No-shows are left out of the bracket at build; the old sweep must not
// disqualify them a second time.
func TestNoShowIsNotDisqualifiedAgain(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()
	addBracketTeam(repo, 1, "Alpha", 10).IsCheckedIn = false
	_ = repo.RegisterTeamForTournament(ctx, 1, 1)
	_ = repo.SetTournamentTeamStatus(ctx, 1, 1, models.EntryNoShow)
	if dq, _ := svc.DisqualifyUnchecked(ctx); len(dq) != 0 {
		t.Errorf("disqualified %d no-shows", len(dq))
	}
}
