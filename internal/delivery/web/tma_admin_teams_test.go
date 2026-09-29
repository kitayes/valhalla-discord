package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blackwatch/internal/application"
	"blackwatch/internal/models"
)

type adminTeamsMock struct {
	application.TelegramService
	teams     []models.TelegramTeam
	kickedID  int
	kickErr   error
	lastTeam  int
	lastAct   string
	deletedID int
}

func (m *adminTeamsMock) AdminListTeams(ctx context.Context, tournamentID int) ([]models.TelegramTeam, error) {
	return m.teams, nil
}

func (m *adminTeamsMock) AdminKickPlayer(ctx context.Context, playerID int) error {
	m.kickedID = playerID
	return m.kickErr
}

func (m *adminTeamsMock) AdminTeamAction(ctx context.Context, teamID int, action string) (string, error) {
	m.lastTeam, m.lastAct = teamID, action
	return "ok", nil
}

func (m *adminTeamsMock) AdminDeleteTeamByID(ctx context.Context, teamID int) error {
	m.deletedID = teamID
	return nil
}

func newAdminTeamsServer(t *testing.T, mock *adminTeamsMock) *AdminServer {
	t.Helper()
	server, err := NewAdminServer(&application.Service{TelegramService: mock}, &captureLogger{}, "8080", "testkey", nil, "my_bot_token")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	server.WithAdminIDs([]int64{99999})
	return server
}

func asUser(id int64, method, path, body string) *http.Request {
	ctx := context.WithValue(context.Background(), userCtxKey, &TelegramUser{ID: id})
	return httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
}

func TestAdminTeams(t *testing.T) {
	mock := &adminTeamsMock{teams: []models.TelegramTeam{{ID: 1, Name: "Alpha"}}}
	server := newAdminTeamsServer(t, mock)

	t.Run("list forbidden for regular user", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.handleAdminTeams(rec, asUser(1, http.MethodGet, "/api/admin/teams", ""))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("list returns teams with non-null players", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.handleAdminTeams(rec, asUser(99999, http.MethodGet, "/api/admin/teams", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Teams []struct {
				Name    string        `json:"name"`
				Players []interface{} `json:"players"`
			} `json:"teams"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Teams) != 1 || resp.Teams[0].Name != "Alpha" || resp.Teams[0].Players == nil {
			t.Errorf("unexpected teams: %+v", resp.Teams)
		}
	})

	t.Run("kick forbidden for regular user and does nothing", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.handleAdminTeamPlayerKick(rec, asUser(1, http.MethodPost, "/api/admin/team/kick", `{"player_id":7}`))
		if rec.Code != http.StatusForbidden || mock.kickedID != 0 {
			t.Fatalf("expected 403 and no kick, got %d kicked=%d", rec.Code, mock.kickedID)
		}
	})

	t.Run("kick error is reported", func(t *testing.T) {
		mock.kickErr = errors.New("нельзя исключить капитана")
		defer func() { mock.kickErr = nil }()
		rec := httptest.NewRecorder()
		server.handleAdminTeamPlayerKick(rec, asUser(99999, http.MethodPost, "/api/admin/team/kick", `{"player_id":7}`))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "капитан") {
			t.Fatalf("expected 400 with reason, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("team action reaches the service", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.handleAdminTeamAction(rec, asUser(99999, http.MethodPost, "/api/admin/team/action", `{"team_id":1,"action":"checkin"}`))
		if rec.Code != http.StatusOK || mock.lastTeam != 1 || mock.lastAct != "checkin" {
			t.Fatalf("got %d team=%d act=%q", rec.Code, mock.lastTeam, mock.lastAct)
		}
	})

	t.Run("delete reaches the service", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.handleAdminTeamDelete(rec, asUser(99999, http.MethodPost, "/api/admin/team/delete", `{"team_id":1}`))
		if rec.Code != http.StatusOK || mock.deletedID != 1 {
			t.Fatalf("got %d deleted=%d", rec.Code, mock.deletedID)
		}
	})
}
