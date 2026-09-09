package telegram

import (
	"testing"
	"time"
)

// The scheduler used to compare only hour and minute against the tournament
// time, which fired every day at that time of day, duplicated whenever two ticks
// shared a minute, and skipped the event when a tick ran late.

func TestShouldFireOnlyOncePerTournament(t *testing.T) {
	b := &Bot{}
	tournament := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	deadline := tournament.Add(-checkInReminderLead)

	if !b.shouldFire(tournament, deadline, deadline.Add(time.Second), &b.remindedFor) {
		t.Fatal("reminder did not fire when its deadline passed")
	}
	if b.shouldFire(tournament, deadline, deadline.Add(2*time.Second), &b.remindedFor) {
		t.Error("reminder fired twice for the same tournament")
	}
}

func TestShouldNotFireBeforeDeadline(t *testing.T) {
	b := &Bot{}
	tournament := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	deadline := tournament.Add(-checkInReminderLead)

	if b.shouldFire(tournament, deadline, deadline.Add(-time.Minute), &b.remindedFor) {
		t.Error("reminder fired before its deadline")
	}
}

// A tick that lands a few minutes late must still fire — the old minute-equality
// check silently dropped the event.
func TestShouldFireOnLateTick(t *testing.T) {
	b := &Bot{}
	tournament := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	deadline := tournament.Add(-checkInReminderLead)

	if !b.shouldFire(tournament, deadline, deadline.Add(5*time.Minute), &b.remindedFor) {
		t.Error("reminder was skipped because the tick arrived late")
	}
}

// Restarting long after a tournament must not replay its notifications.
func TestShouldNotFireForStaleTournament(t *testing.T) {
	b := &Bot{}
	tournament := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	deadline := tournament.Add(-checkInReminderLead)

	if b.shouldFire(tournament, deadline, deadline.Add(scheduleCatchUpWindow+time.Minute), &b.remindedFor) {
		t.Error("a long-past tournament fired its reminder")
	}
}

// Rescheduling the tournament re-arms a stage that already ran.
func TestRescheduledTournamentFiresAgain(t *testing.T) {
	b := &Bot{}
	first := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	firstDeadline := first.Add(-checkInReminderLead)
	if !b.shouldFire(first, firstDeadline, firstDeadline, &b.remindedFor) {
		t.Fatal("first reminder did not fire")
	}

	second := first.Add(24 * time.Hour)
	secondDeadline := second.Add(-checkInReminderLead)
	if !b.shouldFire(second, secondDeadline, secondDeadline, &b.remindedFor) {
		t.Error("reminder did not re-arm after the tournament was rescheduled")
	}
}

// The two stages track their deadlines independently.
func TestStagesFireIndependently(t *testing.T) {
	b := &Bot{}
	tournament := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)

	remind := tournament.Add(-checkInReminderLead)
	if !b.shouldFire(tournament, remind, remind, &b.remindedFor) {
		t.Fatal("reminder did not fire")
	}

	defeat := tournament.Add(technicalDefeatGrace)
	if !b.shouldFire(tournament, defeat, defeat, &b.disqualifiedFor) {
		t.Error("technical defeat sweep did not fire after the reminder")
	}
}
