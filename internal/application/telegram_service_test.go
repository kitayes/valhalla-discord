package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeTelegramRepo is an in-memory stand-in for repository.Telegram, just
// enough to drive the registration state machine.
type fakeTelegramRepo struct {
	players      map[int64]*models.TelegramPlayer // by telegram id
	teams        map[int]*models.TelegramTeam
	members      []*models.TelegramPlayer // teammates created via CreateTeammate
	settings     map[string]string
	reports      []models.TelegramMatchReport
	nextID       int
	bracket         []models.BracketMatch
	participants    map[int]int64 // teamID -> challonge participant id
	tournaments     []*models.TelegramTournament
	tournamentTeams map[string]*models.TournamentTeam
	// failSetting, when set for a key, makes SetSetting return that error
	// instead of writing, so tests can exercise write-failure paths.
	failSetting map[string]error
}

func newFakeTelegramRepo() *fakeTelegramRepo {
	activeT := &models.TelegramTournament{
		ID:       1,
		Name:     "Этап 1",
		Status:   models.TournamentStatusRegistration,
		IsActive: true,
	}
	return &fakeTelegramRepo{
		players:         map[int64]*models.TelegramPlayer{},
		teams:           map[int]*models.TelegramTeam{},
		settings:        map[string]string{},
		nextID:          1,
		participants:    map[int]int64{},
		tournaments:     []*models.TelegramTournament{activeT},
		tournamentTeams: map[string]*models.TournamentTeam{},
	}
}

func (r *fakeTelegramRepo) addPlayer(tgID int64, teamID *int, captain bool, state string) *models.TelegramPlayer {
	p := &models.TelegramPlayer{ID: r.nextID, TelegramID: &tgID, TeamID: teamID, IsCaptain: captain, FSMState: state}
	r.nextID++
	r.players[tgID] = p
	return p
}

func (r *fakeTelegramRepo) CreateOrUpdatePlayer(context.Context, *models.TelegramPlayer) error {
	return nil
}
func (r *fakeTelegramRepo) GetPlayerByTelegramID(_ context.Context, tgID int64) (*models.TelegramPlayer, error) {
	return r.players[tgID], nil
}
func (r *fakeTelegramRepo) GetPlayerByID(_ context.Context, playerID int) (*models.TelegramPlayer, error) {
	for _, p := range r.players {
		if p != nil && p.ID == playerID {
			return p, nil
		}
	}
	return nil, nil
}
func (r *fakeTelegramRepo) UpdatePlayerState(_ context.Context, tgID int64, s string) error {
	if p := r.players[tgID]; p != nil {
		p.FSMState = s
	}
	return nil
}
func (r *fakeTelegramRepo) UpdatePlayerField(_ context.Context, tgID int64, col string, v interface{}) error {
	p := r.players[tgID]
	if p == nil {
		return errors.New("no player")
	}
	setField(p, col, v)
	return nil
}
func (r *fakeTelegramRepo) UpdatePlayerFieldByID(_ context.Context, id int, col string, v interface{}) error {
	for _, p := range r.allRows() {
		if p.ID == id {
			setField(p, col, v)
			return nil
		}
	}
	return errors.New("no row")
}

func setField(p *models.TelegramPlayer, col string, v interface{}) {
	switch col {
	case "stars":
		p.Stars = v.(int)
	case "team_id":
		id := v.(int)
		p.TeamID = &id
	case "is_captain":
		p.IsCaptain = v.(bool)
	case "game_nickname":
		p.GameNickname = v.(string)
	case "game_id":
		p.GameID = v.(string)
	case "zone_id":
		p.ZoneID = v.(string)
	case "main_role":
		p.MainRole = v.(string)
	case "telegram_username":
		p.TelegramUsername = v.(string)
	case "fsm_state":
		p.FSMState = v.(string)
	}
}

