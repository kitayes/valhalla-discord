# Qualifiers, Lower Bracket, BO Formats and Check-in Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Qualifiers that stop at top-N and feed a main stage with invited teams, double-elimination awareness everywhere, per-tournament BO formats shown on every match, LIVE only where a match is really live, and an idempotent check-in that builds the bracket from checked-in teams only.

**Architecture:** All bracket arithmetic (layout, qualifier cutoff, alive teams, placements, BO format, round titles, score validation) lives in one pure file `internal/application/stage.go`, driven by a `Layout` built from the tournament row (with a stored `bracket_size`) and the cached matches. Services, the match desk, the bot and the web layer call those functions instead of their own hardcoded rules. The bracket cache of a qualifier never holds matches beyond the cutoff; the match desk and `GetBracketMatches` only read the active tournament.

**Tech Stack:** Go 1.24, PostgreSQL via golang-migrate (embedded `migrations/`), standard `testing`, vanilla JS in `internal/delivery/web/templates/app.html`.

**Spec:** `docs/superpowers/specs/2026-10-14-qualifiers-design.md`

## Global Constraints

- Stages: `qualifier`, `main`, `standalone` (default for existing rows).
- `advance_count` for a qualifier: power of two, ≥ 2 for single elimination, ≥ 4 for double elimination; default 8 in the UI.
- Qualifiers are single or double elimination only.
- BO values: 1, 3, 5. Defaults: rounds 1, semifinal 3, final 3.
- Tournament-team statuses: `registered`, `checked_in`, `disqualified`, `qualified`, `no_show`. Sources: `registered`, `qualified`, `invited`.
- League points by place: 1 → 100, 2 → 70, 3–4 → 50, 5–8 → 20, 9–16 → 10, else 5. Qualifiers award no points.
- Check-in closes at the tournament start or when the bracket is built, whichever is first; after that only admins change it.
- The bracket is built at the start time from checked-in teams only; unchecked entries become `no_show`. No technical-defeat sweep at T+10.
- User-facing text is Russian; code comments and logs are English. No emojis or decorative icons in UI.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## File Structure

- `migrations/000034_tournament_stages.{up,down}.sql` (new) — columns from the spec plus `telegram_tournaments.bracket_size`.
- `internal/models/telegram.go` — stage/format/source constants and fields.
- `internal/repository/telegram_postgres.go` — scan/write new columns; `EnterTeam`; check-in audit; active-tournament-only bracket reads.
- `internal/repository/match_desk_postgres.go` — desk reads only the active tournament's matches and passes the tournament in `DeskContext`.
- `internal/models/match_desk.go` — `DeskContext.Tournament`.
- `internal/application/stage.go` (new) + `stage_test.go` (new) — pure bracket arithmetic.
- `internal/application/bracket_stage.go` — round titles delegate to `stage.go`; `eliminationMatch` becomes DE-aware.
- `internal/application/bracket.go` — build from checked-in only, `bracket_size`, qualifier cache filter, placements via `stage.go`.
- `internal/application/telegram_service.go` — check-in API, tournament settings, qualifier finish, import/invite, registration guards, event hook.
- `internal/application/telegram_registration.go` — reminder button uses the new check-in.
- `internal/application/match_desk_service.go` — format/title via `stage.go`.
- `internal/delivery/telegram/{bot.go,bracket.go,handlers.go}` — build at T, no-show notices, qualifier auto-finish, `/uncheckin`, format in notices.
- `internal/delivery/web/{admin.go,tma_handlers.go,tma_admin_teams.go}` — endpoints.
- `internal/delivery/web/templates/app.html` — UI.
- `cmd/app/main.go` — wire the event hook to SSE.

---

### Task 1: Schema, models, repository

**Files:**
- Create: `migrations/000034_tournament_stages.up.sql`, `migrations/000034_tournament_stages.down.sql`
- Modify: `internal/models/telegram.go`, `internal/repository/repository.go`, `internal/repository/telegram_postgres.go`, `internal/application/telegram_service_test.go` (fake repo)

