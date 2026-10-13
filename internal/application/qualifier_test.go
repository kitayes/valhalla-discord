package application

import (
	"context"
	"testing"

	"blackwatch/internal/models"
	"blackwatch/internal/repository"
)

func TestTakeQualifierResultAnnouncesOnce(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newBracketSvc(t)
	repo.tournaments = []*models.TelegramTournament{{ID: 7, IsActive: true}}
	repo.settings[repository.QualifierAdvanceSetting(7)] = "2"

	pi := func(v int) *int { return &v }
	// Four teams: two semifinals, then the final that a top-2 qualifier skips.
	repo.bracket = []models.BracketMatch{
		{ID: 1, Round: 1, Team1ID: pi(1), Team2ID: pi(2), WinnerID: pi(1), State: models.BracketComplete},
		{ID: 2, Round: 1, Team1ID: pi(3), Team2ID: pi(4), State: models.BracketOpen},
		{ID: 3, Round: 2, Team1ID: pi(1), Team1Name: "A", State: models.BracketPending},
	}
	if got := svc.TakeQualifierResult(ctx); got != nil {
		t.Fatalf("announced %v while a semifinal is still open", got)
	}

	repo.bracket[1].State, repo.bracket[1].WinnerID = models.BracketComplete, pi(3)
	repo.bracket[2].Team2ID, repo.bracket[2].Team2Name = pi(3), "C"
	got := svc.TakeQualifierResult(ctx)
	if len(got) != 2 || got[0].Name != "A" || got[1].Name != "C" {
		t.Fatalf("TakeQualifierResult = %v, want A and C", got)
	}
	if again := svc.TakeQualifierResult(ctx); again != nil {
		t.Fatalf("announced twice: %v", again)
	}
}

func TestTakeQualifierResultIgnoresOrdinaryTournament(t *testing.T) {
	svc, repo, _ := newBracketSvc(t)
	repo.tournaments = []*models.TelegramTournament{{ID: 7, IsActive: true}}
	repo.bracket = []models.BracketMatch{{ID: 1, Round: 1, State: models.BracketComplete}}
	if got := svc.TakeQualifierResult(context.Background()); got != nil {
		t.Fatalf("ordinary tournament announced %v", got)
	}
}
