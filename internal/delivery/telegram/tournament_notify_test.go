package telegram

import (
	"context"
	"testing"
)

func TestTournamentNotifyWhenDisabled(t *testing.T) {
	log := &captureLogger{}
	b := &Bot{
		tournamentChatID: "",
		logger:           log,
	}

	// None of these should panic or log errors when tournamentChatID is empty
	b.notifyTournamentChat("hello")
	b.notifyTeamRegistered(context.Background(), 123)
	b.notifyCheckIn("Check-in подтверждён. Команда 'Alpha' участвует в турнире.")
	b.notifyTeamDeletedFromResponse("Команда 'Alpha' удалена.", true)
	b.notifyTeamReinstated("Команда 'Alpha' возвращена в турнир")
	b.notifyTechnicalDefeats("СПИСОК ТЕХ. ПОРАЖЕНИЙ")

	if len(log.errors) != 0 {
		t.Errorf("expected no errors when notifications are disabled, got %v", log.errors)
	}
}

func TestResolveTournamentChatIDNumeric(t *testing.T) {
	b := &Bot{
		tournamentChatID: "-1009876543210",
		logger:           &captureLogger{},
	}
	id := b.resolveTournamentChatID()
	if id != -1009876543210 {
		t.Errorf("expected -1009876543210, got %d", id)
	}
}
