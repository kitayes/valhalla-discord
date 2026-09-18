package application

import (
	"blackwatch/internal/models"
	"context"
	"strings"
	"testing"
)

func TestMatchResultPropagationAndNotifications(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTelegramRepo()

	// Create 4 teams: Team A (1), Team B (2), Team C (3), Team D (4)
	teamA, _ := repo.CreateTeam(ctx, "Team A")
	teamB, _ := repo.CreateTeam(ctx, "Team B")
	teamC, _ := repo.CreateTeam(ctx, "Team C")
	teamD, _ := repo.CreateTeam(ctx, "Team D")

	// Create captains for each team
	repo.addPlayer(1001, &teamA.ID, true, "")
	repo.addPlayer(1002, &teamB.ID, true, "")
	repo.addPlayer(1003, &teamC.ID, true, "")
	repo.addPlayer(1004, &teamD.ID, true, "")

	// Setup bracket: Round 1 (Match 1: A vs B, Match 2: C vs D), Round 2 (Match 3: Winner M1 vs Winner M2)
	m1 := models.BracketMatch{ID: 1, PlayOrder: 1, Round: 1, Team1ID: &teamA.ID, Team2ID: &teamB.ID, State: models.BracketOpen}
	m2 := models.BracketMatch{ID: 2, PlayOrder: 2, Round: 1, Team1ID: &teamC.ID, Team2ID: &teamD.ID, State: models.BracketOpen}
	m3 := models.BracketMatch{ID: 3, PlayOrder: 3, Round: 2, State: models.BracketPending}
	_ = repo.ReplaceBracketMatches(ctx, []models.BracketMatch{m1, m2, m3})

	svc := NewTelegramServiceImpl(repo, nopLogger{})

	var sentMessages []struct {
		chatID       int64
		text         string
		hasWebAppBtn bool
	}

	svc.SetMatchNotifier(func(ctx context.Context, chatID int64, text string, hasWebAppBtn bool) {
		sentMessages = append(sentMessages, struct {
			chatID       int64
			text         string
			hasWebAppBtn bool
		}{chatID, text, hasWebAppBtn})
	})

	// Step 1: Team A beats Team B in Match 1 (2:0)
	err := svc.SetWinnerDirect(ctx, 1, "Team A", 2, 0)
	if err != nil {
		t.Fatalf("SetWinnerDirect M1 failed: %v", err)
	}

	// Verify Team A advanced to Match 3 slot 1, but Match 3 is not ready yet
	ms, _ := repo.GetBracketMatches(ctx)
	if ms[2].Team1ID == nil || *ms[2].Team1ID != teamA.ID {
		t.Fatalf("Expected Team A in Match 3 Team1ID, got: %v", ms[2].Team1ID)
	}
	if ms[2].State != models.BracketPending {
		t.Fatalf("Expected Match 3 still pending, got: %s", ms[2].State)
	}

	// Verify notifications from Step 1:
	// - Loser Team B (1002) notified of loss
	// - Winner Team A (1001) notified that they advanced and are waiting for parallel match
	var foundLoserMsg, foundWaitingMsg bool
	for _, msg := range sentMessages {
		if msg.chatID == 1002 && strings.Contains(msg.text, "Матч #1 завершён") && strings.Contains(msg.text, "выбывает") {
			foundLoserMsg = true
		}
		if msg.chatID == 1001 && strings.Contains(msg.text, "Победа со счётом 2:0") && strings.Contains(msg.text, "Ожидаем завершения параллельного матча") {
			foundWaitingMsg = true
			if !msg.hasWebAppBtn {
				t.Errorf("Expected WebApp button on winner message")
			}
		}
	}
	if !foundLoserMsg {
		t.Errorf("Team B captain was not notified about loss: %+v", sentMessages)
	}
	if !foundWaitingMsg {
		t.Errorf("Team A captain was not notified about waiting for opponent: %+v", sentMessages)
	}

	// Clear sent messages for Step 2
	sentMessages = nil

	// Step 2: Team C beats Team D in Match 2 (2:1)
	err = svc.SetWinnerDirect(ctx, 2, "Team C", 2, 1)
	if err != nil {
		t.Fatalf("SetWinnerDirect M2 failed: %v", err)
	}

	// Verify Match 3 is now READY with Team A and Team C!
	ms, _ = repo.GetBracketMatches(ctx)
	if ms[2].Team2ID == nil || *ms[2].Team2ID != teamC.ID {
		t.Fatalf("Expected Team C in Match 3 Team2ID, got: %v", ms[2].Team2ID)
	}
	if ms[2].State != models.BracketOpen {
		t.Fatalf("Expected Match 3 state open, got: %s", ms[2].State)
	}

	// Verify notifications from Step 2:
	// - Team A captain (1001) who was WAITING gets "Ваш следующий соперник определился!" with Team C
	// - Team C captain (1003) who JUST WON gets "Победа... Ваш следующий соперник — Team A"
	// - Team D captain (1004) gets loss notification
	var foundCapAOpponentDetermined, foundCapCOpponentReady, foundCapDLoss bool
	for _, msg := range sentMessages {
		if msg.chatID == 1001 && strings.Contains(msg.text, "Ваш следующий соперник определился") && strings.Contains(msg.text, "Team C") {
			foundCapAOpponentDetermined = true
		}
		if msg.chatID == 1003 && strings.Contains(msg.text, "Победа со счётом 2:1") && strings.Contains(msg.text, "Team A") {
			foundCapCOpponentReady = true
		}
		if msg.chatID == 1004 && strings.Contains(msg.text, "Матч #2 завершён") && strings.Contains(msg.text, "выбывает") {
			foundCapDLoss = true
		}
	}

	if !foundCapAOpponentDetermined {
		t.Errorf("Waiting Team A captain was not notified about opponent determination: %+v", sentMessages)
	}
	if !foundCapCOpponentReady {
		t.Errorf("Winning Team C captain was not notified about next match: %+v", sentMessages)
	}
	if !foundCapDLoss {
		t.Errorf("Team D captain was not notified about loss: %+v", sentMessages)
	}
}
