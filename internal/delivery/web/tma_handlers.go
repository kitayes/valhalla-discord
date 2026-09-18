package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/models"
)

type MeResponse struct {
	User      *TelegramUser           `json:"user"`
	Player    *models.TelegramPlayer  `json:"player,omitempty"`
	Team      *models.TelegramTeam    `json:"team,omitempty"`
	Teammates []models.TelegramPlayer `json:"teammates,omitempty"`
	IsAdmin   bool                    `json:"is_admin"`
}

type BracketResponse struct {
	Matches []models.BracketMatch `json:"matches"`
}

type ActiveMatchResponse struct {
	HasMatch bool             `json:"has_match"`
	Match    *MatchDeskDetail `json:"match,omitempty"`
}

type MatchDeskDetail = struct {
	MatchID                 int    `json:"match_id"`
	Number                  int    `json:"number"`
	Generation              int64  `json:"generation"`
	MyTeamName              string `json:"my_team_name"`
	MyTeamID                int    `json:"my_team_id"`
	MyReady                 bool   `json:"my_ready"`
	OpponentTeamName        string `json:"opponent_team_name"`
	OpponentTeamID          int    `json:"opponent_team_id"`
	OpponentReady           bool   `json:"opponent_ready"`
	OpponentCaptainUsername string `json:"opponent_captain_username"`
	OpponentCaptainGameID   string `json:"opponent_captain_game_id"`
	DeadlineFormatted       string `json:"deadline_formatted"`
	DeadlineSeconds         int64  `json:"deadline_seconds"`
	IsPaused                bool   `json:"is_paused"`
	CanReady                bool   `json:"can_ready"`
	Status                  string `json:"status"`
}

func (s *AdminServer) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	resp := MeResponse{User: user, IsAdmin: s.isAdmin(user.ID)}
	player, _ := s.services.TelegramService.GetPlayer(r.Context(), user.ID)
	if player != nil {
		resp.Player = player
	}

	team, teammates, _ := s.services.TelegramService.GetTeamForPlayer(r.Context(), user.ID)
	if team != nil {
		resp.Team = team
		resp.Teammates = teammates
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *AdminServer) handleBracket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	matches, err := s.services.TelegramService.GetBracket(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to load bracket"}`, http.StatusInternalServerError)
		return
	}
	if matches == nil {
		matches = []models.BracketMatch{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(BracketResponse{Matches: matches})
}

func (s *AdminServer) handleActiveMatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if s.services.MatchDesk == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ActiveMatchResponse{HasMatch: false})
		return
	}

	detail, err := s.services.MatchDesk.GetMatchDeskDetail(r.Context(), user.ID)
	if err != nil || detail == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ActiveMatchResponse{HasMatch: false})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ActiveMatchResponse{
		HasMatch: true,
		Match: &MatchDeskDetail{
			MatchID:                 detail.MatchID,
			Number:                  detail.Number,
			Generation:              detail.Generation,
			MyTeamName:              detail.MyTeamName,
			MyTeamID:                detail.MyTeamID,
			MyReady:                 detail.MyReady,
			OpponentTeamName:        detail.OpponentTeamName,
			OpponentTeamID:          detail.OpponentTeamID,
			OpponentReady:           detail.OpponentReady,
			OpponentCaptainUsername: detail.OpponentCaptainUsername,
			OpponentCaptainGameID:   detail.OpponentCaptainGameID,
			DeadlineFormatted:       detail.Deadline.Format("15:04"),
			DeadlineSeconds:         detail.DeadlineSeconds,
			IsPaused:                detail.IsPaused,
			CanReady:                detail.CanReady,
			Status:                  detail.Status,
		},
	})
}

