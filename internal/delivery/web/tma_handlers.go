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
	User             *TelegramUser           `json:"user"`
	Player           *models.TelegramPlayer  `json:"player,omitempty"`
	Team             *models.TelegramTeam    `json:"team,omitempty"`
	Teammates        []models.TelegramPlayer `json:"teammates,omitempty"`
	IsAdmin          bool                    `json:"is_admin"`
	RegistrationOpen bool                    `json:"registration_open"`
	// BotUsername lets the app build t.me links without hardcoding the bot.
	BotUsername string `json:"bot_username,omitempty"`
}

// inviteStartPrefix marks a /start payload that carries an invite token:
// "/start join_<token>". Shared with the Telegram bot, which completes the
// join when a player opens such a link.
const inviteStartPrefix = "join_"

// InviteStartPayload is the /start payload for an invite token.
func InviteStartPayload(token string) string { return inviteStartPrefix + token }

// InviteTokenFromStart extracts the token from a /start payload, if it is one.
func InviteTokenFromStart(payload string) (string, bool) {
	payload = strings.TrimSpace(payload)
	if !strings.HasPrefix(payload, inviteStartPrefix) || len(payload) == len(inviteStartPrefix) {
		return "", false
	}
	return payload[len(inviteStartPrefix):], true
}

type RoundScheduleItem struct {
	Round         int    `json:"round"`
	RoundName     string `json:"round_name"`
	ScheduledTime string `json:"scheduled_time"`
	MatchFormat   string `json:"match_format"`
}

type BracketResponse struct {
	Matches  []models.BracketMatch `json:"matches"`
	Schedule []RoundScheduleItem   `json:"schedule,omitempty"`
}

type TeamTournamentStatus struct {
	Status             string `json:"status"` // "playing", "advanced", "eliminated", "champion", "waiting"
	LastRound          int    `json:"last_round,omitempty"`
	LastRoundName      string `json:"last_round_name,omitempty"`
	Score              string `json:"score,omitempty"`
	OpponentName       string `json:"opponent_name,omitempty"`
	NextRound          int    `json:"next_round,omitempty"`
	NextRoundName      string `json:"next_round_name,omitempty"`
	NextScheduledTime  string `json:"next_scheduled_time,omitempty"`
	WaitingForOpponent bool   `json:"waiting_for_opponent,omitempty"`
	WaitingOpponents   string `json:"waiting_opponents,omitempty"`
}

type ActiveMatchResponse struct {
	HasMatch         bool                  `json:"has_match"`
	Match            *MatchDeskDetail      `json:"match,omitempty"`
	TournamentStatus *TeamTournamentStatus `json:"tournament_status,omitempty"`
}

