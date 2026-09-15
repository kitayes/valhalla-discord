package application

import (
	"blackwatch/internal/models"
	"context"
	"encoding/json"
	"testing"
	"time"
)

type memoryDeskStore struct {
	state    models.MatchDesk
	snapshot models.DeskContext
}

func (s *memoryDeskStore) UpdateMatchDesk(_ context.Context, f func(*models.MatchDesk, models.DeskContext) error) error {
	data, _ := json.Marshal(s.state)
	var next models.MatchDesk
	_ = json.Unmarshal(data, &next)
	if err := f(&next, s.snapshot); err != nil {
		return err
	}
	s.state = next
	return nil
}

func deskServiceFixture(t *testing.T) (*MatchDeskService, *memoryDeskStore, time.Time) {
	t.Helper()
	_, now := deskFixture(t)
	a, b := 10, 20
	tgA, tgB := int64(100), int64(200)
	store := &memoryDeskStore{snapshot: models.DeskContext{StartsAt: now, Captains: map[int]models.TelegramPlayer{
		a: {TeamID: &a, TelegramID: &tgA, IsCaptain: true, GameID: "12345", ZoneID: "1002", TelegramUsername: "alpha"},
		b: {TeamID: &b, TelegramID: &tgB, IsCaptain: true, GameID: "67890", ZoneID: "1003", TelegramUsername: "beta"},
	}, Matches: []models.BracketMatch{{ID: 1, PlayOrder: 12, Team1ID: &a, Team2ID: &b, Team1Name: "Alpha", Team2Name: "Beta", State: models.BracketOpen}}}}
	svc := NewMatchDeskService(store, []int64{999}, time.UTC)
	svc.now = func() time.Time { return now }
	return svc, store, now
}

func TestDeskServiceOutboxSurvivesRestartAndAcknowledgesIndividually(t *testing.T) {
	s, store, now := deskServiceFixture(t)
	ctx := context.Background()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := s.Pending(ctx)
	if err != nil || len(notices) != 2 {
		t.Fatalf("pending=%v, %v", notices, err)
	}
	if err := s.Acknowledge(ctx, notices[0]); err != nil {
		t.Fatal(err)
	}
	s = NewMatchDeskService(store, []int64{999}, time.UTC)
	s.now = func() time.Time { return now.Add(time.Minute) }
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err = s.Pending(ctx)
	if err != nil || len(notices) != 1 {
		t.Fatalf("pending after restart=%v, %v", notices, err)
	}
}

func TestDeskServiceOnlyCaptainsAndAdminsCanAct(t *testing.T) {
	s, store, _ := deskServiceFixture(t)
	ctx := context.Background()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	gen := store.state.Matches[1].Generation
	if err := s.CaptainAction(ctx, 101, 1, gen, "ready"); err == nil {
		t.Fatal("outsider accepted")
	}
	if err := s.AdminAction(ctx, 100, 1, gen, "pause"); err == nil {
		t.Fatal("captain paused match")
	}
	if err := s.CaptainAction(ctx, 100, 1, gen+1, "ready"); err == nil {
		t.Fatal("stale callback accepted")
	}
	if err := s.CaptainAction(ctx, 100, 1, gen, "ready"); err != nil {
		t.Fatal(err)
	}
	if !store.state.Matches[1].Ready[0] {
		t.Fatal("readiness lost")
	}
}

func TestDeskServiceExpiryAlertsOnlyOnceAndPausedMatchDoesNotExpire(t *testing.T) {
	s, store, now := deskServiceFixture(t)
	ctx := context.Background()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now.Add(11 * time.Minute) }
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, n := range store.state.Outbox {
		if n.ChatID == 999 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("admin alerts=%d", count)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	count = 0
	for _, n := range store.state.Outbox {
		if n.ChatID == 999 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate admin alerts=%d", count)
	}
}

