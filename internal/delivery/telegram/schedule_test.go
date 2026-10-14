package telegram

import (
	"strings"
	"testing"
	"time"
)

// The scheduler used to compare only hour and minute against the tournament
// time, which fired every day at that time of day, duplicated whenever two ticks
// shared a minute, and skipped the event when a tick ran late.

func TestCheckInWavesFireOnceEach(t *testing.T) {
	b := &Bot{}
	tournament := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	if _, ok := b.dueCheckInWave(tournament, tournament.Add(-61*time.Minute)); ok {
		t.Fatal("a wave fired more than an hour before the start")
	}
	for _, want := range []time.Duration{60 * time.Minute, 30 * time.Minute, 10 * time.Minute} {
		at := tournament.Add(-want).Add(time.Second)
		lead, ok := b.dueCheckInWave(tournament, at)
		if !ok || lead != want {
			t.Fatalf("at T-%v: wave %v, %v", want, lead, ok)
		}
		if _, again := b.dueCheckInWave(tournament, at.Add(time.Second)); again {
			t.Fatalf("wave T-%v fired twice", want)
		}
	}
	if _, ok := b.dueCheckInWave(tournament, tournament.Add(time.Minute)); ok {
		t.Error("a wave fired after the start")
	}
}

// A bot that comes up 20 minutes before the start sends one reminder, not the
// missed hour-before one followed by the half-hour one.
func TestCheckInWaveLateStartSendsOne(t *testing.T) {
	b := &Bot{}
	tournament := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	lead, ok := b.dueCheckInWave(tournament, tournament.Add(-20*time.Minute))
	if !ok || lead != 30*time.Minute {
		t.Fatalf("late start: %v, %v; want the T-30 wave", lead, ok)
	}
	if _, ok := b.dueCheckInWave(tournament, tournament.Add(-19*time.Minute)); ok {
		t.Error("the missed T-60 wave went out after the T-30 one")
	}
}

// Rescheduling the tournament re-arms the waves.
func TestRescheduledTournamentRemindsAgain(t *testing.T) {
	b := &Bot{}
	first := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	if _, ok := b.dueCheckInWave(first, first.Add(-5*time.Minute)); !ok {
		t.Fatal("first tournament's reminder did not fire")
	}
	second := first.Add(24 * time.Hour)
	if _, ok := b.dueCheckInWave(second, second.Add(-5*time.Minute)); !ok {
		t.Error("reminders did not re-arm after the tournament was rescheduled")
	}
}

// /set_tourney reads the date in the configured tournament zone and says which
// zone it used, so an admin in Almaty and a server in UTC agree on 18:00.
func TestParseTournamentTimeUsesConfiguredZone(t *testing.T) {
	almaty, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Skip("tzdata not available")
	}
	b := &Bot{location: almaty}

	got, summary, err := b.parseTournamentTime("20.05.2026 18:00")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Location() != almaty || got.Hour() != 18 {
		t.Errorf("parsed %v, want 18:00 in Asia/Almaty", got)
	}
	for _, want := range []string{"20.05.2026 18:00", "Asia/Almaty", "17:00, 17:30, 17:50", "сетка построится: 18:00"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q lacks %q", summary, want)
		}
	}

	if _, _, err := b.parseTournamentTime("завтра"); err == nil {
		t.Error("garbage date was accepted")
	}
}
