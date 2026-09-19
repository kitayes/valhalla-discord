package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/models"
)

type mockDeskStore struct {
	state models.MatchDesk
	ctx   models.DeskContext
}

func (s *mockDeskStore) UpdateMatchDesk(_ context.Context, f func(*models.MatchDesk, models.DeskContext) error) error {
	data, _ := json.Marshal(s.state)
	var next models.MatchDesk
	_ = json.Unmarshal(data, &next)
	if err := f(&next, s.ctx); err != nil {
		return err
	}
	s.state = next
	return nil
}

type mockTelegramSvc struct {
	application.TelegramService
	bracket       []models.BracketMatch
	player        *models.TelegramPlayer
	team          *models.TelegramTeam
	teamMems      []models.TelegramPlayer
	activeTourney *models.TelegramTournament
	tourneyTeam   *models.TournamentTeam
	tournaments   []models.TelegramTournament
	// reportedPhotoIDs records what the handler passed down on the last
	// report, so tests can assert Telegram file IDs arrive here rather than
	// the raw base64 the browser sent.
	reportedPhotoIDs []string
	// openReport is what GetOpenReportForMatch answers; decisions record here.
	openReport *models.TelegramMatchReport
	confirmed  []int
	disputed   []int
}

func (m *mockTelegramSvc) GetActiveTournament(ctx context.Context) (*models.TelegramTournament, error) {
	if m.activeTourney != nil {
		return m.activeTourney, nil
	}
	return &models.TelegramTournament{ID: 1, Name: "Этап 1", Status: models.TournamentStatusRegistration, IsActive: true}, nil
}

func (m *mockTelegramSvc) GetTournamentTeamStatus(ctx context.Context, captainTgID int64, tournamentID int) (*models.TournamentTeam, error) {
	return m.tourneyTeam, nil
}

func (m *mockTelegramSvc) GetAllTournaments(ctx context.Context) ([]models.TelegramTournament, error) {
	return m.tournaments, nil
}

func (m *mockTelegramSvc) RegisterTeamForTournament(ctx context.Context, captainTgID int64, tournamentID int) error {
	m.tourneyTeam = &models.TournamentTeam{TournamentID: tournamentID, Status: "registered"}
	return nil
}

func (m *mockTelegramSvc) UnregisterTeamFromTournament(ctx context.Context, captainTgID int64, tournamentID int) error {
	m.tourneyTeam = nil
	return nil
}

func (m *mockTelegramSvc) CreateTournament(ctx context.Context, name, slug string, tTime *time.Time) (*models.TelegramTournament, error) {
	t := models.TelegramTournament{ID: len(m.tournaments) + 1, Name: name, Status: models.TournamentStatusRegistration}
	m.tournaments = append(m.tournaments, t)
	return &t, nil
}

func (m *mockTelegramSvc) SetActiveTournament(ctx context.Context, id int) error {
	for i := range m.tournaments {
		m.tournaments[i].IsActive = (m.tournaments[i].ID == id)
	}
	return nil
}

func (m *mockTelegramSvc) FinishTournament(ctx context.Context, id int) error {
	return nil
}

func (m *mockTelegramSvc) GetBracketForTournament(ctx context.Context, tournamentID int) ([]models.BracketMatch, error) {
	return m.bracket, nil
}

func (m *mockTelegramSvc) GetOpenReportForMatch(context.Context, int) (*models.TelegramMatchReport, error) {
	return m.openReport, nil
}

func (m *mockTelegramSvc) ConfirmReport(_ context.Context, _ int64, id int) error {
	if m.openReport == nil || m.openReport.ID != id {
		return errors.New("отчёт не найден")
	}
	m.confirmed = append(m.confirmed, id)
	m.openReport = nil
	return nil
}

func (m *mockTelegramSvc) DisputeReport(_ context.Context, _ int64, id int) error {
	if m.openReport == nil || m.openReport.ID != id {
		return errors.New("отчёт не найден")
	}
	m.disputed = append(m.disputed, id)
	m.openReport.Status = models.ReportDisputed
	return nil
}

func (m *mockTelegramSvc) GetBracket(ctx context.Context) ([]models.BracketMatch, error) {
	return m.bracket, nil
}

func (m *mockTelegramSvc) GetPlayer(ctx context.Context, tgID int64) (*models.TelegramPlayer, error) {
	return m.player, nil
}

