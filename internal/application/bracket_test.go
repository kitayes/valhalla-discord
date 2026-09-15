package application

import (
	"blackwatch/internal/challonge"
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"
)

// fakeChallonge is an in-memory single-elimination engine: enough of
// Challonge to drive the service. It seeds 1 vs N, hands byes to the top
// seeds and resets the branch when a completed match is reopened — the
// three behaviours the service relies on.
type fakeChallonge struct {
	nextID   int64
	tourneys map[int64]*fakeTourney
	calls    []string
	fail     map[string]error // method name -> error to return
}

type fakeTourney struct {
	slug         string
	participants []challonge.Participant
	matches      []*fakeMatch
	started      bool
}

type fakeMatch struct {
	challonge.Match
	next     *fakeMatch // where the winner goes
	nextSlot int        // 1 or 2
	src1     *fakeMatch // feeders, nil for a seeded slot
	src2     *fakeMatch
}

func newFakeChallonge() *fakeChallonge {
	return &fakeChallonge{nextID: 100, tourneys: map[int64]*fakeTourney{}, fail: map[string]error{}}
}

func (f *fakeChallonge) call(name string) error {
	f.calls = append(f.calls, name)
	return f.fail[name]
}

func (f *fakeChallonge) CreateTournament(_ context.Context, name, slug string) (challonge.Tournament, error) {
	if err := f.call("CreateTournament"); err != nil {
		return challonge.Tournament{}, err
	}
	f.nextID++
	f.tourneys[f.nextID] = &fakeTourney{slug: slug}
	return challonge.Tournament{ID: f.nextID, Slug: slug, URL: "https://challonge.com/" + slug}, nil
}

func (f *fakeChallonge) BulkAddParticipants(_ context.Context, tID int64, ps []challonge.NewParticipant) ([]challonge.Participant, error) {
	if err := f.call("BulkAddParticipants"); err != nil {
		return nil, err
	}
	t := f.tourneys[tID]
	for _, p := range ps {
		f.nextID++
		t.participants = append(t.participants, challonge.Participant{ID: f.nextID, Name: p.Name, Seed: p.Seed})
	}
	return t.participants, nil
}

// Start builds the bracket: size M = next power of two, top seeds get byes,
// pairings 1 vs M, 2 vs M-1 ... within a plain sequential layout.
func (f *fakeChallonge) Start(_ context.Context, tID int64) error {
	if err := f.call("Start"); err != nil {
		return err
	}
	t := f.tourneys[tID]
	ps := append([]challonge.Participant(nil), t.participants...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Seed < ps[j].Seed })
	n := len(ps)
	m := 1
	for m < n {
		m *= 2
	}
	// slot i (0-based) holds seed order: i even -> seed i/2+1 from the top, odd -> from the bottom.
	slots := make([]*challonge.Participant, m)
	for i := 0; i < m/2; i++ {
		top := i
		bottom := m - 1 - i
		if top < n {
			slots[2*i] = &ps[top]
		}
		if bottom < n {
			slots[2*i+1] = &ps[bottom]
		}
	}
	// Round 1: m/2 matches over the slots. Later rounds pair winners.
	var rounds [][]*fakeMatch
	var r1 []*fakeMatch
	order := 0
	for i := 0; i < m; i += 2 {
		order++
		fm := &fakeMatch{Match: challonge.Match{Round: 1, PlayOrder: order}}
		f.nextID++
		fm.ID = f.nextID
		if slots[i] != nil {
			fm.Player1ID = slots[i].ID
		}
		if slots[i+1] != nil {
			fm.Player2ID = slots[i+1].ID
		}
		r1 = append(r1, fm)
	}
	rounds = append(rounds, r1)
	prev := r1
	round := 1
	for len(prev) > 1 {
		round++
		var cur []*fakeMatch
		for i := 0; i < len(prev); i += 2 {
			order++
			f.nextID++
			fm := &fakeMatch{Match: challonge.Match{ID: f.nextID, Round: round, PlayOrder: order}, src1: prev[i], src2: prev[i+1]}
			prev[i].next, prev[i].nextSlot = fm, 1
			prev[i+1].next, prev[i+1].nextSlot = fm, 2
			cur = append(cur, fm)
		}
		rounds = append(rounds, cur)
		prev = cur
	}
	for _, r := range rounds {
		t.matches = append(t.matches, r...)
	}
	// Byes: a round-1 match with one empty slot completes immediately.
	for _, fm := range r1 {
		if fm.Player1ID != 0 && fm.Player2ID == 0 {
			f.complete(fm, fm.Player1ID, "")
		} else if fm.Player1ID == 0 && fm.Player2ID != 0 {
			f.complete(fm, fm.Player2ID, "")
		}
	}
	f.refreshStates(t)
	t.started = true
	return nil
}

