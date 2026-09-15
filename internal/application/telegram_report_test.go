package application

import (
	"blackwatch/internal/models"
	"context"
	"strings"
	"testing"
)

func TestReportRefusedWithoutTeam(t *testing.T) {
	svc, repo := newTelegramSvc()
	repo.addPlayer(100, nil, false, models.StateIdle)

	resp, kb := svc.StartReport(context.Background(), 100)
	if !strings.Contains(resp, "не состоите в команде") || kb != KbNone {
		t.Errorf("StartReport without team = %q, %q, want team refusal", resp, kb)
	}

	teamID := 1
	repo.teams[teamID] = &models.TelegramTeam{ID: teamID, Name: "Navi", Status: models.TeamStatusActive}
	repo.addPlayer(101, &teamID, false, models.StateIdle)

	resp, kb = svc.StartReport(context.Background(), 101)
	if !strings.Contains(resp, "Только капитан") || kb != KbNone {
		t.Errorf("StartReport as teammate = %q, %q, want captain refusal", resp, kb)
	}
}

func TestReportOpponentSelection(t *testing.T) {
	svc, repo := newTelegramSvc()
	t1 := 1
	t2 := 2
	repo.teams[t1] = &models.TelegramTeam{ID: t1, Name: "Navi", Status: models.TeamStatusActive}
	repo.teams[t2] = &models.TelegramTeam{ID: t2, Name: "VirtusPro", Status: models.TeamStatusActive}

	c1 := repo.addPlayer(101, &t1, true, models.StateIdle)

	resp, kb := svc.StartReport(context.Background(), 101)
	if !strings.Contains(resp, "Выберите команду соперника") || kb != KbReportOpponent {
		t.Fatalf("StartReport = %q, %q, want opponent selection", resp, kb)
	}

	if c1.FSMState != models.StateReportOpponent {
		t.Errorf("FSMState = %q, want %q", c1.FSMState, models.StateReportOpponent)
	}

	opponents, err := svc.GetEligibleOpponents(context.Background(), 101)
	if err != nil || len(opponents) != 1 || opponents[0].ID != t2 {
		t.Fatalf("GetEligibleOpponents = %+v, want only VirtusPro", opponents)
	}

	// Select own team should fail
	resp, _ = svc.SelectReportOpponent(context.Background(), 101, t1)
	if !strings.Contains(resp, "Нельзя выбрать свою команду") {
		t.Errorf("SelectReportOpponent(own) = %q, want refusal", resp)
	}

	// Select valid opponent
	resp, kb = svc.SelectReportOpponent(context.Background(), 101, t2)
	if !strings.Contains(resp, "Navi vs VirtusPro") || kb != KbReportScore {
		t.Fatalf("SelectReportOpponent(valid) = %q, %q, want score prompt", resp, kb)
	}

	if c1.FSMState != models.StateReportScore {
		t.Errorf("FSMState = %q, want %q", c1.FSMState, models.StateReportScore)
	}
}

func TestReportScoreValidation(t *testing.T) {
	svc, repo := newTelegramSvc()
	t1 := 1
	t2 := 2
	repo.teams[t1] = &models.TelegramTeam{ID: t1, Name: "Navi", Status: models.TeamStatusActive}
	repo.teams[t2] = &models.TelegramTeam{ID: t2, Name: "VirtusPro", Status: models.TeamStatusActive}
	c1 := repo.addPlayer(101, &t1, true, models.StateIdle)

	svc.StartReport(context.Background(), 101)
	svc.SelectReportOpponent(context.Background(), 101, t2)

	// 1. Malformed score
	resp, kb := svc.SetReportScore(context.Background(), 101, "abc")
	if !strings.Contains(resp, "Некорректный формат счета") || kb != KbReportScore {
		t.Errorf("SetReportScore(abc) = %q, %q, want invalid format", resp, kb)
	}

	// 2. Losing score (0:2 or 1:2)
	resp, kb = svc.SetReportScore(context.Background(), 101, "1:2")
	if !strings.Contains(resp, "отчет отправляет команда-победитель") || kb != KbReportScore {
		t.Errorf("SetReportScore(1:2) = %q, %q, want victory refusal", resp, kb)
	}

	// 3. Draw score (1:1)
	resp, kb = svc.SetReportScore(context.Background(), 101, "1:1")
	if !strings.Contains(resp, "отчет отправляет команда-победитель") || kb != KbReportScore {
		t.Errorf("SetReportScore(1:1) = %q, %q, want victory refusal", resp, kb)
	}

	// 4. Winning score (2:0)
	resp, kb = svc.SetReportScore(context.Background(), 101, "2:0")
	if !strings.Contains(resp, "Отправьте скриншоты победы") || kb != KbReportPhotos+":0" {
		t.Fatalf("SetReportScore(2:0) = %q, %q, want screenshots prompt", resp, kb)
	}

	if c1.FSMState != models.StateReportScreenshots {
		t.Errorf("FSMState = %q, want %q", c1.FSMState, models.StateReportScreenshots)
	}
}

