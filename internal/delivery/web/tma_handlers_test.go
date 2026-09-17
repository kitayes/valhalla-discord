package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blackwatch/internal/application"
	"blackwatch/internal/models"
)

type mockTelegramSvc struct {
	application.TelegramService
	bracket  []models.BracketMatch
	player   *models.TelegramPlayer
	team     *models.TelegramTeam
	teamMems []models.TelegramPlayer
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

func (m *mockTelegramSvc) UpdateTeamPlayer(ctx context.Context, captainTgID int64, playerID int, nick, gameID, zoneID, role string) error {
	if m.team != nil && m.team.IsCheckedIn {
		return errors.New("редактирование заблокировано: команда уже прошла Check-in")
	}
	return nil
}

func (m *mockTelegramSvc) ReportMatchDirect(ctx context.Context, reporterTgID int64, matchID int, myScore, oppScore int, screenshotData string) (*models.TelegramMatchReport, error) {
	if myScore == oppScore {
		return nil, errors.New("счёт не может быть равным")
	}
	return &models.TelegramMatchReport{
		ReporterTelegramID: reporterTgID,
		Score:              fmt.Sprintf("%d:%d", myScore, oppScore),
		WinnerTeamName:     "Alpha",
		LoserTeamName:      "Beta",
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

	t.Run("POST /api/match/report submits score", func(t *testing.T) {
		user := &TelegramUser{ID: 12345}
		ctx := context.WithValue(context.Background(), userCtxKey, user)

		body := `{"match_id": 1, "my_score": 2, "opp_score": 0, "screenshot": "data:image/png;base64,mock"}`
		req := httptest.NewRequest(http.MethodPost, "/api/match/report", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()

		server.handleMatchReport(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
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
}

type captureLogger struct{}

func (l *captureLogger) Info(msg string, args ...interface{})  {}
func (l *captureLogger) Error(msg string, args ...interface{}) {}
func (l *captureLogger) Debug(msg string, args ...interface{}) {}
func (l *captureLogger) Warn(msg string, args ...interface{})  { _ = fmt.Sprintf(msg, args...) }