func (f *fakeChallonge) complete(fm *fakeMatch, winner int64, scores string) {
	fm.WinnerID = winner
	fm.State = "complete"
	fm.Scores = scores
	if fm.next != nil {
		if fm.nextSlot == 1 {
			fm.next.Player1ID = winner
		} else {
			fm.next.Player2ID = winner
		}
	}
}

func (f *fakeChallonge) refreshStates(t *fakeTourney) {
	for _, fm := range t.matches {
		if fm.State == "complete" {
			continue
		}
		if fm.Player1ID != 0 && fm.Player2ID != 0 {
			fm.State = "open"
		} else {
			fm.State = "pending"
		}
	}
}

func (f *fakeChallonge) ListMatches(_ context.Context, tID int64) ([]challonge.Match, error) {
	if err := f.call("ListMatches"); err != nil {
		return nil, err
	}
	t := f.tourneys[tID]
	out := make([]challonge.Match, 0, len(t.matches))
	for _, fm := range t.matches {
		out = append(out, fm.Match)
	}
	return out, nil
}

func (f *fakeChallonge) find(tID, matchID int64) *fakeMatch {
	for _, fm := range f.tourneys[tID].matches {
		if fm.ID == matchID {
			return fm
		}
	}
	return nil
}

func (f *fakeChallonge) ReportMatch(_ context.Context, tID, matchID, winnerPID, loserPID int64, w, l int) error {
	if err := f.call("ReportMatch"); err != nil {
		return err
	}
	fm := f.find(tID, matchID)
	if fm == nil || fm.State != "open" {
		return fmt.Errorf("fake: match %d not open", matchID)
	}
	if (fm.Player1ID != winnerPID || fm.Player2ID != loserPID) && (fm.Player2ID != winnerPID || fm.Player1ID != loserPID) {
		return fmt.Errorf("fake: participants %d/%d not in match %d", winnerPID, loserPID, matchID)
	}
	f.complete(fm, winnerPID, fmt.Sprintf("%d - %d", w, l))
	f.refreshStates(f.tourneys[tID])
	return nil
}

// ReopenMatch clears the result and everything downstream, as Challonge does.
func (f *fakeChallonge) ReopenMatch(_ context.Context, tID, matchID int64) error {
	if err := f.call("ReopenMatch"); err != nil {
		return err
	}
	fm := f.find(tID, matchID)
	if fm == nil {
		return fmt.Errorf("fake: no match %d", matchID)
	}
	f.reopen(fm)
	f.refreshStates(f.tourneys[tID])
	return nil
}

func (f *fakeChallonge) reopen(fm *fakeMatch) {
	if fm.next != nil {
		if fm.nextSlot == 1 {
			fm.next.Player1ID = 0
		} else {
			fm.next.Player2ID = 0
		}
		if fm.next.State == "complete" {
			f.reopen(fm.next)
		}
	}
	fm.WinnerID, fm.State, fm.Scores = 0, "open", ""
}

func (f *fakeChallonge) DeleteTournament(_ context.Context, tID int64) error {
	if err := f.call("DeleteTournament"); err != nil {
		return err
	}
	delete(f.tourneys, tID)
	return nil
}

// onlyTourney returns the id of the single tournament the fake holds.
func (f *fakeChallonge) onlyTourney() int64 {
	for id := range f.tourneys {
		return id
	}
	return 0
}