func TestReportMultipleScreenshotsAccumulation(t *testing.T) {
	svc, repo := newTelegramSvc()
	t1 := 1
	t2 := 2
	repo.teams[t1] = &models.TelegramTeam{ID: t1, Name: "Navi", Status: models.TeamStatusActive}
	repo.teams[t2] = &models.TelegramTeam{ID: t2, Name: "VirtusPro", Status: models.TeamStatusActive}
	repo.addPlayer(101, &t1, true, models.StateIdle)

	svc.StartReport(context.Background(), 101)
	svc.SelectReportOpponent(context.Background(), 101, t2)
	svc.SetReportScore(context.Background(), 101, "2:1")

	// Add photo 1
	resp, kb, count := svc.AddReportPhoto(context.Background(), 101, "photo_file_1")
	if count != 1 || kb != KbReportPhotos+":1" || !strings.Contains(resp, "Всего загружено: 1") {
		t.Errorf("photo 1: count=%d, kb=%q, resp=%q", count, kb, resp)
	}

	// Add photo 2
	resp, kb, count = svc.AddReportPhoto(context.Background(), 101, "photo_file_2")
	if count != 2 || kb != KbReportPhotos+":2" || !strings.Contains(resp, "Всего загружено: 2") {
		t.Errorf("photo 2: count=%d, kb=%q, resp=%q", count, kb, resp)
	}

	// Add photo 3
	resp, kb, count = svc.AddReportPhoto(context.Background(), 101, "photo_file_3")
	if count != 3 || kb != KbReportPhotos+":3" || !strings.Contains(resp, "Всего загружено: 3") {
		t.Errorf("photo 3: count=%d, kb=%q, resp=%q", count, kb, resp)
	}

	draft := svc.GetReportDraft(101)
	if len(draft.PhotoFileIDs) != 3 {
		t.Errorf("draft photos = %d, want 3", len(draft.PhotoFileIDs))
	}

	// Reset photos
	resp, kb = svc.ResetReportPhotos(context.Background(), 101)
	if kb != KbReportPhotos+":0" || !strings.Contains(resp, "Скриншоты сброшены") {
		t.Errorf("ResetReportPhotos: kb=%q, resp=%q", kb, resp)
	}

	if len(draft.PhotoFileIDs) != 0 {
		t.Errorf("after reset: draft photos = %d, want 0", len(draft.PhotoFileIDs))
	}
}

func TestReportSubmitAndCancel(t *testing.T) {
	svc, repo := newTelegramSvc()
	t1 := 1
	t2 := 2
	repo.teams[t1] = &models.TelegramTeam{ID: t1, Name: "Navi", Status: models.TeamStatusActive}
	repo.teams[t2] = &models.TelegramTeam{ID: t2, Name: "VirtusPro", Status: models.TeamStatusActive}
	c1 := repo.addPlayer(101, &t1, true, models.StateIdle)

	svc.StartReport(context.Background(), 101)
	svc.SelectReportOpponent(context.Background(), 101, t2)
	svc.SetReportScore(context.Background(), 101, "2:0")

	// Submit without photos fails
	resp, kb, rep := svc.SubmitReport(context.Background(), 101)
	if rep != nil || !strings.Contains(resp, "хотя бы один скриншот") {
		t.Errorf("SubmitReport without photos = %v, %q, %q", rep, resp, kb)
	}

	// Add 2 photos
	svc.AddReportPhoto(context.Background(), 101, "photo_A")
	svc.AddReportPhoto(context.Background(), 101, "photo_B")

	// Submit with photos succeeds
	resp, kb, rep = svc.SubmitReport(context.Background(), 101)
	if rep == nil || !strings.Contains(resp, "успешно отправлен") || kb != "main_menu" {
		t.Fatalf("SubmitReport with photos failed: rep=%v, resp=%q, kb=%q", rep, resp, kb)
	}

	if rep.WinnerTeamID != t1 || rep.LoserTeamID != t2 || rep.Score != "2:0" || len(rep.PhotoFileIDs) != 2 {
		t.Errorf("saved report = %+v, want valid fields", rep)
	}

	if c1.FSMState != models.StateIdle {
		t.Errorf("FSMState after submit = %q, want idle", c1.FSMState)
	}

	if svc.GetReportDraft(101) != nil {
		t.Errorf("draft still exists after submit")
	}

	// Test Cancel flow
	svc.StartReport(context.Background(), 101)
	resp, kb = svc.CancelReport(context.Background(), 101)
	if !strings.Contains(resp, "Отчет отменен") || kb != "main_menu" {
		t.Errorf("CancelReport = %q, %q", resp, kb)
	}
	if svc.GetReportDraft(101) != nil {
		t.Errorf("draft still exists after cancel")
	}
}
