# Telegram Registration UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 6-questions-per-player Telegram registration with one line per player, inline buttons for every in-flow control, and a confirmation card before anything is final.

**Architecture:** The registration state machine moves out of `telegram_service.go` into `telegram_registration.go` (application layer) with a pure line parser and a card renderer. Delivery gets a `reg:*` callback dispatcher next to the betting one and builds inline keyboards from the `kbType` strings the service returns. Schema is untouched; `CreateTeammate` grows to insert a full row.

**Tech Stack:** Go 1.24, `github.com/go-telegram-bot-api/telegram-bot-api/v5`, Postgres via `database/sql`. Unit tests use the in-memory `fakeTelegramRepo` in `internal/application/telegram_service_test.go`.

**Spec:** `docs/superpowers/specs/2026-09-13-telegram-registration-ui-design.md`

## Global Constraints

- All user-facing text is Russian; command names stay as they are (`/reg_team`, `/reg_solo`, `/my_team`, `/checkin`, `/delete_team`, `/edit_player`).
- Callback data format: `reg:<action>[:<arg>]`, separator `:` (the existing `callbackSep`).
- Roles whitelist: `Gold`, `Exp`, `Mid`, `Roam`, `Jungle`. Callback args are validated against whitelists, never trusted.
- Roster: slot 1 captain, 2–5 main, 6–7 substitutes (`firstSubstituteSlot = 6`, `maxTeamSlots = 7`, already in `telegram_service.go`).
- Team name: 1–64 runes (`maxTeamNameLen`, already there).
- Old FSM states (`team_reg_*`, `edit_player_*`, `waiting_nickname|game_id|zone_id|stars|role`) reset to Idle with «Начните заново».
- No DB migrations.
- Run `gofmt -l internal/`, `go vet ./...`, `go test ./...`, `golangci-lint run ./...` before every commit — all must be clean.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/models/telegram.go` | FSM state constants (new set replaces old solo/team ones) |
| `internal/application/player_line.go` (new) | `parsePlayerLine` — pure parser, no I/O |
| `internal/application/player_line_test.go` (new) | table test for the parser |
| `internal/application/telegram_registration.go` (new) | registration FSM: text steps, `HandleRegAction`, card rendering, save helpers |
| `internal/application/telegram_registration_test.go` (new) | FSM tests on `fakeTelegramRepo` |
| `internal/application/telegram_service.go` | interface + dispatcher; old `handleTeamLoop`/`handleEditLoop`/solo cases removed |
| `internal/application/telegram_service_test.go` | fake repo (extend `CreateTeammate`, `UpdatePlayerFieldByID`), existing tests updated to new states |
| `internal/repository/repository.go` | `Telegram` interface: `HandleRegAction` not here (service); `CreateTeammate` unchanged signature but full insert |
| `internal/repository/telegram_postgres.go` | `CreateTeammate` inserts all fields |
| `internal/repository/integration_test.go` | `CreateTeammate` full-row test |
| `internal/delivery/telegram/registration.go` (new) | `reg:` callback handler, inline keyboard builders |
| `internal/delivery/telegram/registration_test.go` (new) | callback parsing + keyboard builder tests |
| `internal/delivery/telegram/bot.go` | `handleCallbackQuery` dispatches `reg:` |
| `internal/delivery/telegram/helpers.go` | `trySendMessage` uses `regKeyboard`; old `skip`/`role`/`cancel` reply keyboards removed |
| `internal/delivery/telegram/handlers.go` | `/my_team` uses the new two-value `GetTeamInfo` |

---

### Task 1: Player line parser

**Files:**
- Create: `internal/application/player_line.go`
- Test: `internal/application/player_line_test.go`

**Interfaces:**
- Produces:
  ```go
  type playerLine struct {
      Nick    string
      GameID  string
      ZoneID  string
      Stars   int
      Contact string // "@user" or ""
  }
  func parsePlayerLine(s string) (playerLine, error)
  const playerLineFormat = "Формат: Ник GameID ZoneID Звёзды"
  ```
  Error text is user-facing Russian and does NOT include the format line; callers append `"\n" + playerLineFormat`.

- [ ] **Step 1: Write the failing table test**

```go
package application

import (
	"strings"
	"testing"
)