func (f *fakeChallonge) count(name string) int {
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

// --- fixtures ---------------------------------------------------------------

// addTeam registers a team with a captain (tg id = 100+teamID) and a main
// roster of the given stars; every player is non-substitute.
func addBracketTeam(repo *fakeTelegramRepo, id int, name string, stars ...int) *models.TelegramTeam {
	t := &models.TelegramTeam{ID: id, Name: name, Status: models.TeamStatusActive, IsCheckedIn: true}
	repo.teams[id] = t
	// The fake's GetAllTeams/GetTeamMembers read players from repo.players
	// (captains, keyed by telegram id) and repo.members (the rest).
	for i, s := range stars {
		tg := int64(100*id + i)
		p := &models.TelegramPlayer{ID: id*10 + i, TelegramID: &tg, Stars: s, IsCaptain: i == 0, TeamID: &id}
		if i == 0 {
			repo.players[tg] = p
		} else {
			repo.members = append(repo.members, p)
		}
	}
	return t
}

func newBracketSvc(t *testing.T) (*BracketService, *fakeTelegramRepo, *fakeChallonge) {
	t.Helper()
	repo := newFakeTelegramRepo()
	prov := newFakeChallonge()
	svc := NewBracketService(repo, prov, 1, 0, nopLogger{})
	svc.now = func() time.Time { return time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC) }
	return svc, repo, prov
}

var tourneyAt = time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)

// --- tests ------------------------------------------------------------------

func TestSeedTeamsByAverageStarsOfMainRoster(t *testing.T) {
	sub := true
	teams := []models.TelegramTeam{
		{ID: 1, Name: "Low", Status: models.TeamStatusActive, Players: []models.TelegramPlayer{{Stars: 10}, {Stars: 10}}},
		{ID: 2, Name: "High", Status: models.TeamStatusActive, Players: []models.TelegramPlayer{{Stars: 30}, {Stars: 20}, {Stars: 99, IsSubstitute: sub}}},
		{ID: 3, Name: "Out", Status: models.TeamStatusDisqualified, Players: []models.TelegramPlayer{{Stars: 100}}},
		{ID: 4, Name: "Tie", Status: models.TeamStatusActive, Players: []models.TelegramPlayer{{Stars: 25}}},
	}
	got := SeedTeams(teams)
	if len(got) != 3 {
		t.Fatalf("seeded %d teams, want 3 (disqualified excluded)", len(got))
	}
	// High: (30+20)/2 = 25 (substitute ignored); Tie: 25 -> earlier id (2) first.
	want := []string{"High", "Tie", "Low"}
	for i, w := range want {
		if got[i].Team.Name != w || got[i].Seed != i+1 {
			t.Errorf("seed %d = %s/%d, want %s/%d", i, got[i].Team.Name, got[i].Seed, w, i+1)
		}
	}
	if got[0].AvgStars != 25 {
		t.Errorf("High avg = %v, want 25", got[0].AvgStars)
	}
}

func TestBuildCreatesTournamentAndCachesMatches(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	// 6 teams -> bracket of 8, two byes for seeds 1 and 2.
	for i := 1; i <= 6; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 70-i*10, 70-i*10)
	}
	ctx := context.Background()

	built, err := svc.Build(ctx, tourneyAt)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.URL == "" || repo.settings[settingChallongeURL] != built.URL {
		t.Errorf("URL = %q, setting = %q", built.URL, repo.settings[settingChallongeURL])
	}
	if repo.settings[settingChallongeFor] != tourneyAt.Format(time.RFC3339) || !svc.IsBuiltFor(ctx, tourneyAt) {
		t.Errorf("challonge_tournament_for = %q; IsBuiltFor = %v", repo.settings[settingChallongeFor], svc.IsBuiltFor(ctx, tourneyAt))
	}
	if svc.IsBuiltFor(ctx, tourneyAt.Add(24*time.Hour)) {
		t.Error("IsBuiltFor a different tournament time = true")
	}
	for i := 1; i <= 6; i++ {
		if repo.teams[i].ChallongeParticipantID == nil {
			t.Errorf("team %d has no participant id", i)
		}
	}
	// Two round-1 matches with both teams (T3 vs T6, T4 vs T5), two byes.
	if len(built.Round1) != 2 {
		t.Errorf("Round1 = %d matches, want 2: %+v", len(built.Round1), built.Round1)
	}
	byes := map[string]bool{}
	for _, b := range built.Byes {
		byes[b.Name] = true
	}
	if len(byes) != 2 || !byes["T1"] || !byes["T2"] {
		t.Errorf("byes = %v, want T1 and T2", byes)
	}
	// The two byes (T1, T2) meet immediately in round 2; Sync already marked
	// it notified, so the announcer must ping it separately from Round1.
	if len(built.Later) != 1 || built.Later[0].Round != 2 || !built.Later[0].Has(1) || !built.Later[0].Has(2) {
		t.Errorf("Later = %+v, want one round-2 match with T1 (id 1) and T2 (id 2)", built.Later)
	}
	cached, _ := repo.GetBracketMatches(ctx)
	if len(cached) != 7 {
		t.Errorf("cached %d matches, want 7", len(cached))
	}
	for _, m := range cached {
		if m.Ready() && !m.BothNotified {
			t.Errorf("ready match #%d not marked notified after Build", m.PlayOrder)
		}
	}
	if has, _ := svc.HasResults(ctx); has {
		t.Error("HasResults = true right after build (byes are not results)")
	}
	// Budget: create, bulk, start, one list.
	if prov.count("ListMatches") != 1 {
		t.Errorf("ListMatches called %d times during Build, want 1", prov.count("ListMatches"))
	}
}