func (s *AdminServer) handleMatchReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if s.services.MatchDesk == nil {
		http.Error(w, `{"error":"match desk not available"}`, http.StatusBadRequest)
		return
	}

	if err := s.services.MatchDesk.ReadyCaptain(r.Context(), user.ID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

type refereeRequest struct {
	Reason string `json:"reason"`
}

func (s *AdminServer) handleMatchReferee(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if s.services.MatchDesk == nil {
		http.Error(w, `{"error":"match desk not available"}`, http.StatusBadRequest)
		return
	}

	var req refereeRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.services.MatchDesk.CallJudgeCaptain(r.Context(), user.ID, req.Reason); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *AdminServer) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	msg := s.services.TelegramService.ToggleCheckIn(r.Context(), user.ID)
	msgLower := strings.ToLower(msg)
	isRefusal := strings.Contains(msgLower, "только капитан") ||
		strings.Contains(msgLower, "не найдена") ||
		strings.Contains(msgLower, "невозможен") ||
		strings.Contains(msgLower, "не удалось") ||
		strings.Contains(msgLower, "снята с турнира")
	isSuccess := !isRefusal && (strings.Contains(msgLower, "подтвержд") || strings.Contains(msgLower, "снят"))
	isCheckedIn := isSuccess && strings.Contains(msgLower, "подтвержд") && !strings.Contains(msgLower, "не подтвержд")

	w.Header().Set("Content-Type", "application/json")
	if !isSuccess {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      false,
			"error":   msg,
			"message": msg,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":            true,
		"message":       msg,
		"is_checked_in": isCheckedIn,
	})
}

func (s *AdminServer) handleApp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "app.html", nil); err != nil {
		s.logger.Error("web: failed to render app.html: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

type updatePlayerRequest struct {
	PlayerID int    `json:"player_id"`
	Nickname string `json:"nickname"`
	GameID   string `json:"game_id"`
	ZoneID   string `json:"zone_id"`
	Role     string `json:"role"`
}

func (s *AdminServer) handleUpdateTeamPlayer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req updatePlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := s.services.TelegramService.UpdateTeamPlayer(r.Context(), user.ID, req.PlayerID, req.Nickname, req.GameID, req.ZoneID, req.Role); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Данные игрока успешно сохранены",
	})
}

type matchReportRequest struct {
	MatchID    int    `json:"match_id"`
	MyScore    int    `json:"my_score"`
	OppScore   int    `json:"opp_score"`
	Screenshot string `json:"screenshot"`
}

func (s *AdminServer) handleMatchReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req matchReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	rep, err := s.services.TelegramService.ReportMatchDirect(r.Context(), user.ID, req.MatchID, req.MyScore, req.OppScore, req.Screenshot)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	if s.services.MatchDesk != nil {
		_ = s.services.MatchDesk.Tick(r.Context())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Результат матча (" + rep.Score + ") успешно сохранён и зафиксирован в сетке!",
		"report":  rep,
	})
}

type AdminDeskResponse struct {
	GlobalPaused  bool                         `json:"global_paused"`
	CheckIn       *models.CheckInSummary       `json:"checkin"`
	ActiveMatches []application.AdminMatchItem `json:"active_matches"`
	Disputes      []application.AdminMatchItem `json:"disputes"`
}