func TestParsePlayerLine(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    playerLine
		wantErr string // substring of the error, "" for success
	}{
		{"plain", "Kitayes 123456789 1234 25", playerLine{Nick: "Kitayes", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"zone in parens", "Kitayes 123456789 (1234) 25", playerLine{Nick: "Kitayes", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"commas", "Kitayes, 123456789, 1234, 25", playerLine{Nick: "Kitayes", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"two-word nick", "Big Boss 123456789 1234 25", playerLine{Nick: "Big Boss", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"contact at end", "Vasya 123456789 1234 25 @vasya", playerLine{Nick: "Vasya", GameID: "123456789", ZoneID: "1234", Stars: 25, Contact: "@vasya"}, ""},
		{"contact in middle", "Vasya @vasya 123456789 1234 25", playerLine{Nick: "Vasya", GameID: "123456789", ZoneID: "1234", Stars: 25, Contact: "@vasya"}, ""},
		{"zero stars", "Newbie 123456789 1234 0", playerLine{Nick: "Newbie", GameID: "123456789", ZoneID: "1234", Stars: 0}, ""},
		{"missing zone and stars", "Kitayes 123456789", playerLine{}, "Zone ID"},
		{"missing nick", "123456789 1234 25", playerLine{}, "ник"},
		{"stars not a number", "Kitayes 123456789 1234 много", playerLine{}, "Звёзды"},
		{"trailing garbage", "Kitayes 123456789 1234 25 лишнее", playerLine{}, "лишнее"},
		{"two contacts", "Vasya 123456789 1234 25 @a @b", playerLine{}, "контакт"},
		{"empty", "   ", playerLine{}, "ник"},
		{"nick too long", strings.Repeat("x", 65) + " 123456789 1234 25", playerLine{}, "64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePlayerLine(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/application/ -run TestParsePlayerLine`
Expected: build failure `undefined: parsePlayerLine`.

- [ ] **Step 3: Implement**

```go
package application

import (
	"errors"
	"fmt"
	"strings"
)

// playerLine is one roster entry as typed by the captain:
// "Ник GameID ZoneID Звёзды [@контакт]".
type playerLine struct {
	Nick    string
	GameID  string
	ZoneID  string
	Stars   int
	Contact string
}

const playerLineFormat = "Формат: Ник GameID ZoneID Звёзды"

// parsePlayerLine reads a roster line. Separators are spaces and/or commas;
// the nick is everything before the first numeric token and may contain
// spaces; the zone may be written as "(1234)"; one optional "@contact" token
// may appear anywhere. Errors are user-facing Russian sentences naming what
// was missing — the format hint is appended by the caller.
func parsePlayerLine(s string) (playerLine, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' })

	var line playerLine
	var tokens []string
	for _, f := range fields {
		if strings.HasPrefix(f, "@") {
			if line.Contact != "" {
				return playerLine{}, errors.New("Контакт должен быть один")
			}
			line.Contact = f
			continue
		}
		tokens = append(tokens, f)
	}

	// Nick: everything up to the first numeric token.
	firstNum := -1
	for i, tok := range tokens {
		if isDigits(strings.Trim(tok, "()")) {
			firstNum = i
			break
		}
	}
	if firstNum <= 0 {
		return playerLine{}, errors.New("Не вижу ник — он должен идти первым")
	}
	line.Nick = strings.Join(tokens[:firstNum], " ")
	if n := len([]rune(line.Nick)); n > maxTeamNameLen {
		return playerLine{}, fmt.Errorf("Ник длиннее %d символов", maxTeamNameLen)
	}

	rest := tokens[firstNum:]
	switch len(rest) {
	case 1:
		return playerLine{}, errors.New("Не вижу Zone ID и звёзды")
	case 2:
		return playerLine{}, errors.New("Не вижу звёзды")
	}
	if len(rest) > 3 {
		return playerLine{}, fmt.Errorf("Не понял '%s'", strings.Join(rest[3:], " "))
	}

	line.GameID = rest[0]
	line.ZoneID = strings.Trim(rest[1], "()")
	if !isDigits(line.ZoneID) {
		return playerLine{}, errors.New("Zone ID — число")
	}
	if !isDigits(rest[2]) {
		return playerLine{}, errors.New("Звёзды — число")
	}
	stars, _ := parseStars(rest[2])
	line.Stars = stars
	return line, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
```

Note: `parseStars` and `maxTeamNameLen` already exist in `telegram_service.go`. The "missing zone and stars" case must mention "Zone ID" — the message «Не вижу Zone ID и звёзды» does.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/application/ -run TestParsePlayerLine -v`
Expected: all subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/player_line.go internal/application/player_line_test.go
git commit -m "feat(telegram): парсер строки игрока для регистрации"
```

---

### Task 2: New FSM states and fake repo support

**Files:**
- Modify: `internal/models/telegram.go:3-12`
- Modify: `internal/application/telegram_service_test.go` (fake repo: `CreateTeammate`, `UpdatePlayerFieldByID`, `GetTeamMembers` ordering)

**Interfaces:**
- Produces (in `models`):
  ```go
  const (
      StateIdle            = ""
      StateWaitingTeamName = "waiting_team_name"
      StateWaitingReport   = "waiting_report"

      StateSoloLine    = "solo_line"
      StateSoloRole    = "solo_role"
      StateSoloConfirm = "solo_confirm"

      StateTeamConfirm = "team_confirm"
      // Slot-bearing team states are "<prefix><slot>", e.g. "team_line_3".
      StateTeamLinePrefix     = "team_line_"
      StateTeamRolePrefix     = "team_role_"
      StateTeamFixPrefix      = "team_fix_"
      StateTeamFixRolePrefix  = "team_fixrole_"
      StateTeamEditPrefix     = "team_edit_"
      StateTeamEditRolePrefix = "team_editrole_"
  )
  ```
  The old `StateWaitingNickname/GameID/ZoneID/Stars/Role` constants are deleted.

- [ ] **Step 1: Replace the constants in `internal/models/telegram.go`**

Replace the whole `const (...)` block with the one above.

- [ ] **Step 2: Build to see what breaks**

Run: `go build ./... && go vet ./...`
Expected: errors in `telegram_service.go` (uses of the deleted constants) and in `telegram_service_test.go`. That is the list of what Task 3 rewrites. Do not fix them here beyond the fake repo.

- [ ] **Step 3: Extend the fake repo**

In `internal/application/telegram_service_test.go`:

Replace `UpdatePlayerFieldByID`:
```go
func (r *fakeTelegramRepo) UpdatePlayerFieldByID(_ context.Context, id int, col string, v interface{}) error {
	for _, p := range r.allRows() {
		if p.ID == id {
			setField(p, col, v)
			return nil
		}
	}
	return errors.New("no row")
}
```

Replace `UpdatePlayerField`'s switch body with `setField(p, col, v)` and add:
```go
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
```

Replace `GetTeamMembers` so it returns rows ordered by id (the real query does `ORDER BY id`):
```go
func (r *fakeTelegramRepo) GetTeamMembers(_ context.Context, teamID int) ([]models.TelegramPlayer, error) {
	var out []models.TelegramPlayer
	for _, p := range r.allRows() {
		if p.TeamID != nil && *p.TeamID == teamID {
			out = append(out, *p)
		}
	}
	return out, nil
}
```

`CreateTeammate` stays as is (it already stores the full struct). Add `"sort"` to the imports.

- [ ] **Step 4: Build the test package only for syntax**

Run: `gofmt -l internal/application/`
Expected: no output. (The package will not compile until Task 3 — that is expected; do not commit yet.)

---

### Task 3: Registration FSM in the service

**Files:**
- Create: `internal/application/telegram_registration.go`
- Modify: `internal/application/telegram_service.go` (interface, `HandleUserInput`, `Start*`, `GetTeamInfo`, remove `handleTeamLoop`, `advanceTeamSlot`, `handleEditLoop`, `StartEditPlayer` body)
- Create: `internal/application/telegram_registration_test.go`
- Modify: `internal/application/telegram_service_test.go` (retarget existing tests to new states)

**Interfaces:**
- Produces (on `TelegramService` interface and `TelegramServiceImpl`):
  ```go
  HandleRegAction(ctx context.Context, tgID int64, action, arg string) (string, string)
  GetTeamInfo(ctx context.Context, tgID int64) (string, string)   // was (string)
  ```
  Keyboard type strings the delivery layer must render (all in `application`):
  ```go
  const (
      KbNone           = "empty"       // existing
      KbRegCancel      = "reg_cancel"                 // [❌ Отмена]
      KbRegRoles       = "reg_roles"                  // roles + cancel
      KbRegSkip        = "reg_skip"                   // [⏭ Пропустить][❌ Отмена]
      KbRegConfirm     = "reg_confirm"                // "reg_confirm:<n>"  n = number of ✏️ buttons; + ✅ + 🗑
      KbRegCard        = "reg_card"                   // "reg_card:<n>:<add>" add = "sub"|"player"|"" ; ✏️ ×n, 🗑, optional ➕
      KbRegSoloConfirm = "reg_solo_confirm"           // [✅ Подтвердить][✏️ Исправить]
  )
  ```
  Callback actions accepted by `HandleRegAction`: `role`, `skip`, `cancel`, `confirm`, `fix`, `delete`, `sub`.
  `KbCancel`, `KbRole`, `KbSkip` are deleted.

- [ ] **Step 1: Write the failing FSM tests**

`internal/application/telegram_registration_test.go`:

```go
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

func act(t *testing.T, svc *TelegramServiceImpl, tg int64, action, arg string) (string, string) {
	t.Helper()
	return svc.HandleRegAction(context.Background(), tg, action, arg)
}

func TestTeamRegistrationHappyPath(t *testing.T) {
	svc, repo := newTelegramSvc()
	p := repo.addPlayer(1, nil, false, models.StateIdle)

	resp, kb := svc.StartTeamRegistration(context.Background(), 1)
	if kb != KbRegCancel || p.FSMState != models.StateWaitingTeamName {
		t.Fatalf("start: kb=%q state=%q", kb, p.FSMState)
	}

	resp, kb = drive(t, svc, 1, "Alpha")
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

	resp, kb = act(t, svc, 1, "role", "Mid")
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

	_, kb = drive(t, svc, 1, "S 333333333 3333 5")
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
	_, kb = act(t, svc, 1, "fix", "")
	if p.FSMState != models.StateSoloLine {
		t.Fatalf("fix: state=%q", p.FSMState)
	}
	drive(t, svc, 1, "Solo2 123456789 1234 41")
	act(t, svc, 1, "role", "Roam")
	resp, kb = act(t, svc, 1, "confirm", "")
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
```

- [ ] **Step 2: Retarget the existing tests in `telegram_service_test.go`**

Delete these tests (their behaviour moves to the new file): `TestEditPlayerBadSlotIsRefused`, `TestEditLoopAbortsWhenTeamIsGone`, `TestSoloStarsMustBeNumeric`, `TestTeamStarsMustBeNumeric`, `TestCaptainIsNotAskedForContact`, `TestTeammateIsAskedForContact`.

Edit `TestTeamRegistrationAbortsWhenTeamIsGone`: state `"team_line_2"` instead of `"team_reg_nick_2"`.

Edit `TestEditPlayerWithoutTeamIsRefused` and `TestEditPlayerRequiresCaptain`: unchanged assertions — `StartEditPlayer` keeps its contract.

Edit `TestTeamNameLengthIsValidated`: expect `kb != KbRegCancel` instead of `KbCancel`.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/application/ 2>&1 | head`
Expected: build errors — `HandleRegAction`, `KbRegCancel` etc. undefined.

- [ ] **Step 4: Write `telegram_registration.go`**

```go
package application

import (
	"blackwatch/internal/models"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Keyboard types for the registration flow. Delivery renders them as inline
// keyboards; the ones with a suffix carry rendering parameters after ":".
const (
	KbRegCancel      = "reg_cancel"
	KbRegRoles       = "reg_roles"
	KbRegSkip        = "reg_skip"
	KbRegConfirm     = "reg_confirm"      // "reg_confirm:<n>": ✏️ 1..n, ✅, 🗑
	KbRegCard        = "reg_card"         // "reg_card:<n>:<add>": ✏️ 1..n, 🗑, ➕ when add != ""
	KbRegSoloConfirm = "reg_solo_confirm" // ✅, ✏️
)

// registrationRoles is the whitelist for reg:role:<x>. Callback data is
// client-supplied, so anything else is refused.
var registrationRoles = []string{"Gold", "Exp", "Mid", "Roam", "Jungle"}

func validRole(s string) bool {
	for _, r := range registrationRoles {
		if r == s {
			return true
		}
	}
	return false
}

const (
	msgStaleButton = "Эта кнопка больше не действует."
	msgLegacyReset = "Регистрация обновилась, старый ввод сброшен. Начните заново: /reg_team или /reg_solo"
)

// isRegistrationState reports whether the state belongs to the new flow.
func isRegistrationState(st string) bool {
	return st == models.StateWaitingTeamName || strings.HasPrefix(st, "team_") || strings.HasPrefix(st, "solo_")
}

// isLegacyState matches states from the pre-inline flow that may still sit
// in the database for people who were mid-registration at deploy time.
func isLegacyState(st string) bool {
	switch st {
	case "waiting_nickname", "waiting_game_id", "waiting_zone_id", "waiting_stars", "waiting_role":
		return true
	}
	return strings.HasPrefix(st, "team_reg_") || strings.HasPrefix(st, "edit_player_")
}

// slotState splits "team_line_3" into ("team_line_", 3).
func slotState(st string) (prefix string, slot int, ok bool) {
	for _, p := range []string{
		models.StateTeamLinePrefix, models.StateTeamRolePrefix,
		models.StateTeamFixPrefix, models.StateTeamFixRolePrefix,
		models.StateTeamEditPrefix, models.StateTeamEditRolePrefix,
	} {
		if strings.HasPrefix(st, p) {
			n, err := strconv.Atoi(strings.TrimPrefix(st, p))
			if err != nil || n < 1 || n > maxTeamSlots {
				return "", 0, false
			}
			return p, n, true
		}
	}
	return "", 0, false
}

func (s *TelegramServiceImpl) setState(ctx context.Context, tgID int64, st string) {
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, tgID, st))
}

// ---- prompts -------------------------------------------------------------

func slotLabel(slot int) string {
	switch {
	case slot == 1:
		return fmt.Sprintf("👑 Игрок %d/%d (капитан)", slot, maxTeamSlots)
	case slot >= firstSubstituteSlot:
		return fmt.Sprintf("🔁 Замена %d/%d", slot, maxTeamSlots)
	default:
		return fmt.Sprintf("👤 Игрок %d/%d", slot, maxTeamSlots)
	}
}

func linePrompt(slot int) (string, string) {
	format := "Отправь одной строкой: Ник GameID ZoneID Звёзды"
	if slot != 1 {
		format += " [@telegram]"
	}
	msg := slotLabel(slot) + "\n" + format + "\nПример: Kitayes 123456789 1234 25"
	if slot >= firstSubstituteSlot {
		return msg, KbRegSkip
	}
	return msg, KbRegCancel
}

func rolePrompt(line playerLine) string {
	return fmt.Sprintf("%s · %s (%s) · %d⭐\nРоль?", line.Nick, line.GameID, line.ZoneID, line.Stars)
}

// ---- roster helpers ------------------------------------------------------

// roster returns team members with the captain first; slot N is index N-1.
func (s *TelegramServiceImpl) roster(ctx context.Context, teamID int) []models.TelegramPlayer {
	members, _ := s.repo.GetTeamMembers(ctx, teamID)
	for i, m := range members {
		if m.IsCaptain && i != 0 {
			members[0], members[i] = members[i], members[0]
			break
		}
	}
	return members
}

// saveLine writes a parsed line into slot N: the captain's own row for slot
// 1, an existing roster row when the slot is taken, a new row otherwise.
func (s *TelegramServiceImpl) saveLine(ctx context.Context, captain *models.TelegramPlayer, slot int, line playerLine) {
	teamID := *captain.TeamID
	tg := *captain.TelegramID
	if slot == 1 {
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_nickname", line.Nick))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_id", line.GameID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "zone_id", line.ZoneID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "stars", line.Stars))
		return
	}
	members := s.roster(ctx, teamID)
	if slot <= len(members) {
		id := members[slot-1].ID
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "game_nickname", line.Nick))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "game_id", line.GameID))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "zone_id", line.ZoneID))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "stars", line.Stars))
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, id, "telegram_username", line.Contact))
		return
	}
	s.logWrite("CreateTeammate", s.repo.CreateTeammate(ctx, &models.TelegramPlayer{
		TeamID:           &teamID,
		GameNickname:     line.Nick,
		GameID:           line.GameID,
		ZoneID:           line.ZoneID,
		Stars:            line.Stars,
		TelegramUsername: line.Contact,
		IsSubstitute:     slot >= firstSubstituteSlot,
	}))
}