func TestBuildRefusesFewerThanTwoTeams(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	addBracketTeam(repo, 1, "Solo", 10)
	_, err := svc.Build(context.Background(), tourneyAt)
	if !errors.Is(err, ErrTooFewTeams) {
		t.Errorf("Build with one team = %v, want ErrTooFewTeams", err)
	}
	if prov.count("CreateTournament") != 0 {
		t.Error("CreateTournament called with too few teams")
	}
}

func TestRebuildDeletesPreviousTournament(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	firstID := repo.settings[settingChallongeID]
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	if prov.count("DeleteTournament") != 1 {
		t.Errorf("DeleteTournament called %d times, want 1", prov.count("DeleteTournament"))
	}
	if repo.settings[settingChallongeID] == firstID {
		t.Error("tournament id unchanged after rebuild")
	}
	if len(prov.tourneys) != 1 {
		t.Errorf("%d tournaments left in Challonge, want 1", len(prov.tourneys))
	}
}

func TestBuildFailureLeavesNothingBuilt(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	prov.fail["Start"] = errors.New("boom")
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err == nil {
		t.Fatal("Build succeeded with Start failing")
	}
	if svc.IsBuiltFor(ctx, tourneyAt) {
		t.Error("IsBuiltFor = true after a failed build")
	}
	if repo.settings[settingBuildFailures] != "1" {
		t.Errorf("bracket_build_failures = %q, want 1", repo.settings[settingBuildFailures])
	}
	// The half-made tournament is removed on the next attempt.
	delete(prov.fail, "Start")
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	if prov.count("DeleteTournament") != 1 || len(prov.tourneys) != 1 {
		t.Errorf("stale tournament not cleaned: deletes=%d left=%d", prov.count("DeleteTournament"), len(prov.tourneys))
	}
	if repo.settings[settingBuildFailures] != "0" {
		t.Errorf("bracket_build_failures = %q after success, want 0", repo.settings[settingBuildFailures])
	}
}

func TestSyncReportsNewlyReadyOnce(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	// Nothing changed: no new ready matches, but one request spent.
	ready, err := svc.Sync(ctx)
	if err != nil || len(ready) != 0 {
		t.Fatalf("Sync on idle bracket = %v, %v", ready, err)
	}
	// Finish both round-1 matches behind the service's back; the final opens.
	tID := prov.onlyTourney()
	for _, fm := range prov.tourneys[tID].matches {
		if fm.Round == 1 {
			_ = prov.ReportMatch(ctx, tID, fm.ID, fm.Player1ID, fm.Player2ID, 2, 0)
		}
	}
	ready, err = svc.Sync(ctx)
	if err != nil || len(ready) != 1 || ready[0].Round != 2 {
		t.Fatalf("Sync after round 1 = %+v, %v; want the final", ready, err)
	}
	ready, _ = svc.Sync(ctx)
	if len(ready) != 0 {
		t.Errorf("second Sync re-reported the final: %+v", ready)
	}
}

func TestSyncWithoutBracket(t *testing.T) {
	svc, _, _ := newBracketSvc(t)
	if _, err := svc.Sync(context.Background()); !errors.Is(err, ErrBracketNotBuilt) {
		t.Errorf("Sync without bracket = %v, want ErrBracketNotBuilt", err)
	}
}