**Interfaces:**
- Produces:
  - `models.TelegramTournament` fields `Stage string`, `AdvanceCount int`, `FormatRounds int`, `FormatSemifinal int`, `FormatFinal int`, `BracketSize int` (json `stage`, `advance_count`, `format_rounds`, `format_semifinal`, `format_final`, `bracket_size`).
  - `models.TournamentTeam` fields `Source string`, `QualifiedFrom *int`, `CheckedInAt *time.Time`, `CheckedInBy *int64`.
  - Constants `StageQualifier="qualifier"`, `StageMain="main"`, `StageStandalone="standalone"`, `EntryRegistered="registered"`, `EntryCheckedIn="checked_in"`, `EntryQualified="qualified"`, `EntryNoShow="no_show"`, `SourceRegistered/SourceQualified/SourceInvited`.
  - Repo: `SetTournamentCheckIn(ctx, tournamentID, teamID int, status bool, by int64) error` (writes `checked_in_at=NOW()`, `checked_in_by=by` on every change); `EnterTeam(ctx, tournamentID, teamID int, source string, qualifiedFrom *int) error` (upsert; keeps check-in); `GetTournamentEntries(ctx, tournamentID int) ([]models.TournamentTeam, error)`.
  - `GetBracketMatches` returns an empty slice when no tournament is active.

- [ ] **Step 1: Migration**

```sql
-- 000034_tournament_stages.up.sql
ALTER TABLE telegram_tournaments
    ADD COLUMN IF NOT EXISTS stage VARCHAR(16) NOT NULL DEFAULT 'standalone',
    ADD COLUMN IF NOT EXISTS advance_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS format_rounds SMALLINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS format_semifinal SMALLINT NOT NULL DEFAULT 3,
    ADD COLUMN IF NOT EXISTS format_final SMALLINT NOT NULL DEFAULT 3,
    ADD COLUMN IF NOT EXISTS bracket_size INT NOT NULL DEFAULT 0;

ALTER TABLE telegram_tournament_teams
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'registered',
    ADD COLUMN IF NOT EXISTS qualified_from INT REFERENCES telegram_tournaments(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS checked_in_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS checked_in_by BIGINT;
```

```sql
-- 000034_tournament_stages.down.sql
ALTER TABLE telegram_tournament_teams
    DROP COLUMN IF EXISTS checked_in_by,
    DROP COLUMN IF EXISTS checked_in_at,
    DROP COLUMN IF EXISTS qualified_from,
    DROP COLUMN IF EXISTS source;
ALTER TABLE telegram_tournaments
    DROP COLUMN IF EXISTS bracket_size,
    DROP COLUMN IF EXISTS format_final,
    DROP COLUMN IF EXISTS format_semifinal,
    DROP COLUMN IF EXISTS format_rounds,
    DROP COLUMN IF EXISTS advance_count,
    DROP COLUMN IF EXISTS stage;
```

- [ ] **Step 2: Models** — add the fields and constants listed under Interfaces. `TelegramTournament` gets a helper:

```go
// Qualifier reports whether the tournament stops at AdvanceCount teams.
func (t *TelegramTournament) Qualifier() bool {
	return t != nil && t.Stage == StageQualifier && t.AdvanceCount > 0
}
```

- [ ] **Step 3: Repository** — one column list constant `tournamentColumns` used by `GetActiveTournament`, `GetTournamentByID`, `GetAllTournaments`, `CreateTournament ... RETURNING`, with a shared `scanTournament(row)`; `UpdateTournament` writes the six new columns. Defaults (`standalone`, 1/3/3) are applied in `CreateTournament`/`UpdateTournament` when zero. `GetTournamentTeam` scans the four new entry columns. `SetTournamentCheckIn` gains `by int64`; `checked_in` status is only written when the current status is `registered`/`checked_in`/`no_show` (it must not resurrect `qualified`). `EnterTeam`:

