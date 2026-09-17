package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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
	if p.FSMState != "team_line_1" || kb != KbRegCancel || !strings.Contains(resp, "1/5") {
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
	if p.MainRole != "Mid" || p.FSMState != "team_line_2" || !strings.Contains(resp, "2/5") {
		t.Fatalf("after captain role: role=%q state=%q resp=%q", p.MainRole, p.FSMState, resp)
	}

	for slot := 2; slot <= 4; slot++ {
		drive(t, svc, 1, fmt.Sprintf("P 22222222%d 2222 10 @p", slot))
		act(t, svc, 1, "role", "Gold")
	}
	// Slot 5 is the last of the main roster
	drive(t, svc, 1, "P5 222222225 2222 10 @p")
	resp, kb = act(t, svc, 1, "role", "Gold")

	// Main 5 players complete: should immediately transition to confirmation card without forcing substitutes
	if p.FSMState != models.StateTeamConfirm || !strings.HasPrefix(kb, KbRegConfirm+":5") || !strings.Contains(resp, "Alpha") || !strings.Contains(resp, "Cap") {
		t.Fatalf("after main five: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	if len(repo.members) != 4 {
		t.Fatalf("expected 4 teammates + 1 captain, got %d members", len(repo.members))
	}

	// Captain confirms registration
	resp, kb = act(t, svc, 1, "confirm", "")
	if p.FSMState != models.StateIdle || kb != "main_menu" || !strings.Contains(resp, "/checkin") {
		t.Fatalf("confirm: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
}

func TestAddSubstituteFromConfirmCard(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "Alpha"}
	p := repo.addPlayer(1, &team, true, models.StateTeamConfirm)
	for i := 2; i <= 5; i++ {
		repo.members = append(repo.members, &models.TelegramPlayer{
			TeamID:   &team,
			GameID:   fmt.Sprintf("22222222%d", i),
			MainRole: "Gold",
		})
	}

	// Tap "+ Замена" from confirmation card
	resp, kb := act(t, svc, 1, "sub", "")
	if p.FSMState != "team_fix_6" || kb != KbRegCancel || !strings.Contains(resp, "Замена 1/2") {
		t.Fatalf("after sub button: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}

	// Cancel returns back to confirm card
	resp, kb = act(t, svc, 1, "cancel", "")
	if p.FSMState != models.StateTeamConfirm || !strings.HasPrefix(kb, KbRegConfirm+":5") {
		t.Fatalf("after cancel sub: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
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

func TestCancelOnCaptainSlotDeletesTeam(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_1")

	resp, kb := act(t, svc, 1, "cancel", "")
	if p.FSMState != models.StateIdle || kb != "main_menu" || !strings.Contains(resp, "освобождено") {
		t.Errorf("state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	if _, ok := repo.teams[team]; ok {
		t.Error("cancel on slot 1 did not delete the team")
	}
	if p.TeamID != nil {
		t.Errorf("captain still attached to team: %v", p.TeamID)
	}
}

func TestCancelOnTeammateSlotKeepsTeam(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_3")

	resp, kb := act(t, svc, 1, "cancel", "")
	if p.FSMState != models.StateIdle || kb != "main_menu" || !strings.Contains(resp, "/my_team") {
		t.Errorf("state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	if _, ok := repo.teams[team]; !ok {
		t.Error("cancel on slot 3 deleted the team, want kept")
	}
	if p.TeamID == nil {
		t.Error("captain detached from team, want kept")
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

// One accidental tap used to wipe a seven-player roster. Deleting now asks.
func TestDeleteAsksForConfirmation(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, models.StateTeamConfirm)

	resp, kb := act(t, svc, 1, "delete", "")
	if _, ok := repo.teams[team]; !ok || kb != KbRegDeleteConfirm || !strings.Contains(resp, "A") {
		t.Fatalf("delete: exists=%v kb=%q resp=%q", ok, kb, resp)
	}

	_, kb = act(t, svc, 1, "delete_no", "")
	if _, ok := repo.teams[team]; !ok || !strings.HasPrefix(kb, KbRegConfirm) || p.FSMState != models.StateTeamConfirm {
		t.Fatalf("delete_no: exists=%v kb=%q state=%q, want back on the card", ok, kb, p.FSMState)
	}

	act(t, svc, 1, "delete", "")
	_, kb = act(t, svc, 1, "delete_yes", "")
	if _, ok := repo.teams[team]; ok || kb != "main_menu" || p.FSMState != models.StateIdle {
		t.Errorf("delete_yes: exists=%v kb=%q state=%q", ok, kb, p.FSMState)
	}
}

// /delete_team goes through the same confirmation.
func TestDeleteCommandAsksToo(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	repo.addPlayer(1, &team, true, models.StateIdle)

	_, kb := act(t, svc, 1, "delete", "")
	if kb != KbRegDeleteConfirm {
		t.Fatalf("kb=%q", kb)
	}
	_, kb = act(t, svc, 1, "delete_no", "")
	if _, ok := repo.teams[team]; !ok || !strings.HasPrefix(kb, KbRegCard) {
		t.Errorf("delete_no from idle: exists=%v kb=%q, want the /my_team card", ok, kb)
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

// Registration closes itself an hour before the tournament; /open_reg forces
// it back open until /close_reg.
func TestRegistrationAutoClosesBeforeTournament(t *testing.T) {
	svc, repo := newTelegramSvc()
	repo.addPlayer(1, nil, false, models.StateIdle)
	start := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	svc.SetTournamentTime(context.Background(), start)

	svc.now = func() time.Time { return start.Add(-2 * time.Hour) }
	if open, _ := svc.RegistrationStatus(context.Background()); !open {
		t.Fatal("closed two hours before start")
	}

	svc.now = func() time.Time { return start.Add(-30 * time.Minute) }
	open, reason := svc.RegistrationStatus(context.Background())
	if open || !strings.Contains(reason, "час") {
		t.Fatalf("30 min before start: open=%v reason=%q", open, reason)
	}
	if resp, _ := svc.StartTeamRegistration(context.Background(), 1); !strings.Contains(resp, "час") {
		t.Errorf("StartTeamRegistration = %q, want the auto-close reason", resp)
	}

	svc.SetRegistrationOpen(context.Background(), true)
	if open, _ := svc.RegistrationStatus(context.Background()); !open {
		t.Error("/open_reg did not override the auto-close")
	}

	svc.SetRegistrationOpen(context.Background(), false)
	if open, _ := svc.RegistrationStatus(context.Background()); open {
		t.Error("/close_reg did not close")
	}
}

func TestRegistrationOpenWithoutTournamentTime(t *testing.T) {
	svc, _ := newTelegramSvc()
	if open, _ := svc.RegistrationStatus(context.Background()); !open {
		t.Error("closed with no tournament scheduled")
	}
}

// The 30-minute reminder carries a button; it must work whatever the captain
// is doing in the bot at that moment and must never un-check (unlike /checkin).
func TestCheckinButtonConfirmsFromAnyState(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_3")
	for i := 2; i <= 5; i++ {
		repo.members = append(repo.members, &models.TelegramPlayer{
			ID: i, TeamID: &team, GameNickname: fmt.Sprintf("P%d", i), MainRole: "Mid",
		})
	}

	for i := 0; i < 2; i++ {
		resp, kb := act(t, svc, 1, "checkin", "")
		if !repo.teams[team].IsCheckedIn || !strings.Contains(resp, "подтверждён") || kb != KbNone {
			t.Fatalf("press %d: checked=%v resp=%q kb=%q", i+1, repo.teams[team].IsCheckedIn, resp, kb)
		}
	}
	if p.FSMState != "team_line_3" {
		t.Errorf("check-in changed the FSM state to %q", p.FSMState)
	}
}

func TestCheckinButtonNeedsACaptain(t *testing.T) {
	svc, repo := newTelegramSvc()
	repo.addPlayer(1, nil, false, models.StateIdle)

	resp, _ := act(t, svc, 1, "checkin", "")
	if !strings.Contains(resp, "капитан") {
		t.Errorf("resp = %q, want captain-only refusal", resp)
	}
}

// A suspicious id is flagged above the role prompt, and the role step always
// offers a way back to the line without cancelling the whole registration.
func TestSuspiciousLineWarnsAndRedoReturnsToLine(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_line_2")

	resp, kb := drive(t, svc, 1, "Vasya 123 1234 25")
	if p.FSMState != "team_role_2" || kb != KbRegRoles || !strings.Contains(resp, "обычно 8") {
		t.Fatalf("state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}

	resp, kb = act(t, svc, 1, "redo", "")
	if p.FSMState != "team_line_2" || kb != KbRegCancel || !strings.Contains(resp, "2/5") {
		t.Fatalf("redo: state=%q kb=%q resp=%q", p.FSMState, kb, resp)
	}
	// The row already written is reused, not duplicated.
	drive(t, svc, 1, "Vasya 123456789 1234 25")
	if len(repo.members) != 1 || repo.members[0].GameID != "123456789" {
		t.Errorf("after redo: members=%d first=%+v", len(repo.members), repo.members[0])
	}
}

func TestRedoKeepsFixAndEditFlows(t *testing.T) {
	svc, repo := newTelegramSvc()
	team := 10
	repo.teams[team] = &models.TelegramTeam{ID: team, Name: "A"}
	p := repo.addPlayer(1, &team, true, "team_fixrole_1")

	act(t, svc, 1, "redo", "")
	if p.FSMState != "team_fix_1" {
		t.Errorf("fixrole redo → %q, want team_fix_1", p.FSMState)
	}
	p.FSMState = "team_editrole_1"
	act(t, svc, 1, "redo", "")
	if p.FSMState != "team_edit_1" {
		t.Errorf("editrole redo → %q, want team_edit_1", p.FSMState)
	}
	p.FSMState = models.StateSoloRole
	act(t, svc, 1, "redo", "")
	if p.FSMState != models.StateSoloLine {
		t.Errorf("solo redo → %q, want solo_line", p.FSMState)
	}
}

// One person cannot be on two rosters. The same id re-entered for the same
// slot (a fix) is not a duplicate.
func TestDuplicateGameIDIsRefused(t *testing.T) {
	svc, repo := newTelegramSvc()
	other, mine := 10, 11
	repo.teams[other] = &models.TelegramTeam{ID: other, Name: "Bravo"}
	repo.teams[mine] = &models.TelegramTeam{ID: mine, Name: "Alpha"}
	_ = repo.CreateTeammate(context.Background(), &models.TelegramPlayer{TeamID: &other, GameNickname: "Taken", GameID: "123456789"})
	p := repo.addPlayer(1, &mine, true, "team_line_2")

	resp, _ := drive(t, svc, 1, "Vasya 123456789 1234 25")
	if p.FSMState != "team_line_2" || !strings.Contains(resp, "Bravo") {
		t.Fatalf("state=%q resp=%q, want refusal naming Bravo", p.FSMState, resp)
	}

	drive(t, svc, 1, "Vasya 987654321 1234 25")
	act(t, svc, 1, "role", "Gold")
	// Fixing slot 2 with its own id again is fine.
	p.FSMState = "team_fix_2"
	drive(t, svc, 1, "Vasya 987654321 1234 26")
	if p.FSMState != "team_fixrole_2" {
		t.Errorf("re-entering own id: state=%q", p.FSMState)
	}
}

// A solo registration with the same id is not a conflict — people sign up
// solo first and join a team later.
func TestDuplicateGameIDIgnoresSoloRows(t *testing.T) {
	svc, repo := newTelegramSvc()
	mine := 11
	repo.teams[mine] = &models.TelegramTeam{ID: mine, Name: "Alpha"}
	solo := repo.addPlayer(2, nil, false, models.StateIdle)
	solo.GameID = "123456789"
	p := repo.addPlayer(1, &mine, true, "team_line_2")

	drive(t, svc, 1, "Vasya 123456789 1234 25")
	if p.FSMState != "team_role_2" {
		t.Errorf("state=%q, want the line accepted", p.FSMState)
	}
}

// The technical-defeat sweep records who is out; a disqualified team cannot
// check itself back in, only an admin can reinstate it.
func TestTechnicalDefeatIsRecorded(t *testing.T) {
	svc, repo := newTelegramSvc()
	in, out := 10, 11
	repo.teams[in] = &models.TelegramTeam{ID: in, Name: "In", IsCheckedIn: true}
	repo.teams[out] = &models.TelegramTeam{ID: out, Name: "Out"}
	repo.addPlayer(1, &out, true, models.StateIdle)

	dq, err := svc.DisqualifyUnchecked(context.Background())
	if err != nil || len(dq) != 1 || dq[0].Name != "Out" {
		t.Fatalf("DisqualifyUnchecked = (%v, %v), want [Out]", dq, err)
	}
	if repo.teams[out].Status != models.TeamStatusDisqualified || repo.teams[in].Status == models.TeamStatusDisqualified {
		t.Fatalf("statuses: out=%q in=%q", repo.teams[out].Status, repo.teams[in].Status)
	}

	// Running the sweep again does not report the same team twice.
	if again, _ := svc.DisqualifyUnchecked(context.Background()); len(again) != 0 {
		t.Errorf("second sweep reported %v", again)
	}

	if resp := svc.ToggleCheckIn(context.Background(), 1); !strings.Contains(resp, "снята") || repo.teams[out].IsCheckedIn {
		t.Errorf("/checkin on a disqualified team: resp=%q checked=%v", resp, repo.teams[out].IsCheckedIn)
	}
	if resp, _ := act(t, svc, 1, "checkin", ""); !strings.Contains(resp, "снята") || repo.teams[out].IsCheckedIn {
		t.Errorf("checkin button on a disqualified team: resp=%q", resp)
	}

	resp := svc.AdminReinstateTeam(context.Background(), "Out")
	if !strings.Contains(resp, "Out") || repo.teams[out].Status != models.TeamStatusActive || !repo.teams[out].IsCheckedIn {
		t.Errorf("reinstate: resp=%q status=%q checked=%v", resp, repo.teams[out].Status, repo.teams[out].IsCheckedIn)
	}
	if resp := svc.AdminReinstateTeam(context.Background(), "Nope"); !strings.Contains(resp, "не найдена") {
		t.Errorf("reinstate unknown: %q", resp)
	}
}

func TestTeamsListMarksDisqualified(t *testing.T) {
	svc, repo := newTelegramSvc()
	repo.teams[10] = &models.TelegramTeam{ID: 10, Name: "Out", Status: models.TeamStatusDisqualified}
	repo.teams[11] = &models.TelegramTeam{ID: 11, Name: "In", IsCheckedIn: true}

	list := svc.GetTeamsList(context.Background())
	if !strings.Contains(list, "[ТП] Out") || !strings.Contains(list, "[+] In") {
		t.Errorf("list = %q", list)
	}
}

type fakeProfiles struct{ byTG map[int64]*models.ProfileLink }

func (f fakeProfiles) GetLinkByTelegramID(_ context.Context, tg int64) (*models.ProfileLink, error) {
	if l, ok := f.byTG[tg]; ok {
		return l, nil
	}
	return nil, errors.New("no link")
}

func (f fakeProfiles) UpdateTelegramProfile(_ context.Context, tg int64, nick, gameID, zoneID string, stars int, role string) error {
	if l, ok := f.byTG[tg]; ok && l != nil {
		l.GameNickname = nick
		l.GameID = gameID
		l.ZoneID = zoneID
		l.Stars = stars
		l.MainRole = role
	}
	return nil
}

// A captain the bot already knows (previous tournament, or a linked Discord
// profile) is offered their data instead of typing the line again. Typing a
// line anyway still works.
func TestPrefillOffersKnownCaptainData(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateIdle)
	p.GameNickname, p.GameID, p.ZoneID, p.Stars = "Cap", "123456789", "1234", 30

	svc.StartTeamRegistration(context.Background(), 1)
	resp, kb := drive(t, svc, 1, "Alpha")
	if kb != KbRegPrefill || !strings.Contains(resp, "Cap") || p.FSMState != "team_line_1" {
		t.Fatalf("after name: kb=%q resp=%q state=%q", kb, resp, p.FSMState)
	}

	resp, kb = act(t, svc, 1, "prefill", "")
	if p.FSMState != "team_role_1" || kb != KbRegRoles || p.GameID != "123456789" || !strings.Contains(resp, "Cap") {
		t.Fatalf("prefill: state=%q kb=%q id=%q resp=%q", p.FSMState, kb, p.GameID, resp)
	}
}

func TestPrefillRetypeAndTypedLine(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateIdle)
	p.GameNickname, p.GameID, p.ZoneID = "Old", "111111111", "1111"

	svc.StartTeamRegistration(context.Background(), 1)
	drive(t, svc, 1, "Alpha")

	_, kb := act(t, svc, 1, "retype", "")
	if kb != KbRegCancel || p.FSMState != "team_line_1" {
		t.Fatalf("retype: kb=%q state=%q", kb, p.FSMState)
	}
	drive(t, svc, 1, "New 222222222 2222 5")
	if p.GameNickname != "New" || p.FSMState != "team_role_1" {
		t.Errorf("typed line after offer: nick=%q state=%q", p.GameNickname, p.FSMState)
	}
}

func TestPrefillFromDiscordProfileForSolo(t *testing.T) {
	svc, repo := newTelegramSvc()
	tg := int64(1)
	repo.addPlayer(tg, nil, false, models.StateIdle)
	svc.WithProfileLookup(fakeProfiles{byTG: map[int64]*models.ProfileLink{
		tg: {GameNickname: "Linked", GameID: "555555555", ZoneID: "5555", Stars: 12},
	}})

	resp, kb := svc.StartSoloRegistration(context.Background(), tg)
	if kb != KbRegPrefill || !strings.Contains(resp, "Linked") {
		t.Fatalf("solo start: kb=%q resp=%q", kb, resp)
	}
	act(t, svc, tg, "prefill", "")
	p := repo.players[tg]
	if p.FSMState != models.StateSoloRole || p.GameID != "555555555" {
		t.Errorf("after prefill: state=%q id=%q", p.FSMState, p.GameID)
	}
}

func TestNoPrefillWhenNothingIsKnown(t *testing.T) {
	svc, repo := newTelegramSvc()
	repo.addPlayer(1, nil, false, models.StateIdle)

	if _, kb := svc.StartSoloRegistration(context.Background(), 1); kb != KbRegCancel {
		t.Errorf("solo start kb=%q, want plain line prompt", kb)
	}
	svc.StartTeamRegistration(context.Background(), 1)
	if _, kb := drive(t, svc, 1, "Alpha"); kb != KbRegCancel {
		t.Errorf("after name kb=%q, want plain line prompt", kb)
	}
}

func TestProfileEditFlow(t *testing.T) {
	svc, repo := newTelegramSvc()
	tg := int64(42)
	repo.addPlayer(tg, nil, false, models.StateIdle)

	linked := &models.ProfileLink{DiscordPlayerID: 100}
	svc.WithProfileLookup(fakeProfiles{byTG: map[int64]*models.ProfileLink{tg: linked}})

	// 1. Start profile edit
	resp, kb := svc.StartProfileEdit(context.Background(), tg)
	if kb != KbRegCancel || !strings.Contains(resp, "Отправьте ваши игровые данные") {
		t.Fatalf("StartProfileEdit: kb=%q resp=%q", kb, resp)
	}
	if repo.players[tg].FSMState != models.StateProfileLine {
		t.Fatalf("state = %q, want %s", repo.players[tg].FSMState, models.StateProfileLine)
	}

	// 2. Cancel works
	_, kb = act(t, svc, tg, "cancel", "")
	if kb != "main_menu" || repo.players[tg].FSMState != models.StateIdle {
		t.Fatalf("cancel: kb=%q state=%q", kb, repo.players[tg].FSMState)
	}

	// 3. Start again, enter line
	svc.StartProfileEdit(context.Background(), tg)
	resp, kb = drive(t, svc, tg, "ProPlayer\n12345678 (9999)\n50")
	if kb != KbRegRoles || !strings.Contains(resp, "ProPlayer") {
		t.Fatalf("drive line: kb=%q resp=%q", kb, resp)
	}
	if repo.players[tg].FSMState != models.StateProfileRole {
		t.Fatalf("state = %q, want %s", repo.players[tg].FSMState, models.StateProfileRole)
	}

	// 4. Redo line works
	_, kb = act(t, svc, tg, "redo", "")
	if kb != KbRegCancel || repo.players[tg].FSMState != models.StateProfileLine {
		t.Fatalf("redo: kb=%q state=%q", kb, repo.players[tg].FSMState)
	}

	// 5. Enter line again and pick role
	drive(t, svc, tg, "ProPlayer\n12345678 (9999)\n50")
	resp, kb = act(t, svc, tg, "role", "Mid")
	if kb != "main_menu" || !strings.Contains(resp, "Профиль успешно сохранён") {
		t.Fatalf("pick role: kb=%q resp=%q", kb, resp)
	}
	if repo.players[tg].FSMState != models.StateIdle {
		t.Fatalf("state = %q, want idle", repo.players[tg].FSMState)
	}

	p := repo.players[tg]
	if p.GameNickname != "ProPlayer" || p.GameID != "12345678" || p.ZoneID != "9999" || p.Stars != 50 || p.MainRole != "Mid" {
		t.Fatalf("player in repo = %+v", p)
	}

	// Check linked profile also updated
	if linked.GameNickname != "ProPlayer" || linked.MainRole != "Mid" {
		t.Fatalf("linked profile not updated: %+v", linked)
	}
}