func (s *TelegramServiceImpl) saveRole(ctx context.Context, captain *models.TelegramPlayer, slot int, role string) {
	if slot == 1 {
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, *captain.TelegramID, "main_role", role))
		return
	}
	members := s.roster(ctx, *captain.TeamID)
	if slot <= len(members) {
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, members[slot-1].ID, "main_role", role))
	}
}

// slotSummary is the one-line form used in the role acknowledgement.
func (s *TelegramServiceImpl) slotSummary(ctx context.Context, captain *models.TelegramPlayer, slot int) string {
	members := s.roster(ctx, *captain.TeamID)
	if slot < 1 || slot > len(members) {
		return ""
	}
	m := members[slot-1]
	return fmt.Sprintf("%s — %s", m.GameNickname, m.MainRole)
}

// ---- cards -----------------------------------------------------------------

func renderTeamCard(team *models.TelegramTeam, members []models.TelegramPlayer) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Команда '%s'\n", team.Name)
	for i, m := range members {
		icon := "👤"
		if i == 0 {
			icon = "👑"
		} else if m.IsSubstitute {
			icon = "🔁"
		}
		fmt.Fprintf(&sb, "%d. %s %s · %s · %s (%s) · %d⭐", i+1, icon, m.GameNickname, m.MainRole, m.GameID, m.ZoneID, m.Stars)
		if i != 0 && m.TelegramUsername != "" {
			fmt.Fprintf(&sb, " · %s", m.TelegramUsername)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// confirmCard is the pre-confirmation card: ✅ / ✏️ N / 🗑.
func (s *TelegramServiceImpl) confirmCard(ctx context.Context, captain *models.TelegramPlayer) (string, string) {
	team, err := s.repo.GetTeamByID(ctx, *captain.TeamID)
	if err != nil || team == nil {
		return "Команда не найдена.", KbNone
	}
	members := s.roster(ctx, team.ID)
	return renderTeamCard(team, members) + "\nВсё верно?", fmt.Sprintf("%s:%d", KbRegConfirm, len(members))
}

// teamCard is the /my_team card: ✏️ N / 🗑 / ➕.
func (s *TelegramServiceImpl) teamCard(ctx context.Context, captain *models.TelegramPlayer) (string, string) {
	team, err := s.repo.GetTeamByID(ctx, *captain.TeamID)
	if err != nil || team == nil {
		return "Команда не найдена.", KbNone
	}
	members := s.roster(ctx, team.ID)
	status := "⚪ Check-in не пройден"
	if team.IsCheckedIn {
		status = "✅ Check-in пройден"
	}
	add := ""
	switch next := len(members) + 1; {
	case next > maxTeamSlots:
	case next >= firstSubstituteSlot:
		add = "sub"
	default:
		add = "player"
	}
	return renderTeamCard(team, members) + status, fmt.Sprintf("%s:%d:%s", KbRegCard, len(members), add)
}

func renderSoloCard(p *models.TelegramPlayer) string {
	return fmt.Sprintf("%s · %s · %s (%s) · %d⭐\nВсё верно?", p.GameNickname, p.MainRole, p.GameID, p.ZoneID, p.Stars)
}

// ---- text steps ------------------------------------------------------------

// handleRegistrationText is the text half of the FSM. Called by
// HandleUserInput for any state isRegistrationState accepts.
func (s *TelegramServiceImpl) handleRegistrationText(ctx context.Context, p *models.TelegramPlayer, input string) (string, string) {
	tg := *p.TelegramID

	switch p.FSMState {
	case models.StateWaitingTeamName:
		name := strings.TrimSpace(input)
		if name == "" || len([]rune(name)) > maxTeamNameLen {
			return fmt.Sprintf("Название должно быть от 1 до %d символов. Введите другое:", maxTeamNameLen), KbRegCancel
		}
		team, err := s.repo.CreateTeam(ctx, name)
		if err != nil {
			return "Это имя занято, попробуйте другое:", KbRegCancel
		}
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "team_id", team.ID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "is_captain", true))
		s.setState(ctx, tg, models.StateTeamLinePrefix+"1")
		msg, kb := linePrompt(1)
		return fmt.Sprintf("Команда '%s' создана.\n%s", name, msg), kb

	case models.StateSoloLine:
		line, err := parsePlayerLine(input)
		if err != nil {
			return err.Error() + "\n" + playerLineFormat, KbRegCancel
		}
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_nickname", line.Nick))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "game_id", line.GameID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "zone_id", line.ZoneID))
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tg, "stars", line.Stars))
		s.setState(ctx, tg, models.StateSoloRole)
		return rolePrompt(line), KbRegRoles

	case models.StateSoloRole:
		if validRole(input) {
			return s.HandleRegAction(ctx, tg, "role", input)
		}
		return "Выберите роль кнопкой 👆", KbRegRoles

	case models.StateSoloConfirm:
		return renderSoloCard(p), KbRegSoloConfirm

	case models.StateTeamConfirm:
		if p.TeamID == nil {
			return s.teamGone(ctx, tg)
		}
		return s.confirmCard(ctx, p)
	}

	prefix, slot, ok := slotState(p.FSMState)
	if !ok {
		s.setState(ctx, tg, models.StateIdle)
		return msgLegacyReset, KbNone
	}
	if p.TeamID == nil {
		return s.teamGone(ctx, tg)
	}

	switch prefix {
	case models.StateTeamLinePrefix, models.StateTeamFixPrefix, models.StateTeamEditPrefix:
		line, err := parsePlayerLine(input)
		if err != nil {
			_, kb := linePrompt(slot)
			if prefix != models.StateTeamLinePrefix {
				kb = KbRegCancel
			}
			return err.Error() + "\n" + playerLineFormat, kb
		}
		if slot == 1 {
			line.Contact = ""
		}
		s.saveLine(ctx, p, slot, line)
		s.setState(ctx, tg, roleStateFor(prefix)+strconv.Itoa(slot))
		return rolePrompt(line), KbRegRoles

	case models.StateTeamRolePrefix, models.StateTeamFixRolePrefix, models.StateTeamEditRolePrefix:
		if validRole(input) {
			return s.HandleRegAction(ctx, tg, "role", input)
		}
		return "Выберите роль кнопкой 👆", KbRegRoles
	}
	return msgStaleButton, KbNone
}