```go
func (r *TelegramPostgres) EnterTeam(ctx context.Context, tournamentID, teamID int, source string, qualifiedFrom *int) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO telegram_tournament_teams (tournament_id, team_id, status, is_checked_in, source, qualified_from)
		VALUES ($1, $2, 'registered', FALSE, $3, $4)
		ON CONFLICT (tournament_id, team_id) DO UPDATE SET source = EXCLUDED.source, qualified_from = EXCLUDED.qualified_from
	`, tournamentID, teamID, source, qualifiedFrom)
	return err
}
```

`GetBracketMatches` without an active tournament returns `nil, nil` instead of every tournament's matches. Update `repository.Telegram` and the fake repo (`fakeTelegramRepo`) to match (fake `SetTournamentCheckIn` records `CheckedInBy`; fake `EnterTeam`, `GetTournamentEntries`).

- [ ] **Step 4: Build and test** — `go build ./... && go test ./internal/...` → PASS (update the existing `SetTournamentCheckIn` callers to pass `0` for now).

- [ ] **Step 5: Commit** — `feat(tournament): stages, BO formats and check-in audit columns`

---

### Task 2: Pure bracket arithmetic (`stage.go`)

**Files:**
- Create: `internal/application/stage.go`, `internal/application/stage_test.go`
- Modify: `internal/application/bracket_stage.go`

**Interfaces:**
- Produces:

```go
type Layout struct {
	Double   bool // double elimination
	UBRounds int  // rounds in the upper (or only) bracket; final/UB final = UBRounds
	LBRounds int  // number of lower-bracket rounds (2*UBRounds-2 for DE)
	Other    bool // round robin / swiss: no stages
}
func NewLayout(t *models.TelegramTournament, ms []models.BracketMatch) Layout
func (l Layout) Cutoff(advance int) (c Cutoff)           // qualifier stop line
type Cutoff struct{ All bool; UB, LB int }               // All: everyone qualifies
func (c Cutoff) InStage(round int) bool
func (c Cutoff) Decider(round int) bool
func QualifierStatus(t *models.TelegramTournament, ms []models.BracketMatch, participants []int) QualifierOutcome
type QualifierOutcome struct { Done bool; Qualified []int; Pending []models.BracketMatch; Err error }
func FilterStage(t *models.TelegramTournament, ms []models.BracketMatch) []models.BracketMatch
func MatchFormat(t *models.TelegramTournament, ms []models.BracketMatch, round int) int   // 1, 3 or 5
func FormatLabel(bo int) string                                                         // "BO3"
func ValidateScore(bo, winner, loser int) error
func RoundTitle(t *models.TelegramTournament, ms []models.BracketMatch, round int) string
func Placements(t *models.TelegramTournament, ms []models.BracketMatch, teamIDs []int) (map[int]int, map[int]int)
func PointsForPlace(place int) int
func NextPow2(n int) int
```

Rules (from the spec):
- `NewLayout`: `Other` for round robin/swiss. Otherwise size = `t.BracketSize` if > 0, else derived: SE → `2^maxPositiveRound` (third-place match shares the final round); DE → if a negative round exists, `UBRounds = (LBRounds+2)/2` with `LBRounds = -minRound`; else `UBRounds = maxPositiveRound`. With `BracketSize > 0`: `UBRounds = log2(BracketSize)`, `LBRounds = 2*UBRounds-2` for DE.
- `Cutoff(A)`: SE `c = UBRounds - log2(A)`, `All = c <= 0`, `UB = c`. DE `u = UBRounds + 1 - log2(A)`, `All = u <= 1`, `UB = u`, `LB = 2u-2`. `InStage(r)`: `r>0 && r<=UB` or `r<0 && -r<=LB`. `Decider(r)`: `r==UB` or (DE) `r==-LB`.
- `QualifierStatus`: needed losses 1 (SE) / 2 (DE); only `InStage` matches count; `Pending` = in-stage matches not complete that have at least one team; `Done = len(Pending)==0`; `Qualified` = participants with fewer than needed losses, sorted; if `!All` and `len(Qualified) != A` → `Err` "после отсечения живы N команд, ожидалось A".
- `FilterStage`: for `t.Qualifier()` keep only `InStage` matches (all when `All`); other stages unchanged.
- `MatchFormat`: qualifier → `FormatFinal` for `Decider`, else `FormatRounds`. `Other` → `FormatRounds`. SE: `round==UBRounds` → final, `round==UBRounds-1` → semifinal. DE: `round>UBRounds` → final (grand final), `round==UBRounds` or `round==-LBRounds` → semifinal. Else rounds. Zero fields fall back to 1/3/3.
- `RoundTitle`: qualifier decider gets suffix " — за выход в основу". SE: existing `FormatRoundTitleWithContext` wording against `UBRounds`. DE: `r>UBRounds` "Гранд-финал", `r==UBRounds` "Финал верхней сетки", `r>0` "Верхняя сетка, Раунд r", `-r==LBRounds` "Финал нижней сетки", else "Нижняя сетка, Раунд -r". Other: "Тур |r|".
- `ValidateScore(bo, w, l)`: `need=(bo+1)/2`; `w==need && l>=0 && l<need` else error `"для BO%d счёт победителя должен быть %d, проигравшего — от 0 до %d"`.
- `Placements`: elimination key per team = `RoundSortKey(round, total, hasNeg)` of the match that gave it its eliminating loss (1st in SE, 2nd in DE; matches processed in sort-key order); never eliminated → key `+inf`. `place = 1 + count(other teams with a strictly greater key)`. Points via `PointsForPlace`.

- [ ] **Step 1: Write failing tests** in `stage_test.go` with hand-built match slices (helper `bm(round, play, t1, t2, winner int, state string)`; 0 means nil):
  - `TestCutoffSingle` — `Layout{UBRounds:4}.Cutoff(8)` → `{UB:1}`; `UBRounds:5` → `UB:2`; `UBRounds:3` → `All`.
  - `TestCutoffDouble` — `Layout{Double:true,UBRounds:4,LBRounds:6}.Cutoff(8)` → `{UB:2, LB:2}`; `UBRounds:5` → `{UB:3, LB:4}`; `UBRounds:3` → `All`.
  - `TestQualifierStatusSingle16` — 16 teams, R1 8 matches complete, one R2 match open → `Done`, 8 qualified = R1 winners, R2 match ignored.
  - `TestQualifierStatusPending` — one R1 match still open → `!Done`, `Pending` has it.
  - `TestQualifierStatusByes` — 12 teams (`BracketSize:16`), R1 4 matches complete → qualified = 4 winners + 4 bye teams.
  - `TestQualifierStatusDouble16` — UB R1 (8), UB R2 (4), LB -1 (4), LB -2 (4) complete → 4 UB winners + 4 LB survivors qualified; UB R3/LB -3 matches present but ignored.
  - `TestQualifierStatusCountMismatch` — participants list has an extra team with no matches and no bye slot → `Err != nil`.
  - `TestMatchFormat` — qualifier decider → FormatFinal; SE final/semi/early; DE grand final/UB final/LB final/early; zeros → 1/3/3.
  - `TestRoundTitleDouble` — titles listed above.
  - `TestValidateScore` — BO1 1:0 ok, 2:0 bad; BO3 2:1 ok, 3:0 bad, 1:0 bad; BO5 3:2 ok.
  - `TestPlacementsSingle8` — places 1,2,3,3,5,5,5,5; `TestPlacementsDouble8` — 1,2,3,4,5,5,7,7.
- [ ] **Step 2:** `go test ./internal/application -run 'Cutoff|QualifierStatus|MatchFormat|RoundTitle|ValidateScore|Placements' -v` → FAIL (undefined).
- [ ] **Step 3:** Implement `stage.go` per the rules. `FormatRoundTitleWithContext` stays for callers without a tournament.
- [ ] **Step 4:** Re-run → PASS; `go test ./internal/application` → PASS.
- [ ] **Step 5: Commit** — `feat(bracket): stage arithmetic for qualifiers, lower bracket and BO formats`

---

### Task 3: Bracket service

**Files:**
- Modify: `internal/application/bracket.go`, `internal/application/bracket_stage.go`, `internal/application/bracket_test.go`

**Interfaces:**
- Consumes: `NewLayout`, `FilterStage`, `NextPow2`, `Placements` (Task 2); `EnterTeam`, `SetTournamentCheckIn(..., by)` (Task 1).
- Produces: `BracketBuilt.NoShows []models.TelegramTeam`; `CalculatePlacements` now delegates to `Placements` (signature unchanged); `eliminationMatch` picks the loss with the highest `RoundSortKey`.

- [ ] **Step 1: Failing tests** in `bracket_test.go`:
  - `TestBuildOnlyCheckedIn` — 4 entries, 3 checked in → Challonge gets 3 participants; the 4th entry status `no_show`; `built.NoShows` has it.
  - `TestBuildStoresBracketSize` — 5 checked-in teams → active tournament `BracketSize == 8`.
  - `TestSyncQualifierDropsLaterRounds` — qualifier `AdvanceCount:2`, 4 checked-in teams: after build the cache holds only round 1 (final dropped).
  - Existing build tests: check teams in before building (helper `checkInAll(repo)`).
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3: Implement** — in `build`: pick teams with `IsCheckedIn` (entries of the active tournament; legacy fallback `GetAllTeams` also filters `IsCheckedIn`); entries not checked in and not disqualified → `SetTournamentTeamStatus(no_show)` and `NoShows`; set `activeTourney.BracketSize = NextPow2(len(seeded))` before `UpdateTournament`. In `sync` and `syncAfterReport`: after building `ms`, `ms = FilterStage(activeTourney, ms)` (for `syncAfterReport` read the active tournament). `syncAfterReport` resolves participants through tournament entries, like `sync`. Rewrite `CalculatePlacements` as `Placements(nil-type-aware)` — detect DE by negative rounds; `eliminationMatch` by max sort key.
- [ ] **Step 4:** `go test ./internal/application` → PASS.
- [ ] **Step 5: Commit** — `feat(bracket): build from checked-in teams, cut qualifiers at the advance line`

---

### Task 4: Tournament service — settings, qualifier finish, main stage

**Files:**
- Modify: `internal/application/telegram_service.go`, `internal/application/tournament_lifecycle_test.go`, `internal/delivery/web/tma_handlers_test.go` (mock)

**Interfaces:**
- Produces (on `TelegramService`):

```go
type TournamentSettings struct {
	Name, Slug, TournamentType, SeedingType, Stage string
	HoldThirdPlace bool
	TournamentTime *time.Time
	AdvanceCount, FormatRounds, FormatSemifinal, FormatFinal int
}
CreateTournament(ctx context.Context, s TournamentSettings) (*models.TelegramTournament, error)
UpdateTournamentSettings(ctx context.Context, id int, s TournamentSettings) (*models.TelegramTournament, error)
FinishTournament(ctx context.Context, id int) (*FinishResult, error)
type FinishResult struct { Tournament *models.TelegramTournament; Qualified []models.TelegramTeam }
TryFinishQualifier(ctx context.Context) (*FinishResult, error) // nil,nil when not done / not a qualifier
ImportQualified(ctx context.Context, mainID int, fromIDs []int) (int, error)
InviteTeam(ctx context.Context, mainID, teamID int) error
SetEventHook(fn func(event string, payload map[string]any))
```

- Validation (`validateSettings`): stage in the three values; qualifier → type SE or DE, `AdvanceCount` power of two, ≥2 (SE) / ≥4 (DE); formats in {1,3,5}. Update refuses stage/advance changes on `completed` tournaments.
- `FinishTournament`: qualifier → `QualifierStatus` over the **full** cache (`GetBracketMatchesForTournament`), participants = entries with a participant id; `!Done` → error listing pending matches `#play_order`; `Err` → return it; else entries of `Qualified` → status `qualified`; cache rewritten with `FilterStage`; `FinishTournamentRecord(id, nil, "")`; placements not written. Other stages → `Placements` as before. When the finished tournament is the active one, clear `challonge_tournament_id/_url/_for`, `tournament_time` setting and `s.tournamentTime`.
- `TryFinishQualifier`: active tournament is a qualifier with status `active`, `QualifierStatus.Done && Err==nil` → `FinishTournament`.
- `ImportQualified`: main stage only; for each `fromID` that is a completed qualifier, entries with status `qualified` → `EnterTeam(main, team, qualified, &fromID)`. Returns count.
- `InviteTeam`: main stage only, team exists, not disqualified → `EnterTeam(main, team, invited, nil)`.
- Guards: `RegisterTeamForTournament` and the auto-entry in `recordCheckIn` refuse a `main` tournament ("на основной этап попадают только прошедшие квалификацию и приглашённые команды") and refuse a qualifier when the team has a `qualified` entry in another qualifier ("команда уже прошла в основной этап через другую квалификацию"). New repo method `HasQualifiedEntry(ctx, teamID, exceptTournamentID int) (bool, error)`.
- Callers: bot `/new_tournament` and web create pass `TournamentSettings`.