func (m *mockTelegramSvc) GetTeamForPlayer(ctx context.Context, tgID int64) (*models.TelegramTeam, []models.TelegramPlayer, error) {
	return m.team, m.teamMems, nil
}

func (m *mockTelegramSvc) ToggleCheckIn(ctx context.Context, tgID int64) string {
	return "Check-in подтвержден!"
}

func (m *mockTelegramSvc) RegistrationStatus(ctx context.Context) (bool, string) {
	return true, ""
}

func (m *mockTelegramSvc) CreateTeamInApp(ctx context.Context, captainTgID int64, teamName, nick, gameID, zoneID, role string) error {
	m.team = &models.TelegramTeam{ID: 2, Name: teamName}
	m.player = &models.TelegramPlayer{
		TelegramID:   &captainTgID,
		GameNickname: nick,
		GameID:       gameID,
		ZoneID:       zoneID,
		MainRole:     role,
		IsCaptain:    true,
	}
	return nil
}

func (m *mockTelegramSvc) AddTeamPlayer(ctx context.Context, captainTgID int64, nick, gameID, zoneID, role string, isSubstitute bool) (*models.TelegramPlayer, error) {
	p := models.TelegramPlayer{
		ID:           99,
		GameNickname: nick,
		GameID:       gameID,
		ZoneID:       zoneID,
		MainRole:     role,
		IsSubstitute: isSubstitute,
	}
	m.teamMems = append(m.teamMems, p)
	return &p, nil
}

func (m *mockTelegramSvc) UpdateTeamPlayer(ctx context.Context, captainTgID int64, playerID int, nick, gameID, zoneID, role string) error {
	if m.team != nil && m.team.IsCheckedIn {
		return errors.New("редактирование заблокировано: команда уже прошла Check-in")
	}
	return nil
}

func (m *mockTelegramSvc) ReportMatchDirect(ctx context.Context, reporterTgID int64, matchID int, myScore, oppScore int, photoFileIDs []string) (*models.TelegramMatchReport, error) {
	m.reportedPhotoIDs = photoFileIDs
	if myScore == oppScore {
		return nil, errors.New("счёт не может быть равным")
	}
	return &models.TelegramMatchReport{
		ReporterTelegramID: reporterTgID,
		Score:              fmt.Sprintf("%d:%d", myScore, oppScore),
		WinnerTeamName:     "Alpha",
		LoserTeamName:      "Beta",
		PhotoFileIDs:       photoFileIDs,
	}, nil
}

func (m *mockTelegramSvc) GetCheckInSummary(ctx context.Context) (*models.CheckInSummary, error) {
	return &models.CheckInSummary{
		TotalTeams:     2,
		CheckedInCount: 1,
		PendingCount:   1,
		Debtors: []models.DebtorTeam{
			{ID: 2, Name: "Beta", CaptainName: "CapBeta", Status: "pending"},
		},
	}, nil
}

func (m *mockTelegramSvc) DisqualifyUnchecked(ctx context.Context) ([]models.TelegramTeam, error) {
	return []models.TelegramTeam{{ID: 2, Name: "Beta"}}, nil
}

func (m *mockTelegramSvc) GetTournamentTime(ctx context.Context) time.Time {
	return time.Time{}
}

func (m *mockTelegramSvc) RollbackMatch(ctx context.Context, matchID int) error {
	return nil
}

func (m *mockTelegramSvc) DeleteTeamInApp(_ context.Context, tgID int64) error {
	if m.player == nil || !m.player.IsCaptain {
		return errors.New("только капитан команды может удалить команду")
	}
	m.team = nil
	m.teamMems = nil
	m.player.TeamID = nil
	m.player.IsCaptain = false
	return nil
}

func (m *mockTelegramSvc) LeaveTeam(_ context.Context, tgID int64) error {
	if m.player != nil && m.player.IsCaptain {
		return errors.New("капитан не может покинуть команду")
	}
	if m.player != nil {
		m.player.TeamID = nil
	}
	return nil
}

func (m *mockTelegramSvc) ChangeWinnerDirect(ctx context.Context, matchID int, winnerTeamName string, winScore, loseScore int) error {
	return nil
}