func TestDeskServiceGlobalPauseAppliesToNewMatches(t *testing.T) {
	s, store, now := deskServiceFixture(t)
	ctx := context.Background()
	if err := s.AdminAction(ctx, 999, 0, 0, "pause"); err != nil {
		t.Fatal(err)
	}
	a, b := 10, 20
	store.snapshot.Matches = append(store.snapshot.Matches, models.BracketMatch{ID: 2, Team1ID: &a, Team2ID: &b, State: models.BracketOpen})
	s.now = func() time.Time { return now.Add(time.Minute) }
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if store.state.Matches[2].PausedAt.IsZero() {
		t.Fatal("new match timer running during global pause")
	}
	if err := s.AdminAction(ctx, 999, 0, 0, "resume"); err != nil {
		t.Fatal(err)
	}
	if !store.state.Matches[2].PausedAt.IsZero() {
		t.Fatal("global resume missed new match")
	}
}

func TestDeskServiceAttentionIncludesMissingContact(t *testing.T) {
	s, store, _ := deskServiceFixture(t)
	delete(store.snapshot.Captains, 10)
	notices, err := s.Attention(context.Background(), 999)
	if err != nil || len(notices) != 1 {
		t.Fatalf("missing contact not visible: %v, %v", notices, err)
	}
}

func TestDeskServiceReopenBetweenTicksRejectsOldButton(t *testing.T) {
	s, store, _ := deskServiceFixture(t)
	ctx := context.Background()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	old := store.state.Matches[1].Generation
	store.snapshot.Revisions = map[int]int64{1: 2}
	if err := s.CaptainAction(ctx, 100, 1, old, "ready"); err == nil {
		t.Fatal("old button accepted after reopen")
	}
}

func TestDeskServiceOldBatchCannotAcknowledgeAnotherTournament(t *testing.T) {
	s, store, _ := deskServiceFixture(t)
	ctx := context.Background()
	store.state.Tournament = "old"
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	old := store.state.Outbox[0]
	store.state.Tournament = "new"
	if err := s.Acknowledge(ctx, old); err != nil {
		t.Fatal(err)
	}
	if len(store.state.Outbox) != 2 {
		t.Fatal("old ack deleted new tournament notice")
	}
	current, err := s.NoticeCurrent(ctx, old)
	if err != nil || current {
		t.Fatalf("old notice deliverable: %v %v", current, err)
	}
}

func TestDeskServiceAdminCanPauseByPublicMatchNumberAndReadHistory(t *testing.T) {
	s, store, _ := deskServiceFixture(t)
	ctx := context.Background()
	if err := s.AdminNumberAction(ctx, 100, 12, "pause"); err == nil {
		t.Fatal("captain paused by number")
	}
	if err := s.AdminNumberAction(ctx, 999, 12, "pause"); err != nil {
		t.Fatal(err)
	}
	if !store.state.Matches[1].LocalPaused {
		t.Fatal("public number did not select match")
	}
	if _, err := s.History(ctx, 300, 12); err == nil {
		t.Fatal("outsider read history")
	}
	events, err := s.History(ctx, 100, 12)
	if err != nil || len(events) == 0 {
		t.Fatalf("history: %v %v", events, err)
	}
}

func TestDeskServiceRemovedCaptainDoesNotReceiveQueuedCard(t *testing.T) {
	s, store, _ := deskServiceFixture(t)
	ctx := context.Background()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var old models.DeskNotice
	for _, n := range store.state.Outbox {
		if n.ChatID == 100 {
			old = n
		}
	}
	delete(store.snapshot.Captains, 10)
	current, err := s.NoticeCurrent(ctx, old)
	if err != nil || current {
		t.Fatalf("removed captain notice current=%v err=%v", current, err)
	}
}

func TestDeskServiceUnresolvedDisputeSurvivesReportedResult(t *testing.T){
 s,store,_:=deskServiceFixture(t)
 ctx:=context.Background()
 if err:=s.Tick(ctx);err!=nil{t.Fatal(err)}
 gen:=store.state.Matches[1].Generation
 if err:=s.CaptainAction(ctx,100,1,gen,"judge_score");err!=nil{t.Fatal(err)}
 store.snapshot.Matches[0].State=models.BracketComplete
 store.snapshot.Revisions=map[int]int64{1:1}
 notices,err:=s.Attention(ctx,999)
 if err!=nil || len(notices)!=1{t.Fatalf("reported result hid unresolved dispute: %v %v",notices,err)}
 if err:=s.AdminAction(ctx,999,1,gen,"resolve");err!=nil{t.Fatal(err)}
 notices,err=s.Attention(ctx,999)
 if err!=nil || len(notices)!=0{t.Fatalf("resolved dispute still present: %v %v",notices,err)}
}
