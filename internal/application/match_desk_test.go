package application

import (
	"blackwatch/internal/models"
	"testing"
	"time"
)

func deskFixture(t *testing.T) (*models.MatchDesk, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	a, b := 10, 20
	d := &models.MatchDesk{}
	syncDesk(d, []models.BracketMatch{{ID: 1, PlayOrder: 12, Team1ID: &a, Team2ID: &b, Team1Name: "Alpha", Team2Name: "Beta", State: models.BracketOpen}}, now, now)
	return d, now
}

func TestDeskDeadlineWaitsForTournamentStart(t *testing.T) {
	d, now := deskFixture(t)
	d.Matches = nil
	a, b := 10, 20
	syncDesk(d, []models.BracketMatch{{ID: 1, Team1ID: &a, Team2ID: &b, State: models.BracketOpen}}, now.Add(time.Hour), now)
	if got := d.Matches[1].Deadline; !got.Equal(now.Add(70 * time.Minute)) {
		t.Fatalf("deadline = %s", got)
	}
}

func TestDeskReadyRequiresParticipantAndDoesNotRestartDeadline(t *testing.T) {
	d, now := deskFixture(t)
	m := d.Matches[1]
	if err := deskCaptainAction(m, 99, "ready", now); err == nil {
		t.Fatal("outsider marked ready")
	}
	if err := deskCaptainAction(m, 10, "ready", now); err != nil {
		t.Fatal(err)
	}
	if err := deskCaptainAction(m, 10, "ready", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !m.Ready[0] || m.Ready[1] || !m.Deadline.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("unexpected state: %+v", m)
	}
	if err := deskCaptainAction(m, 20, "ready", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !m.StartedAt.Equal(now.Add(3 * time.Minute)) {
		t.Fatalf("started = %s", m.StartedAt)
	}
}

func TestDeskJudgeRequestIsIdempotentAndRetainsReason(t *testing.T) {
	d, now := deskFixture(t)
	m := d.Matches[1]
	if err := deskCaptainAction(m, 10, "judge_lobby", now); err != nil {
		t.Fatal(err)
	}
	n := len(m.History)
	if err := deskCaptainAction(m, 10, "judge_lobby", now); err != nil {
		t.Fatal(err)
	}
	if m.Issues[0] != "Проблема с лобби" || len(m.History) != n {
		t.Fatalf("duplicate issue: %+v", m)
	}
}

func TestDeskOverlappingPausesOnlyCountOnce(t *testing.T) {
	d, now := deskFixture(t)
	m := d.Matches[1]
	setDeskPause(m, true, false, now.Add(2*time.Minute))
	setDeskPause(m, true, true, now.Add(3*time.Minute))
	setDeskPause(m, false, true, now.Add(5*time.Minute))
	if m.PausedAt.IsZero() {
		t.Fatal("global pause was lost")
	}
	setDeskPause(m, false, false, now.Add(8*time.Minute))
	if !m.Deadline.Equal(now.Add(16 * time.Minute)) {
		t.Fatalf("deadline = %s", m.Deadline)
	}
}

func TestDeskPairChangeAndReopenInvalidateReadyAndButtons(t *testing.T) {
	d, now := deskFixture(t)
	old := d.Matches[1].Generation
	d.Matches[1].Ready[0] = true
	a, c := 10, 30
	ms := []models.BracketMatch{{ID: 1, Team1ID: &a, Team2ID: &c, State: models.BracketOpen}}
	syncDesk(d, ms, now, now.Add(time.Minute))
	if d.Matches[1].Ready[0] || d.Matches[1].Generation == old {
		t.Fatal("pair change retained old controls")
	}
	old = d.Matches[1].Generation
	ms[0].State = models.BracketComplete
	syncDesk(d, ms, now, now.Add(2*time.Minute))
	ms[0].State = models.BracketOpen
	syncDesk(d, ms, now, now.Add(3*time.Minute))
	if d.Matches[1].Generation == old {
		t.Fatal("reopened match retained buttons")
	}
}