- [ ] **Step 1: Failing tests** in `tournament_lifecycle_test.go`: `TestFinishQualifierMarksQualified` (16-team SE cache in fake repo → 8 entries `qualified`, cache keeps round 1 only, settings cleared); `TestFinishQualifierPendingFails`; `TestImportAndInvite`; `TestMainStageRefusesSelfRegistration`; `TestQualifiedTeamBlockedFromSecondQualifier`; `TestSettingsValidation`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/...` → PASS.
- [ ] **Step 5: Commit** — `feat(tournament): qualifier finish, main stage entries and settings`

---

### Task 5: Check-in

**Files:**
- Modify: `internal/application/telegram_service.go`, `internal/application/telegram_registration.go`, `internal/application/telegram_admin_teams.go`, `internal/application/telegram_service_test.go`, `internal/application/tournament_lifecycle_test.go`

**Interfaces:**
- Produces:

```go
type CheckInResult struct { OK bool; CheckedIn bool; Message string }
SetTeamCheckIn(ctx context.Context, tgID int64, want bool) CheckInResult  // replaces ToggleCheckIn
```

- Rules: captain only; disqualified → refusal; `want == current` → OK with the current-state message, no write; window closed (`tournament.Status` not registration/draft, or start time passed) → refusal "Check-in закрыт: турнир уже начался. Обратитесь к организаторам."; `want` true needs `mainRosterSize >= 5` (non-substitutes); write via `recordCheckIn(ctx, st, teamID, want, by=tgID)`; on change fire hook `checkin_update` `{team_id, is_checked_in}`.
- `confirmCheckIn` (reminder button) → `SetTeamCheckIn(..., true)`.
- `AdminTeamAction` check-in passes `by = 0` and bypasses the window.
- `GetCheckInStatus` uses active tournament entries (same source as `GetCheckInSummary`); incomplete = main roster < 5.
- `DisqualifyUnchecked` and `GetUncheckedTeams` skip `no_show` entries.

- [ ] **Step 1: Failing tests**: `TestCheckInIdempotent` (true twice → still checked in, one write), `TestUncheckInExplicit`, `TestCheckInClosedAfterStart` (clock past start → refusal both ways), `TestCheckInClosedWhenActive`, `TestCheckInCountsMainRosterOnly` (4 main + 1 substitute → refusal), `TestCheckInRecordsActor`, `TestCheckInFiresHook`. Rewrite existing `ToggleCheckIn` tests to the new API.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement; remove `ToggleCheckIn` from the interface and mock.
- [ ] **Step 4:** `go test ./internal/...` → PASS.
- [ ] **Step 5: Commit** — `fix(checkin): explicit state, closed after start, main roster only`

---

### Task 6: Match desk and repository reads per tournament

**Files:**
- Modify: `internal/models/match_desk.go`, `internal/repository/match_desk_postgres.go`, `internal/application/match_desk_service.go`, `internal/application/telegram_service.go` (`GetBracketMatchDetails`)

- [ ] **Step 1:** `DeskContext` gains `Tournament *TelegramTournament`. `UpdateMatchDesk` loads the active tournament; none → error "сетка ещё не построена"; bracket rows filtered `WHERE m.tournament_id = $1`.
- [ ] **Step 2:** Replace the three hardcoded format/title blocks in `match_desk_service.go` and `GetBracketMatchDetails` with `FormatLabel(MatchFormat(t, ms, round))` and `RoundTitle(t, ms, round)`.
- [ ] **Step 3:** `go test ./internal/...` → PASS (fix fixtures that build `DeskContext` without a tournament: nil tournament → defaults).
- [ ] **Step 4: Commit** — `fix(desk): read only the active tournament, BO format from settings`

---

### Task 7: Telegram bot

**Files:**
- Modify: `internal/delivery/telegram/bot.go`, `internal/delivery/telegram/bracket.go`, `internal/delivery/telegram/handlers.go`, tests in the same package

- [ ] **Step 1:** `bracketBuildDue(t, now)` → `!now.Before(t) && now.Sub(t) <= scheduleCatchUpWindow`; update its test. Remove the `processTechnicalDefeat` call from `runScheduledChecks` (keep the function for `/tech_defeat`-style manual use only if referenced; otherwise delete it with `technicalDefeatGrace` usages in texts). Reminder text: "иначе команда не попадёт в сетку" with deadline = start time.
- [ ] **Step 2:** `announceBracket`: no check-in button or deadline in round-1 texts; `built.NoShows` → each team "Команда '%s' не прошла check-in и не попала в сетку." and admins "Не прошли check-in: …".
- [ ] **Step 3:** `runBracketChecks`: after flush, `res, err := b.service.TryFinishQualifier(ctx)`; on result → tournament chat "КВАЛИФИКАЦИЯ ЗАВЕРШЕНА\n\n«%s»: в основной этап прошли:\n1. …"; admins on error (once per error text).
- [ ] **Step 4:** `notifyMatchesReady` text: "Матч #%d — %s, %s: %s vs %s" with `RoundTitle` and `FormatLabel(MatchFormat)` (needs active tournament + matches from `b.bracket.Matches`). `formatBracketRound` header uses `RoundTitle`.
- [ ] **Step 5:** `/checkin` → `SetTeamCheckIn(true)`, new `/uncheckin` → `SetTeamCheckIn(false)`; add to command lists. `/finish_tournament` prints qualified list for qualifiers.
- [ ] **Step 6:** `go test ./internal/delivery/telegram` → PASS. **Commit** — `feat(telegram): build at start, qualifier results, BO in notices`

---

### Task 8: Web API

**Files:**
- Modify: `internal/delivery/web/admin.go`, `internal/delivery/web/tma_handlers.go`, `internal/delivery/web/tma_handlers_test.go`, `cmd/app/main.go`

- [ ] **Step 1:** `POST /api/team/checkin` body `{"checked_in": bool}` (missing body → `true`); response `{ok, message, is_checked_in}`; 400 with message when `!OK`. No string parsing.
- [ ] **Step 2:** Create accepts `stage, advance_count, format_rounds, format_semifinal, format_final`; new `POST /api/admin/tournament/update` `{tournament_id, ...same fields}`; finish returns `{ok, qualified:[names]}`; new `POST /api/admin/tournament/import-qualified {tournament_id, from_ids}`, `POST /api/admin/tournament/invite {tournament_id, team_id}`. All broadcast `tournaments_update`.
- [ ] **Step 3:** `/api/bracket` response adds per match `match_format` and `round_name` (from `stage.go`), plus `tournament_status` and `stage`; schedule items use the same functions. Bracket for a qualifier passes through `FilterStage`.
- [ ] **Step 4:** `AdminServer.Broadcast(event string, payload map[string]any)`; `main.go` wires `services.TelegramService.SetEventHook(adminServer.Broadcast)`.
- [ ] **Step 5:** Tests for the check-in body and the update endpoint (mock records the call). `go test ./internal/delivery/web` → PASS. **Commit** — `feat(web): stage settings, qualifier finish, import/invite, explicit check-in`

---

### Task 9: Mini App UI

**Files:**
- Modify: `internal/delivery/web/templates/app.html`

- [ ] **Step 1:** Bracket cards: status LIVE only when `tournament_status === 'active' && m.state === 'open'`; open matches of a completed tournament → "НЕ СЫГРАН"; plain-text pill `BO3` from `m.match_format` next to the status; round header uses `round_name`. Stage pill above the bracket ("Квалификация", "Основной этап"). Live counter counts only LIVE.
- [ ] **Step 2:** Match modal subtitle and "Мой матч" use the server's `match_format`/`round_name` (no 'BO1' fallback hiding errors — show nothing when absent).
- [ ] **Step 3:** Check-in button sends `{checked_in: !currentTeamCheckedIn}`; un-check asks `confirm`; after the response apply `res.is_checked_in`; `visibilitychange` → `loadProfile()` when visible. Button disabled with text "Check-in закрыт" when the active tournament is not in registration.
- [ ] **Step 4:** Admin create form: selects "Этап" (Обычный/Квалификация/Основной), "Сколько выходит" (shown for qualifier, default 8), three BO selects. Tournament list: "Настройки" opens the same form prefilled → update endpoint. Qualifier rows: "Завершить квалификацию" (finish endpoint, shows qualified names). Main rows: "Добавить прошедших" with checkboxes of completed qualifiers. Admin team panel: "Пригласить в основной этап" when the active tournament is `main` and the team has no entry.
- [ ] **Step 5:** Manual check in the browser preview with `go run ./cmd/app` is not possible without Telegram auth; verify by `go test ./internal/delivery/web` (template parses) and by reading the rendered JS for syntax with `node --check` on the extracted script. **Commit** — `feat(webapp): BO pills, stage badge, qualifier admin tools, explicit check-in`

---

### Task 10: Final verification

- [ ] `gofmt -l internal cmd` → empty; `go vet ./...`; `go test ./...` → PASS.
- [ ] Re-read the spec sections and tick each against the code.