func roleStateFor(linePrefix string) string {
	switch linePrefix {
	case models.StateTeamFixPrefix:
		return models.StateTeamFixRolePrefix
	case models.StateTeamEditPrefix:
		return models.StateTeamEditRolePrefix
	}
	return models.StateTeamRolePrefix
}

func (s *TelegramServiceImpl) teamGone(ctx context.Context, tg int64) (string, string) {
	s.setState(ctx, tg, models.StateIdle)
	return "Команда была удалена. Начните регистрацию заново: /reg_team", KbNone
}

// ---- callbacks -------------------------------------------------------------

// HandleRegAction is the button half of the FSM: reg:<action>[:<arg>].
// A button pressed outside the state that showed it is inert.
func (s *TelegramServiceImpl) HandleRegAction(ctx context.Context, tgID int64, action, arg string) (string, string) {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil {
		return "Используйте /start для начала.", KbNone
	}

	if action == "cancel" {
		if !isRegistrationState(p.FSMState) {
			return msgStaleButton, KbNone
		}
		s.setState(ctx, tgID, models.StateIdle)
		if p.TeamID != nil {
			if name := s.currentTeamName(ctx, tgID); name != "" {
				return fmt.Sprintf("Регистрация прервана. Команда '%s' сохранена с введёнными игроками — дополнить или удалить можно через /my_team.", name), "main_menu"
			}
		}
		return "Действие отменено.", "main_menu"
	}

	// Solo.
	switch p.FSMState {
	case models.StateSoloRole:
		if action != "role" || !validRole(arg) {
			return rolePromptFromRow(p), KbRegRoles
		}
		s.logWrite("UpdatePlayerField", s.repo.UpdatePlayerField(ctx, tgID, "main_role", arg))
		p.MainRole = arg
		s.setState(ctx, tgID, models.StateSoloConfirm)
		return renderSoloCard(p), KbRegSoloConfirm
	case models.StateSoloConfirm:
		switch action {
		case "confirm":
			s.setState(ctx, tgID, models.StateIdle)
			return "✅ Соло-регистрация завершена!", "main_menu"
		case "fix":
			s.setState(ctx, tgID, models.StateSoloLine)
			return "Отправь одной строкой: Ник GameID ZoneID Звёзды", KbRegCancel
		}
		return msgStaleButton, KbNone
	}

	// Team: everything below needs a team.
	if p.TeamID == nil {
		if isRegistrationState(p.FSMState) {
			return s.teamGone(ctx, tgID)
		}
		return msgStaleButton, KbNone
	}

	// Idle with a team: buttons from the /my_team card.
	if p.FSMState == models.StateIdle {
		if !p.IsCaptain {
			return "Только капитан может менять состав.", KbNone
		}
		members := s.roster(ctx, *p.TeamID)
		switch action {
		case "fix":
			slot, err := strconv.Atoi(arg)
			if err != nil || slot < 1 || slot > len(members) {
				return msgStaleButton, KbNone
			}
			s.setState(ctx, tgID, models.StateTeamEditPrefix+strconv.Itoa(slot))
			return fmt.Sprintf("Сейчас: %s\n%s", renderSlot(members[slot-1]), lineOnly(slot)), KbRegCancel
		case "sub":
			slot := len(members) + 1
			if slot > maxTeamSlots {
				return msgStaleButton, KbNone
			}
			s.setState(ctx, tgID, models.StateTeamEditPrefix+strconv.Itoa(slot))
			return lineOnly(slot), KbRegCancel
		case "delete":
			s.setState(ctx, tgID, models.StateIdle)
			return s.DeleteTeam(ctx, tgID), "main_menu"
		}
		return msgStaleButton, KbNone
	}

	if p.FSMState == models.StateTeamConfirm {
		members := s.roster(ctx, *p.TeamID)
		switch action {
		case "confirm":
			s.setState(ctx, tgID, models.StateIdle)
			return "✅ Команда зарегистрирована. В день турнира нажми /checkin.", "main_menu"
		case "fix":
			slot, err := strconv.Atoi(arg)
			if err != nil || slot < 1 || slot > len(members) {
				return msgStaleButton, KbNone
			}
			s.setState(ctx, tgID, models.StateTeamFixPrefix+strconv.Itoa(slot))
			return fmt.Sprintf("Сейчас: %s\n%s", renderSlot(members[slot-1]), lineOnly(slot)), KbRegCancel
		case "delete":
			s.setState(ctx, tgID, models.StateIdle)
			return s.DeleteTeam(ctx, tgID), "main_menu"
		}
		return msgStaleButton, KbNone
	}

	prefix, slot, ok := slotState(p.FSMState)
	if !ok {
		return msgStaleButton, KbNone
	}

	switch prefix {
	case models.StateTeamLinePrefix:
		if action == "skip" && slot >= firstSubstituteSlot {
			return s.afterSlot(ctx, p, slot, "")
		}
		return msgStaleButton, KbNone

	case models.StateTeamRolePrefix, models.StateTeamFixRolePrefix, models.StateTeamEditRolePrefix:
		if action != "role" || !validRole(arg) {
			return "Выберите роль кнопкой 👆", KbRegRoles
		}
		s.saveRole(ctx, p, slot, arg)
		switch prefix {
		case models.StateTeamFixRolePrefix:
			s.setState(ctx, tgID, models.StateTeamConfirm)
			return s.confirmCard(ctx, p)
		case models.StateTeamEditRolePrefix:
			s.setState(ctx, tgID, models.StateIdle)
			return s.teamCard(ctx, p)
		}
		return s.afterSlot(ctx, p, slot, fmt.Sprintf("✅ Игрок %d: %s\n", slot, s.slotSummary(ctx, p, slot)))
	}
	return msgStaleButton, KbNone
}