func TestTMAHandlers(t *testing.T) {
	tgSvc := &mockTelegramSvc{
		bracket: []models.BracketMatch{
			{ID: 1, Round: 1, PlayOrder: 1, Team1Name: "Alpha", Team2Name: "Beta", State: models.BracketOpen},
		},
		player: &models.TelegramPlayer{
			ID:           10,
			GameNickname: "CapAlpha",
			GameID:       "12345",
			IsCaptain:    true,
		},
		team: &models.TelegramTeam{
			ID:          1,
			Name:        "Alpha",
			IsCheckedIn: true,
		},
	}

	services := &application.Service{
		TelegramService: tgSvc,
	}

	server, err := NewAdminServer(services, &captureLogger{}, "8080", "testkey", nil, "my_bot_token")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	server.WithAdminIDs([]int64{99999})
	server.WithDebtorNotifier(func(ctx context.Context, adminChatID int64, msg string) error {
		return nil
	})
	// Stand in for Telegram: echo back a file ID shaped like the real ones so
	// tests can tell an uploaded photo from the base64 that came in.
	var uploadedNames []string
	server.WithPhotoUploader(func(ctx context.Context, data []byte, filename string) (string, error) {
		uploadedNames = append(uploadedNames, filename)
		return fmt.Sprintf("tg-file-%d", len(uploadedNames)), nil
	})

	t.Run("GET /app renders template", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/app", nil)
		rec := httptest.NewRecorder()

		server.handleApp(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if len(body) == 0 || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("expected html response")
		}
	})

	t.Run("GET /api/bracket returns matches", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/bracket", nil)
		rec := httptest.NewRecorder()

		server.handleBracket(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp BracketResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Matches) != 1 || resp.Matches[0].Team1Name != "Alpha" {
			t.Errorf("unexpected matches: %+v", resp.Matches)
		}
	})

	t.Run("GET /api/me with authenticated context", func(t *testing.T) {
		user := &TelegramUser{ID: 12345, FirstName: "Alex", Username: "alex_cap"}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		req := httptest.NewRequest(http.MethodGet, "/api/me", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleMe(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp MeResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.User == nil || resp.User.ID != 12345 {
			t.Errorf("user ID mismatch: %+v", resp.User)
		}
		if resp.Team == nil || resp.Team.Name != "Alpha" {
			t.Errorf("team mismatch: %+v", resp.Team)
		}
		if resp.IsAdmin {
			t.Errorf("expected regular user to not be admin")
		}
	})

	t.Run("GET /api/me with admin user", func(t *testing.T) {
		adminUser := &TelegramUser{ID: 99999, FirstName: "Boss", Username: "admin_boss"}
		ctx := context.WithValue(context.Background(), userCtxKey, adminUser)

		req := httptest.NewRequest(http.MethodGet, "/api/me", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleMe(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp MeResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !resp.IsAdmin {
			t.Errorf("expected user 99999 to be admin")
		}
	})

	t.Run("POST /api/team/checkin toggles status", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		req := httptest.NewRequest(http.MethodPost, "/api/team/checkin", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleCheckIn(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if result["ok"] != true {
			t.Errorf("expected ok: true, got %+v", result)
		}
	})

	t.Run("PUT /api/team/player rejected when checked in", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := `{"player_id": 10, "nickname": "UpdatedNick", "game_id": "999", "zone_id": "1001", "role": "Mid"}`
		req := httptest.NewRequest(http.MethodPut, "/api/team/player", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleUpdateTeamPlayer(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when team is checked in, got %d", rec.Code)
		}
	})

	t.Run("PUT /api/team/player allowed when not checked in", func(t *testing.T) {
		tgSvc.team.IsCheckedIn = false
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := `{"player_id": 10, "nickname": "UpdatedNick", "game_id": "999", "zone_id": "1001", "role": "Mid"}`
		req := httptest.NewRequest(http.MethodPut, "/api/team/player", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleUpdateTeamPlayer(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 when team is not checked in, got %d", rec.Code)
		}
	})

	t.Run("POST /api/team/create with captain fields", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := `{"name": "Team Omega", "game_nickname": "OmegaCap", "game_id": "778899", "zone_id": "1001", "role": "Exp"}`
		req := httptest.NewRequest(http.MethodPost, "/api/team/create", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleCreateTeam(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if tgSvc.team == nil || tgSvc.team.Name != "Team Omega" {
			t.Fatalf("expected team Team Omega, got %+v", tgSvc.team)
		}
		if tgSvc.player == nil || tgSvc.player.GameNickname != "OmegaCap" || tgSvc.player.GameID != "778899" {
			t.Fatalf("expected captain data saved, got %+v", tgSvc.player)
		}
	})

	t.Run("POST /api/team/player/add adds teammate", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := `{"nickname": "OmegaMate", "game_id": "112233", "zone_id": "1001", "role": "Gold", "is_substitute": false}`
		req := httptest.NewRequest(http.MethodPost, "/api/team/player/add", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleAddTeamPlayer(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp["ok"] != true {
			t.Fatalf("expected ok: true, got %s", rec.Body.String())
		}
	})

	t.Run("POST /api/team/delete deletes team", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		tgSvc.player = &models.TelegramPlayer{
			TelegramID: &user.ID,
			IsCaptain:  true,
		}
		tgSvc.team = &models.TelegramTeam{ID: 1, Name: "TestTeam"}

		req := httptest.NewRequest(http.MethodPost, "/api/team/delete", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleDeleteTeam(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if tgSvc.team != nil {
			t.Fatalf("expected team deleted, but team is still set")
		}
	})

	t.Run("POST /api/team/leave leaves team", func(t *testing.T) {
		user := &TelegramUser{ID: 99999}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		teamID := 1
		tgSvc.player = &models.TelegramPlayer{
			TelegramID: &user.ID,
			TeamID:     &teamID,
			IsCaptain:  false,
		}

		req := httptest.NewRequest(http.MethodPost, "/api/team/leave", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleLeaveTeam(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if tgSvc.player.TeamID != nil {
			t.Fatalf("expected team_id nil, got %v", tgSvc.player.TeamID)
		}
	})

	t.Run("POST /api/match/report submits score", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := `{"match_id": 1, "my_score": 2, "opp_score": 0, "screenshot": "` + pngDataURL(32) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/match/report", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleMatchReport(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST /api/match/report carries every screenshot", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := fmt.Sprintf(`{"match_id": 7, "my_score": 2, "opp_score": 1, "screenshots": [%q, %q, %q]}`,
			pngDataURL(32), pngDataURL(64), pngDataURL(16))
		req := httptest.NewRequest(http.MethodPost, "/api/match/report", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		tgSvc.reportedPhotoIDs = nil
		server.handleMatchReport(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if len(tgSvc.reportedPhotoIDs) != 3 {
			t.Fatalf("service received %d photo IDs, want 3: %v", len(tgSvc.reportedPhotoIDs), tgSvc.reportedPhotoIDs)
		}
		// The whole point of the change: what reaches the service must be
		// Telegram file IDs, because base64 sent as a FileID is rejected by
		// the API and the referee silently gets nothing.
		for _, id := range tgSvc.reportedPhotoIDs {
			if strings.HasPrefix(id, "data:") {
				t.Errorf("raw data URL reached the service: %q", id)
			}
		}
	})

	t.Run("POST /api/match/report rejects a report with no proof", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := `{"match_id": 1, "my_score": 2, "opp_score": 0}`
		req := httptest.NewRequest(http.MethodPost, "/api/match/report", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleMatchReport(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 without screenshots, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST /api/match/report enforces the screenshot cap", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		// The browser limits this too, but a client is not a place to enforce
		// anything: the cap has to hold when the request is crafted by hand.
		urls := make([]string, maxReportPhotos+1)
		for i := range urls {
			urls[i] = pngDataURL(16)
		}
		payload, err := json.Marshal(map[string]interface{}{
			"match_id": 1, "my_score": 2, "opp_score": 0, "screenshots": urls,
		})
		if err != nil {
			t.Fatalf("failed to build payload: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/match/report", bytes.NewReader(payload)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleMatchReport(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 above the cap, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST /api/match/report does not record a score it cannot prove", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		// Telegram being unreachable is a tournament-day reality. Saving the
		// result anyway would advance the bracket while the referee is left
		// with a disputed match and no screenshots to judge it by.
		server.WithPhotoUploader(func(ctx context.Context, data []byte, filename string) (string, error) {
			return "", errors.New("telegram is down")
		})
		defer server.WithPhotoUploader(func(ctx context.Context, data []byte, filename string) (string, error) {
			uploadedNames = append(uploadedNames, filename)
			return fmt.Sprintf("tg-file-%d", len(uploadedNames)), nil
		})

		body := `{"match_id": 3, "my_score": 2, "opp_score": 0, "screenshots": ["` + pngDataURL(32) + `"]}`
		req := httptest.NewRequest(http.MethodPost, "/api/match/report", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		tgSvc.reportedPhotoIDs = nil
		server.handleMatchReport(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502 when the upload fails, got %d: %s", rec.Code, rec.Body.String())
		}
		if tgSvc.reportedPhotoIDs != nil {
			t.Error("the result was recorded even though its screenshots never uploaded")
		}
	})

	t.Run("GET /api/admin/desk forbidden for regular user", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		req := httptest.NewRequest(http.MethodGet, "/api/admin/desk", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleAdminDesk(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 forbidden, got %d", rec.Code)
		}
	})

	t.Run("GET /api/admin/desk allowed for admin", func(t *testing.T) {
		adminUser := &TelegramUser{ID: 99999}
		ctx := context.WithValue(context.Background(), userCtxKey, adminUser)

		req := httptest.NewRequest(http.MethodGet, "/api/admin/desk", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleAdminDesk(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp AdminDeskResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.CheckIn == nil || resp.CheckIn.TotalTeams != 2 {
			t.Errorf("unexpected checkin in desk: %+v", resp.CheckIn)
		}
	})

	t.Run("POST /api/admin/ping_debtors works for admin", func(t *testing.T) {
		adminUser := &TelegramUser{ID: 99999}
		ctx := context.WithValue(context.Background(), userCtxKey, adminUser)

		body := `{"message": "Please check in!"}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/ping_debtors", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleAdminPingDebtors(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("POST /api/admin/disqualify_uncheck works for admin", func(t *testing.T) {
		adminUser := &TelegramUser{ID: 99999}
		ctx := context.WithValue(context.Background(), userCtxKey, adminUser)

		req := httptest.NewRequest(http.MethodPost, "/api/admin/disqualify_uncheck", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleAdminDisqualifyUncheck(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("POST /api/admin/match_action rollback works for admin", func(t *testing.T) {
		adminUser := &TelegramUser{ID: 99999}
		ctx := context.WithValue(context.Background(), userCtxKey, adminUser)

		body := `{"action": "rollback", "match_id": 1}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/match_action", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleAdminMatchAction(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if result["ok"] != true {
			t.Errorf("expected ok: true, got %+v", result)
		}
	})

	t.Run("POST /api/admin/match_action change_winner works for admin", func(t *testing.T) {
		adminUser := &TelegramUser{ID: 99999}
		ctx := context.WithValue(context.Background(), userCtxKey, adminUser)

		body := `{"action": "change_winner", "match_id": 1, "winner_team_name": "Beta", "score": "2:1"}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/match_action", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleAdminMatchAction(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if result["ok"] != true {
			t.Errorf("expected ok: true, got %+v", result)
		}
	})

	t.Run("GET /api/match/active returns active match with status playing", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		uid := user.ID
		team1 := 1
		team2 := 2
		deskStore := &mockDeskStore{
			state: models.MatchDesk{
				Matches: map[int]*models.DeskMatch{
					1: {
						ID:         1,
						Number:     1,
						Active:     true,
						Teams:      [2]int{1, 2},
						Names:      [2]string{"Alpha", "Beta"},
						Generation: 1,
					},
				},
			},
			ctx: models.DeskContext{
				Matches: []models.BracketMatch{
					{
						ID:        1,
						Round:     1,
						PlayOrder: 1,
						Team1ID:   &team1,
						Team2ID:   &team2,
						Team1Name: "Alpha",
						Team2Name: "Beta",
						State:     models.BracketOpen,
					},
				},
				Captains: map[int]models.TelegramPlayer{
					1: {TelegramID: &uid, IsCaptain: true},
				},
			},
		}
		services.MatchDesk = application.NewMatchDeskService(deskStore, []int64{99999}, time.UTC)
		t.Cleanup(func() { services.MatchDesk = nil })

		req := httptest.NewRequest(http.MethodGet, "/api/match/active", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleActiveMatch(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp ActiveMatchResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !resp.HasMatch {
			t.Errorf("expected HasMatch: true")
		}
		if resp.TournamentStatus == nil || resp.TournamentStatus.Status != "playing" {
			t.Errorf("expected TournamentStatus.Status: playing, got %+v", resp.TournamentStatus)
		}
		if resp.PendingResult != nil {
			t.Errorf("no report filed, yet pending_result = %+v", resp.PendingResult)
		}

		// A report filed by the opposing captain shows up for this captain to
		// act on, with the window still counting down.
		teamID := 1
		tgSvc.player.TeamID = &teamID
		exp := time.Now().Add(4 * time.Minute)
		bmID := 1
		tgSvc.openReport = &models.TelegramMatchReport{
			ID: 7, ReporterTelegramID: 777, WinnerTeamID: 2, LoserTeamID: 1,
			WinnerTeamName: "Beta", LoserTeamName: "Alpha", Score: "2:0",
			PhotoFileIDs: []string{"a", "b"}, BracketMatchID: &bmID,
			Status: models.ReportPending, ExpiresAt: &exp,
		}
		t.Cleanup(func() { tgSvc.openReport = nil; tgSvc.player.TeamID = nil })

		rec = httptest.NewRecorder()
		server.handleActiveMatch(rec, httptest.NewRequest(http.MethodGet, "/api/match/active", nil).WithContext(ctx))
		resp = ActiveMatchResponse{}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		pr := resp.PendingResult
		if pr == nil {
			t.Fatalf("pending_result missing")
		}
		if pr.ReportID != 7 || pr.Score != "2:0" || pr.ScreenshotsCount != 2 || pr.Status != models.ReportPending {
			t.Errorf("pending_result = %+v", pr)
		}
		// GetPlayer in the mock returns the viewer's own player row for any id,
		// so the reporter resolves to the viewer's team: reported_by_me is true
		// here and exercises that branch; i_won reflects the winner side.
		if !pr.ReportedByMe || pr.IWon {
			t.Errorf("sides wrong: %+v", pr)
		}
		if pr.ExpiresInSeconds < 200 || pr.ExpiresInSeconds > 240 {
			t.Errorf("expires_in_seconds = %d", pr.ExpiresInSeconds)
		}
	})

	t.Run("POST /api/match/result/confirm and dispute", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)
		post := func(path string, body string) (int, map[string]interface{}) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
			rec := httptest.NewRecorder()
			if strings.HasSuffix(path, "confirm") {
				server.handleReportConfirm(rec, req)
			} else {
				server.handleReportDispute(rec, req)
			}
			var out map[string]interface{}
			_ = json.NewDecoder(rec.Body).Decode(&out)
			return rec.Code, out
		}

		if code, _ := post("/api/match/result/confirm", `{"report_id":0}`); code != http.StatusBadRequest {
			t.Errorf("empty id: %d", code)
		}
		tgSvc.openReport = &models.TelegramMatchReport{ID: 7, Status: models.ReportPending}
		if code, out := post("/api/match/result/confirm", `{"report_id":8}`); code != http.StatusBadRequest || out["ok"] != false {
			t.Errorf("wrong id accepted: %d %+v", code, out)
		}
		if code, out := post("/api/match/result/confirm", `{"report_id":7}`); code != http.StatusOK || out["ok"] != true {
			t.Errorf("confirm: %d %+v", code, out)
		}
		if len(tgSvc.confirmed) != 1 || tgSvc.confirmed[0] != 7 {
			t.Errorf("confirmed = %v", tgSvc.confirmed)
		}

		tgSvc.openReport = &models.TelegramMatchReport{ID: 9, Status: models.ReportPending}
		if code, out := post("/api/match/result/dispute", `{"report_id":9}`); code != http.StatusOK || out["ok"] != true {
			t.Errorf("dispute: %d %+v", code, out)
		}
		if len(tgSvc.disputed) != 1 || tgSvc.disputed[0] != 9 {
			t.Errorf("disputed = %v", tgSvc.disputed)
		}
		tgSvc.openReport = nil
	})

	t.Run("GET /api/match/active returns eliminated when team lost match", func(t *testing.T) {
		// Mock team 2 (Beta) losing to team 1 (Alpha)
		tgSvc.team = &models.TelegramTeam{ID: 2, Name: "Beta", Status: "active"}
		winnerID := 1
		tgSvc.bracket = []models.BracketMatch{
			{
				ID:        1,
				Round:     1,
				PlayOrder: 1,
				Team1ID:   &winnerID,
				Team2ID:   &tgSvc.team.ID,
				Team1Name: "Alpha",
				Team2Name: "Beta",
				WinnerID:  &winnerID,
				State:     models.BracketComplete,
				ScoresCSV: "2-0",
			},
		}

		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		req := httptest.NewRequest(http.MethodGet, "/api/match/active", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleActiveMatch(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp ActiveMatchResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.HasMatch {
			t.Errorf("expected HasMatch: false for completed match")
		}
		if resp.TournamentStatus == nil || resp.TournamentStatus.Status != "eliminated" {
			t.Errorf("expected TournamentStatus.Status: eliminated, got %+v", resp.TournamentStatus)
		}
		if resp.TournamentStatus.OpponentName != "Alpha" || resp.TournamentStatus.Score != "2-0" {
			t.Errorf("unexpected opponent or score: %+v", resp.TournamentStatus)
		}
	})

	t.Run("GET /api/match/active returns advanced when team won match", func(t *testing.T) {
		// Mock team 1 (Alpha) winning match #1
		tgSvc.team = &models.TelegramTeam{ID: 1, Name: "Alpha", Status: "active"}
		winnerID := 1
		team2ID := 2
		tgSvc.bracket = []models.BracketMatch{
			{
				ID:        1,
				Round:     1,
				PlayOrder: 1,
				Team1ID:   &tgSvc.team.ID,
				Team2ID:   &team2ID,
				Team1Name: "Alpha",
				Team2Name: "Beta",
				WinnerID:  &winnerID,
				State:     models.BracketComplete,
				ScoresCSV: "2-0",
			},
			{
				ID:        2,
				Round:     1,
				PlayOrder: 2,
				Team1Name: "Gamma",
				Team2Name: "Delta",
				State:     models.BracketOpen,
			},
			{
				ID:        3,
				Round:     2,
				PlayOrder: 3,
				State:     models.BracketPending,
			},
		}

		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		req := httptest.NewRequest(http.MethodGet, "/api/match/active", nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleActiveMatch(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var resp ActiveMatchResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.HasMatch {
			t.Errorf("expected HasMatch: false for completed match with no open desk")
		}
		if resp.TournamentStatus == nil || resp.TournamentStatus.Status != "advanced" {
			t.Errorf("expected TournamentStatus.Status: advanced, got %+v", resp.TournamentStatus)
		}
		if !resp.TournamentStatus.WaitingForOpponent {
			t.Errorf("expected WaitingForOpponent: true")
		}
		if resp.TournamentStatus.WaitingOpponents != "Gamma vs Delta" {
			t.Errorf("expected WaitingOpponents: Gamma vs Delta, got %q", resp.TournamentStatus.WaitingOpponents)
		}
	})

	t.Run("handleBracket includes challonge_url", func(t *testing.T) {
		tgSvc.activeTourney = &models.TelegramTournament{
			ID:           1,
			Name:         "Cup 1",
			Status:       models.TournamentStatusActive,
			ChallongeURL: "https://challonge.com/cup_1",
			IsActive:     true,
		}
		req := httptest.NewRequest(http.MethodGet, "/api/bracket", nil)
		rec := httptest.NewRecorder()
		server.handleBracket(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp BracketResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ChallongeURL != "https://challonge.com/cup_1" {
			t.Errorf("expected challonge_url: https://challonge.com/cup_1, got %q", resp.ChallongeURL)
		}
	})

	t.Run("handleAdminTournamentStart", func(t *testing.T) {
		var bracketBuilderCalled bool
		server.WithBracketBuilder(func(ctx context.Context, adminChatID int64) error {
			bracketBuilderCalled = true
			return nil
		})

		// Non-admin forbidden
		nonAdminCtx := context.WithValue(context.Background(), userCtxKey, &TelegramUser{ID: 11111})
		req := httptest.NewRequest(http.MethodPost, "/api/admin/tournament/start", nil).WithContext(nonAdminCtx)
		rec := httptest.NewRecorder()
		server.handleAdminTournamentStart(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}

		// Admin starts tournament
		adminCtx := context.WithValue(context.Background(), userCtxKey, &TelegramUser{ID: 99999})
		req = httptest.NewRequest(http.MethodPost, "/api/admin/tournament/start", nil).WithContext(adminCtx)
		rec = httptest.NewRecorder()
		server.handleAdminTournamentStart(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if !bracketBuilderCalled {
			t.Errorf("expected bracketBuilder to be called")
		}
	})
}

type captureLogger struct{}

func (l *captureLogger) Info(msg string, args ...interface{})  {}
func (l *captureLogger) Error(msg string, args ...interface{}) {}
func (l *captureLogger) Debug(msg string, args ...interface{}) {}
func (l *captureLogger) Warn(msg string, args ...interface{})  { _ = fmt.Sprintf(msg, args...) }