// allRows returns account rows and roster rows together, sorted by id.
func (r *fakeTelegramRepo) allRows() []*models.TelegramPlayer {
	var out []*models.TelegramPlayer
	for _, p := range r.players {
		out = append(out, p)
	}
	out = append(out, r.members...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (r *fakeTelegramRepo) CreateTeam(_ context.Context, name string) (*models.TelegramTeam, error) {
	t := &models.TelegramTeam{ID: r.nextID, Name: name}
	r.nextID++
	r.teams[t.ID] = t
	return t, nil
}
func (r *fakeTelegramRepo) GetTeamByID(_ context.Context, id int) (*models.TelegramTeam, error) {
	if t := r.teams[id]; t != nil {
		return t, nil
	}
	return nil, errors.New("no team")
}
func (r *fakeTelegramRepo) GetTeamByName(_ context.Context, name string) (*models.TelegramTeam, error) {
	for _, t := range r.teams {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, errors.New("no team")
}
func (r *fakeTelegramRepo) DeleteTeam(_ context.Context, id int) error {
	delete(r.teams, id)
	return nil
}
func (r *fakeTelegramRepo) GetAllTeams(context.Context) ([]models.TelegramTeam, error) {
	var ids []int
	for id := range r.teams {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var out []models.TelegramTeam
	for _, id := range ids {
		t := *r.teams[id]
		t.Players, _ = r.GetTeamMembers(context.Background(), id)
		out = append(out, t)
	}
	return out, nil
}
func (r *fakeTelegramRepo) GetTeamMembers(_ context.Context, teamID int) ([]models.TelegramPlayer, error) {
	var out []models.TelegramPlayer
	for _, p := range r.allRows() {
		if p.TeamID != nil && *p.TeamID == teamID {
			out = append(out, *p)
		}
	}
	return out, nil
}
func (r *fakeTelegramRepo) CreateTeammate(_ context.Context, p *models.TelegramPlayer) error {
	p.ID = r.nextID
	r.nextID++
	r.members = append(r.members, p)
	return nil
}
func (r *fakeTelegramRepo) DeleteTeammate(_ context.Context, playerID int) error {
	for i, m := range r.members {
		if m.ID == playerID {
			r.members = append(r.members[:i], r.members[i+1:]...)
			break
		}
	}
	return nil
}
func (r *fakeTelegramRepo) ReleaseTeamMembers(_ context.Context, teamID int) error {
	var kept []*models.TelegramPlayer
	for _, m := range r.members {
		if m.TeamID == nil || *m.TeamID != teamID {
			kept = append(kept, m)
		}
	}
	r.members = kept
	for _, p := range r.players {
		if p.TeamID != nil && *p.TeamID == teamID {
			p.TeamID = nil
			p.IsCaptain = false
			p.FSMState = ""
		}
	}
	return nil
}
func (r *fakeTelegramRepo) SetTeamStatus(_ context.Context, id int, st string) error {
	r.teams[id].Status = st
	return nil
}
func (r *fakeTelegramRepo) FindByGameID(_ context.Context, gameID string) ([]models.TelegramPlayer, error) {
	var out []models.TelegramPlayer
	for _, p := range r.allRows() {
		if p.GameID == gameID {
			out = append(out, *p)
		}
	}
	return out, nil
}
func (r *fakeTelegramRepo) SetCheckIn(_ context.Context, id int, v bool) error {
	r.teams[id].IsCheckedIn = v
	return nil
}
func (r *fakeTelegramRepo) GetAllCaptains(context.Context) ([]models.TelegramPlayer, error) {
	return nil, nil
}
func (r *fakeTelegramRepo) GetSoloPlayers(context.Context) ([]models.TelegramPlayer, error) {
	return nil, nil
}
func (r *fakeTelegramRepo) GetSetting(_ context.Context, k string) (string, error) {
	return r.settings[k], nil
}
func (r *fakeTelegramRepo) SetSetting(_ context.Context, k, v string) error {
	if err := r.failSetting[k]; err != nil {
		return err
	}
	r.settings[k] = v
	return nil
}
func (r *fakeTelegramRepo) CreateMatchReport(_ context.Context, rep *models.TelegramMatchReport) error {
	rep.ID = r.nextID
	r.nextID++
	if rep.Status == "" {
		rep.Status = models.ReportConfirmed
	}
	r.reports = append(r.reports, *rep)
	return nil
}
func (r *fakeTelegramRepo) GetRecentMatchReports(_ context.Context, limit int) ([]models.TelegramMatchReport, error) {
	return r.reports, nil
}
func (r *fakeTelegramRepo) GetMatchReport(_ context.Context, id int) (*models.TelegramMatchReport, error) {
	for i := range r.reports {
		if r.reports[i].ID == id {
			rep := r.reports[i]
			return &rep, nil
		}
	}
	return nil, nil
}
func (r *fakeTelegramRepo) GetOpenReportForMatch(_ context.Context, matchID int) (*models.TelegramMatchReport, error) {
	for i := len(r.reports) - 1; i >= 0; i-- {
		rep := r.reports[i]
		if rep.BracketMatchID != nil && *rep.BracketMatchID == matchID && rep.Open() {
			return &rep, nil
		}
	}
	return nil, nil
}
func (r *fakeTelegramRepo) GetExpiredPendingReports(_ context.Context, now time.Time) ([]models.TelegramMatchReport, error) {
	var out []models.TelegramMatchReport
	for _, rep := range r.reports {
		if rep.Status == models.ReportPending && rep.ExpiresAt != nil && !rep.ExpiresAt.After(now) {
			out = append(out, rep)
		}
	}
	return out, nil
}
func (r *fakeTelegramRepo) SetReportStatus(_ context.Context, id int, status string) error {
	for i := range r.reports {
		if r.reports[i].ID == id {
			r.reports[i].Status = status
			if status != models.ReportPending {
				now := time.Now()
				r.reports[i].ResolvedAt = &now
			}
		}
	}
	return nil
}

func (r *fakeTelegramRepo) ReplaceBracketMatches(_ context.Context, ms []models.BracketMatch) error {
	prev := map[int64]models.BracketMatch{}
	for _, m := range r.bracket {
		prev[m.ChallongeMatchID] = m
	}
	r.bracket = nil
	for i, m := range ms {
		m.ID = 1000 + i
		if old, ok := prev[m.ChallongeMatchID]; ok {
			m.ID = old.ID
			if samePair(old, m) {
				m.BothNotified = old.BothNotified
			}
		}
		if m.Team1ID != nil {
			if t := r.teams[*m.Team1ID]; t != nil {
				m.Team1Name = t.Name
			}
		}
		if m.Team2ID != nil {
			if t := r.teams[*m.Team2ID]; t != nil {
				m.Team2Name = t.Name
			}
		}
		r.bracket = append(r.bracket, m)
	}
	return nil
}

// samePair mirrors the SQL rule: both_notified survives only while the two
// slots hold the same teams.
func samePair(a, b models.BracketMatch) bool {
	eq := func(x, y *int) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	return eq(a.Team1ID, b.Team1ID) && eq(a.Team2ID, b.Team2ID)
}

func (r *fakeTelegramRepo) GetBracketMatches(context.Context) ([]models.BracketMatch, error) {
	out := make([]models.BracketMatch, len(r.bracket))
	copy(out, r.bracket)
	return out, nil
}
func (r *fakeTelegramRepo) MarkBracketNotified(_ context.Context, ids []int) error {
	for _, id := range ids {
		for i := range r.bracket {
			if r.bracket[i].ID == id {
				r.bracket[i].BothNotified = true
			}
		}
	}
	return nil
}
func (r *fakeTelegramRepo) SetTeamParticipantID(_ context.Context, teamID int, pid int64) error {
	r.participants[teamID] = pid
	if t := r.teams[teamID]; t != nil {
		p := pid
		t.ChallongeParticipantID = &p
	}
	return nil
}
func (r *fakeTelegramRepo) ClearTeamParticipantIDs(context.Context) error {
	r.participants = map[int]int64{}
	for _, t := range r.teams {
		t.ChallongeParticipantID = nil
	}
	return nil
}
func (r *fakeTelegramRepo) SetReportSynced(_ context.Context, id int) error {
	for i := range r.reports {
		if r.reports[i].ID == id {
			now := time.Now()
			r.reports[i].SyncedAt = &now
		}
	}
	return nil
}
func (r *fakeTelegramRepo) GetUnsyncedReports(context.Context) ([]models.TelegramMatchReport, error) {
	var out []models.TelegramMatchReport
	for _, rep := range r.reports {
		if rep.BracketMatchID != nil && rep.SyncedAt == nil && (rep.Status == models.ReportConfirmed || rep.Status == models.ReportAutoConfirmed) {
			out = append(out, rep)
		}
	}
	return out, nil
}

func (r *fakeTelegramRepo) ReplaceBracketMatchesForTournament(ctx context.Context, tournamentID int, matches []models.BracketMatch) error {
	return r.ReplaceBracketMatches(ctx, matches)
}

func (r *fakeTelegramRepo) GetBracketMatchesForTournament(ctx context.Context, tournamentID int) ([]models.BracketMatch, error) {
	return r.GetBracketMatches(ctx)
}

func (r *fakeTelegramRepo) CreateTournament(ctx context.Context, t *models.TelegramTournament) (*models.TelegramTournament, error) {
	t.ID = len(r.tournaments) + 1
	r.tournaments = append(r.tournaments, t)
	return t, nil
}

func (r *fakeTelegramRepo) GetActiveTournament(ctx context.Context) (*models.TelegramTournament, error) {
	for _, t := range r.tournaments {
		if t.IsActive {
			return t, nil
		}
	}
	return nil, nil
}

func (r *fakeTelegramRepo) GetTournamentByID(ctx context.Context, id int) (*models.TelegramTournament, error) {
	for _, t := range r.tournaments {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

func (r *fakeTelegramRepo) GetAllTournaments(ctx context.Context) ([]models.TelegramTournament, error) {
	out := make([]models.TelegramTournament, len(r.tournaments))
	for i, t := range r.tournaments {
		out[i] = *t
	}
	return out, nil
}

func (r *fakeTelegramRepo) SetActiveTournament(ctx context.Context, id int) error {
	for _, t := range r.tournaments {
		t.IsActive = (t.ID == id)
	}
	return nil
}

func (r *fakeTelegramRepo) UpdateTournament(ctx context.Context, t *models.TelegramTournament) error {
	for i, existing := range r.tournaments {
		if existing.ID == t.ID {
			r.tournaments[i] = t
			return nil
		}
	}
	return nil
}

func (r *fakeTelegramRepo) UpdateTournamentStatus(ctx context.Context, id int, status string) error {
	for _, t := range r.tournaments {
		if t.ID == id {
			t.Status = status
			return nil
		}
	}
	return nil
}

func (r *fakeTelegramRepo) RegisterTeamForTournament(ctx context.Context, tournamentID, teamID int) error {
	key := fmt.Sprintf("%d:%d", tournamentID, teamID)
	r.tournamentTeams[key] = &models.TournamentTeam{
		TournamentID: tournamentID,
		TeamID:       teamID,
		Status:       "registered",
	}
	return nil
}

func (r *fakeTelegramRepo) UnregisterTeamFromTournament(ctx context.Context, tournamentID, teamID int) error {
	key := fmt.Sprintf("%d:%d", tournamentID, teamID)
	delete(r.tournamentTeams, key)
	return nil
}

func (r *fakeTelegramRepo) GetTournamentTeams(ctx context.Context, tournamentID int) ([]models.TelegramTeam, error) {
	var ids []int
	for id := range r.teams {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var out []models.TelegramTeam
	for _, id := range ids {
		t := r.teams[id]
		teamCopy := *t
		key := fmt.Sprintf("%d:%d", tournamentID, t.ID)
		if tt, ok := r.tournamentTeams[key]; ok {
			teamCopy.IsCheckedIn = tt.IsCheckedIn
		}
		teamCopy.Players, _ = r.GetTeamMembers(ctx, t.ID)
		out = append(out, teamCopy)
	}
	return out, nil
}

func (r *fakeTelegramRepo) GetTournamentTeam(ctx context.Context, tournamentID, teamID int) (*models.TournamentTeam, error) {
	key := fmt.Sprintf("%d:%d", tournamentID, teamID)
	return r.tournamentTeams[key], nil
}

func (r *fakeTelegramRepo) SetTournamentCheckIn(ctx context.Context, tournamentID, teamID int, status bool) error {
	key := fmt.Sprintf("%d:%d", tournamentID, teamID)
	if tt, ok := r.tournamentTeams[key]; ok {
		tt.IsCheckedIn = status
	}
	if t, ok := r.teams[teamID]; ok {
		t.IsCheckedIn = status
	}
	return nil
}

func (r *fakeTelegramRepo) SetTournamentTeamStatus(ctx context.Context, tournamentID, teamID int, status string) error {
	key := fmt.Sprintf("%d:%d", tournamentID, teamID)
	if tt, ok := r.tournamentTeams[key]; ok {
		tt.Status = status
	}
	if t, ok := r.teams[teamID]; ok {
		t.Status = status
	}
	return nil
}

func (r *fakeTelegramRepo) SetTournamentTeamParticipantID(ctx context.Context, tournamentID, teamID int, participantID int64) error {
	key := fmt.Sprintf("%d:%d", tournamentID, teamID)
	if tt, ok := r.tournamentTeams[key]; ok {
		tt.ChallongeParticipantID = &participantID
	}
	return r.SetTeamParticipantID(ctx, teamID, participantID)
}

func (r *fakeTelegramRepo) ClearTournamentTeamParticipantIDs(ctx context.Context, tournamentID int) error {
	for _, tt := range r.tournamentTeams {
		if tt.TournamentID == tournamentID {
			tt.ChallongeParticipantID = nil
		}
	}
	return r.ClearTeamParticipantIDs(ctx)
}

func (r *fakeTelegramRepo) UpdateTournamentPlacements(ctx context.Context, tournamentID int, placements map[int]int, points map[int]int) error {
	for teamID, placement := range placements {
		key := fmt.Sprintf("%d:%d", tournamentID, teamID)
		if tt, ok := r.tournamentTeams[key]; ok {
			p := placement
			tt.Placement = &p
			if pts, okPts := points[teamID]; okPts {
				tt.Points = pts
			}
		}
	}
	return nil
}

func (r *fakeTelegramRepo) GetLeagueStandings(ctx context.Context) ([]models.LeagueStanding, error) {
	return nil, nil
}

func newTelegramSvc() (*TelegramServiceImpl, *fakeTelegramRepo) {
	repo := newFakeTelegramRepo()
	return NewTelegramServiceImpl(repo, nopLogger{}), repo
}

// A solo player (no team) typing /edit_player 1 used to be put into the edit
// state anyway; their next message dereferenced a nil team id and took the
// whole process down.
func TestEditPlayerWithoutTeamIsRefused(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateIdle)

	resp, _ := svc.StartEditPlayer(context.Background(), 1, 1)

	if !strings.Contains(resp, "не в команде") {
		t.Errorf("StartEditPlayer without team = %q, want refusal", resp)
	}
	if p.FSMState != models.StateIdle {
		t.Errorf("state = %q, want idle", p.FSMState)
	}
	// Whatever the state, the next message must not crash.
	svc.HandleUserInput(context.Background(), 1, "Nick")
}

// Only the captain may edit the roster.
func TestEditPlayerRequiresCaptain(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	repo.addPlayer(1, &team, false, models.StateIdle)

	resp, _ := svc.StartEditPlayer(context.Background(), 1, 1)
	if !strings.Contains(resp, "капитан") {
		t.Errorf("resp = %q, want captain-only refusal", resp)
	}
}

// If the team disappears (admin /del_team, or /delete_team mid-flow) while the
// captain is still in a team_reg_* state, the next message must drop them back
// to the menu instead of dereferencing a nil team id.
func TestTeamRegistrationAbortsWhenTeamIsGone(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, true, "team_line_2")

	resp, _ := svc.HandleUserInput(context.Background(), 1, "Nick")

	if !strings.Contains(resp, "удалена") {
		t.Errorf("resp = %q, want notice that the team is gone", resp)
	}
	if p.FSMState != models.StateIdle {
		t.Errorf("state = %q, want idle", p.FSMState)
	}
}

// A captain who already has a team must not be able to spawn a second one:
// the old team was left orphaned with its roster and the captain's team_id
// silently repointed.
func TestTeamRegistrationRefusedForExistingCaptain(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "Alpha"}
	p := repo.addPlayer(1, &team, true, models.StateIdle)

	resp, _ := svc.StartTeamRegistration(context.Background(), 1)

	if !strings.Contains(resp, "Alpha") || !strings.Contains(resp, "/delete_team") {
		t.Errorf("resp = %q, want refusal naming the team and /delete_team", resp)
	}
	if p.FSMState != models.StateIdle {
		t.Errorf("state = %q, want idle", p.FSMState)
	}
}

// Solo registration for someone who is in a team would overwrite their team
// profile while never showing them in the solo list (team_id is not null).
func TestSoloRegistrationRefusedForTeamMember(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "Alpha"}
	p := repo.addPlayer(1, &team, true, models.StateIdle)

	resp, _ := svc.StartSoloRegistration(context.Background(), 1)

	if !strings.Contains(resp, "Alpha") {
		t.Errorf("resp = %q, want refusal naming the team", resp)
	}
	if p.FSMState != models.StateIdle {
		t.Errorf("state = %q, want idle", p.FSMState)
	}
}

// /checkin toggles, so the reply must say which way it went — a captain who
// tapped twice used to un-check silently and get a technical defeat.
func TestCheckInReplyStatesResult(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	repo.addPlayer(1, &team, true, models.StateIdle)
	for i := 2; i <= 5; i++ {
		repo.members = append(repo.members, &models.TelegramPlayer{
			ID: i, TeamID: &team, GameNickname: fmt.Sprintf("P%d", i), MainRole: "Mid",
		})
	}

	on := svc.ToggleCheckIn(context.Background(), 1)
	if !strings.Contains(on, "подтверждён") || !strings.Contains(on, "A") {
		t.Errorf("first /checkin = %q, want confirmation naming the team", on)
	}
	off := svc.ToggleCheckIn(context.Background(), 1)
	if !strings.Contains(off, "снят") {
		t.Errorf("second /checkin = %q, want notice that check-in was removed", off)
	}
}

func TestCheckInRefusedOnIncompleteTeam(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	repo.addPlayer(1, &team, true, models.StateIdle)

	resp := svc.ToggleCheckIn(context.Background(), 1)
	if !strings.Contains(resp, "невозможен") || !strings.Contains(resp, "1 из 5") {
		t.Errorf("ToggleCheckIn on incomplete team = %q, want refusal", resp)
	}
	if repo.teams[team].IsCheckedIn {
		t.Errorf("team was checked in despite incomplete roster")
	}

	resp, kb := svc.HandleRegAction(context.Background(), 1, "checkin", "")
	if !strings.Contains(resp, "невозможен") || !strings.Contains(resp, "1 из 5") || kb != KbNone {
		t.Errorf("confirmCheckIn on incomplete team = %q kb = %q, want refusal", resp, kb)
	}

	// Add players up to 5
	for i := 2; i <= 5; i++ {
		repo.members = append(repo.members, &models.TelegramPlayer{
			ID: i, TeamID: &team, GameNickname: fmt.Sprintf("P%d", i), MainRole: "Mid",
		})
	}

	resp = svc.ToggleCheckIn(context.Background(), 1)
	if !strings.Contains(resp, "подтверждён") {
		t.Errorf("ToggleCheckIn with 5 players = %q, want success", resp)
	}
}

// Names longer than the column (64) used to fail in the DB and come back as
// "this name is taken".
func TestTeamNameLengthIsValidated(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateWaitingTeamName)

	resp, kb := svc.HandleUserInput(context.Background(), 1, strings.Repeat("x", 65))

	if !strings.Contains(resp, "64") || kb != KbRegCancel {
		t.Errorf("resp = %q kb = %q, want length hint with cancel keyboard", resp, kb)
	}
	if p.FSMState != models.StateWaitingTeamName || len(repo.teams) != 0 {
		t.Errorf("state = %q teams = %d, want still waiting and no team created", p.FSMState, len(repo.teams))
	}
}

func TestGetCheckInStatus(t *testing.T) {
	svc, repo := newTelegramSvc()

	// Empty state
	if got := svc.GetCheckInStatus(context.Background()); got != "Команд пока нет." {
		t.Errorf("GetCheckInStatus() on empty repo = %q, want %q", got, "Команд пока нет.")
	}

	// 1. Checked-in team (5 players)
	t1 := 1
	repo.teams[t1] = &models.TelegramTeam{ID: t1, Name: "Navi", IsCheckedIn: true}
	c1 := repo.addPlayer(101, &t1, true, models.StateIdle)
	c1.GameNickname = "Dendi"
	c1.TelegramUsername = "dendi_tg"
	for i := 2; i <= 5; i++ {
		repo.members = append(repo.members, &models.TelegramPlayer{
			ID: 100 + i, TeamID: &t1, GameNickname: fmt.Sprintf("NaviP%d", i),
		})
	}

	// 2. Pending team (5 players)
	t2 := 2
	repo.teams[t2] = &models.TelegramTeam{ID: t2, Name: "VirtusPro", IsCheckedIn: false}
	c2 := repo.addPlayer(201, &t2, true, models.StateIdle)
	c2.GameNickname = "Solo"
	c2.TelegramUsername = "solo_322"
	for i := 2; i <= 5; i++ {
		repo.members = append(repo.members, &models.TelegramPlayer{
			ID: 200 + i, TeamID: &t2, GameNickname: fmt.Sprintf("VPP%d", i),
		})
	}

	// 3. Incomplete team (1 player)
	t3 := 3
	repo.teams[t3] = &models.TelegramTeam{ID: t3, Name: "SoloSquad", IsCheckedIn: false}
	c3 := repo.addPlayer(301, &t3, true, models.StateIdle)
	c3.GameNickname = "Lonely"
	c3.TelegramUsername = "lonely_guy"

	// 4. Disqualified team
	t4 := 4
	repo.teams[t4] = &models.TelegramTeam{ID: t4, Name: "Cheaters", Status: models.TeamStatusDisqualified}
	repo.addPlayer(401, &t4, true, models.StateIdle)

	dashboard := svc.GetCheckInStatus(context.Background())

	// Assert dashboard contents
	expectedSnippets := []string{
		"Дашборд Check-in:",
		"Готовы к игре: 1 из 2 (50%)",
		"Ожидают подтверждения: 1",
		"Не укомплектованы: 1",
		"Подтвердили участие (1):",
		"[+] Navi — Кап: Dendi (@dendi_tg) (5 чел.)",
		"Не подтвердили (1):",
		"[-] VirtusPro — Кап: Solo (@solo_322) (5 чел.)",
		"Неполный состав (1):",
		"[!] SoloSquad — Кап: Lonely (@lonely_guy) (состав: 1/5)",
		"Дисквалифицированы (1):",
		"[ТП] Cheaters",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(dashboard, snippet) {
			t.Errorf("GetCheckInStatus() missing snippet %q\nFull output:\n%s", snippet, dashboard)
		}
	}
}

func TestUpdateTeamPlayerRosterLock(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()

	teamID := 1
	repo.teams[teamID] = &models.TelegramTeam{ID: teamID, Name: "LockTest", IsCheckedIn: false}
	c := repo.addPlayer(100, &teamID, true, models.StateIdle)
	c.IsCaptain = true
	p := repo.addPlayer(101, &teamID, false, models.StateIdle)

	// 1. Success when registration is open and not checked in
	err := svc.UpdateTeamPlayer(ctx, 100, p.ID, "NewNick", "12345", "1001", "Mid")
	if err != nil {
		t.Fatalf("expected update to succeed, got: %v", err)
	}

	// 2. Blocked when checked in
	repo.teams[teamID].IsCheckedIn = true
	err = svc.UpdateTeamPlayer(ctx, 100, p.ID, "AnotherNick", "12345", "1001", "Mid")
	if err == nil || !strings.Contains(err.Error(), "Check-in") {
		t.Fatalf("expected error containing 'Check-in', got: %v", err)
	}

	// 3. Blocked when registration is closed
	repo.teams[teamID].IsCheckedIn = false
	svc.SetRegistrationOpen(ctx, false)
	err = svc.UpdateTeamPlayer(ctx, 100, p.ID, "AnotherNick", "12345", "1001", "Mid")
	if err == nil || !strings.Contains(err.Error(), "регистрация на турнир закрыта") {
		t.Fatalf("expected error containing 'регистрация на турнир закрыта', got: %v", err)
	}
}

func TestSetWinnerRollbackAndChangeWinner(t *testing.T) {
	svc, repo := newTelegramSvc()
	ctx := context.Background()

	t1ID, t2ID, t3ID, t4ID := 1, 2, 3, 4
	repo.teams[t1ID] = &models.TelegramTeam{ID: t1ID, Name: "TeamAlpha"}
	repo.teams[t2ID] = &models.TelegramTeam{ID: t2ID, Name: "TeamBeta"}
	repo.teams[t3ID] = &models.TelegramTeam{ID: t3ID, Name: "TeamGamma"}
	repo.teams[t4ID] = &models.TelegramTeam{ID: t4ID, Name: "TeamDelta"}

	// Single elimination 4-team bracket:
	// Round 1: Match 1 (Alpha vs Beta), Match 2 (Gamma vs Delta)
	// Round 2: Match 3 (Winner M1 vs Winner M2)
	repo.bracket = []models.BracketMatch{
		{ID: 1, Round: 1, PlayOrder: 1, Team1ID: &t1ID, Team2ID: &t2ID, Team1Name: "TeamAlpha", Team2Name: "TeamBeta", State: models.BracketOpen},
		{ID: 2, Round: 1, PlayOrder: 2, Team1ID: &t3ID, Team2ID: &t4ID, Team1Name: "TeamGamma", Team2Name: "TeamDelta", State: models.BracketOpen},
		{ID: 3, Round: 2, PlayOrder: 3, State: models.BracketPending},
	}

	// 1. SetWinnerDirect: TeamAlpha wins Match 1
	err := svc.SetWinnerDirect(ctx, 1, "TeamAlpha", 2, 0)
	if err != nil {
		t.Fatalf("SetWinnerDirect failed: %v", err)
	}
	if repo.bracket[0].State != models.BracketComplete || *repo.bracket[0].WinnerID != t1ID {
		t.Fatalf("expected Match 1 complete with Alpha winner, got state %s, winner %v", repo.bracket[0].State, repo.bracket[0].WinnerID)
	}
	if repo.bracket[2].Team1ID == nil || *repo.bracket[2].Team1ID != t1ID {
		t.Fatalf("expected Alpha propagated to Match 3 Team1ID, got: %v", repo.bracket[2].Team1ID)
	}

	// 2. ChangeWinnerDirect: Admin realized TeamBeta actually won (e.g. dispute / misclick)
	err = svc.ChangeWinnerDirect(ctx, 1, "TeamBeta", 2, 1)
	if err != nil {
		t.Fatalf("ChangeWinnerDirect failed: %v", err)
	}
	if *repo.bracket[0].WinnerID != t2ID || repo.bracket[0].ScoresCSV != "2-1" {
		t.Fatalf("expected Match 1 updated to TeamBeta winner (2-1), got %v (%s)", repo.bracket[0].WinnerID, repo.bracket[0].ScoresCSV)
	}
	if repo.bracket[2].Team1ID == nil || *repo.bracket[2].Team1ID != t2ID {
		t.Fatalf("expected Match 3 Team1ID safely swapped to TeamBeta, got: %v", repo.bracket[2].Team1ID)
	}

	// 3. RollbackMatch: Admin resets Match 1
	err = svc.RollbackMatch(ctx, 1)
	if err != nil {
		t.Fatalf("RollbackMatch failed: %v", err)
	}
	if repo.bracket[0].State != models.BracketOpen || repo.bracket[0].WinnerID != nil || repo.bracket[0].ScoresCSV != "" {
		t.Fatalf("expected Match 1 reset to BracketOpen without winner/score, got: %+v", repo.bracket[0])
	}
	if repo.bracket[2].Team1ID != nil {
		t.Fatalf("expected Match 3 Team1ID revoked back to nil, got: %v", repo.bracket[2].Team1ID)
	}

	// 4. Downstream safety check: if next round match is already complete, rollback must be rejected
	_ = svc.SetWinnerDirect(ctx, 1, "TeamAlpha", 2, 0)
	repo.bracket[2].State = models.BracketComplete
	err = svc.RollbackMatch(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "следующего раунда уже завершён") {
		t.Fatalf("expected rollback error when downstream match complete, got: %v", err)
	}
}