// afterSlot moves on from slot N during initial registration: the next line
// prompt, or the confirmation card after the last slot.
func (s *TelegramServiceImpl) afterSlot(ctx context.Context, p *models.TelegramPlayer, slot int, ack string) (string, string) {
	tg := *p.TelegramID
	if slot >= maxTeamSlots {
		s.setState(ctx, tg, models.StateTeamConfirm)
		text, kb := s.confirmCard(ctx, p)
		return ack + text, kb
	}
	next := slot + 1
	s.setState(ctx, tg, models.StateTeamLinePrefix+strconv.Itoa(next))
	msg, kb := linePrompt(next)
	return ack + msg, kb
}

func lineOnly(slot int) string {
	msg, _ := linePrompt(slot)
	return msg
}

func renderSlot(m models.TelegramPlayer) string {
	return fmt.Sprintf("%s · %s · %s (%s) · %d⭐", m.GameNickname, m.MainRole, m.GameID, m.ZoneID, m.Stars)
}

func rolePromptFromRow(p *models.TelegramPlayer) string {
	return rolePrompt(playerLine{Nick: p.GameNickname, GameID: p.GameID, ZoneID: p.ZoneID, Stars: p.Stars})
}
```

- [ ] **Step 5: Rewire `telegram_service.go`**

1. In the `TelegramService` interface: change `GetTeamInfo(ctx, tgID) string` to `GetTeamInfo(ctx context.Context, tgID int64) (string, string)`; add `HandleRegAction(ctx context.Context, tgID int64, action, arg string) (string, string)`.
2. Delete `KbCancel`, `KbRole`, `KbSkip` from the first const block (keep `KbNone`).
3. Replace the body of `HandleUserInput` after the `player == nil` check with:
```go
	if isLegacyState(player.FSMState) {
		s.setState(ctx, tgID, models.StateIdle)
		return msgLegacyReset, KbNone
	}
	if isRegistrationState(player.FSMState) {
		return s.handleRegistrationText(ctx, player, input)
	}
	return "Используйте меню для управления.", KbNone
