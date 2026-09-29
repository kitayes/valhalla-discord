package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"blackwatch/internal/application"
	"blackwatch/internal/models"
)

// Referee panel: registered teams and their rosters, with admin overrides.

// adminGate checks method, session and admin rights, writing the error
// response itself. It reports whether the handler may proceed.
func (s *AdminServer) adminGate(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return false
	}
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	if !s.isAdmin(user.ID) || s.services.TelegramService == nil {
		http.Error(w, `{"error":"forbidden: admin access required"}`, http.StatusForbidden)
		return false
	}
	return true
}

func writeAdminResult(w http.ResponseWriter, err error, message string) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": message})
}

func (s *AdminServer) broadcastTeamChange(action string) {
	if s.sseBroker != nil {
		s.sseBroker.Broadcast("team_update", map[string]interface{}{"action": action, "admin": true})
	}
}

func (s *AdminServer) handleAdminTeams(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodGet) {
		return
	}
	tournamentID, _ := strconv.Atoi(r.URL.Query().Get("tournament_id"))
	teams, err := s.services.TelegramService.AdminListTeams(r.Context(), tournamentID)
	if err != nil {
		writeAdminResult(w, err, "")
		return
	}
	if teams == nil {
		teams = []models.TelegramTeam{}
	}
	for i := range teams {
		if teams[i].Players == nil {
			teams[i].Players = []models.TelegramPlayer{}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "teams": teams})
}

func (s *AdminServer) handleAdminTeamPlayerUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req updatePlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	err := s.services.TelegramService.AdminUpdatePlayer(r.Context(), req.PlayerID, req.Nickname, req.GameID, req.ZoneID, req.Role)
	if err == nil {
		s.broadcastTeamChange("admin_player_update")
	}
	writeAdminResult(w, err, "Данные игрока сохранены")
}

func (s *AdminServer) handleAdminTeamPlayerKick(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req playerIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	err := s.services.TelegramService.AdminKickPlayer(r.Context(), req.PlayerID)
	if err == nil {
		s.broadcastTeamChange("admin_kick")
	}
	writeAdminResult(w, err, "Игрок исключён из команды")
}

func (s *AdminServer) handleAdminTeamCaptain(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req playerIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	err := s.services.TelegramService.AdminSetCaptain(r.Context(), req.PlayerID)
	if err == nil {
		s.broadcastTeamChange("admin_captain")
	}
	writeAdminResult(w, err, "Капитан назначен")
}

type adminTeamActionRequest struct {
	TeamID int    `json:"team_id"`
	Action string `json:"action"` // disqualify, reinstate, checkin, uncheckin
}

func (s *AdminServer) handleAdminTeamAction(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req adminTeamActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	msg, err := s.services.TelegramService.AdminTeamAction(r.Context(), req.TeamID, req.Action)
	if err != nil {
		writeAdminResult(w, err, "")
		return
	}
	if req.Action == application.TeamActionDisqualify {
		// Same follow-up as the bulk disqualification: forfeit the team's
		// open matches and let the desk move on.
		if s.services.Bracket != nil {
			_, _ = s.services.Bracket.ForfeitDisqualified(r.Context())
		}
		if s.services.MatchDesk != nil {
			_ = s.services.MatchDesk.Tick(r.Context())
		}
		if s.sseBroker != nil {
			s.sseBroker.Broadcast("bracket_update", map[string]interface{}{"type": "team_disqualify"})
		}
	}
	if s.sseBroker != nil {
		s.sseBroker.Broadcast("checkin_update", map[string]interface{}{"type": req.Action})
	}
	s.broadcastTeamChange("admin_" + req.Action)
	writeAdminResult(w, nil, msg)
}

func (s *AdminServer) handleAdminTeamDelete(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req adminTeamActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	err := s.services.TelegramService.AdminDeleteTeamByID(r.Context(), req.TeamID)
	if err == nil {
		s.broadcastTeamChange("admin_delete")
	}
	writeAdminResult(w, err, "Команда удалена")
}
