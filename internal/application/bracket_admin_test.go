package application

import (
	"context"
	"testing"

	"blackwatch/internal/models"
)

// changeLog collects the bracket changes the service hands to the bot.
type changeLog struct{ ready, reset []models.BracketMatch }

func (l *changeLog) hook(_ context.Context, ch *BracketChange) {
	if ch == nil {
		return
	}
	l.ready = append(l.ready, ch.Ready...)
	l.reset = append(l.reset, ch.Reset...)
}

func (l *changeLog) readyHas(id int) bool {
	for _, m := range l.ready {
		if m.ID == id {
			return true
		}
	}
	return false
}

func (l *changeLog) resetHas(id int) bool {
	for _, m := range l.reset {
		if m.ID == id {
			return true
		}
	}
	return false
}

func matchByID(t *testing.T, repo *fakeTelegramRepo, id int) models.BracketMatch {
	t.Helper()
	ms, _ := repo.GetBracketMatches(context.Background())
	for _, m := range ms {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("match %d not in the cache", id)
	return models.BracketMatch{}
}

func finalOf(t *testing.T, repo *fakeTelegramRepo) models.BracketMatch {
	t.Helper()
	ms, _ := repo.GetBracketMatches(context.Background())
	for _, m := range ms {
		if m.Round == 2 {
			return m
		}
	}
	t.Fatal("no final in the cache")
	return models.BracketMatch{}
}

// confirmSemi files a 2:0 for the captain's team and has the other captain
// confirm it.
func confirmSemi(t *testing.T, svc *TelegramServiceImpl, reporter, confirmer int64) {
	t.Helper()
	ctx := context.Background()
	rep, err := svc.ReportMatchDirect(ctx, reporter, 0, 2, 0, nil)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := svc.ConfirmReport(ctx, confirmer, rep.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
}

// The next pair used to be marked announced by the sync after a result and
// then dropped, so nobody but the captains' desk cards heard of it.
func TestConfirmedResultAnnouncesTheNextPair(t *testing.T) {
	bsvc, repo, _ := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	var log changeLog
	svc.SetBracketChangeNotifier(log.hook)

	confirmSemi(t, svc, 100, 400) // T1 beats T4
	if len(log.ready) != 0 {
		t.Fatalf("ready after one semi = %+v, the final has one side only", log.ready)
	}
	confirmSemi(t, svc, 200, 300) // T2 beats T3
	final := finalOf(t, repo)
	if !final.Ready() || !log.readyHas(final.ID) {
		t.Fatalf("final %+v not handed to the bot: ready = %+v", final, log.ready)
	}
}

// A referee's technical defeat goes the same way.
func TestRefereeWalkoverAnnouncesTheNextPair(t *testing.T) {
	bsvc, repo, _ := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	var log changeLog
	svc.SetBracketChangeNotifier(log.hook)
	ctx := context.Background()

	confirmSemi(t, svc, 100, 400)
	m2, _ := bsvc.OpenMatchFor(ctx, 2)
	if err := svc.SetWinnerDirect(ctx, m2.ID, "T3", 1, 0); err != nil {
		t.Fatalf("walkover: %v", err)
	}
	if final := finalOf(t, repo); !log.readyHas(final.ID) {
		t.Fatalf("final not handed to the bot after a walkover: ready = %+v", log.ready)
	}
}

// A rollback from the Mini App reopens the match in Challonge. It used to
// reopen it only in the cache: the replayed result was refused by Challonge,
// the next sync brought the old winner back and admins got a conflict alert.
func TestRollbackReopensTheMatchInChallonge(t *testing.T) {
	bsvc, repo, prov := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	var log changeLog
	svc.SetBracketChangeNotifier(log.hook)
	ctx := context.Background()
	m, _ := bsvc.OpenMatchFor(ctx, 1)
	confirmSemi(t, svc, 100, 400) // T1 beats T4

	if err := svc.RollbackMatch(ctx, m.ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if prov.count("ReopenMatch") != 1 {
		t.Fatalf("ReopenMatch calls = %d, want 1", prov.count("ReopenMatch"))
	}
	if got := matchByID(t, repo, m.ID); !got.Ready() {
		t.Fatalf("match after rollback = %+v, want open", got)
	}
	if !log.resetHas(m.ID) || !log.readyHas(m.ID) {
		t.Errorf("teams not told the match is replayed: reset %+v, ready %+v", log.reset, log.ready)
	}

	confirmSemi(t, svc, 400, 100) // replayed: T4 beats T1
	if _, err := bsvc.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got := matchByID(t, repo, m.ID)
	if got.State != models.BracketComplete || got.WinnerID == nil || *got.WinnerID != 4 {
		t.Fatalf("match after replay and sync = %+v, want T4 winner", got)
	}
	if queued, _ := repo.GetUnsyncedReports(ctx); len(queued) != 0 {
		t.Errorf("replayed result left unsynced: %+v", queued)
	}
}

func TestRollbackRefusedOnceTheNextMatchIsPlayed(t *testing.T) {
	bsvc, repo, prov := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	ctx := context.Background()
	m, _ := bsvc.OpenMatchFor(ctx, 1)
	confirmSemi(t, svc, 100, 400)
	confirmSemi(t, svc, 200, 300)
	confirmSemi(t, svc, 100, 200) // the final

	if err := svc.RollbackMatch(ctx, m.ID); err == nil {
		t.Fatal("rollback of a semi after the final was played succeeded")
	}
	if prov.count("ReopenMatch") != 0 {
		t.Errorf("ReopenMatch called %d times on a refused rollback", prov.count("ReopenMatch"))
	}
}

// Changing the winner writes Challonge and rebuilds the cache from it; the
// next match's pair changes and both pairs are told.
func TestChangeWinnerGoesThroughChallonge(t *testing.T) {
	bsvc, repo, prov := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	var log changeLog
	svc.SetBracketChangeNotifier(log.hook)
	ctx := context.Background()
	m, _ := bsvc.OpenMatchFor(ctx, 1)
	confirmSemi(t, svc, 100, 400) // T1 beats T4
	confirmSemi(t, svc, 200, 300) // final T1 vs T2 is ready
	log = changeLog{}

	if err := svc.ChangeWinnerDirect(ctx, m.ID, "T4", 2, 1); err != nil {
		t.Fatalf("change winner: %v", err)
	}
	if prov.count("ReopenMatch") != 1 {
		t.Errorf("ReopenMatch calls = %d, want 1", prov.count("ReopenMatch"))
	}
	if _, err := bsvc.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := matchByID(t, repo, m.ID); got.WinnerID == nil || *got.WinnerID != 4 {
		t.Fatalf("semi after change and sync = %+v, want T4 winner", got)
	}
	final := finalOf(t, repo)
	if !final.Has(4) || final.Has(1) {
		t.Fatalf("final after change = %+v, want T4 in place of T1", final)
	}
	if !log.resetHas(final.ID) || !log.readyHas(final.ID) {
		t.Errorf("final pair change not told: reset %+v, ready %+v", log.reset, log.ready)
	}
}

func TestChangeWinnerRefusedOnceTheNextMatchIsPlayed(t *testing.T) {
	bsvc, repo, prov := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	ctx := context.Background()
	m, _ := bsvc.OpenMatchFor(ctx, 1)
	confirmSemi(t, svc, 100, 400)
	confirmSemi(t, svc, 200, 300)
	confirmSemi(t, svc, 100, 200)

	if err := svc.ChangeWinnerDirect(ctx, m.ID, "T4", 2, 1); err == nil {
		t.Fatal("changing a semi's winner after the final was played succeeded")
	}
	if prov.count("ReopenMatch") != 0 {
		t.Errorf("ReopenMatch called %d times on a refused change", prov.count("ReopenMatch"))
	}
}

// In double elimination the loser of an upper match plays on in the lower
// bracket: that counts as played after. Earlier upper matches of a lower
// bracket team do not.
func TestPlayedAfterDoubleElimination(t *testing.T) {
	id := func(n int) *int { return &n }
	done := func(matchID, round, po, a, b int) models.BracketMatch {
		return models.BracketMatch{ID: matchID, Round: round, PlayOrder: po, Team1ID: id(a), Team2ID: id(b), WinnerID: id(a), State: models.BracketComplete}
	}
	ub1 := done(1, 1, 1, 1, 2)  // T1 beats T2
	ub2 := done(2, 1, 2, 3, 4)  // T3 beats T4
	lb1 := done(3, -1, 3, 2, 4) // T2 beats T4 in the lower bracket
	ubf := models.BracketMatch{ID: 4, Round: 2, PlayOrder: 4, Team1ID: id(1), Team2ID: id(3), State: models.BracketOpen}
	gf := models.BracketMatch{ID: 5, Round: 3, PlayOrder: 5, State: models.BracketPending}
	ms := []models.BracketMatch{ub1, ub2, lb1, ubf, gf}

	if got := playedAfter(ms, ub1); got == nil || got.ID != lb1.ID {
		t.Errorf("playedAfter(upper round 1) = %+v, want the lower bracket match", got)
	}
	if got := playedAfter(ms, lb1); got != nil {
		t.Errorf("playedAfter(lower round 1) = %+v, want nil: the upper matches came before", got)
	}
}