```
   Keep the `"Отмена" || "/cancel"` branch at the top but change it to call `return s.HandleRegAction(ctx, tgID, "cancel", "")` when the player is in a registration state, else keep the old idle reset.
4. Delete `handleTeamLoop`, `advanceTeamSlot`, `handleEditLoop`.
5. `StartSoloRegistration`: state → `models.StateSoloLine`; return `"Отправь одной строкой: Ник GameID ZoneID Звёзды\nПример: Kitayes 123456789 1234 25", KbRegCancel`.
6. `StartTeamRegistration`: return `"Введите Название команды:", KbRegCancel`.
7. `StartEditPlayer`: keep the guards (no team / not captain / slot range); replace the final two lines with `return s.HandleRegAction(ctx, tgID, "fix", strconv.Itoa(slot))`. It must only be reachable when `p.FSMState == models.StateIdle` — add `if p.FSMState != models.StateIdle { return "Сначала завершите текущее действие или нажмите Отмена.", KbNone }` after the captain guard.
8. `GetTeamInfo`: 
```go
func (s *TelegramServiceImpl) GetTeamInfo(ctx context.Context, tgID int64) (string, string) {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return "Вы не в команде.", KbNone
	}
	return s.teamCard(ctx, p)
}
```
9. Remove `parseStars`'s only other caller if any remains; keep the function (the parser uses it).

- [ ] **Step 6: Run the application tests**

Run: `gofmt -l internal/ && go vet ./internal/application/ && go test ./internal/application/`
Expected: PASS. Delivery will not compile yet (`GetTeamInfo` signature) — fine, that is Task 4.

- [ ] **Step 7: Commit**

```bash
git add internal/models/telegram.go internal/application/
git commit -m "feat(telegram): регистрация одной строкой с карточкой подтверждения — сервисный слой"
```

---

### Task 4: Delivery — `reg:` callbacks and inline keyboards

**Files:**
- Create: `internal/delivery/telegram/registration.go`
- Create: `internal/delivery/telegram/registration_test.go`
- Modify: `internal/delivery/telegram/bot.go:100-113` (`handleCallbackQuery`)
- Modify: `internal/delivery/telegram/helpers.go:36-75` (`trySendMessage`)
- Modify: `internal/delivery/telegram/handlers.go:187-189` (`/my_team`)

**Interfaces:**
- Consumes: `application.HandleRegAction`, `application.KbReg*` constants, `callbackSep`.
- Produces:
  ```go
  const callbackRegPrefix = "reg"
  func parseRegCallback(data string) (action, arg string, ok bool)
  func regKeyboard(kbType string) (tgbotapi.InlineKeyboardMarkup, bool)
  func (b *Bot) handleRegCallback(ctx context.Context, cb *tgbotapi.CallbackQuery)
  ```

- [ ] **Step 1: Write the failing tests**

```go
package telegram

import (
	"blackwatch/internal/application"
	"testing"
)