func (s *AdminServer) handleAdminDesk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if !s.isAdmin(user.ID) {
		http.Error(w, `{"error":"forbidden: admin access required"}`, http.StatusForbidden)
		return
	}

	resp := AdminDeskResponse{
		ActiveMatches: []application.AdminMatchItem{},
		Disputes:      []application.AdminMatchItem{},
	}

	checkin, err := s.services.TelegramService.GetCheckInSummary(r.Context())
	if err == nil {
		resp.CheckIn = checkin
	}

	if s.services.MatchDesk != nil {
		globalPaused, matches, err := s.services.MatchDesk.GetAdminMatches(r.Context(), user.ID)
		if err == nil {
			resp.GlobalPaused = globalPaused
			resp.ActiveMatches = matches
			for _, m := range matches {
				if m.HasIssues {
					resp.Disputes = append(resp.Disputes, m)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type AdminMatchActionRequest struct {
	Action         string `json:"action"` // "pause", "resume", "resolve", "tech_win"
	MatchID        int    `json:"match_id"`
	Generation     int64  `json:"generation"`
	WinnerTeamName string `json:"winner_team_name,omitempty"`
	Score          string `json:"score,omitempty"`
}

func (s *AdminServer) handleAdminMatchAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if !s.isAdmin(user.ID) {
		http.Error(w, `{"error":"forbidden: admin access required"}`, http.StatusForbidden)
		return
	}

	var req AdminMatchActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "tech_win":
		if s.services.Bracket == nil {
			http.Error(w, `{"error":"bracket service not configured"}`, http.StatusBadRequest)
			return
		}
		winScore, loseScore := 2, 0
		if req.Score != "" {
			parts := strings.Split(req.Score, ":")
			if len(parts) == 2 {
				wScore, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
				lScore, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
				if err1 == nil && err2 == nil && wScore > lScore {
					winScore, loseScore = wScore, lScore
				}
			}
		}
		playOrder := req.MatchID
		ms, _ := s.services.TelegramService.GetBracket(r.Context())
		for _, m := range ms {
			if m.ID == req.MatchID {
				playOrder = m.PlayOrder
				break
			}
		}
		change, err := s.services.Bracket.SetWinner(r.Context(), playOrder, req.WinnerTeamName, winScore, loseScore)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		_ = change
		if s.services.MatchDesk != nil {
			_ = s.services.MatchDesk.AdminAction(r.Context(), user.ID, req.MatchID, req.Generation, "resolve")
			_ = s.services.MatchDesk.Tick(r.Context())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"message": fmt.Sprintf("Техническая победа '%s' (%d:%d) успешно зафиксирована!", req.WinnerTeamName, winScore, loseScore),
		})
		return

	case "pause", "resume", "resolve":
		if s.services.MatchDesk == nil {
			http.Error(w, `{"error":"match desk not configured"}`, http.StatusBadRequest)
			return
		}
		if err := s.services.MatchDesk.AdminAction(r.Context(), user.ID, req.MatchID, req.Generation, req.Action); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": "Действие успешно выполнено"})
		return

	default:
		http.Error(w, `{"error":"неизвестное действие"}`+req.Action, http.StatusBadRequest)
		return
	}
}

type adminPingRequest struct {
	Message string `json:"message"`
}

func (s *AdminServer) handleAdminPingDebtors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if !s.isAdmin(user.ID) {
		http.Error(w, `{"error":"forbidden: admin access required"}`, http.StatusForbidden)
		return
	}

	var req adminPingRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	if s.services.TelegramService != nil {
		summary, err := s.services.TelegramService.GetCheckInSummary(r.Context())
		if err == nil && summary != nil {
			actionableCount := summary.PendingCount + summary.IncompleteCount
			if actionableCount == 0 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"ok":      false,
					"error":   "Все команды уже подтвердили Check-in! Должников нет.",
					"count":   0,
				})
				return
			}
		}
	}

	if s.debtorNotifier != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = s.debtorNotifier(bgCtx, user.ID, req.Message)
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Оповещение должников успешно отправлено в Telegram",
	})
}

func (s *AdminServer) handleAdminDisqualifyUncheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if !s.isAdmin(user.ID) {
		http.Error(w, `{"error":"forbidden: admin access required"}`, http.StatusForbidden)
		return
	}

	disqualified, err := s.services.TelegramService.DisqualifyUnchecked(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	if s.services.Bracket != nil {
		_, _ = s.services.Bracket.ForfeitDisqualified(r.Context())
	}
	if s.services.MatchDesk != nil {
		_ = s.services.MatchDesk.Tick(r.Context())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": fmt.Sprintf("Дисквалифицировано команд: %d", len(disqualified)),
		"count":   len(disqualified),
	})
}
