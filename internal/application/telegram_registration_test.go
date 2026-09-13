package application

import (
	"blackwatch/internal/models"
	"context"
	"strings"
	"testing"
)

// drive sends one text message and returns the reply.
func drive(t *testing.T, svc *TelegramServiceImpl, tg int64, text string) (string, string) {
	t.Helper()
	return svc.HandleUserInput(context.Background(), tg, text)
}

// act presses one inline button.
func act(t *testing.T, svc *TelegramServiceImpl, tg int64, action, arg string) (string, string) {
	t.Helper()
	return svc.HandleRegAction(context.Background(), tg, action, arg)
}

func TestTeamRegistrationHappyPath(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateIdle)

	_, kb := svc.StartTeamRegistration(context.Background(), 1)
	if kb != KbRegCancel || p.FSMState != models.StateWaitingTeamName {
		t.Fatalf("start: kb=%q state=%q", kb, p.FSMState)
	}

	resp, kb := drive(t, svc, 1, "Alpha")
	if p.FSMState != "team_line_1" || kb != KbRegCancel || !strings.Contains(resp, "1/7") {
		t.Fatalf("after name: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}

	resp, kb = drive(t, svc, 1, "Cap 111111111 1111 30")
	if p.FSMState != "team_role_1" || kb != KbRegRoles || !strings.Contains(resp, "Cap") {
		t.Fatalf("after captain line: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	if p.GameNickname != "Cap" || p.GameID != "111111111" || p.ZoneID != "1111" || p.Stars != 30 {
		t.Fatalf("captain row not saved: %+v", p)
	}

	resp, _ = act(t, svc, 1, "role", "Mid")
	if p.MainRole != "Mid" || p.FSMState != "team_line_2" || !strings.Contains(resp, "2/7") {
		t.Fatalf("after captain role: role=%q state=%q resp=%q", p.MainRole, p.FSMState, resp)
	}

	for slot := 2; slot <= 5; slot++ {
		drive(t, svc, 1, "P 222222222 2222 10 @p")
		act(t, svc, 1, "role", "Gold")
	}
	if p.FSMState != "team_line_6" || len(repo.members) != 4 {
		t.Fatalf("after main five: state=%q members=%d", p.FSMState, len(repo.members))
	}
	if m := repo.members[0]; m.GameID != "222222222" || m.Stars != 10 || m.TelegramUsername != "@p" || m.MainRole != "Gold" || m.IsSubstitute {
		t.Fatalf("teammate row: %+v", m)
	}

	drive(t, svc, 1, "S 333333333 3333 5")
	act(t, svc, 1, "role", "Roam")
	if !repo.members[4].IsSubstitute || p.FSMState != "team_line_7" {
		t.Fatalf("slot 6: sub=%v state=%q", repo.members[4].IsSubstitute, p.FSMState)
	}

	resp, kb = act(t, svc, 1, "skip", "")
	if p.FSMState != models.StateTeamConfirm || !strings.HasPrefix(kb, KbRegConfirm+":6") || !strings.Contains(resp, "Alpha") || !strings.Contains(resp, "Cap") {
		t.Fatalf("card: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}

	resp, kb = act(t, svc, 1, "confirm", "")
	if p.FSMState != models.StateIdle || kb != "main_menu" || !strings.Contains(resp, "/checkin") {
		t.Fatalf("confirm: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
}

func TestTeamSkipOnSlotSixGoesToSeven(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_6")

	_, kb := act(t, svc, 1, "skip", "")
	if p.FSMState != "team_line_7" || kb != KbRegSkip {
		t.Errorf("state=%q kb=%q, want team_line_7 with skip keyboard", p.FSMState, kb)
	}
}

func TestSkipIsRefusedOnMainSlots(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_3")

	resp, _ := act(t, svc, 1, "skip", "")
	if p.FSMState != "team_line_3" || !strings.Contains(resp, "больше не действует") {
		t.Errorf("state=%q resp=%q", p.FSMState, resp)
	}
}

func TestBadLineKeepsStateAndExplains(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_2")

	resp, kb := drive(t, svc, 1, "Vasya 123")
	if p.FSMState != "team_line_2" || kb != KbRegCancel || !strings.Contains(resp, "Zone ID") || !strings.Contains(resp, playerLineFormat) {
		t.Errorf("state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	if len(repo.members) != 0 {
		t.Errorf("a row was created from a bad line")
	}
}

func TestRoleMustBeWhitelisted(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_role_1")

	_, kb := act(t, svc, 1, "role", "Hacker")
	if p.FSMState != "team_role_1" || p.MainRole != "" || kb != KbRegRoles {
		t.Errorf("state=%q role=%q kb=%q", p.FSMState, p.MainRole, kb)
	}
	// Typed role text is accepted as a fallback for clients without inline buttons.
	drive(t, svc, 1, "Mid")
	if p.MainRole != "Mid" || p.FSMState != "team_line_2" {
		t.Errorf("typed role: role=%q state=%q", p.MainRole, p.FSMState)
	}
}

func TestTextInButtonStateReshowsButtons(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, models.StateTeamConfirm)

	resp, kb := drive(t, svc, 1, "ну чо")
	if p.FSMState != models.StateTeamConfirm || !strings.HasPrefix(kb, KbRegConfirm) || !strings.Contains(resp, "A") {
		t.Errorf("state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
}

func TestFixFromConfirmReturnsToCard(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, models.StateTeamConfirm)
	p.GameNickname = "Cap"
	_ = repo.CreateTeammate(context.Background(), &models.TelegramPlayer{TeamID: &team, GameNickname: "Old", GameID: "1", ZoneID: "1", MainRole: "Gold"})

	resp, kb := act(t, svc, 1, "fix", "2")
	if p.FSMState != "team_fix_2" || kb != KbRegCancel || !strings.Contains(resp, "Old") {
		t.Fatalf("fix: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	drive(t, svc, 1, "New 999999999 9999 50")
	if p.FSMState != "team_fixrole_2" || repo.members[0].GameNickname != "New" || repo.members[0].Stars != 50 {
		t.Fatalf("fix line: state=%q row=%+v", p.FSMState, repo.members[0])
	}
	_, kb = act(t, svc, 1, "role", "Exp")
	if p.FSMState != models.StateTeamConfirm || repo.members[0].MainRole != "Exp" || !strings.HasPrefix(kb, KbRegConfirm) {
		t.Fatalf("fix role: state=%q role=%q kb=%q", p.FSMState, repo.members[0].MainRole, kb)
	}
	if len(repo.members) != 1 {
		t.Errorf("fix created a new row instead of updating")
	}
}

func TestFixOutOfRangeIsRefused(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, models.StateTeamConfirm)

	for _, arg := range []string{"0", "9", "abc", ""} {
		act(t, svc, 1, "fix", arg)
		if p.FSMState != models.StateTeamConfirm {
			t.Errorf("fix %q moved state to %q", arg, p.FSMState)
		}
	}
}

func TestMyTeamCardAndEditAfterConfirm(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, models.StateIdle)
	p.GameNickname = "Cap"
	_ = repo.CreateTeammate(context.Background(), &models.TelegramPlayer{TeamID: &team, GameNickname: "Two"})

	resp, kb := svc.GetTeamInfo(context.Background(), 1)
	if kb != KbRegCard+":2:player" || !strings.Contains(resp, "Cap") || !strings.Contains(resp, "Two") {
		t.Fatalf("card: kb=%q resp=%q", kb, resp)
	}

	act(t, svc, 1, "fix", "2")
	if p.FSMState != "team_edit_2" {
		t.Fatalf("edit: state=%q", p.FSMState)
	}
	drive(t, svc, 1, "Two2 222222222 2222 12")
	_, kb = act(t, svc, 1, "role", "Jungle")
	if p.FSMState != models.StateIdle || !strings.HasPrefix(kb, KbRegCard) || repo.members[0].GameNickname != "Two2" {
		t.Fatalf("edit done: state=%q kb=%q row=%+v", p.FSMState, kb, repo.members[0])
	}
}

func TestAddSubstituteFromCard(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, models.StateIdle)
	for i := 0; i < 5; i++ {
		_ = repo.CreateTeammate(context.Background(), &models.TelegramPlayer{TeamID: &team, GameNickname: "M", IsSubstitute: i == 4})
	}

	_, kb := svc.GetTeamInfo(context.Background(), 1)
	if kb != KbRegCard+":6:sub" {
		t.Fatalf("card kb=%q, want one free sub slot", kb)
	}
	act(t, svc, 1, "sub", "")
	if p.FSMState != "team_edit_7" {
		t.Fatalf("sub: state=%q", p.FSMState)
	}
	drive(t, svc, 1, "Sub 777777777 7777 7")
	act(t, svc, 1, "role", "Gold")
	if len(repo.members) != 6 || !repo.members[5].IsSubstitute {
		t.Fatalf("sub row: n=%d last=%+v", len(repo.members), repo.members[len(repo.members)-1])
	}
	_, kb = svc.GetTeamInfo(context.Background(), 1)
	if kb != KbRegCard+":7:" {
		t.Errorf("full roster kb=%q, want no add button", kb)
	}
}

func TestCancelMidRegistrationKeepsTeam(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_3")

	resp, kb := act(t, svc, 1, "cancel", "")
	if p.FSMState != models.StateIdle || kb != "main_menu" || !strings.Contains(resp, "/my_team") {
		t.Errorf("state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	if _, ok := repo.teams[team]; !ok {
		t.Error("cancel deleted the team")
	}
}

// Typed "Отмена" behaves like the button.
func TestTypedCancelDuringRegistration(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateSoloLine)

	_, kb := drive(t, svc, 1, "Отмена")
	if p.FSMState != models.StateIdle || kb != "main_menu" {
		t.Errorf("state=%q kb=%q", p.FSMState, kb)
	}
}

func TestDeleteFromCard(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, models.StateTeamConfirm)

	_, kb := act(t, svc, 1, "delete", "")
	if _, ok := repo.teams[team]; ok || kb != "main_menu" || p.FSMState != models.StateIdle {
		t.Errorf("team still there=%v kb=%q state=%q", ok, kb, p.FSMState)
	}
}

func TestCallbackOutsideItsStateIsInert(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_2")

	resp, kb := act(t, svc, 1, "confirm", "")
	if p.FSMState != "team_line_2" || kb != KbNone || !strings.Contains(resp, "больше не действует") {
		t.Errorf("state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
}

func TestSoloHappyPath(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateIdle)

	_, kb := svc.StartSoloRegistration(context.Background(), 1)
	if p.FSMState != models.StateSoloLine || kb != KbRegCancel {
		t.Fatalf("start: state=%q kb=%q", p.FSMState, kb)
	}
	_, kb = drive(t, svc, 1, "Solo 123456789 1234 40")
	if p.FSMState != models.StateSoloRole || kb != KbRegRoles || p.GameNickname != "Solo" {
		t.Fatalf("line: state=%q kb=%q nick=%q", p.FSMState, kb, p.GameNickname)
	}
	resp, kb := act(t, svc, 1, "role", "Roam")
	if p.FSMState != models.StateSoloConfirm || kb != KbRegSoloConfirm || !strings.Contains(resp, "Solo") {
		t.Fatalf("role: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	_, _ = act(t, svc, 1, "fix", "")
	if p.FSMState != models.StateSoloLine {
		t.Fatalf("fix: state=%q", p.FSMState)
	}
	drive(t, svc, 1, "Solo2 123456789 1234 41")
	act(t, svc, 1, "role", "Roam")
	_, kb = act(t, svc, 1, "confirm", "")
	if p.FSMState != models.StateIdle || kb != "main_menu" || p.GameNickname != "Solo2" {
		t.Fatalf("confirm: state=%q kb=%q nick=%q", p.FSMState, kb, p.GameNickname)
	}
}

func TestLegacyStatesResetToIdle(t *testing.T) {
	svc, repo := newTelegramSvc()
	for _, st := range []string{"team_reg_nick_3", "edit_player_id_1", "waiting_stars"} {
		p := repo.addPlayer(1, nil, false, st)
		resp, _ := drive(t, svc, 1, "что-то")
		if p.FSMState != models.StateIdle || !strings.Contains(resp, "заново") {
			t.Errorf("%s: state=%q resp=%q", st, p.FSMState, resp)
		}
	}
}