func TestParseRegCallback(t *testing.T) {
	cases := []struct {
		in          string
		action, arg string
		ok          bool
	}{
		{"reg:role:Mid", "role", "Mid", true},
		{"reg:skip", "skip", "", true},
		{"reg:fix:3", "fix", "3", true},
		{"reg:fix", "fix", "", true},
		{"reg", "", "", false},
		{"bet:a:10:5", "", "", false},
		{"reg:role:Mid:extra", "", "", false},
		{"reg::", "", "", false},
	}
	for _, tc := range cases {
		action, arg, ok := parseRegCallback(tc.in)
		if action != tc.action || arg != tc.arg || ok != tc.ok {
			t.Errorf("%q → (%q,%q,%v), want (%q,%q,%v)", tc.in, action, arg, ok, tc.action, tc.arg, tc.ok)
		}
	}
}

func flatten(kb tgbotapi.InlineKeyboardMarkup) []string {
	var out []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil {
				out = append(out, *btn.CallbackData)
			}
		}
	}
	return out
}

func TestRegKeyboards(t *testing.T) {
	cases := []struct {
		kbType string
		want   []string
	}{
		{application.KbRegCancel, []string{"reg:cancel"}},
		{application.KbRegSkip, []string{"reg:skip", "reg:cancel"}},
		{application.KbRegRoles, []string{"reg:role:Gold", "reg:role:Exp", "reg:role:Mid", "reg:role:Roam", "reg:role:Jungle", "reg:cancel"}},
		{application.KbRegConfirm + ":3", []string{"reg:confirm", "reg:fix:1", "reg:fix:2", "reg:fix:3", "reg:delete"}},
		{application.KbRegCard + ":2:sub", []string{"reg:fix:1", "reg:fix:2", "reg:sub", "reg:delete"}},
		{application.KbRegCard + ":7:", []string{"reg:fix:1", "reg:fix:2", "reg:fix:3", "reg:fix:4", "reg:fix:5", "reg:fix:6", "reg:fix:7", "reg:delete"}},
		{application.KbRegSoloConfirm, []string{"reg:confirm", "reg:fix"}},
	}
	for _, tc := range cases {
		kb, ok := regKeyboard(tc.kbType)
		if !ok {
			t.Errorf("%q: not recognised", tc.kbType)
			continue
		}
		got := flatten(kb)
		if len(got) != len(tc.want) {
			t.Errorf("%q: %v, want %v", tc.kbType, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: %v, want %v", tc.kbType, got, tc.want)
				break
			}
		}
	}
	if _, ok := regKeyboard("main_menu"); ok {
		t.Error("main_menu must not be treated as a registration keyboard")
	}
}
```

Imports: `"blackwatch/internal/application"`, `"testing"`, `tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/delivery/telegram/ 2>&1 | head`
Expected: build errors (`parseRegCallback`, `regKeyboard` undefined; also `GetTeamInfo` mismatch from Task 3).

- [ ] **Step 3: Write `registration.go`**

```go
package telegram

import (
	"blackwatch/internal/application"
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// callbackRegPrefix marks registration buttons: "reg:<action>[:<arg>]".
const callbackRegPrefix = "reg"

// parseRegCallback splits "reg:<action>[:<arg>]". Anything with a different
// prefix, an empty action, or more than one argument is rejected.
func parseRegCallback(data string) (action, arg string, ok bool) {
	parts := strings.Split(data, callbackSep)
	if len(parts) < 2 || len(parts) > 3 || parts[0] != callbackRegPrefix || parts[1] == "" {
		return "", "", false
	}
	if len(parts) == 3 {
		if parts[2] == "" {
			return "", "", false
		}
		arg = parts[2]
	}
	return parts[1], arg, true
}

func regData(parts ...string) string {
	return callbackRegPrefix + callbackSep + strings.Join(parts, callbackSep)
}

func regBtn(label string, data ...string) tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData(label, regData(data...))
}

// regKeyboard renders the application's registration keyboard types. Types
// with parameters carry them after ":" (see application.KbReg* docs).
func regKeyboard(kbType string) (tgbotapi.InlineKeyboardMarkup, bool) {
	kind, params, _ := strings.Cut(kbType, ":")
	cancel := tgbotapi.NewInlineKeyboardRow(regBtn("❌ Отмена", "cancel"))

	switch kind {
	case application.KbRegCancel:
		return tgbotapi.NewInlineKeyboardMarkup(cancel), true

	case application.KbRegSkip:
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(regBtn("⏭ Пропустить", "skip")),
			cancel,
		), true

	case application.KbRegRoles:
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(regBtn("Gold", "role", "Gold"), regBtn("Exp", "role", "Exp"), regBtn("Mid", "role", "Mid")),
			tgbotapi.NewInlineKeyboardRow(regBtn("Roam", "role", "Roam"), regBtn("Jungle", "role", "Jungle")),
			cancel,
		), true

	case application.KbRegConfirm:
		n, _ := strconv.Atoi(params)
		rows := [][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(regBtn("✅ Подтвердить", "confirm")),
		}
		rows = append(rows, fixRows(n)...)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("🗑 Удалить команду", "delete")))
		return tgbotapi.NewInlineKeyboardMarkup(rows...), true

	case application.KbRegCard:
		nStr, add, _ := strings.Cut(params, ":")
		n, _ := strconv.Atoi(nStr)
		rows := fixRows(n)
		switch add {
		case "sub":
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("➕ Замена", "sub")))
		case "player":
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("➕ Игрок", "sub")))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(regBtn("🗑 Удалить команду", "delete")))
		return tgbotapi.NewInlineKeyboardMarkup(rows...), true

	case application.KbRegSoloConfirm:
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(regBtn("✅ Подтвердить", "confirm"), regBtn("✏️ Исправить", "fix")),
		), true
	}
	return tgbotapi.InlineKeyboardMarkup{}, false
}

