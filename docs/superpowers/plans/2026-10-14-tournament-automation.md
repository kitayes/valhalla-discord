# Tournament Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** The bot runs a match end to end: correct result messages, game-by-game series with screenshots in the bot, ready and check-in reminders, BO format everywhere.

**Architecture:** Results go through one `recordResult` (Challonge first, cache by sync, stage-aware messages). Games are rows in `telegram_match_games` handled by `TelegramServiceImpl` under its report mutex; the bot routes photos and `game:` callbacks. Ready reminders live in the desk state machine (`DeskMatch.Reminded`) and reach every team member through `DeskContext.Members`. Check-in waves reuse the scheduler's `shouldFire`.

**Tech Stack:** Go, PostgreSQL, go-telegram-bot-api v5, vanilla JS.

**Spec:** `docs/superpowers/specs/2026-10-14-tournament-automation-design.md`

## Global Constraints

- Game auto-confirm: 5 minutes. Ready reminders: +5 and +9 minutes; deadline +10.
- Check-in waves: T-60, T-30, T-10.
- Need to win a series: `(bo+1)/2`.
- Russian user text, English comments; no emojis or icons.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

### Task 0: recordResult
- `internal/application/match_result.go` (new): `recordResult(ctx, m models.BracketMatch, winnerID, w, l int, override bool) error` and `notifyResult(...)` using `eliminated`/`QualifierStatus` style checks on the post-sync cache.
- `applyReport` and `SetWinnerDirect` call it; `propagateBracketResult` stays for the no-Challonge path only.
- Tests: DE upper loser gets "нижнюю сетку"; qualifier decider winner gets "основной этап"; SE final winner gets "Победа в финале"; Challonge written once.

### Task 1: games storage
- Migration `000035_match_games`; model `TelegramMatchGame`; repo `CreateMatchGame`, `GetMatchGame`, `ListMatchGames(matchID, revision)`, `SetMatchGameStatus`, `GetExpiredPendingGames(now)`, `ListGamesForMatches(ids)`; bracket matches expose `DeskRevision`. Integration test.

### Task 2: series service
- `internal/application/match_series.go`: `AddGamePhoto`, `ClaimGame(tgID, matchID, weWon)`, `ConfirmGame`, `DisputeGame`, `JudgeGame(admin, gameID, winnerID|0)`, `AutoConfirmExpiredGames`, `SeriesFor(matchID)`; on decision builds a confirmed report and calls `recordResult`.
- Tests for BO1/BO3/BO5, one pending at a time, dispute, judge, auto-confirm, stale revision, finished match.

### Task 3: bot wiring
- Photos outside FSM → series draft (album debounce like reports); callbacks `game:*`; opponent and judge media messages; auto-confirm sweep in `runScheduledChecks`.

### Task 4: ready reminders and judge buttons
- `DeskMatch.Reminded [2]int`, `DeskContext.Members map[int][]int64`; reminders in `queueNotices`; attention card buttons `forfeit0/forfeit1/extend_5`; bot handles forfeit through `SetWinnerDirect` with the walkover score.

### Task 5: check-in waves
- Scheduler fires T-60/T-30/T-10 with distinct texts.

### Task 6: BO everywhere + Mini App series
- `FormatLong(bo)` "BO3 — до 2 побед"; desk card, notices; `/api/bracket` `series_score`; active match `games`; UI.

### Task 7: verification
- `go vet`, unit + integration tests, JS syntax check.