type MatchDeskDetail = struct {
	MatchID                 int    `json:"match_id"`
	Number                  int    `json:"number"`
	Round                   int    `json:"round"`
	RoundName               string `json:"round_name"`
	ScheduledTime           string `json:"scheduled_time"`
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
	IsHost                  bool   `json:"is_host"`
	HostTeamName            string `json:"host_team_name"`
	MySide                  string `json:"my_side"`
	FirstPickTeamName       string `json:"first_pick_team_name"`
	MatchFormat             string `json:"match_format"`
	RoomRules               string `json:"room_rules"`
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

	regOpen := true
	if s.services.TelegramService != nil {
		open, _ := s.services.TelegramService.RegistrationStatus(r.Context())
		regOpen = open
	}

	resp := MeResponse{User: user, IsAdmin: s.isAdmin(user.ID), RegistrationOpen: regOpen, BotUsername: s.botUsername}
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

	var schedule []RoundScheduleItem
	if len(matches) > 0 && s.services.TelegramService != nil {
		tTime := s.services.TelegramService.GetTournamentTime(r.Context())
		totalRounds := application.TotalRounds(matches)
		for rNum := 1; rNum <= totalRounds; rNum++ {
			fmtStr := "BO1"
			if rNum >= totalRounds-1 && totalRounds > 1 {
				fmtStr = "BO3"
			}
			schedule = append(schedule, RoundScheduleItem{
				Round:         rNum,
				RoundName:     application.FormatRoundTitle(rNum, totalRounds),
				ScheduledTime: application.FormatScheduledTime(tTime, rNum, time.FixedZone("MSK", 3*3600)),
				MatchFormat:   fmtStr,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(BracketResponse{Matches: matches, Schedule: schedule})
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
		status := s.getTeamTournamentStatus(r.Context(), user.ID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ActiveMatchResponse{
			HasMatch:         false,
			TournamentStatus: status,
		})
		return
	}

	detail, err := s.services.MatchDesk.GetMatchDeskDetail(r.Context(), user.ID)
	if (err != nil || detail == nil) && s.services.TelegramService != nil {
		team, teammates, _ := s.services.TelegramService.GetTeamForPlayer(r.Context(), user.ID)
		if team != nil {
			for _, tm := range teammates {
				if tm.IsCaptain && tm.TelegramID != nil {
					detail, _ = s.services.MatchDesk.GetMatchDeskDetail(r.Context(), *tm.TelegramID)
					if detail != nil {
						detail.CanReady = false
						break
					}
				}
			}
		}
	}
	if detail == nil {
		status := s.getTeamTournamentStatus(r.Context(), user.ID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ActiveMatchResponse{
			HasMatch:         false,
			TournamentStatus: status,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ActiveMatchResponse{
		HasMatch: true,
		TournamentStatus: &TeamTournamentStatus{
			Status:        "playing",
			LastRound:     detail.Round,
			LastRoundName: detail.RoundName,
		},
		Match: &MatchDeskDetail{
			MatchID:                 detail.MatchID,
			Number:                  detail.Number,
			Round:                   detail.Round,
			RoundName:               detail.RoundName,
			ScheduledTime:           detail.ScheduledTime,
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
			IsHost:                  detail.IsHost,
			HostTeamName:            detail.HostTeamName,
			MySide:                  detail.MySide,
			FirstPickTeamName:       detail.FirstPickTeamName,
			MatchFormat:             detail.MatchFormat,
			RoomRules:               detail.RoomRules,
		},
	})
}

func (s *AdminServer) getTeamTournamentStatus(ctx context.Context, userID int64) *TeamTournamentStatus {
	if s.services.TelegramService == nil {
		return nil
	}
	team, _, err := s.services.TelegramService.GetTeamForPlayer(ctx, userID)
	if err != nil || team == nil {
		return nil
	}

	matches, err := s.services.TelegramService.GetBracket(ctx)
	if err != nil || len(matches) == 0 {
		return nil
	}

	totalRounds := application.TotalRounds(matches)
	tTime := s.services.TelegramService.GetTournamentTime(ctx)
	loc := time.FixedZone("MSK", 3*3600)

	// Find the team's latest match (by Round, then PlayOrder)
	var latest *models.BracketMatch
	for i := range matches {
		m := &matches[i]
		if (m.Team1ID != nil && *m.Team1ID == team.ID) || (m.Team2ID != nil && *m.Team2ID == team.ID) {
			if latest == nil || m.Round > latest.Round || (m.Round == latest.Round && m.PlayOrder > latest.PlayOrder) {
				latest = m
			}
		}
	}

	if latest == nil {
		return &TeamTournamentStatus{
			Status: "waiting",
		}
	}

	oppName := ""
	if latest.Team1ID != nil && *latest.Team1ID == team.ID {
		oppName = latest.Team2Name
	} else if latest.Team2ID != nil && *latest.Team2ID == team.ID {
		oppName = latest.Team1Name
	}
	roundName := application.FormatRoundTitle(latest.Round, totalRounds)

	// If latest match is pending/open without both teams:
	if latest.State == models.BracketOpen || latest.State == models.BracketPending {
		if latest.Team1ID == nil || latest.Team2ID == nil {
			schedTime := application.FormatScheduledTime(tTime, latest.Round, loc)
			return &TeamTournamentStatus{
				Status:             "advanced",
				NextRound:          latest.Round,
				NextRoundName:      roundName,
				NextScheduledTime:  schedTime,
				WaitingForOpponent: true,
			}
		}
	}

	// If latest match is complete:
	if latest.State == models.BracketComplete {
		if latest.WinnerID != nil && *latest.WinnerID == team.ID {
			if latest.Round >= totalRounds && totalRounds > 0 {
				return &TeamTournamentStatus{
					Status:        "champion",
					LastRound:     latest.Round,
					LastRoundName: roundName,
					Score:         latest.ScoresCSV,
					OpponentName:  oppName,
				}
			}

			nextRound := latest.Round + 1
			nextRoundName := application.FormatRoundTitle(nextRound, totalRounds)
			schedTime := application.FormatScheduledTime(tTime, nextRound, loc)

			// Look for parallel match that feeds into the same next round match
			var waitingOpponents string
			siblingPO := latest.PlayOrder + 1
			if latest.PlayOrder%2 == 0 {
				siblingPO = latest.PlayOrder - 1
			}
			for i := range matches {
				if matches[i].Round == latest.Round && matches[i].PlayOrder == siblingPO {
					m2 := &matches[i]
					if m2.Team1Name != "" && m2.Team2Name != "" {
						waitingOpponents = m2.Team1Name + " vs " + m2.Team2Name
					} else if m2.Team1Name != "" {
						waitingOpponents = m2.Team1Name
					} else if m2.Team2Name != "" {
						waitingOpponents = m2.Team2Name
					}
					break
				}
			}

			return &TeamTournamentStatus{
				Status:             "advanced",
				LastRound:          latest.Round,
				LastRoundName:      roundName,
				Score:              latest.ScoresCSV,
				OpponentName:       oppName,
				NextRound:          nextRound,
				NextRoundName:      nextRoundName,
				NextScheduledTime:  schedTime,
				WaitingForOpponent: true,
				WaitingOpponents:   waitingOpponents,
			}
		} else if latest.WinnerID != nil && *latest.WinnerID != team.ID {
			return &TeamTournamentStatus{
				Status:        "eliminated",
				LastRound:     latest.Round,
				LastRoundName: roundName,
				Score:         latest.ScoresCSV,
				OpponentName:  oppName,
			}
		}
	}

	return &TeamTournamentStatus{
		Status: "waiting",
	}
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

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("match_update", map[string]interface{}{
			"type":    "ready",
			"user_id": user.ID,
		})
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

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("match_update", map[string]interface{}{
			"type":    "referee",
			"user_id": user.ID,
			"reason":  req.Reason,
		})
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

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("checkin_update", map[string]interface{}{
			"user_id":       user.ID,
			"is_checked_in": isCheckedIn,
		})
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

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("team_update", map[string]interface{}{
			"user_id":   user.ID,
			"player_id": req.PlayerID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Данные игрока успешно сохранены",
	})
}

type matchReportRequest struct {
	MatchID  int `json:"match_id"`
	MyScore  int `json:"my_score"`
	OppScore int `json:"opp_score"`
	// Screenshots carries the proof as data URLs, at most maxReportPhotos.
	Screenshots []string `json:"screenshots"`
	// Screenshot is the single-image field earlier builds of the mini app
	// sent. It is still honoured so a client cached on someone's phone keeps
	// working across the deploy that introduces the array.
	Screenshot string `json:"screenshot"`
}

// proof returns the screenshots the client sent, whichever field it used.
func (r matchReportRequest) proof() []string {
	if len(r.Screenshots) > 0 {
		return r.Screenshots
	}
	if r.Screenshot != "" {
		return []string{r.Screenshot}
	}
	return nil
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

	photos, err := decodeReportPhotos(req.proof())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// Screenshots are uploaded before the result is recorded. Saving the score
	// first and losing the proof afterwards would leave the referee with a
	// disputed match and nothing to look at.
	fileIDs, err := s.uploadReportPhotos(r.Context(), req.MatchID, photos)
	if err != nil {
		s.logger.Error("web: failed to upload report screenshots for match %d: %v", req.MatchID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "не удалось загрузить скриншоты, попробуйте ещё раз",
		})
		return
	}

	rep, err := s.services.TelegramService.ReportMatchDirect(r.Context(), user.ID, req.MatchID, req.MyScore, req.OppScore, fileIDs)
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

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("match_update", map[string]interface{}{
			"type":     "report",
			"match_id": req.MatchID,
		})
		s.sseBroker.Broadcast("bracket_update", map[string]interface{}{
			"match_id": req.MatchID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Результат матча (" + rep.Score + ") успешно сохранён и зафиксирован в сетке!",
		"report":  rep,
	})
}

type AdminDeskResponse struct {
	GlobalPaused    bool                              `json:"global_paused"`
	CheckIn         *models.CheckInSummary            `json:"checkin"`
	ActiveMatches   []application.AdminMatchItem      `json:"active_matches"`
	Disputes        []application.AdminMatchItem      `json:"disputes"`
	ArchivedMatches []application.AdminArchivedMatchItem `json:"archived_matches"`
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
		ActiveMatches:   []application.AdminMatchItem{},
		Disputes:        []application.AdminMatchItem{},
		ArchivedMatches: []application.AdminArchivedMatchItem{},
	}

	checkin, err := s.services.TelegramService.GetCheckInSummary(r.Context())
	if err == nil {
		resp.CheckIn = checkin
	}

	if s.services.MatchDesk != nil {
		globalPaused, matches, archived, err := s.services.MatchDesk.GetAdminMatches(r.Context(), user.ID)
		if err == nil {
			resp.GlobalPaused = globalPaused
			resp.ActiveMatches = matches
			resp.ArchivedMatches = archived
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
	Action         string `json:"action"` // "pause", "resume", "resolve", "tech_win", "rollback", "change_winner", "extend_5", "extend_10", "reset_ready"
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
		winScore, loseScore := 1, 0
		if req.Score != "" {
			parts := strings.Split(req.Score, ":")
			if len(parts) != 2 {
				parts = strings.Split(req.Score, "-")
			}
			if len(parts) == 2 {
				wScore, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
				lScore, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
				if err1 == nil && err2 == nil && wScore > lScore {
					winScore, loseScore = wScore, lScore
				}
			}
		}

		if s.services.TelegramService == nil {
			http.Error(w, `{"error":"telegram service not configured"}`, http.StatusBadRequest)
			return
		}

		if err := s.services.TelegramService.SetWinnerDirect(r.Context(), req.MatchID, req.WinnerTeamName, winScore, loseScore); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if s.services.MatchDesk != nil {
			_ = s.services.MatchDesk.AdminAction(r.Context(), user.ID, req.MatchID, req.Generation, "resolve")
			_ = s.services.MatchDesk.Tick(r.Context())
		}
		if s.sseBroker != nil {
			s.sseBroker.Broadcast("match_update", map[string]interface{}{
				"type":     "tech_win",
				"match_id": req.MatchID,
			})
			s.sseBroker.Broadcast("bracket_update", map[string]interface{}{
				"match_id": req.MatchID,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"message": fmt.Sprintf("Техническая победа '%s' (%d:%d) успешно зафиксирована!", req.WinnerTeamName, winScore, loseScore),
		})
		return

	case "rollback":
		if s.services.TelegramService == nil {
			http.Error(w, `{"error":"telegram service not configured"}`, http.StatusBadRequest)
			return
		}
		if err := s.services.TelegramService.RollbackMatch(r.Context(), req.MatchID); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if s.services.MatchDesk != nil {
			_ = s.services.MatchDesk.Tick(r.Context())
		}
		if s.sseBroker != nil {
			s.sseBroker.Broadcast("match_update", map[string]interface{}{
				"type":     "rollback",
				"match_id": req.MatchID,
			})
			s.sseBroker.Broadcast("bracket_update", map[string]interface{}{
				"match_id": req.MatchID,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"message": "Результат матча успешно откачен. Матч возвращен в активные.",
		})
		return

	case "change_winner":
		winScore, loseScore := 1, 0
		if req.Score != "" {
			parts := strings.Split(req.Score, ":")
			if len(parts) != 2 {
				parts = strings.Split(req.Score, "-")
			}
			if len(parts) == 2 {
				wScore, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
				lScore, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
				if err1 == nil && err2 == nil && wScore > lScore {
					winScore, loseScore = wScore, lScore
				}
			}
		}
		if s.services.TelegramService == nil {
			http.Error(w, `{"error":"telegram service not configured"}`, http.StatusBadRequest)
			return
		}
		if err := s.services.TelegramService.ChangeWinnerDirect(r.Context(), req.MatchID, req.WinnerTeamName, winScore, loseScore); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if s.services.MatchDesk != nil {
			_ = s.services.MatchDesk.Tick(r.Context())
		}
		if s.sseBroker != nil {
			s.sseBroker.Broadcast("match_update", map[string]interface{}{
				"type":     "change_winner",
				"match_id": req.MatchID,
			})
			s.sseBroker.Broadcast("bracket_update", map[string]interface{}{
				"match_id": req.MatchID,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"message": fmt.Sprintf("Победитель успешно изменён на '%s' (%d:%d)!", req.WinnerTeamName, winScore, loseScore),
		})
		return

	case "pause", "resume", "resolve", "extend_5", "extend_10", "reset_ready":
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
		if s.sseBroker != nil {
			s.sseBroker.Broadcast("match_update", map[string]interface{}{
				"type":       req.Action,
				"match_id":   req.MatchID,
				"generation": req.Generation,
			})
		}
		msg := "Действие успешно выполнено"
		switch req.Action {
		case "extend_5":
			msg = "Дедлайн успешно продлен на 5 минут"
		case "extend_10":
			msg = "Дедлайн успешно продлен на 10 минут"
		case "reset_ready":
			msg = "Статус готовности команд сброшен"
		case "pause":
			msg = "Матч поставлен на паузу"
		case "resume":
			msg = "Матч снят с паузы"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": msg})
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

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("bracket_update", map[string]interface{}{
			"type": "disqualify_uncheck",
		})
		s.sseBroker.Broadcast("checkin_update", map[string]interface{}{
			"type": "disqualify_uncheck",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": fmt.Sprintf("Дисквалифицировано команд: %d", len(disqualified)),
		"count":   len(disqualified),
	})
}

func (s *AdminServer) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}

	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if s.sseBroker == nil {
		http.Error(w, `{"error":"events broker unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cleanup := s.sseBroker.Subscribe(r.Context(), user.ID)
	defer cleanup()

	_, _ = fmt.Fprintf(w, "event: connected\ndata: {\"user_id\":%d}\n\n", user.ID)
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case msg, open := <-ch:
			if !open {
				return
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Event, msg.Data)
			flusher.Flush()
		}
	}
}


// ──────────────────────────────────────────────────────────────────────────────
// Пачка 4: In-app registration & roster management
// ──────────────────────────────────────────────────────────────────────────────

type createTeamRequest struct {
	Name string `json:"name"`
}

func (s *AdminServer) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req createTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := s.services.TelegramService.CreateTeamInApp(r.Context(), user.ID, req.Name); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("team_update", map[string]interface{}{"user_id": user.ID})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": "Команда создана"})
}

func (s *AdminServer) handleGenerateInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	token, err := s.services.TelegramService.GenerateInviteToken(r.Context(), user.ID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "token": token, "link": s.inviteLink(token)})
}

type joinTeamRequest struct {
	Token string `json:"token"`
}

func (s *AdminServer) handleJoinTeam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req joinTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := s.services.TelegramService.JoinTeamByToken(r.Context(), user.ID, req.Token); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("team_update", map[string]interface{}{"user_id": user.ID})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": "Вы успешно вступили в команду"})
}

type playerIDRequest struct {
	PlayerID int `json:"player_id"`
}

func (s *AdminServer) handleKickPlayer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req playerIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := s.services.TelegramService.KickTeamPlayer(r.Context(), user.ID, req.PlayerID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("team_update", map[string]interface{}{"user_id": user.ID, "player_id": req.PlayerID})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": "Игрок исключён из команды"})
}

func (s *AdminServer) handleTransferCaptain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	user, ok := UserFromContext(r.Context())
	if !ok || user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req playerIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := s.services.TelegramService.TransferCaptain(r.Context(), user.ID, req.PlayerID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	if s.sseBroker != nil {
		s.sseBroker.Broadcast("team_update", map[string]interface{}{"user_id": user.ID, "player_id": req.PlayerID})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": "Капитанство передано"})
}

func (s *AdminServer) handleBracketMatchDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		idStr = r.URL.Query().Get("match_id")
	}
	matchID, err := strconv.Atoi(idStr)
	if err != nil || matchID <= 0 {
		http.Error(w, `{"error":"invalid match id"}`, http.StatusBadRequest)
		return
	}

	details, err := s.services.TelegramService.GetBracketMatchDetails(r.Context(), matchID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"details": details,
	})
}

func (s *AdminServer) handleTeamDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		idStr = r.URL.Query().Get("team_id")
	}
	teamID, err := strconv.Atoi(idStr)
	if err != nil || teamID <= 0 {
		http.Error(w, `{"error":"invalid team id"}`, http.StatusBadRequest)
		return
	}

	team, members, err := s.services.TelegramService.GetTeamDetails(r.Context(), teamID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "team not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"team":    team,
		"members": members,
	})
}