// fixRows lays out ✏️ 1..n, up to four per row.
func fixRows(n int) [][]tgbotapi.InlineKeyboardButton {
	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for i := 1; i <= n; i++ {
		row = append(row, regBtn(fmt.Sprintf("✏️ %d", i), "fix", strconv.Itoa(i)))
		if len(row) == 4 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// handleRegCallback answers a registration button: strips the keyboard from
// the message that carried it (so a second tap does nothing), runs the
// action, and replies with a fresh message.
func (b *Bot) handleRegCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	action, arg, ok := parseRegCallback(cb.Data)
	if !ok || cb.Message == nil {
		b.apiRespond(cb, "Эта кнопка больше не действует.", true)
		return
	}
	chatID := cb.Message.Chat.ID

	noKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	if _, err := b.bot.Request(tgbotapi.NewEditMessageReplyMarkup(chatID, cb.Message.MessageID, noKeyboard)); err != nil {
		b.logger.Warn("telegram: failed to strip registration keyboard in %d: %v", chatID, err)
	}

	text, kbType := b.service.HandleRegAction(ctx, cb.From.ID, action, arg)
	b.apiRespond(cb, "", false)
	b.sendMessage(chatID, text, kbType)
}
```

- [ ] **Step 4: Wire the dispatcher in `bot.go`**

Replace `handleCallbackQuery` with:
```go
func (b *Bot) handleCallbackQuery(parent context.Context, callback *tgbotapi.CallbackQuery) {
	if callback.From == nil {
		return
	}

	ctx, cancel := context.WithTimeout(parent, updateTimeout)
	defer cancel()

	if strings.HasPrefix(callback.Data, callbackRegPrefix+callbackSep) {
		b.handleRegCallback(ctx, callback)
		return
	}
	if b.bettingBot == nil {
		b.apiRespond(callback, "Ставки сейчас недоступны.", true)
		return
	}
	b.bettingBot.HandleCallback(ctx, callback)
}
```

- [ ] **Step 5: Use `regKeyboard` in `helpers.go`**

In `trySendMessage`, before the `switch kbType`, add:
```go
	if kb, ok := regKeyboard(kbType); ok {
		msg.ReplyMarkup = kb
		_, err := b.bot.Send(msg)
		return err
	}
```
Delete the `case "skip":`, `case "role":`, `case "cancel":` blocks. Keep `main_menu` and `default` (remove keyboard).

- [ ] **Step 6: `/my_team` in `handlers.go`**

```go
	case "/my_team":
		response, kbType = b.service.GetTeamInfo(ctx, chatID)
```

- [ ] **Step 7: Run everything**

Run: `gofmt -l internal/ && go build ./... && go vet ./... && go test ./... && golangci-lint run ./...`
Expected: all clean.

- [ ] **Step 8: Commit**

```bash
git add internal/delivery/telegram/
git commit -m "feat(telegram): inline-кнопки регистрации и диспетчер reg:-callback'ов"
```

---

### Task 5: `CreateTeammate` inserts the full row

**Files:**
- Modify: `internal/repository/telegram_postgres.go` (`CreateTeammate`)
- Modify: `internal/repository/integration_test.go` (new test)

**Interfaces:**
- Consumes: `models.TelegramPlayer` with `GameID`, `ZoneID`, `Stars`, `TelegramUsername`, `IsSubstitute` set by `saveLine` (Task 3).

- [ ] **Step 1: Write the failing integration test**

Append to `integration_test.go`:
```go
// Roster rows are now written in one insert from the parsed line; the old
// insert stored only the nickname and relied on a chain of updates.
func TestIntegrationCreateTeammateStoresEveryField(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewTelegramPostgres(db)

	team, err := repo.CreateTeam(ctx, uniqueName(t, "full"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM telegram_players WHERE team_id = $1`, team.ID)
		_, _ = db.Exec(`DELETE FROM telegram_teams WHERE id = $1`, team.ID)
	})

	want := &models.TelegramPlayer{
		TeamID: &team.ID, GameNickname: "Mate", GameID: "123456789", ZoneID: "1234",
		Stars: 25, TelegramUsername: "@mate", IsSubstitute: true,
	}
	if err := repo.CreateTeammate(ctx, want); err != nil {
		t.Fatalf("CreateTeammate: %v", err)
	}
	got, err := repo.GetTeamMembers(ctx, team.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("GetTeamMembers = (%d rows, %v)", len(got), err)
	}
	g := got[0]
	if g.GameNickname != want.GameNickname || g.GameID != want.GameID || g.ZoneID != want.ZoneID ||
		g.Stars != want.Stars || g.TelegramUsername != want.TelegramUsername || !g.IsSubstitute {
		t.Errorf("stored %+v, want %+v", g, *want)
	}
}
```

- [ ] **Step 2: Vet it (compiles against the old implementation, would fail at runtime)**

Run: `go vet -tags integration ./internal/repository/`
Expected: clean. If `BW_TEST_DSN` is set, `go test -tags integration ./internal/repository/ -run TestIntegrationCreateTeammateStoresEveryField` must FAIL with empty `GameID`.

- [ ] **Step 3: Implement**

```go
func (r *TelegramPostgres) CreateTeammate(ctx context.Context, p *models.TelegramPlayer) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO telegram_players
			(team_id, game_nickname, game_id, zone_id, stars, telegram_username, is_substitute)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, p.TeamID, p.GameNickname, p.GameID, p.ZoneID, p.Stars, p.TelegramUsername, p.IsSubstitute)
	return err
}
```

- [ ] **Step 4: Run**

Run: `go vet -tags integration ./... && go test ./...`; with a DB: `go test -tags integration ./internal/repository/ -count=1`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repository/
git commit -m "feat(telegram): CreateTeammate пишет всю строку игрока одной вставкой"
```

---

### Task 6: Manual smoke and cleanup

**Files:**
- Modify: `internal/application/telegram_service.go` (dead code), `internal/delivery/telegram/handlers.go` (admin help text unchanged)

- [ ] **Step 1: Dead code sweep**

Run: `golangci-lint run ./...` and `grep -rn "UpdateLastTeammateData" internal/` — it should have no callers left in `application`. If so, remove it from `repository.Telegram`, `TelegramPostgres`, and `fakeTelegramRepo`.

- [ ] **Step 2: Full check**

Run: `gofmt -l internal/ && go build ./... && go vet -tags integration ./... && go test ./... && golangci-lint run ./...`
Expected: clean.

- [ ] **Step 3: Live smoke (needs `TELEGRAM_TOKEN`)**

Start the bot, then in a private chat: `/reg_team` → name → `Cap 111111111 1111 30` → tap Mid → four players → slot 6 tap ⏭ → slot 7 tap ⏭ → card → tap ✏️ 2 → new line → role → card → ✅. Then `/my_team` → ➕ Замена → line → role → card shows 6 rows. Confirm the buttons vanish from each message after the tap.

- [ ] **Step 4: Commit**

```bash
git add -A internal/
git commit -m "chore(telegram): убрать UpdateLastTeammateData после перехода на полную вставку"
```
