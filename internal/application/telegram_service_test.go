package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

// fakeTelegramRepo is an in-memory stand-in for repository.Telegram, just
// enough to drive the registration state machine.
type fakeTelegramRepo struct {
	players  map[int64]*models.TelegramPlayer // by telegram id
	teams    map[int]*models.TelegramTeam
	members  []*models.TelegramPlayer // teammates created via CreateTeammate
	settings map[string]string
	nextID   int
}

func newFakeTelegramRepo() *fakeTelegramRepo {
	return &fakeTelegramRepo{
		players:  map[int64]*models.TelegramPlayer{},
		teams:    map[int]*models.TelegramTeam{},
		settings: map[string]string{},
		nextID:   1,
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
func (r *fakeTelegramRepo) GetTeamByName(context.Context, string) (*models.TelegramTeam, error) {
	return nil, errors.New("no team")
}
func (r *fakeTelegramRepo) DeleteTeam(_ context.Context, id int) error {
	delete(r.teams, id)
	return nil
}
func (r *fakeTelegramRepo) GetAllTeams(context.Context) ([]models.TelegramTeam, error) {
	return nil, nil
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
func (r *fakeTelegramRepo) ReleaseTeamMembers(context.Context, int) error { return nil }
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
	r.settings[k] = v
	return nil
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

	on := svc.ToggleCheckIn(context.Background(), 1)
	if !strings.Contains(on, "подтверждён") || !strings.Contains(on, "A") {
		t.Errorf("first /checkin = %q, want confirmation naming the team", on)
	}
	off := svc.ToggleCheckIn(context.Background(), 1)
	if !strings.Contains(off, "снят") {
		t.Errorf("second /checkin = %q, want notice that check-in was removed", off)
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
