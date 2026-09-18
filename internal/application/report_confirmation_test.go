package application

import (
	"blackwatch/internal/models"
	"context"
	"strings"
	"testing"
	"time"
)

type sentNote struct {
	chatID int64
	text   string
}

// twoTeamBracket wires one open match (A vs B, ids 1/2, captains 1001/1002)
// feeding a pending final, and returns the service with a fixed clock.
func twoTeamBracket(t *testing.T) (*TelegramServiceImpl, *fakeTelegramRepo, *[]sentNote, *time.Time) {
	t.Helper()
	ctx := context.Background()
	repo := newFakeTelegramRepo()
	teamA, _ := repo.CreateTeam(ctx, "Team A")
	teamB, _ := repo.CreateTeam(ctx, "Team B")
	repo.addPlayer(1001, &teamA.ID, true, "")
	repo.addPlayer(1002, &teamB.ID, true, "")
	repo.addPlayer(1003, &teamB.ID, false, "") // rank-and-file player of B
	m1 := models.BracketMatch{ChallongeMatchID: 11, PlayOrder: 1, Round: 1, Team1ID: &teamA.ID, Team2ID: &teamB.ID, State: models.BracketOpen}
	m2 := models.BracketMatch{ChallongeMatchID: 12, PlayOrder: 2, Round: 2, State: models.BracketPending}
	_ = repo.ReplaceBracketMatches(ctx, []models.BracketMatch{m1, m2})

	svc := NewTelegramServiceImpl(repo, nopLogger{})
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	var notes []sentNote
	svc.SetMatchNotifier(func(_ context.Context, chatID int64, text string, _ bool) {
		notes = append(notes, sentNote{chatID, text})
	})
	return svc, repo, &notes, &now
}

// matchState finds a match by play order; the fake repo assigns its own ids.
func matchState(t *testing.T, repo *fakeTelegramRepo, playOrder int) models.BracketMatch {
	t.Helper()
	ms, _ := repo.GetBracketMatches(context.Background())
	for _, m := range ms {
		if m.PlayOrder == playOrder {
			return m
		}
	}
	t.Fatalf("match %d missing", playOrder)
	return models.BracketMatch{}
}

func TestReportWaitsForOpponentConfirmation(t *testing.T) {
	ctx := context.Background()
	svc, repo, notes, now := twoTeamBracket(t)

	rep, err := svc.ReportMatchDirect(ctx, 1001, 0, 2, 0, []string{"f1"})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Status != models.ReportPending {
		t.Fatalf("status = %q, want pending", rep.Status)
	}
	if rep.ExpiresAt == nil || !rep.ExpiresAt.Equal(now.Add(ReportConfirmWindow)) {
		t.Fatalf("expires_at = %v, want %v", rep.ExpiresAt, now.Add(ReportConfirmWindow))
	}
	if got := matchState(t, repo, 1).State; got != models.BracketOpen {
		t.Fatalf("bracket moved before confirmation: state %q", got)
	}

	// The opposing captain, and only they, hears about it.
	var toB bool
	for _, n := range *notes {
		if n.chatID == 1002 && strings.Contains(n.text, "2:0") && strings.Contains(n.text, "Team A") {
			toB = true
		}
		if n.chatID == 1001 {
			t.Errorf("reporter was notified about their own report: %q", n.text)
		}
	}
	if !toB {
		t.Errorf("opponent captain not notified: %+v", *notes)
	}

	// A second report from either side is refused while one is open.
	if _, err := svc.ReportMatchDirect(ctx, 1002, 0, 2, 1, []string{"f2"}); err == nil {
		t.Fatal("second report accepted while first is pending")
	}

	// The open report is visible to both sides.
	open, err := svc.GetOpenReportForMatch(ctx, *rep.BracketMatchID)
	if err != nil || open == nil || open.ID != rep.ID {
		t.Fatalf("GetOpenReportForMatch = %+v, %v", open, err)
	}
}

