package web

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func writeJSONError(w http.ResponseWriter, err error) {
	http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
}

// handleAdminTournamentUpdate edits a tournament's settings: name, bracket
// type, seeding, stage, advance count and BO formats.
func (s *AdminServer) handleAdminTournamentUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req tournamentSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TournamentID <= 0 {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	t, err := s.services.TelegramService.UpdateTournamentSettings(r.Context(), req.TournamentID, req.settings())
	if err != nil {
		writeJSONError(w, err)
		return
	}
	s.sseBroker.Broadcast("tournaments_update", map[string]interface{}{"action": "updated", "id": t.ID})
	s.sseBroker.Broadcast("bracket_update", map[string]interface{}{"action": "settings"})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "tournament": t})
}

// handleAdminTournamentImportQualified enters the qualified teams of finished
// qualifiers into a main stage.
func (s *AdminServer) handleAdminTournamentImportQualified(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req struct {
		TournamentID int   `json:"tournament_id"`
		FromIDs      []int `json:"from_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TournamentID <= 0 {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	n, err := s.services.TelegramService.ImportQualified(r.Context(), req.TournamentID, req.FromIDs)
	if err != nil {
		writeJSONError(w, err)
		return
	}
	s.sseBroker.Broadcast("tournaments_update", map[string]interface{}{"action": "entries", "id": req.TournamentID})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "count": n,
		"message": fmt.Sprintf("Добавлено команд: %d", n)})
}

// handleAdminTournamentInvite enters a team into a main stage by invitation.
func (s *AdminServer) handleAdminTournamentInvite(w http.ResponseWriter, r *http.Request) {
	if !s.adminGate(w, r, http.MethodPost) {
		return
	}
	var req struct {
		TournamentID int `json:"tournament_id"`
		TeamID       int `json:"team_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TeamID <= 0 {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.TournamentID <= 0 {
		active, _ := s.services.TelegramService.GetActiveTournament(r.Context())
		if active == nil {
			http.Error(w, `{"error":"нет активного турнира"}`, http.StatusBadRequest)
			return
		}
		req.TournamentID = active.ID
	}
	if err := s.services.TelegramService.InviteTeam(r.Context(), req.TournamentID, req.TeamID); err != nil {
		writeJSONError(w, err)
		return
	}
	s.sseBroker.Broadcast("team_update", map[string]interface{}{"action": "invited", "team_id": req.TeamID})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": "Команда приглашена в основной этап"})
}