func TestOpponentConfirmsReport(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := twoTeamBracket(t)
	rep, _ := svc.ReportMatchDirect(ctx, 1001, 0, 2, 0, []string{"f1"})

	// Reporter cannot confirm their own report; a non-captain cannot either.
	if err := svc.ConfirmReport(ctx, 1001, rep.ID); err == nil {
		t.Fatal("reporter confirmed own report")
	}
	if err := svc.ConfirmReport(ctx, 1003, rep.ID); err == nil {
		t.Fatal("non-captain confirmed report")
	}

	if err := svc.ConfirmReport(ctx, 1002, rep.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if got := repo.reports[0].Status; got != models.ReportConfirmed {
		t.Fatalf("status = %q, want confirmed", got)
	}
	if got := matchState(t, repo, 1); got.State != models.BracketComplete || got.WinnerID == nil || *got.WinnerID != 1 {
		t.Fatalf("bracket not propagated after confirm: %+v", got)
	}
	if open, _ := svc.GetOpenReportForMatch(ctx, *rep.BracketMatchID); open != nil {
		t.Fatalf("report still open after confirm: %+v", open)
	}
	// Confirming twice is a no-op error, not a second propagation.
	if err := svc.ConfirmReport(ctx, 1002, rep.ID); err == nil {
		t.Fatal("confirmed an already closed report")
	}
}

func TestOpponentDisputesReport(t *testing.T) {
	ctx := context.Background()
	svc, repo, notes, _ := twoTeamBracket(t)
	rep, _ := svc.ReportMatchDirect(ctx, 1001, 0, 2, 0, []string{"f1"})
	*notes = nil

	if err := svc.DisputeReport(ctx, 1002, rep.ID); err != nil {
		t.Fatalf("dispute: %v", err)
	}
	if got := repo.reports[0].Status; got != models.ReportDisputed {
		t.Fatalf("status = %q, want disputed", got)
	}
	if got := matchState(t, repo, 1).State; got != models.BracketOpen {
		t.Fatalf("disputed report moved the bracket: %q", got)
	}
	// The reporter learns the result is contested.
	var toA bool
	for _, n := range *notes {
		if n.chatID == 1001 && strings.Contains(n.text, "оспорил") {
			toA = true
		}
	}
	if !toA {
		t.Errorf("reporter not told about dispute: %+v", *notes)
	}
	// A disputed report is still "open": nobody can file a fresh one, and the
	// auto-confirm sweep must leave it alone.
	if _, err := svc.ReportMatchDirect(ctx, 1002, 0, 2, 1, nil); err == nil {
		t.Fatal("new report accepted while dispute is open")
	}
	svc.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	done, err := svc.AutoConfirmExpiredReports(ctx)
	if err != nil || len(done) != 0 {
		t.Fatalf("sweep touched a disputed report: %+v, %v", done, err)
	}

	// The referee's decision closes it.
	if err := svc.SetWinnerDirect(ctx, 1, "Team B", 2, 1); err != nil {
		t.Fatalf("SetWinnerDirect: %v", err)
	}
	if got := repo.reports[0].Status; got != models.ReportOverridden {
		t.Fatalf("status after admin decision = %q, want overridden", got)
	}
}

func TestPendingReportAutoConfirms(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, now := twoTeamBracket(t)
	rep, _ := svc.ReportMatchDirect(ctx, 1001, 0, 2, 0, []string{"f1"})

	// Not yet.
	svc.now = func() time.Time { return now.Add(ReportConfirmWindow - time.Second) }
	if done, _ := svc.AutoConfirmExpiredReports(ctx); len(done) != 0 {
		t.Fatalf("auto-confirmed early: %+v", done)
	}
	svc.now = func() time.Time { return now.Add(ReportConfirmWindow + time.Second) }
	done, err := svc.AutoConfirmExpiredReports(ctx)
	if err != nil || len(done) != 1 || done[0].ID != rep.ID {
		t.Fatalf("sweep = %+v, %v", done, err)
	}
	if got := repo.reports[0].Status; got != models.ReportAutoConfirmed {
		t.Fatalf("status = %q, want auto_confirmed", got)
	}
	if got := matchState(t, repo, 1); got.State != models.BracketComplete {
		t.Fatalf("bracket not propagated after auto-confirm: %+v", got)
	}
	// Second sweep finds nothing.
	if done, _ := svc.AutoConfirmExpiredReports(ctx); len(done) != 0 {
		t.Fatalf("sweep repeated: %+v", done)
	}
}

// A losing captain may file the result too; the winner then confirms.
func TestLoserCanReportAndWinnerConfirms(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := twoTeamBracket(t)
	rep, err := svc.ReportMatchDirect(ctx, 1002, 0, 0, 2, nil)
	if err != nil {
		t.Fatalf("loser report: %v", err)
	}
	if rep.WinnerTeamID != 1 || rep.Score != "2:0" {
		t.Fatalf("report normalised wrong: %+v", rep)
	}
	if err := svc.ConfirmReport(ctx, 1001, rep.ID); err != nil {
		t.Fatalf("winner confirm: %v", err)
	}
	if got := matchState(t, repo, 1); got.WinnerID == nil || *got.WinnerID != 1 {
		t.Fatalf("winner not set: %+v", got)
	}
}
