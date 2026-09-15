# Турнирная сетка через Challonge — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Сетка Single Elimination строится в Challonge за час до турнира; неявка на чек-ин — тех. поражение в матче сетки; `/report` привязан к матчу; победитель продвигается по отчёту капитана; откат — командой админа.

**Architecture:** Challonge — источник правды. Тонкий HTTP-клиент `internal/challonge` за интерфейсом `application.BracketProvider`. `application.BracketService` пишет только в Challonge и после каждой записи перезаписывает кэш `telegram_bracket_matches`; все чтения — из кэша. Telegram-слой получает `/bracket`, `/build_bracket`, `/set_winner`, хуки в планировщик (построение за час до старта, ТП, досылка отчётов).

**Tech Stack:** Go 1.2x, `net/http` + `encoding/json` (без внешних SDK), PostgreSQL через `database/sql` + `lib/pq`, golang-migrate (embedded SQL), `go-telegram-bot-api/v5`, стандартный `testing` + `httptest`.

**Spec:** `docs/superpowers/specs/2026-09-15-challonge-bracket-design.md`

## Global Constraints

- Challonge API v2.1: база `https://api.challonge.com/v2.1`, заголовки `Authorization-Type: v1`, `Authorization: <key>`, `Content-Type: application/vnd.api+json`, `Accept: application/json`. Пути оканчиваются на `.json`.
- Бесплатная квота — 500 запросов/месяц. Ни одна команда чтения (`/bracket`, `/report` до сабмита, пинги) не ходит в API.
- `CHALLONGE_API_KEY` пустой → сетка выключена, `/report` работает как сейчас. Ничего из существующего поведения не ломается без ключа.
- `BRACKET_WALKOVER_SCORE` по умолчанию `1-0`.
- Только Single Elimination, один активный турнир (как `tournament_time`).
- Тексты пользователю — по-русски, в стиле существующих сообщений бота (без Markdown, `ParseMode` пустой).
- Модуль называется `blackwatch` (см. `go.mod`); импорты вида `blackwatch/internal/...`.
- Проверка: `go build ./... && go vet ./... && go test ./...`. Интеграционные тесты репозитория — `go test -tags integration ./internal/repository/...` с `BW_TEST_DSN` (Postgres из quay.io, см. память проекта).
- Коммиты — на русском в стиле `feat(telegram): ...`, `docs: ...`, с трейлером `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`.

## Файлы

| Файл | Ответственность |
|---|---|
| `pkg/config/config.go` | три новые переменные окружения + валидация |
| `.env.example` | документация переменных |
| `migrations/000027_bracket_matches.{up,down}.sql` | кэш матчей, participant id команды, привязка отчёта |
| `internal/models/telegram.go` | `BracketMatch`, поля у `TelegramTeam` и `TelegramMatchReport` |
| `internal/repository/repository.go` | новые методы интерфейса `Telegram` |
| `internal/repository/telegram_postgres.go` | SQL новых методов, скан `challonge_participant_id` |
| `internal/repository/integration_test.go` | интеграционный тест кэша |
| `internal/challonge/client.go` | HTTP-клиент v2.1 |
| `internal/challonge/client_test.go` | тесты на `httptest` + live-тест по ключу |
| `internal/application/bracket.go` | `BracketProvider`, `BracketService`, посев |
| `internal/application/bracket_test.go` | тесты сервиса на фейках |
| `internal/application/telegram_report.go` | `/report` по матчу из сетки |
| `internal/application/telegram_service.go` | `WithBracket`, поле `bracket` |
| `internal/application/service.go` | сборка `BracketService` |
| `internal/delivery/telegram/bracket.go` | команды, рассылки, форматирование |
| `internal/delivery/telegram/bracket_test.go` | форматирование, решение «пора строить» |
| `internal/delivery/telegram/bot.go` | поле `bracket`, хуки в воркер и ТП |
| `internal/delivery/telegram/handlers.go` | диспетчеризация новых команд, `/reinstate` |
| `internal/delivery/telegram/report.go` | пинг «пара готова» после сабмита |
| `cmd/app/main.go` | wiring клиента |

---

### Task 1: Конфиг

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `.env.example`
- Test: `pkg/config/config_test.go` (создать, если нет)

**Interfaces:**
- Produces: `Config.ChallongeAPIKey string`, `Config.ChallongeSubdomain string`, `Config.BracketWalkoverScore string`, `func (c *Config) BracketEnabled() bool`.

- [ ] **Step 1: Проверить, есть ли уже тест конфига**

Run: `ls pkg/config/`
Если `config_test.go` нет — создать с пакетом `config` и импортами `testing`, `strings`.

- [ ] **Step 2: Написать падающий тест**

```go
func TestBracketWalkoverScoreMustBeTwoNumbers(t *testing.T) {
	c := &Config{ChallongeAPIKey: "k", BracketWalkoverScore: "abc"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "BRACKET_WALKOVER_SCORE") {
		t.Fatalf("Validate() = %v, want BRACKET_WALKOVER_SCORE error", err)
	}
	c.BracketWalkoverScore = "1-0"
	if got := c.BracketEnabled(); !got {
		t.Error("BracketEnabled() = false with API key set")
	}
	c.ChallongeAPIKey = ""
	if c.BracketEnabled() {
		t.Error("BracketEnabled() = true with empty key")
	}
}
```

Если `Validate()` в этом пакете требует другие обязательные поля (проверь начало функции), заполни их в тесте минимально валидными значениями.

- [ ] **Step 3: Запустить — убедиться, что падает**

Run: `go test ./pkg/config/ -run TestBracketWalkover -v`
Expected: FAIL — `c.ChallongeAPIKey undefined`.

- [ ] **Step 4: Реализация**

В структуру `Config` после `TournamentTZ`:

```go
	// ChallongeAPIKey turns the tournament bracket on. Empty means no bracket:
	// /report keeps its free-form opponent picker and nothing calls Challonge.
	ChallongeAPIKey string `env:"CHALLONGE_API_KEY" envDefault:""`
	// ChallongeSubdomain is the Challonge community the tournaments are created
	// under; empty creates them on the key owner's account.
	ChallongeSubdomain string `env:"CHALLONGE_SUBDOMAIN" envDefault:""`
	// BracketWalkoverScore is reported for a technical defeat, "<winner>-<loser>".
	BracketWalkoverScore string `env:"BRACKET_WALKOVER_SCORE" envDefault:"1-0"`
```

В `Validate()` перед `return errors.Join(errs...)`:

```go
	if _, _, err := ParseWalkoverScore(c.BracketWalkoverScore); err != nil {
		errs = append(errs, fmt.Errorf("BRACKET_WALKOVER_SCORE: %w", err))
	}
```

В конец файла:

```go
// BracketEnabled reports whether the Challonge bracket is configured.
func (c *Config) BracketEnabled() bool {
	return c.ChallongeAPIKey != ""
}

// ParseWalkoverScore reads "W-L" into two non-negative ints with W > L.
func ParseWalkoverScore(s string) (win, lose int, err error) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want W-L, got %q", s)
	}
	win, err1 := strconv.Atoi(parts[0])
	lose, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || win < 0 || lose < 0 || win <= lose {
		return 0, 0, fmt.Errorf("want W-L with W > L, got %q", s)
	}
	return win, lose, nil
}
```

Добавь `strconv` и `strings` в импорты, если их нет.

- [ ] **Step 5: Запустить тест**

Run: `go test ./pkg/config/ -v`
Expected: PASS.

- [ ] **Step 6: `.env.example`**

После блока `TOURNAMENT_TZ=...`:

```
# Турнирная сетка в Challonge. Пустой ключ = сетка выключена, /report работает
# без привязки к матчу. Бесплатный план — 500 запросов/месяц (~1 турнир на 100 команд).
CHALLONGE_API_KEY=
CHALLONGE_SUBDOMAIN=
BRACKET_WALKOVER_SCORE=1-0
```

- [ ] **Step 7: Коммит**

```bash
git add pkg/config/ .env.example
git commit -m "feat(config): CHALLONGE_API_KEY, CHALLONGE_SUBDOMAIN, BRACKET_WALKOVER_SCORE"
```

---

### Task 2: Миграция, модели, репозиторий

**Files:**
- Create: `migrations/000027_bracket_matches.up.sql`, `migrations/000027_bracket_matches.down.sql`
- Modify: `internal/models/telegram.go`
- Modify: `internal/repository/repository.go` (интерфейс `Telegram`)
- Modify: `internal/repository/telegram_postgres.go`
- Modify: `internal/application/telegram_service_test.go` (фейк репо — новые методы)
- Test: `internal/repository/integration_test.go`

**Interfaces:**
- Produces:
  - `models.BracketMatch{ID int; ChallongeMatchID int64; Round, PlayOrder int; Team1ID, Team2ID, WinnerID *int; Team1Name, Team2Name string; State, ScoresCSV string; BothNotified bool}`
  - константы `models.BracketPending = "pending"`, `models.BracketOpen = "open"`, `models.BracketComplete = "complete"`
  - `models.TelegramTeam.ChallongeParticipantID *int64`
  - `models.TelegramMatchReport.BracketMatchID *int`, `.SyncedAt *time.Time`
  - `repository.Telegram`:
    - `ReplaceBracketMatches(ctx, matches []models.BracketMatch) error`
    - `GetBracketMatches(ctx) ([]models.BracketMatch, error)`
    - `MarkBracketNotified(ctx, ids []int) error`
    - `SetTeamParticipantID(ctx, teamID int, participantID int64) error`
    - `ClearTeamParticipantIDs(ctx) error`
    - `SetReportSynced(ctx, reportID int) error`
    - `GetUnsyncedReports(ctx) ([]models.TelegramMatchReport, error)`

- [ ] **Step 1: Миграция**

`migrations/000027_bracket_matches.up.sql`:

```sql
-- The bracket lives in Challonge; this table is a read cache of its matches so
-- "who is my opponent" and /bracket cost no API requests (500/month free).
-- It is rewritten whole after every write to Challonge.
CREATE TABLE telegram_bracket_matches (
    id                 SERIAL PRIMARY KEY,
    challonge_match_id BIGINT NOT NULL UNIQUE,
    round              INT NOT NULL,
    play_order         INT NOT NULL,
    team1_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    team2_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    winner_id          INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    state              VARCHAR(16) NOT NULL,
    scores_csv         VARCHAR(32) NOT NULL DEFAULT '',
    both_notified      BOOLEAN NOT NULL DEFAULT FALSE,
    synced_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_bracket_matches_team1 ON telegram_bracket_matches(team1_id);
CREATE INDEX idx_bracket_matches_team2 ON telegram_bracket_matches(team2_id);

ALTER TABLE telegram_teams ADD COLUMN challonge_participant_id BIGINT;

ALTER TABLE telegram_match_reports
    ADD COLUMN bracket_match_id INT REFERENCES telegram_bracket_matches(id) ON DELETE SET NULL,
    ADD COLUMN synced_at TIMESTAMPTZ;
```

`migrations/000027_bracket_matches.down.sql`:

```sql
ALTER TABLE telegram_match_reports DROP COLUMN synced_at, DROP COLUMN bracket_match_id;
ALTER TABLE telegram_teams DROP COLUMN challonge_participant_id;
DROP TABLE telegram_bracket_matches;
```

- [ ] **Step 2: Модели**

В `internal/models/telegram.go` — к `TelegramTeam` после `Status`:

```go
	// ChallongeParticipantID is the team's id in the Challonge bracket, nil
	// until a bracket is built.
	ChallongeParticipantID *int64 `json:"challonge_participant_id"`
```

К `TelegramMatchReport` после `PhotoFileIDs`:

```go
	// BracketMatchID links the report to the cached bracket match; nil for
	// reports made without a bracket.
	BracketMatchID *int `json:"bracket_match_id"`
	// SyncedAt is when the result reached Challonge; nil means still queued.
	SyncedAt *time.Time `json:"synced_at"`
```

В конец файла:

```go
// Bracket match states, as Challonge reports them.
const (
	BracketPending  = "pending"  // waiting for an earlier match
	BracketOpen     = "open"     // both sides known, not played
	BracketComplete = "complete"
)

// BracketMatch is one cached Challonge match. Team ids are nil while the
// slot is not decided yet.
type BracketMatch struct {
	ID               int    `json:"id"`
	ChallongeMatchID int64  `json:"challonge_match_id"`
	Round            int    `json:"round"`
	PlayOrder        int    `json:"play_order"`
	Team1ID          *int   `json:"team1_id"`
	Team2ID          *int   `json:"team2_id"`
	WinnerID         *int   `json:"winner_id"`
	Team1Name        string `json:"team1_name,omitempty"`
	Team2Name        string `json:"team2_name,omitempty"`
	State            string `json:"state"`
	ScoresCSV        string `json:"scores_csv"`
	BothNotified     bool   `json:"both_notified"`
}

// Has reports whether the team plays in this match.
func (m BracketMatch) Has(teamID int) bool {
	return (m.Team1ID != nil && *m.Team1ID == teamID) || (m.Team2ID != nil && *m.Team2ID == teamID)
}

// Opponent returns the other side for teamID, or nil if unknown.
func (m BracketMatch) Opponent(teamID int) *int {
	if m.Team1ID != nil && *m.Team1ID == teamID {
		return m.Team2ID
	}
	if m.Team2ID != nil && *m.Team2ID == teamID {
		return m.Team1ID
	}
	return nil
}

// Ready reports whether both sides are known and the match is unplayed.
func (m BracketMatch) Ready() bool {
	return m.State == BracketOpen && m.Team1ID != nil && m.Team2ID != nil
}
```

- [ ] **Step 3: Интерфейс репозитория**

В `internal/repository/repository.go`, интерфейс `Telegram`, после `GetRecentMatchReports`:

```go
	// Bracket cache. ReplaceBracketMatches rewrites the table in one
	// transaction; both_notified survives for rows whose team pair is unchanged.
	ReplaceBracketMatches(ctx context.Context, matches []models.BracketMatch) error
	GetBracketMatches(ctx context.Context) ([]models.BracketMatch, error)
	MarkBracketNotified(ctx context.Context, ids []int) error
	SetTeamParticipantID(ctx context.Context, teamID int, participantID int64) error
	ClearTeamParticipantIDs(ctx context.Context) error
	// Reports queued for Challonge: bracket_match_id set, synced_at NULL.
	SetReportSynced(ctx context.Context, reportID int) error
	GetUnsyncedReports(ctx context.Context) ([]models.TelegramMatchReport, error)
```

- [ ] **Step 4: Заглушки в фейке, чтобы всё компилировалось**

В `internal/application/telegram_service_test.go` к `fakeTelegramRepo` добавить поля и методы:

```go
	bracket      []models.BracketMatch
	participants map[int]int64 // teamID -> challonge participant id
```

(инициализировать `participants: map[int]int64{}` в `newFakeTelegramRepo`)

```go
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
		if rep.BracketMatchID != nil && rep.SyncedAt == nil {
			out = append(out, rep)
		}
	}
	return out, nil
}
```

Добавить `"time"` в импорты теста. Убедись, что `GetAllTeams` в фейке возвращает команды с `ChallongeParticipantID` (он читает из `r.teams`, значит достаточно того, что `SetTeamParticipantID` проставляет поле).

Run: `go build ./... && go vet ./internal/application/`
Expected: ошибка только о том, что `*TelegramPostgres` не реализует интерфейс — переходим к Step 5.

- [ ] **Step 5: Postgres — participant id в командах**

В `telegram_postgres.go`:

`GetTeamByID` и `GetTeamByName` — заменить запрос и Scan:

```go
	err := r.db.QueryRowContext(ctx, `SELECT id, name, COALESCE(is_checked_in, FALSE), COALESCE(status, 'active'), challonge_participant_id FROM telegram_teams WHERE id = $1`, id).
		Scan(&t.ID, &t.Name, &t.IsCheckedIn, &t.Status, &t.ChallongeParticipantID)
```

(для `GetTeamByName` — `WHERE name = $1`, аргумент `name`). `*int64` сканируется `database/sql` напрямую: NULL → nil.

`GetAllTeams` — в SELECT после `COALESCE(t.status, 'active')` добавить `t.challonge_participant_id`; в объявлениях `var pParticipant sql.NullInt64`; в `rows.Scan` после `&t.Status` — `&pParticipant`; при создании записи в `teamsMap`:

```go
			team := &models.TelegramTeam{ID: t.ID, Name: t.Name, IsCheckedIn: t.IsCheckedIn, Status: t.Status, Players: []models.TelegramPlayer{}}
			if pParticipant.Valid {
				pid := pParticipant.Int64
				team.ChallongeParticipantID = &pid
			}
			teamsMap[t.ID] = team
```

- [ ] **Step 6: Postgres — новые методы**

В конец `telegram_postgres.go`:

```go
func (r *TelegramPostgres) SetTeamParticipantID(ctx context.Context, teamID int, pid int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_teams SET challonge_participant_id = $2 WHERE id = $1`, teamID, pid)
	return err
}

func (r *TelegramPostgres) ClearTeamParticipantIDs(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_teams SET challonge_participant_id = NULL`)
	return err
}

// ReplaceBracketMatches rewrites the cache. both_notified is the only column
// Challonge does not own: it survives the rewrite while the two slots hold the
// same teams and resets when a rollback puts a different pair in the match.
// Rows are upserted by challonge_match_id (reports point at cache ids) and
// only the vanished ones are deleted.
func (r *TelegramPostgres) ReplaceBracketMatches(ctx context.Context, matches []models.BracketMatch) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	ids := make([]int64, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ChallongeMatchID)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO telegram_bracket_matches
				(challonge_match_id, round, play_order, team1_id, team2_id, winner_id, state, scores_csv, both_notified, synced_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, FALSE, NOW())
			ON CONFLICT (challonge_match_id) DO UPDATE SET
				round = EXCLUDED.round, play_order = EXCLUDED.play_order,
				team1_id = EXCLUDED.team1_id, team2_id = EXCLUDED.team2_id, winner_id = EXCLUDED.winner_id,
				state = EXCLUDED.state, scores_csv = EXCLUDED.scores_csv, synced_at = NOW(),
				both_notified = CASE
					WHEN telegram_bracket_matches.team1_id IS NOT DISTINCT FROM EXCLUDED.team1_id
					 AND telegram_bracket_matches.team2_id IS NOT DISTINCT FROM EXCLUDED.team2_id
					THEN telegram_bracket_matches.both_notified ELSE FALSE END
		`, m.ChallongeMatchID, m.Round, m.PlayOrder, m.Team1ID, m.Team2ID, m.WinnerID, m.State, m.ScoresCSV)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_bracket_matches WHERE NOT (challonge_match_id = ANY($1))`, pq.Array(ids)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TelegramPostgres) GetBracketMatches(ctx context.Context) ([]models.BracketMatch, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.id, m.challonge_match_id, m.round, m.play_order,
		       m.team1_id, m.team2_id, m.winner_id,
		       COALESCE(t1.name, ''), COALESCE(t2.name, ''),
		       m.state, m.scores_csv, m.both_notified
		FROM telegram_bracket_matches m
		LEFT JOIN telegram_teams t1 ON t1.id = m.team1_id
		LEFT JOIN telegram_teams t2 ON t2.id = m.team2_id
		ORDER BY m.round, m.play_order
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var out []models.BracketMatch
	for rows.Next() {
		var m models.BracketMatch
		if err := rows.Scan(&m.ID, &m.ChallongeMatchID, &m.Round, &m.PlayOrder,
			&m.Team1ID, &m.Team2ID, &m.WinnerID, &m.Team1Name, &m.Team2Name,
			&m.State, &m.ScoresCSV, &m.BothNotified); err != nil {
			return nil, fmt.Errorf("scan bracket match: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *TelegramPostgres) MarkBracketNotified(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_bracket_matches SET both_notified = TRUE WHERE id = ANY($1)`, pq.Array(ids))
	return err
}

func (r *TelegramPostgres) SetReportSynced(ctx context.Context, reportID int) error {
	_, err := r.db.ExecContext(ctx, `UPDATE telegram_match_reports SET synced_at = NOW() WHERE id = $1`, reportID)
	return err
}

func (r *TelegramPostgres) GetUnsyncedReports(ctx context.Context) ([]models.TelegramMatchReport, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, reporter_telegram_id, winner_team_id, loser_team_id, score, photo_file_ids, created_at, bracket_match_id
		FROM telegram_match_reports
		WHERE bracket_match_id IS NOT NULL AND synced_at IS NULL
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var out []models.TelegramMatchReport
	for rows.Next() {
		var rep models.TelegramMatchReport
		if err := rows.Scan(&rep.ID, &rep.ReporterTelegramID, &rep.WinnerTeamID, &rep.LoserTeamID,
			&rep.Score, pq.Array(&rep.PhotoFileIDs), &rep.CreatedAt, &rep.BracketMatchID); err != nil {
			return nil, fmt.Errorf("scan unsynced report: %w", err)
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}
```

`CreateMatchReport` — добавить колонку: в INSERT `(reporter_telegram_id, winner_team_id, loser_team_id, score, photo_file_ids, bracket_match_id) VALUES ($1, $2, $3, $4, $5, $6)`, шестой аргумент `report.BracketMatchID`.

Run: `go build ./... && go vet ./...`
Expected: чисто.

- [ ] **Step 7: Интеграционный тест кэша**

В `internal/repository/integration_test.go`:

```go
// The bracket cache is rewritten after every Challonge write; both_notified
// is ours, not Challonge's, and must survive the rewrite.
func TestIntegrationBracketCacheKeepsNotified(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewTelegramPostgres(db)

	a, err := repo.CreateTeam(ctx, uniqueName(t, "brA"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	b, err := repo.CreateTeam(ctx, uniqueName(t, "brB"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM telegram_bracket_matches WHERE challonge_match_id IN (9001, 9002)`)
		_, _ = db.Exec(`DELETE FROM telegram_teams WHERE id IN ($1, $2)`, a.ID, b.ID)
	})
	if err := repo.SetTeamParticipantID(ctx, a.ID, 501); err != nil {
		t.Fatalf("SetTeamParticipantID: %v", err)
	}
	gotA, _ := repo.GetTeamByID(ctx, a.ID)
	if gotA.ChallongeParticipantID == nil || *gotA.ChallongeParticipantID != 501 {
		t.Fatalf("participant id = %v, want 501", gotA.ChallongeParticipantID)
	}

	first := []models.BracketMatch{
		{ChallongeMatchID: 9001, Round: 1, PlayOrder: 1, Team1ID: &a.ID, Team2ID: &b.ID, State: models.BracketOpen},
		{ChallongeMatchID: 9002, Round: 2, PlayOrder: 2, State: models.BracketPending},
	}
	if err := repo.ReplaceBracketMatches(ctx, first); err != nil {
		t.Fatalf("ReplaceBracketMatches: %v", err)
	}
	cached, err := repo.GetBracketMatches(ctx)
	if err != nil {
		t.Fatalf("GetBracketMatches: %v", err)
	}
	var m1 *models.BracketMatch
	for i := range cached {
		if cached[i].ChallongeMatchID == 9001 {
			m1 = &cached[i]
		}
	}
	if m1 == nil || m1.Team1Name != a.Name || m1.Team2Name != b.Name {
		t.Fatalf("match 9001 = %+v, want names joined", m1)
	}
	if err := repo.MarkBracketNotified(ctx, []int{m1.ID}); err != nil {
		t.Fatalf("MarkBracketNotified: %v", err)
	}

	second := []models.BracketMatch{
		{ChallongeMatchID: 9001, Round: 1, PlayOrder: 1, Team1ID: &a.ID, Team2ID: &b.ID, WinnerID: &a.ID, State: models.BracketComplete, ScoresCSV: "2-0"},
	}
	if err := repo.ReplaceBracketMatches(ctx, second); err != nil {
		t.Fatalf("ReplaceBracketMatches#2: %v", err)
	}
	cached, _ = repo.GetBracketMatches(ctx)
	var seen9001, seen9002 bool
	for _, m := range cached {
		switch m.ChallongeMatchID {
		case 9001:
			seen9001 = true
			if !m.BothNotified || m.State != models.BracketComplete || m.ID != m1.ID {
				t.Errorf("9001 after rewrite = %+v, want notified, complete, same id", m)
			}
		case 9002:
			seen9002 = true
		}
	}
	if !seen9001 || seen9002 {
		t.Errorf("after rewrite seen9001=%v seen9002=%v, want true/false", seen9001, seen9002)
	}

	// A rollback that puts a different pair into the match must re-arm the ping.
	third := []models.BracketMatch{
		{ChallongeMatchID: 9001, Round: 1, PlayOrder: 1, Team1ID: &a.ID, State: models.BracketOpen},
	}
	if err := repo.ReplaceBracketMatches(ctx, third); err != nil {
		t.Fatalf("ReplaceBracketMatches#3: %v", err)
	}
	cached, _ = repo.GetBracketMatches(ctx)
	for _, m := range cached {
		if m.ChallongeMatchID == 9001 && m.BothNotified {
			t.Error("both_notified survived a pair change")
		}
	}

	// Queued report round trip.
	rep := &models.TelegramMatchReport{ReporterTelegramID: 1, WinnerTeamID: a.ID, LoserTeamID: b.ID, Score: "2:0", PhotoFileIDs: []string{"f"}, BracketMatchID: &m1.ID}
	if err := repo.CreateMatchReport(ctx, rep); err != nil {
		t.Fatalf("CreateMatchReport: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM telegram_match_reports WHERE id = $1`, rep.ID) })
	queued, _ := repo.GetUnsyncedReports(ctx)
	found := false
	for _, q := range queued {
		if q.ID == rep.ID && q.BracketMatchID != nil && *q.BracketMatchID == m1.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("report %d missing from unsynced queue", rep.ID)
	}
	if err := repo.SetReportSynced(ctx, rep.ID); err != nil {
		t.Fatalf("SetReportSynced: %v", err)
	}
	queued, _ = repo.GetUnsyncedReports(ctx)
	for _, q := range queued {
		if q.ID == rep.ID {
			t.Error("report still queued after SetReportSynced")
		}
	}
}
```

- [ ] **Step 8: Прогнать интеграционные тесты**

Попроси пользователя поднять Docker Desktop (память проекта: не запускать демон из тула). Затем, при живом Postgres с накатанными миграциями:

Run: `BW_TEST_DSN='...' go test -tags integration ./internal/repository/ -run TestIntegrationBracketCache -count=1 -v`
Expected: PASS. Если Postgres недоступен — тест `Skip`; отметь это в отчёте задачи явно, не считай пройденным.

Также: `go test ./internal/...` — всё зелёное.

- [ ] **Step 9: Коммит**

```bash
git add migrations/000027_bracket_matches.up.sql migrations/000027_bracket_matches.down.sql internal/models/telegram.go internal/repository/ internal/application/telegram_service_test.go
git commit -m "feat(telegram): кэш матчей сетки, participant id команды, очередь отчётов в Challonge"
```

---

### Task 3: HTTP-клиент Challonge v2.1

**Files:**
- Create: `internal/challonge/client.go`
- Test: `internal/challonge/client_test.go`

**Interfaces:**
- Produces (пакет `challonge`):
  - `type Tournament struct { ID int64; Slug string; URL string }` — `URL` полный публичный адрес.
  - `type NewParticipant struct { Name string; Seed int }`
  - `type Participant struct { ID int64; Name string; Seed int }`
  - `type Match struct { ID int64; Round int; PlayOrder int; State string; Player1ID, Player2ID, WinnerID int64; Scores string }` — нулевой id = слот не определён.
  - `var ErrQuotaExceeded = errors.New("challonge: monthly request quota exceeded")`
  - `func New(apiKey, subdomain string, httpClient *http.Client) *Client`
  - `func (c *Client) CreateTournament(ctx, name, slug string) (Tournament, error)`
  - `func (c *Client) BulkAddParticipants(ctx, tournamentID int64, ps []NewParticipant) ([]Participant, error)`
  - `func (c *Client) Start(ctx, tournamentID int64) error`
  - `func (c *Client) ListMatches(ctx, tournamentID int64) ([]Match, error)`
  - `func (c *Client) ReportMatch(ctx, tournamentID, matchID, winnerPID, loserPID int64, winnerScore, loserScore int) error`
  - `func (c *Client) ReopenMatch(ctx, tournamentID, matchID int64) error`
  - `func (c *Client) DeleteTournament(ctx, tournamentID int64) error`

Формы запросов/ответов взяты из https://challonge.apidog.io (v2.1): `POST /tournaments.json`, `POST /tournaments/{id}/participants/bulk_add.json` (type `Participants`, `attributes.participants[{name, seed}]`), `PUT /tournaments/{id}/change_state.json` (type `TournamentState`, `state: start`), `GET /tournaments/{id}/matches.json?page&per_page`, `PUT /tournaments/{id}/matches/{mid}.json` (type `match`, `attributes.match[{participant_id, score_set, rank, advancing}]`, `tie`), `PUT /tournaments/{id}/matches/{mid}/change_state.json` (type `MatchState`, `state: reopen`), `DELETE /tournaments/{id}.json`. Ответ матча: `attributes.{state, round, suggested_play_order, scores, winner_id}` и `relationships.player1/player2.data.id` — в документированном примере `relationships` лежит **внутри** `attributes`, по JSON:API — рядом; парсим оба.

- [ ] **Step 1: Тесты на httptest**

`internal/challonge/client_test.go`:

```go
package challonge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestClient serves fn and records every request and body.
func newTestClient(t *testing.T, fn http.HandlerFunc) (*Client, *[]*http.Request, *[]string) {
	t.Helper()
	var reqs []*http.Request
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, r)
		bodies = append(bodies, string(b))
		fn(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New("KEY", "", srv.Client())
	c.baseURL = srv.URL
	return c, &reqs, &bodies
}

func TestHeadersAndCreateTournament(t *testing.T) {
	c, reqs, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"data":{"id":"30201","type":"tournament","attributes":{"name":"Valhalla","url":"valhalla_x","tournament_type":"single elimination"}}}`)
	})
	tr, err := c.CreateTournament(context.Background(), "Valhalla", "valhalla_x")
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	if tr.ID != 30201 || tr.Slug != "valhalla_x" || tr.URL != "https://challonge.com/valhalla_x" {
		t.Errorf("tournament = %+v", tr)
	}
	r := (*reqs)[0]
	if r.Method != http.MethodPost || r.URL.Path != "/tournaments.json" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	for k, want := range map[string]string{
		"Authorization-Type": "v1", "Authorization": "KEY",
		"Content-Type": "application/vnd.api+json", "Accept": "application/json",
	} {
		if got := r.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*bodies)[0]), &body)
	attrs := body["data"].(map[string]any)["attributes"].(map[string]any)
	if attrs["tournament_type"] != "single elimination" || attrs["url"] != "valhalla_x" {
		t.Errorf("body attrs = %v", attrs)
	}
}

func TestSubdomainGoesToQueryAndURL(t *testing.T) {
	c, reqs, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"data":{"id":"1","attributes":{"url":"cup"}}}`)
	})
	c.subdomain = "valhalla"
	tr, err := c.CreateTournament(context.Background(), "Cup", "cup")
	if err != nil {
		t.Fatal(err)
	}
	if (*reqs)[0].URL.Query().Get("community_id") != "valhalla" {
		t.Errorf("community_id missing: %s", (*reqs)[0].URL.String())
	}
	if tr.URL != "https://valhalla.challonge.com/cup" {
		t.Errorf("URL = %s", tr.URL)
	}
}

func TestBulkAddSendsSeeds(t *testing.T) {
	c, _, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"76","type":"participant","attributes":{"name":"A","seed":1}},{"id":"77","type":"participant","attributes":{"name":"B","seed":2}}]}`)
	})
	ps, err := c.BulkAddParticipants(context.Background(), 5, []NewParticipant{{"A", 1}, {"B", 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != 76 || ps[1].Name != "B" || ps[1].Seed != 2 {
		t.Errorf("participants = %+v", ps)
	}
	if !strings.Contains((*bodies)[0], `"type":"Participants"`) || !strings.Contains((*bodies)[0], `"seed":2`) {
		t.Errorf("body = %s", (*bodies)[0])
	}
}

func TestStartAndReopenUseChangeState(t *testing.T) {
	c, reqs, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"id":"1"}}`)
	})
	if err := c.Start(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if err := c.ReopenMatch(context.Background(), 5, 42); err != nil {
		t.Fatal(err)
	}
	if p := (*reqs)[0].URL.Path; p != "/tournaments/5/change_state.json" || !strings.Contains((*bodies)[0], `"state":"start"`) {
		t.Errorf("start: %s %s", p, (*bodies)[0])
	}
	if p := (*reqs)[1].URL.Path; p != "/tournaments/5/matches/42/change_state.json" || !strings.Contains((*bodies)[1], `"state":"reopen"`) {
		t.Errorf("reopen: %s %s", p, (*bodies)[1])
	}
}

// Challonge's documented example nests relationships inside attributes; the
// JSON:API norm puts them beside attributes. Both must parse.
func TestListMatchesParsesBothRelationshipPlacements(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[
		 {"id":"8008135","type":"match","attributes":{"state":"complete","round":1,"suggested_play_order":1,"scores":"2 - 0","winner_id":355,
		   "relationships":{"player1":{"data":{"id":"355","type":"participant"}},"player2":{"data":{"id":"354","type":"participant"}}}}},
		 {"id":"8008136","type":"match","attributes":{"state":"pending","round":2,"suggested_play_order":3,"winner_id":null},
		   "relationships":{"player1":{"data":{"id":"355","type":"participant"}},"player2":{"data":null}}}
		]}`)
	})
	ms, err := c.ListMatches(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("got %d matches", len(ms))
	}
	m := ms[0]
	if m.ID != 8008135 || m.State != "complete" || m.Round != 1 || m.PlayOrder != 1 || m.Player1ID != 355 || m.Player2ID != 354 || m.WinnerID != 355 || m.Scores != "2 - 0" {
		t.Errorf("match[0] = %+v", m)
	}
	m = ms[1]
	if m.Player1ID != 355 || m.Player2ID != 0 || m.WinnerID != 0 || m.State != "pending" {
		t.Errorf("match[1] = %+v", m)
	}
}

func TestListMatchesPaginates(t *testing.T) {
	page := 0
	c, reqs, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			var sb strings.Builder
			sb.WriteString(`{"data":[`)
			for i := 0; i < perPage; i++ {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `{"id":"%d","attributes":{"state":"open","round":1}}`, 1000+i)
			}
			sb.WriteString(`]}`)
			io.WriteString(w, sb.String())
			return
		}
		io.WriteString(w, `{"data":[{"id":"999","attributes":{"state":"open","round":2}}]}`)
	})
	ms, err := c.ListMatches(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != perPage+1 {
		t.Errorf("got %d matches, want %d", len(ms), perPage+1)
	}
	if q := (*reqs)[1].URL.Query(); q.Get("page") != "2" || q.Get("per_page") == "" {
		t.Errorf("second request query = %v", q)
	}
}

func TestReportMatchBody(t *testing.T) {
	c, reqs, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"id":"42"}}`)
	})
	if err := c.ReportMatch(context.Background(), 5, 42, 355, 354, 2, 1); err != nil {
		t.Fatal(err)
	}
	if (*reqs)[0].Method != http.MethodPut || (*reqs)[0].URL.Path != "/tournaments/5/matches/42.json" {
		t.Errorf("request = %s %s", (*reqs)[0].Method, (*reqs)[0].URL.Path)
	}
	var body struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Match []struct {
					ParticipantID string `json:"participant_id"`
					ScoreSet      string `json:"score_set"`
					Rank          int    `json:"rank"`
					Advancing     bool   `json:"advancing"`
				} `json:"match"`
				Tie bool `json:"tie"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte((*bodies)[0]), &body); err != nil {
		t.Fatal(err)
	}
	m := body.Data.Attributes.Match
	if body.Data.Type != "match" || len(m) != 2 ||
		m[0].ParticipantID != "355" || m[0].ScoreSet != "2" || m[0].Rank != 1 || !m[0].Advancing ||
		m[1].ParticipantID != "354" || m[1].ScoreSet != "1" || m[1].Rank != 2 || m[1].Advancing {
		t.Errorf("body = %s", (*bodies)[0])
	}
}

func TestQuotaAndAPIErrors(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if err := c.Start(context.Background(), 5); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("429 -> %v, want ErrQuotaExceeded", err)
	}
	c2, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"errors":[{"detail":"Url has already been taken","status":"422"}]}`)
	})
	err := c2.Start(context.Background(), 5)
	if err == nil || !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "already been taken") {
		t.Errorf("422 -> %v, want status and detail in error", err)
	}
}

// TestLive runs the whole lifecycle against the real API — the spike the spec
// asks for. It fails loudly if a shape guessed from the docs does not match.
// Needs CHALLONGE_API_KEY; skipped otherwise. Costs 7 requests of the quota.
func TestLive(t *testing.T) {
	key := os.Getenv("CHALLONGE_API_KEY")
	if key == "" {
		t.Skip("CHALLONGE_API_KEY not set")
	}
	ctx := context.Background()
	c := New(key, os.Getenv("CHALLONGE_SUBDOMAIN"), http.DefaultClient)
	slug := fmt.Sprintf("bw_live_%d", time.Now().Unix())
	tr, err := c.CreateTournament(ctx, "blackwatch live test", slug)
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteTournament(ctx, tr.ID) })
	t.Logf("tournament %d %s", tr.ID, tr.URL)

	ps, err := c.BulkAddParticipants(ctx, tr.ID, []NewParticipant{{"A", 1}, {"B", 2}, {"C", 3}})
	if err != nil || len(ps) != 3 {
		t.Fatalf("BulkAddParticipants: %v (%d)", err, len(ps))
	}
	if err := c.Start(ctx, tr.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ms, err := c.ListMatches(ctx, tr.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("ListMatches: %v (%d)", err, len(ms))
	}
	var open *Match
	for i := range ms {
		if ms[i].State == "open" {
			open = &ms[i]
		}
	}
	if open == nil || open.Player1ID == 0 || open.Player2ID == 0 {
		t.Fatalf("no open match with both players in %+v", ms)
	}
	if err := c.ReportMatch(ctx, tr.ID, open.ID, open.Player1ID, open.Player2ID, 2, 0); err != nil {
		t.Fatalf("ReportMatch: %v", err)
	}
	ms, _ = c.ListMatches(ctx, tr.ID)
	for _, m := range ms {
		if m.ID == open.ID && (m.State != "complete" || m.WinnerID != open.Player1ID) {
			t.Errorf("after report: %+v", m)
		}
	}
	if err := c.ReopenMatch(ctx, tr.ID, open.ID); err != nil {
		t.Fatalf("ReopenMatch: %v", err)
	}
}
```

- [ ] **Step 2: Убедиться, что не компилируется**

Run: `go test ./internal/challonge/`
Expected: FAIL — пакет не существует / `New` не определён.

- [ ] **Step 3: Реализация клиента**

`internal/challonge/client.go`:

```go
// Package challonge is a thin client for the Challonge API v2.1, covering
// only what the tournament bracket needs. Request and response shapes follow
// https://challonge.apidog.io; TestLive checks them against the real service.
package challonge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

const (
	defaultBaseURL = "https://api.challonge.com/v2.1"
	// perPage is the page size for ListMatches. A 128-team bracket has 127
	// matches, so two pages cover any tournament this bot runs.
	perPage = 100
)

// ErrQuotaExceeded is returned on HTTP 429: the free plan allows 500
// requests a month and the service refuses the rest.
var ErrQuotaExceeded = errors.New("challonge: monthly request quota exceeded")

type Tournament struct {
	ID   int64
	Slug string
	URL  string // public bracket page
}

type NewParticipant struct {
	Name string
	Seed int
}

type Participant struct {
	ID   int64
	Name string
	Seed int
}

// Match mirrors one Challonge match. Zero ids mean the slot is undecided.
type Match struct {
	ID        int64
	Round     int
	PlayOrder int
	State     string // pending | open | complete
	Player1ID int64
	Player2ID int64
	WinnerID  int64
	Scores    string
}

type Client struct {
	http      *http.Client
	baseURL   string
	apiKey    string
	subdomain string
}

func New(apiKey, subdomain string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{http: httpClient, baseURL: defaultBaseURL, apiKey: apiKey, subdomain: subdomain}
}

// --- wire types -----------------------------------------------------------

type envelope[T any] struct {
	Data   T          `json:"data"`
	Errors []apiError `json:"errors"`
}

type apiError struct {
	Detail string `json:"detail"`
	Status string `json:"status"`
}

type resource[A any] struct {
	ID            string        `json:"id"`
	Attributes    A             `json:"attributes"`
	Relationships relationships `json:"relationships"`
}

type relationships struct {
	Player1 relation `json:"player1"`
	Player2 relation `json:"player2"`
}

type relation struct {
	Data *struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (r relation) id() int64 {
	if r.Data == nil {
		return 0
	}
	id, _ := strconv.ParseInt(r.Data.ID, 10, 64)
	return id
}

type tournamentAttrs struct {
	URL string `json:"url"`
}

type participantAttrs struct {
	Name string `json:"name"`
	Seed int    `json:"seed"`
}

// matchAttrs carries relationships too: the documented example nests them
// under attributes, the JSON:API layout puts them beside. Whichever is set wins.
type matchAttrs struct {
	State              string         `json:"state"`
	Round              int            `json:"round"`
	SuggestedPlayOrder int            `json:"suggested_play_order"`
	Scores             string         `json:"scores"`
	WinnerID           *int64         `json:"winner_id"`
	Relationships      *relationships `json:"relationships"`
}

// --- requests ---------------------------------------------------------------

func (c *Client) CreateTournament(ctx context.Context, name, slug string) (Tournament, error) {
	body := map[string]any{"data": map[string]any{"type": "tournament", "attributes": map[string]any{
		"name":            name,
		"url":             slug,
		"tournament_type": "single elimination",
		"private":         false,
	}}}
	var out envelope[resource[tournamentAttrs]]
	if err := c.do(ctx, http.MethodPost, "/tournaments.json", body, &out); err != nil {
		return Tournament{}, err
	}
	id, _ := strconv.ParseInt(out.Data.ID, 10, 64)
	return Tournament{ID: id, Slug: out.Data.Attributes.URL, URL: c.publicURL(out.Data.Attributes.URL)}, nil
}

func (c *Client) publicURL(slug string) string {
	if c.subdomain != "" {
		return "https://" + c.subdomain + ".challonge.com/" + slug
	}
	return "https://challonge.com/" + slug
}

func (c *Client) BulkAddParticipants(ctx context.Context, tournamentID int64, ps []NewParticipant) ([]Participant, error) {
	items := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		items = append(items, map[string]any{"name": p.Name, "seed": p.Seed})
	}
	body := map[string]any{"data": map[string]any{"type": "Participants", "attributes": map[string]any{"participants": items}}}
	var out envelope[[]resource[participantAttrs]]
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/tournaments/%d/participants/bulk_add.json", tournamentID), body, &out); err != nil {
		return nil, err
	}
	res := make([]Participant, 0, len(out.Data))
	for _, r := range out.Data {
		id, _ := strconv.ParseInt(r.ID, 10, 64)
		res = append(res, Participant{ID: id, Name: r.Attributes.Name, Seed: r.Attributes.Seed})
	}
	return res, nil
}

func (c *Client) Start(ctx context.Context, tournamentID int64) error {
	body := map[string]any{"data": map[string]any{"type": "TournamentState", "attributes": map[string]any{"state": "start"}}}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tournaments/%d/change_state.json", tournamentID), body, nil)
}

func (c *Client) ListMatches(ctx context.Context, tournamentID int64) ([]Match, error) {
	var all []Match
	for page := 1; ; page++ {
		path := fmt.Sprintf("/tournaments/%d/matches.json?page=%d&per_page=%d", tournamentID, page, perPage)
		var out envelope[[]resource[matchAttrs]]
		if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		for _, r := range out.Data {
			id, _ := strconv.ParseInt(r.ID, 10, 64)
			rel := r.Relationships
			if r.Attributes.Relationships != nil {
				rel = *r.Attributes.Relationships
			}
			m := Match{
				ID:        id,
				Round:     r.Attributes.Round,
				PlayOrder: r.Attributes.SuggestedPlayOrder,
				State:     r.Attributes.State,
				Player1ID: rel.Player1.id(),
				Player2ID: rel.Player2.id(),
				Scores:    r.Attributes.Scores,
			}
			if r.Attributes.WinnerID != nil {
				m.WinnerID = *r.Attributes.WinnerID
			}
			all = append(all, m)
		}
		if len(out.Data) < perPage {
			return all, nil
		}
	}
}

// ReportMatch closes a match. Scores are one "set" per side — the map count
// of a Bo3 — so Challonge renders "2 - 0".
func (c *Client) ReportMatch(ctx context.Context, tournamentID, matchID, winnerPID, loserPID int64, winnerScore, loserScore int) error {
	body := map[string]any{"data": map[string]any{"type": "match", "attributes": map[string]any{
		"match": []map[string]any{
			{"participant_id": strconv.FormatInt(winnerPID, 10), "score_set": strconv.Itoa(winnerScore), "rank": 1, "advancing": true},
			{"participant_id": strconv.FormatInt(loserPID, 10), "score_set": strconv.Itoa(loserScore), "rank": 2, "advancing": false},
		},
		"tie": false,
	}}}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tournaments/%d/matches/%d.json", tournamentID, matchID), body, nil)
}

// ReopenMatch clears a result. Challonge resets every match branching from it.
func (c *Client) ReopenMatch(ctx context.Context, tournamentID, matchID int64) error {
	body := map[string]any{"data": map[string]any{"type": "MatchState", "attributes": map[string]any{"state": "reopen"}}}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tournaments/%d/matches/%d/change_state.json", tournamentID, matchID), body, nil)
}

func (c *Client) DeleteTournament(ctx context.Context, tournamentID int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/tournaments/%d.json", tournamentID), nil, nil)
}

// --- transport ---------------------------------------------------------------

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return err
	}
	if c.subdomain != "" {
		q := u.Query()
		q.Set("community_id", c.subdomain)
		u.RawQuery = q.Encode()
	}
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization-Type", "v1")
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/vnd.api+json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("challonge: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrQuotaExceeded
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env envelope[json.RawMessage]
		_ = json.Unmarshal(raw, &env)
		detail := ""
		for _, e := range env.Errors {
			detail += " " + e.Detail
		}
		return fmt.Errorf("challonge: %s %s: HTTP %d%s", method, path, resp.StatusCode, detail)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("challonge: %s %s: decode: %w", method, path, err)
	}
	return nil
}
```

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/challonge/ -v`
Expected: все PASS, `TestLive` — SKIP.

- [ ] **Step 5: Live-тест (spike из спека)**

Попроси пользователя положить тестовый ключ в окружение (ключ в чат не передавать) и запустить самому:

```bash
CHALLONGE_API_KEY=... go test ./internal/challonge/ -run TestLive -v -count=1
```

Ожидание: PASS, в логе — URL созданного турнира; турнир удаляется в cleanup. Если какая-то форма не совпала — поправить парсер в `client.go` и фикстуру соответствующего httptest-теста, чтобы она отражала реальный ответ. Пока live-тест не пройден, задача **не завершена** — сказать об этом явно в отчёте.

- [ ] **Step 6: Коммит**

```bash
git add internal/challonge/
git commit -m "feat(challonge): клиент API v2.1 — турнир, участники, матчи, отчёт, переоткрытие"
```

---

### Task 4: BracketService — посев, построение, синхронизация

**Files:**
- Create: `internal/application/bracket.go`
- Test: `internal/application/bracket_test.go`

**Interfaces:**
- Consumes: `challonge.Tournament/NewParticipant/Participant/Match` (Task 3), репозиторий из Task 2.
- Produces (пакет `application`):
  - `type BracketProvider interface` — семь методов, сигнатуры 1:1 с `*challonge.Client`.
  - `type BracketService struct`; `func NewBracketService(repo repository.Telegram, provider BracketProvider, walkoverWin, walkoverLose int, logger Logger) *BracketService`
  - `type SeededTeam struct { Team models.TelegramTeam; Seed int; AvgStars float64 }`; `func SeedTeams(teams []models.TelegramTeam) []SeededTeam`
  - `type BracketBuilt struct { URL string; Round1 []models.BracketMatch; Byes []models.TelegramTeam }`
  - `func (s *BracketService) Build(ctx, forTournament time.Time) (*BracketBuilt, error)`
  - `func (s *BracketService) IsBuiltFor(ctx, t time.Time) bool`
  - `func (s *BracketService) HasResults(ctx) (bool, error)`
  - `func (s *BracketService) URL(ctx) string`
  - `func (s *BracketService) Matches(ctx) ([]models.BracketMatch, error)`
  - `func (s *BracketService) Sync(ctx) ([]models.BracketMatch, error)` — возвращает матчи, ставшие готовыми (обе команды, не сыгран), и помечает их `both_notified`.
  - ошибки `ErrBracketNotBuilt`, `ErrTooFewTeams`.
  - ключи настроек: `settingChallongeID = "challonge_tournament_id"`, `settingChallongeURL = "challonge_tournament_url"`, `settingChallongeFor = "challonge_tournament_for"`, `settingBuildFailures = "bracket_build_failures"`.

- [ ] **Step 1: Фейковый провайдер и тесты посева/построения**

`internal/application/bracket_test.go`:

```go
package application

import (
	"blackwatch/internal/challonge"
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"
)

// fakeChallonge is an in-memory single-elimination engine: enough of
// Challonge to drive the service. It seeds 1 vs N, hands byes to the top
// seeds and resets the branch when a completed match is reopened — the
// three behaviours the service relies on.
type fakeChallonge struct {
	nextID   int64
	tourneys map[int64]*fakeTourney
	calls    []string
	fail     map[string]error // method name -> error to return
}

type fakeTourney struct {
	slug         string
	participants []challonge.Participant
	matches      []*fakeMatch
	started      bool
}

type fakeMatch struct {
	challonge.Match
	next     *fakeMatch // where the winner goes
	nextSlot int        // 1 or 2
	src1     *fakeMatch // feeders, nil for a seeded slot
	src2     *fakeMatch
}

func newFakeChallonge() *fakeChallonge {
	return &fakeChallonge{nextID: 100, tourneys: map[int64]*fakeTourney{}, fail: map[string]error{}}
}

func (f *fakeChallonge) call(name string) error {
	f.calls = append(f.calls, name)
	return f.fail[name]
}

func (f *fakeChallonge) CreateTournament(_ context.Context, name, slug string) (challonge.Tournament, error) {
	if err := f.call("CreateTournament"); err != nil {
		return challonge.Tournament{}, err
	}
	f.nextID++
	f.tourneys[f.nextID] = &fakeTourney{slug: slug}
	return challonge.Tournament{ID: f.nextID, Slug: slug, URL: "https://challonge.com/" + slug}, nil
}

func (f *fakeChallonge) BulkAddParticipants(_ context.Context, tID int64, ps []challonge.NewParticipant) ([]challonge.Participant, error) {
	if err := f.call("BulkAddParticipants"); err != nil {
		return nil, err
	}
	t := f.tourneys[tID]
	for _, p := range ps {
		f.nextID++
		t.participants = append(t.participants, challonge.Participant{ID: f.nextID, Name: p.Name, Seed: p.Seed})
	}
	return t.participants, nil
}

// Start builds the bracket: size M = next power of two, top seeds get byes,
// pairings 1 vs M, 2 vs M-1 ... within a plain sequential layout.
func (f *fakeChallonge) Start(_ context.Context, tID int64) error {
	if err := f.call("Start"); err != nil {
		return err
	}
	t := f.tourneys[tID]
	ps := append([]challonge.Participant(nil), t.participants...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Seed < ps[j].Seed })
	n := len(ps)
	m := 1
	for m < n {
		m *= 2
	}
	// slot i (0-based) holds seed order: i even -> seed i/2+1 from the top, odd -> from the bottom.
	slots := make([]*challonge.Participant, m)
	for i := 0; i < m/2; i++ {
		top := i
		bottom := m - 1 - i
		if top < n {
			slots[2*i] = &ps[top]
		}
		if bottom < n {
			slots[2*i+1] = &ps[bottom]
		}
	}
	// Round 1: m/2 matches over the slots. Later rounds pair winners.
	var rounds [][]*fakeMatch
	var r1 []*fakeMatch
	order := 0
	for i := 0; i < m; i += 2 {
		order++
		fm := &fakeMatch{Match: challonge.Match{Round: 1, PlayOrder: order}}
		f.nextID++
		fm.ID = f.nextID
		if slots[i] != nil {
			fm.Player1ID = slots[i].ID
		}
		if slots[i+1] != nil {
			fm.Player2ID = slots[i+1].ID
		}
		r1 = append(r1, fm)
	}
	rounds = append(rounds, r1)
	prev := r1
	round := 1
	for len(prev) > 1 {
		round++
		var cur []*fakeMatch
		for i := 0; i < len(prev); i += 2 {
			order++
			f.nextID++
			fm := &fakeMatch{Match: challonge.Match{ID: f.nextID, Round: round, PlayOrder: order}, src1: prev[i], src2: prev[i+1]}
			prev[i].next, prev[i].nextSlot = fm, 1
			prev[i+1].next, prev[i+1].nextSlot = fm, 2
			cur = append(cur, fm)
		}
		rounds = append(rounds, cur)
		prev = cur
	}
	for _, r := range rounds {
		t.matches = append(t.matches, r...)
	}
	// Byes: a round-1 match with one empty slot completes immediately.
	for _, fm := range r1 {
		if fm.Player1ID != 0 && fm.Player2ID == 0 {
			f.complete(fm, fm.Player1ID, "")
		} else if fm.Player1ID == 0 && fm.Player2ID != 0 {
			f.complete(fm, fm.Player2ID, "")
		}
	}
	f.refreshStates(t)
	t.started = true
	return nil
}

func (f *fakeChallonge) complete(fm *fakeMatch, winner int64, scores string) {
	fm.WinnerID = winner
	fm.State = "complete"
	fm.Scores = scores
	if fm.next != nil {
		if fm.nextSlot == 1 {
			fm.next.Player1ID = winner
		} else {
			fm.next.Player2ID = winner
		}
	}
}

func (f *fakeChallonge) refreshStates(t *fakeTourney) {
	for _, fm := range t.matches {
		if fm.State == "complete" {
			continue
		}
		if fm.Player1ID != 0 && fm.Player2ID != 0 {
			fm.State = "open"
		} else {
			fm.State = "pending"
		}
	}
}

func (f *fakeChallonge) ListMatches(_ context.Context, tID int64) ([]challonge.Match, error) {
	if err := f.call("ListMatches"); err != nil {
		return nil, err
	}
	t := f.tourneys[tID]
	out := make([]challonge.Match, 0, len(t.matches))
	for _, fm := range t.matches {
		out = append(out, fm.Match)
	}
	return out, nil
}

func (f *fakeChallonge) find(tID, matchID int64) *fakeMatch {
	for _, fm := range f.tourneys[tID].matches {
		if fm.ID == matchID {
			return fm
		}
	}
	return nil
}

func (f *fakeChallonge) ReportMatch(_ context.Context, tID, matchID, winnerPID, loserPID int64, w, l int) error {
	if err := f.call("ReportMatch"); err != nil {
		return err
	}
	fm := f.find(tID, matchID)
	if fm == nil || fm.State != "open" {
		return fmt.Errorf("fake: match %d not open", matchID)
	}
	if (fm.Player1ID != winnerPID || fm.Player2ID != loserPID) && (fm.Player2ID != winnerPID || fm.Player1ID != loserPID) {
		return fmt.Errorf("fake: participants %d/%d not in match %d", winnerPID, loserPID, matchID)
	}
	f.complete(fm, winnerPID, fmt.Sprintf("%d - %d", w, l))
	f.refreshStates(f.tourneys[tID])
	return nil
}

// ReopenMatch clears the result and everything downstream, as Challonge does.
func (f *fakeChallonge) ReopenMatch(_ context.Context, tID, matchID int64) error {
	if err := f.call("ReopenMatch"); err != nil {
		return err
	}
	fm := f.find(tID, matchID)
	if fm == nil {
		return fmt.Errorf("fake: no match %d", matchID)
	}
	f.reopen(fm)
	f.refreshStates(f.tourneys[tID])
	return nil
}

func (f *fakeChallonge) reopen(fm *fakeMatch) {
	if fm.next != nil {
		if fm.nextSlot == 1 {
			fm.next.Player1ID = 0
		} else {
			fm.next.Player2ID = 0
		}
		if fm.next.State == "complete" {
			f.reopen(fm.next)
		}
	}
	fm.WinnerID, fm.State, fm.Scores = 0, "open", ""
}

func (f *fakeChallonge) DeleteTournament(_ context.Context, tID int64) error {
	if err := f.call("DeleteTournament"); err != nil {
		return err
	}
	delete(f.tourneys, tID)
	return nil
}

// onlyTourney returns the id of the single tournament the fake holds.
func (f *fakeChallonge) onlyTourney() int64 {
	for id := range f.tourneys {
		return id
	}
	return 0
}

func (f *fakeChallonge) count(name string) int {
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

// --- fixtures ---------------------------------------------------------------

// addTeam registers a team with a captain (tg id = 100+teamID) and a main
// roster of the given stars; every player is non-substitute.
func addBracketTeam(repo *fakeTelegramRepo, id int, name string, stars ...int) *models.TelegramTeam {
	t := &models.TelegramTeam{ID: id, Name: name, Status: models.TeamStatusActive, IsCheckedIn: true}
	repo.teams[id] = t
	// The fake's GetAllTeams/GetTeamMembers read players from repo.players
	// (captains, keyed by telegram id) and repo.members (the rest).
	for i, s := range stars {
		tg := int64(100*id + i)
		p := &models.TelegramPlayer{ID: id*10 + i, TelegramID: &tg, Stars: s, IsCaptain: i == 0, TeamID: &id}
		if i == 0 {
			repo.players[tg] = p
		} else {
			repo.members = append(repo.members, p)
		}
	}
	return t
}

func newBracketSvc(t *testing.T) (*BracketService, *fakeTelegramRepo, *fakeChallonge) {
	t.Helper()
	repo := newFakeTelegramRepo()
	prov := newFakeChallonge()
	svc := NewBracketService(repo, prov, 1, 0, nopLogger{})
	svc.now = func() time.Time { return time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC) }
	return svc, repo, prov
}

var tourneyAt = time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)

// --- tests ------------------------------------------------------------------

func TestSeedTeamsByAverageStarsOfMainRoster(t *testing.T) {
	sub := true
	teams := []models.TelegramTeam{
		{ID: 1, Name: "Low", Status: models.TeamStatusActive, Players: []models.TelegramPlayer{{Stars: 10}, {Stars: 10}}},
		{ID: 2, Name: "High", Status: models.TeamStatusActive, Players: []models.TelegramPlayer{{Stars: 30}, {Stars: 20}, {Stars: 99, IsSubstitute: sub}}},
		{ID: 3, Name: "Out", Status: models.TeamStatusDisqualified, Players: []models.TelegramPlayer{{Stars: 100}}},
		{ID: 4, Name: "Tie", Status: models.TeamStatusActive, Players: []models.TelegramPlayer{{Stars: 25}}},
	}
	got := SeedTeams(teams)
	if len(got) != 3 {
		t.Fatalf("seeded %d teams, want 3 (disqualified excluded)", len(got))
	}
	// High: (30+20)/2 = 25 (substitute ignored); Tie: 25 -> earlier id (2) first.
	want := []string{"High", "Tie", "Low"}
	for i, w := range want {
		if got[i].Team.Name != w || got[i].Seed != i+1 {
			t.Errorf("seed %d = %s/%d, want %s/%d", i, got[i].Team.Name, got[i].Seed, w, i+1)
		}
	}
	if got[0].AvgStars != 25 {
		t.Errorf("High avg = %v, want 25", got[0].AvgStars)
	}
}

func TestBuildCreatesTournamentAndCachesMatches(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	// 6 teams -> bracket of 8, two byes for seeds 1 and 2.
	for i := 1; i <= 6; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 70-i*10, 70-i*10)
	}
	ctx := context.Background()

	built, err := svc.Build(ctx, tourneyAt)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.URL == "" || repo.settings[settingChallongeURL] != built.URL {
		t.Errorf("URL = %q, setting = %q", built.URL, repo.settings[settingChallongeURL])
	}
	if repo.settings[settingChallongeFor] != tourneyAt.Format(time.RFC3339) || !svc.IsBuiltFor(ctx, tourneyAt) {
		t.Errorf("challonge_tournament_for = %q; IsBuiltFor = %v", repo.settings[settingChallongeFor], svc.IsBuiltFor(ctx, tourneyAt))
	}
	if svc.IsBuiltFor(ctx, tourneyAt.Add(24*time.Hour)) {
		t.Error("IsBuiltFor a different tournament time = true")
	}
	for i := 1; i <= 6; i++ {
		if repo.teams[i].ChallongeParticipantID == nil {
			t.Errorf("team %d has no participant id", i)
		}
	}
	// Two round-1 matches with both teams (T3 vs T6, T4 vs T5), two byes.
	if len(built.Round1) != 2 {
		t.Errorf("Round1 = %d matches, want 2: %+v", len(built.Round1), built.Round1)
	}
	byes := map[string]bool{}
	for _, b := range built.Byes {
		byes[b.Name] = true
	}
	if len(byes) != 2 || !byes["T1"] || !byes["T2"] {
		t.Errorf("byes = %v, want T1 and T2", byes)
	}
	cached, _ := repo.GetBracketMatches(ctx)
	if len(cached) != 7 {
		t.Errorf("cached %d matches, want 7", len(cached))
	}
	for _, m := range cached {
		if m.Ready() && !m.BothNotified {
			t.Errorf("ready match #%d not marked notified after Build", m.PlayOrder)
		}
	}
	if has, _ := svc.HasResults(ctx); has {
		t.Error("HasResults = true right after build (byes are not results)")
	}
	// Budget: create, bulk, start, one list.
	if prov.count("ListMatches") != 1 {
		t.Errorf("ListMatches called %d times during Build, want 1", prov.count("ListMatches"))
	}
}

func TestBuildRefusesFewerThanTwoTeams(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	addBracketTeam(repo, 1, "Solo", 10)
	_, err := svc.Build(context.Background(), tourneyAt)
	if !errors.Is(err, ErrTooFewTeams) {
		t.Errorf("Build with one team = %v, want ErrTooFewTeams", err)
	}
	if prov.count("CreateTournament") != 0 {
		t.Error("CreateTournament called with too few teams")
	}
}

func TestRebuildDeletesPreviousTournament(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	firstID := repo.settings[settingChallongeID]
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	if prov.count("DeleteTournament") != 1 {
		t.Errorf("DeleteTournament called %d times, want 1", prov.count("DeleteTournament"))
	}
	if repo.settings[settingChallongeID] == firstID {
		t.Error("tournament id unchanged after rebuild")
	}
	if len(prov.tourneys) != 1 {
		t.Errorf("%d tournaments left in Challonge, want 1", len(prov.tourneys))
	}
}

func TestBuildFailureLeavesNothingBuilt(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	prov.fail["Start"] = errors.New("boom")
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err == nil {
		t.Fatal("Build succeeded with Start failing")
	}
	if svc.IsBuiltFor(ctx, tourneyAt) {
		t.Error("IsBuiltFor = true after a failed build")
	}
	if repo.settings[settingBuildFailures] != "1" {
		t.Errorf("bracket_build_failures = %q, want 1", repo.settings[settingBuildFailures])
	}
	// The half-made tournament is removed on the next attempt.
	delete(prov.fail, "Start")
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	if prov.count("DeleteTournament") != 1 || len(prov.tourneys) != 1 {
		t.Errorf("stale tournament not cleaned: deletes=%d left=%d", prov.count("DeleteTournament"), len(prov.tourneys))
	}
	if repo.settings[settingBuildFailures] != "0" {
		t.Errorf("bracket_build_failures = %q after success, want 0", repo.settings[settingBuildFailures])
	}
}

func TestSyncReportsNewlyReadyOnce(t *testing.T) {
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 10*i)
	}
	ctx := context.Background()
	if _, err := svc.Build(ctx, tourneyAt); err != nil {
		t.Fatal(err)
	}
	// Nothing changed: no new ready matches, but one request spent.
	ready, err := svc.Sync(ctx)
	if err != nil || len(ready) != 0 {
		t.Fatalf("Sync on idle bracket = %v, %v", ready, err)
	}
	// Finish both round-1 matches behind the service's back; the final opens.
	tID := prov.onlyTourney()
	for _, fm := range prov.tourneys[tID].matches {
		if fm.Round == 1 {
			_ = prov.ReportMatch(ctx, tID, fm.ID, fm.Player1ID, fm.Player2ID, 2, 0)
		}
	}
	ready, err = svc.Sync(ctx)
	if err != nil || len(ready) != 1 || ready[0].Round != 2 {
		t.Fatalf("Sync after round 1 = %+v, %v; want the final", ready, err)
	}
	ready, _ = svc.Sync(ctx)
	if len(ready) != 0 {
		t.Errorf("second Sync re-reported the final: %+v", ready)
	}
}

func TestSyncWithoutBracket(t *testing.T) {
	svc, _, _ := newBracketSvc(t)
	if _, err := svc.Sync(context.Background()); !errors.Is(err, ErrBracketNotBuilt) {
		t.Errorf("Sync without bracket = %v, want ErrBracketNotBuilt", err)
	}
}
```

- [ ] **Step 2: Убедиться, что падает**

Run: `go test ./internal/application/ -run 'TestSeed|TestBuild|TestRebuild|TestSync' 2>&1 | head`
Expected: ошибки компиляции — `NewBracketService` не определён.

- [ ] **Step 3: Реализация**

`internal/application/bracket.go`:

```go
package application

import (
	"blackwatch/internal/challonge"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// BracketProvider is the slice of Challonge the bracket needs. *challonge.Client
// satisfies it; tests use an in-memory engine.
type BracketProvider interface {
	CreateTournament(ctx context.Context, name, slug string) (challonge.Tournament, error)
	BulkAddParticipants(ctx context.Context, tournamentID int64, ps []challonge.NewParticipant) ([]challonge.Participant, error)
	Start(ctx context.Context, tournamentID int64) error
	ListMatches(ctx context.Context, tournamentID int64) ([]challonge.Match, error)
	ReportMatch(ctx context.Context, tournamentID, matchID, winnerPID, loserPID int64, winnerScore, loserScore int) error
	ReopenMatch(ctx context.Context, tournamentID, matchID int64) error
	DeleteTournament(ctx context.Context, tournamentID int64) error
}

// Settings keys, stored next to tournament_time.
const (
	settingChallongeID   = "challonge_tournament_id"
	settingChallongeURL  = "challonge_tournament_url"
	settingChallongeFor  = "challonge_tournament_for" // RFC3339 tournament time the bracket was built for
	settingBuildFailures = "bracket_build_failures"
)

var (
	ErrBracketNotBuilt = errors.New("сетка ещё не построена")
	ErrTooFewTeams     = errors.New("для сетки нужно минимум две команды")
	ErrMatchNotFound   = errors.New("матч не найден")
	ErrMatchNotOpen    = errors.New("матч не открыт: обе стороны ещё не определены или он уже сыгран")
	ErrNotInMatch      = errors.New("команда не участвует в этом матче")
	ErrHasResults      = errors.New("в сетке уже есть результаты")
)

// BracketService keeps the Challonge bracket and its local cache in step.
// Challonge is the source of truth; every write goes there first and is
// followed by exactly one ListMatches that rewrites the cache. Reads never
// touch the API.
type BracketService struct {
	repo     repository.Telegram
	provider BracketProvider
	logger   Logger
	// walkoverWin/Lose is the score reported for a technical defeat.
	walkoverWin, walkoverLose int
	now                       func() time.Time
	// mu serialises writes: two captains reporting at once must not
	// interleave a PUT with the other's cache rewrite.
	mu sync.Mutex
}

func NewBracketService(repo repository.Telegram, provider BracketProvider, walkoverWin, walkoverLose int, logger Logger) *BracketService {
	return &BracketService{repo: repo, provider: provider, logger: logger, walkoverWin: walkoverWin, walkoverLose: walkoverLose, now: time.Now}
}

// SeededTeam is a team with its bracket seed: 1 is the strongest.
type SeededTeam struct {
	Team     models.TelegramTeam
	Seed     int
	AvgStars float64
}

// SeedTeams orders active teams by the average stars of the main roster
// (substitutes excluded); ties go to the earlier-registered team. Byes in
// Challonge go to the top seeds, so the strongest teams skip round 1.
func SeedTeams(teams []models.TelegramTeam) []SeededTeam {
	var out []SeededTeam
	for _, t := range teams {
		if t.Status == models.TeamStatusDisqualified {
			continue
		}
		sum, n := 0, 0
		for _, p := range t.Players {
			if p.IsSubstitute {
				continue
			}
			sum += p.Stars
			n++
		}
		avg := 0.0
		if n > 0 {
			avg = float64(sum) / float64(n)
		}
		out = append(out, SeededTeam{Team: t, AvgStars: avg})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AvgStars != out[j].AvgStars {
			return out[i].AvgStars > out[j].AvgStars
		}
		return out[i].Team.ID < out[j].Team.ID
	})
	for i := range out {
		out[i].Seed = i + 1
	}
	return out
}

// BracketBuilt is what the bot announces after a build.
type BracketBuilt struct {
	URL    string
	Round1 []models.BracketMatch  // round-1 matches with both teams
	Byes   []models.TelegramTeam // teams that skip round 1
}

func (s *BracketService) tournamentID(ctx context.Context) int64 {
	v, _ := s.repo.GetSetting(ctx, settingChallongeID)
	id, _ := strconv.ParseInt(v, 10, 64)
	return id
}

// URL is the public bracket page, empty when nothing is built.
func (s *BracketService) URL(ctx context.Context) string {
	v, _ := s.repo.GetSetting(ctx, settingChallongeURL)
	return v
}

// IsBuiltFor reports whether a bracket exists for this tournament time. A
// bracket from last month's tournament does not count.
func (s *BracketService) IsBuiltFor(ctx context.Context, t time.Time) bool {
	if s.tournamentID(ctx) == 0 {
		return false
	}
	v, _ := s.repo.GetSetting(ctx, settingChallongeFor)
	return v == t.UTC().Format(time.RFC3339)
}

// BuildFailures is how many consecutive builds failed since the last success.
func (s *BracketService) BuildFailures(ctx context.Context) int {
	v, _ := s.repo.GetSetting(ctx, settingBuildFailures)
	n, _ := strconv.Atoi(v)
	return n
}

// Matches is the cached bracket, ordered by round and play order.
func (s *BracketService) Matches(ctx context.Context) ([]models.BracketMatch, error) {
	return s.repo.GetBracketMatches(ctx)
}

// HasResults reports whether any match has been played. Byes complete
// matches too, so only matches with both teams count.
func (s *BracketService) HasResults(ctx context.Context) (bool, error) {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return false, err
	}
	for _, m := range ms {
		if m.State == models.BracketComplete && m.Team1ID != nil && m.Team2ID != nil {
			return true, nil
		}
	}
	return false, nil
}

// Build creates the tournament in Challonge from every active team, seeded
// by strength, starts it and fills the cache. A previous tournament — a
// failed attempt or an admin rebuild — is deleted first.
func (s *BracketService) Build(ctx context.Context, forTournament time.Time) (*BracketBuilt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	built, err := s.build(ctx, forTournament)
	if err != nil {
		n := s.BuildFailures(ctx) + 1
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingBuildFailures, strconv.Itoa(n)))
		// The id stays so the next attempt deletes the half-made tournament;
		// "for" is cleared so IsBuiltFor says no and the worker retries.
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeFor, ""))
		return nil, err
	}
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingBuildFailures, "0"))
	return built, nil
}

func (s *BracketService) build(ctx context.Context, forTournament time.Time) (*BracketBuilt, error) {
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	seeded := SeedTeams(teams)
	if len(seeded) < 2 {
		return nil, ErrTooFewTeams
	}

	if old := s.tournamentID(ctx); old != 0 {
		if err := s.provider.DeleteTournament(ctx, old); err != nil {
			// Not fatal: an orphan in Challonge costs nothing here.
			s.logger.Warn("bracket: delete previous tournament %d: %v", old, err)
		}
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeID, ""))
	}
	if err := s.repo.ClearTeamParticipantIDs(ctx); err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceBracketMatches(ctx, nil); err != nil {
		return nil, err
	}

	slug := "valhalla_" + s.now().UTC().Format("20060102_150405")
	name := "Valhalla " + forTournament.Format("02.01.2006")
	tr, err := s.provider.CreateTournament(ctx, name, slug)
	if err != nil {
		return nil, err
	}
	// Remember the id before anything else can fail, so a retry deletes it.
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeID, strconv.FormatInt(tr.ID, 10)))
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeURL, tr.URL))
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeFor, forTournament.UTC().Format(time.RFC3339)))

	ps := make([]challonge.NewParticipant, 0, len(seeded))
	byName := make(map[string]int, len(seeded))
	for _, st := range seeded {
		ps = append(ps, challonge.NewParticipant{Name: st.Team.Name, Seed: st.Seed})
		byName[st.Team.Name] = st.Team.ID
	}
	created, err := s.provider.BulkAddParticipants(ctx, tr.ID, ps)
	if err != nil {
		return nil, err
	}
	for _, p := range created {
		teamID, ok := byName[p.Name]
		if !ok {
			return nil, fmt.Errorf("bracket: participant %q matches no team", p.Name)
		}
		if err := s.repo.SetTeamParticipantID(ctx, teamID, p.ID); err != nil {
			return nil, err
		}
	}
	if err := s.provider.Start(ctx, tr.ID); err != nil {
		return nil, err
	}

	ready, err := s.sync(ctx, tr.ID)
	if err != nil {
		return nil, err
	}
	inRound1 := map[int]bool{}
	for _, m := range ready {
		if m.Round == 1 {
			inRound1[*m.Team1ID] = true
			inRound1[*m.Team2ID] = true
		}
	}
	var byes []models.TelegramTeam
	for _, st := range seeded {
		if !inRound1[st.Team.ID] {
			byes = append(byes, st.Team)
		}
	}
	return &BracketBuilt{URL: tr.URL, Round1: ready, Byes: byes}, nil
}

// Sync pulls the bracket from Challonge, rewrites the cache and returns the
// matches that became ready to play since the last sync, marking them so
// they are reported once.
func (s *BracketService) Sync(ctx context.Context) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, ErrBracketNotBuilt
	}
	return s.sync(ctx, tID)
}

// sync is Sync without the lock, for callers that already hold it.
func (s *BracketService) sync(ctx context.Context, tID int64) ([]models.BracketMatch, error) {
	raw, err := s.provider.ListMatches(ctx, tID)
	if err != nil {
		return nil, err
	}
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	byPID := make(map[int64]int, len(teams))
	for _, t := range teams {
		if t.ChallongeParticipantID != nil {
			byPID[*t.ChallongeParticipantID] = t.ID
		}
	}
	toTeam := func(pid int64) *int {
		if id, ok := byPID[pid]; ok {
			return &id
		}
		return nil
	}
	ms := make([]models.BracketMatch, 0, len(raw))
	for _, m := range raw {
		ms = append(ms, models.BracketMatch{
			ChallongeMatchID: m.ID,
			Round:            m.Round,
			PlayOrder:        m.PlayOrder,
			Team1ID:          toTeam(m.Player1ID),
			Team2ID:          toTeam(m.Player2ID),
			WinnerID:         toTeam(m.WinnerID),
			State:            m.State,
			ScoresCSV:        m.Scores,
		})
	}
	if err := s.repo.ReplaceBracketMatches(ctx, ms); err != nil {
		return nil, err
	}
	cached, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	var ready []models.BracketMatch
	var ids []int
	for _, m := range cached {
		if m.Ready() && !m.BothNotified {
			ready = append(ready, m)
			ids = append(ids, m.ID)
		}
	}
	if err := s.repo.MarkBracketNotified(ctx, ids); err != nil {
		return nil, err
	}
	return ready, nil
}

func (s *BracketService) logWrite(op string, err error) {
	if err != nil {
		s.logger.Error("bracket: %s failed: %v", op, err)
	}
}
```

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/application/ -run 'TestSeed|TestBuild|TestRebuild|TestSync' -v`
Expected: PASS. Если `TestBuildCreatesTournamentAndCachesMatches` даёт другие byes — проверь раскладку слотов в `fakeChallonge.Start` (при 6 командах в сетке на 8: слоты `[1,_,3,6,4,5,2,_]` → byes у 1 и 2, матчи 3v6 и 4v5).

- [ ] **Step 5: `go vet ./... && go test ./internal/...` — всё зелёное. Коммит**

```bash
git add internal/application/bracket.go internal/application/bracket_test.go
git commit -m "feat(telegram): BracketService — посев по звёздам, построение сетки в Challonge, синхронизация кэша"
```

---

### Task 5: BracketService — результат, тех. поражение, откат, очередь отчётов

**Files:**
- Modify: `internal/application/bracket.go`
- Test: `internal/application/bracket_test.go`

**Interfaces:**
- Consumes: всё из Task 4.
- Produces:
  - `func (s *BracketService) OpenMatchFor(ctx, teamID int) (*models.BracketMatch, error)` — `nil, nil`, если открытого матча нет.
  - `func (s *BracketService) ReportResult(ctx, matchID, winnerTeamID, winnerScore, loserScore int) ([]models.BracketMatch, error)` — возвращает новые готовые матчи.
  - `func (s *BracketService) ForfeitDisqualified(ctx) ([]models.BracketMatch, error)`
  - `type BracketChange struct { Reset []models.BracketMatch; Ready []models.BracketMatch }` — `Reset`: матчи (в состоянии **до** изменения), которые были сыграны или готовы и перестали быть такими.
  - `func (s *BracketService) SetWinner(ctx, playOrder int, teamName string, winnerScore, loserScore int) (*BracketChange, error)`
  - `func (s *BracketService) Reinstate(ctx, teamName string) (*BracketChange, error)` — `nil, nil`, если команде нечего возвращать; ошибка, если команды нет.
  - `func (s *BracketService) FlushPendingReports(ctx) ([]models.BracketMatch, error)`

- [ ] **Step 1: Тесты**

Добавить в `internal/application/bracket_test.go`:

```go
// buildFour builds a 4-team bracket: T1 (strongest) vs T4, T2 vs T3, final.
func buildFour(t *testing.T) (*BracketService, *fakeTelegramRepo, *fakeChallonge) {
	t.Helper()
	svc, repo, prov := newBracketSvc(t)
	for i := 1; i <= 4; i++ {
		addBracketTeam(repo, i, fmt.Sprintf("T%d", i), 50-10*i)
	}
	if _, err := svc.Build(context.Background(), tourneyAt); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return svc, repo, prov
}

func TestOpenMatchFor(t *testing.T) {
	svc, _, _ := buildFour(t)
	ctx := context.Background()
	m, err := svc.OpenMatchFor(ctx, 1)
	if err != nil || m == nil || !m.Has(1) || !m.Has(4) || m.Round != 1 {
		t.Fatalf("OpenMatchFor(1) = %+v, %v; want round-1 match T1 vs T4", m, err)
	}
	if opp := m.Opponent(1); opp == nil || *opp != 4 {
		t.Errorf("Opponent(1) = %v, want 4", opp)
	}
	m, err = svc.OpenMatchFor(ctx, 99)
	if err != nil || m != nil {
		t.Errorf("OpenMatchFor(unknown) = %+v, %v; want nil, nil", m, err)
	}
}

func TestReportResultAdvancesAndPingsFinalOnce(t *testing.T) {
	svc, _, prov := buildFour(t)
	ctx := context.Background()
	m1, _ := svc.OpenMatchFor(ctx, 1)
	m2, _ := svc.OpenMatchFor(ctx, 2)

	before := prov.count("ListMatches")
	ready, err := svc.ReportResult(ctx, m1.ID, 1, 2, 0)
	if err != nil || len(ready) != 0 {
		t.Fatalf("first result: ready=%v err=%v; the final is not ready yet", ready, err)
	}
	if prov.count("ListMatches") != before+1 || prov.count("ReportMatch") != 1 {
		t.Errorf("budget: ListMatches +%d, ReportMatch %d; want +1, 1", prov.count("ListMatches")-before, prov.count("ReportMatch"))
	}
	ready, err = svc.ReportResult(ctx, m2.ID, 3, 2, 1) // upset: T3 beats T2
	if err != nil || len(ready) != 1 || ready[0].Round != 2 || !ready[0].Has(1) || !ready[0].Has(3) {
		t.Fatalf("second result: ready=%+v err=%v; want final T1 vs T3", ready, err)
	}
	if ready[0].Team1Name == "" || ready[0].Team2Name == "" {
		t.Errorf("ready match has no team names: %+v", ready[0])
	}
	// Reporting a finished match is refused without a request.
	calls := len(prov.calls)
	if _, err := svc.ReportResult(ctx, m1.ID, 1, 2, 0); !errors.Is(err, ErrMatchNotOpen) {
		t.Errorf("re-report = %v, want ErrMatchNotOpen", err)
	}
	if _, err := svc.ReportResult(ctx, ready[0].ID, 2, 2, 0); !errors.Is(err, ErrNotInMatch) {
		t.Errorf("stranger reports = %v, want ErrNotInMatch", err)
	}
	if len(prov.calls) != calls {
		t.Error("refused reports still hit Challonge")
	}
}

func TestForfeitDisqualifiedGivesOpponentTheWin(t *testing.T) {
	svc, repo, prov := buildFour(t)
	ctx := context.Background()
	repo.teams[4].Status = models.TeamStatusDisqualified

	ready, err := svc.ForfeitDisqualified(ctx)
	if err != nil || len(ready) != 0 {
		t.Fatalf("ForfeitDisqualified = %v, %v", ready, err)
	}
	m, _ := svc.OpenMatchFor(ctx, 1)
	if m != nil {
		t.Errorf("T1 still has an open match: %+v", m)
	}
	ms, _ := svc.Matches(ctx)
	for _, x := range ms {
		if x.Round == 1 && x.Has(4) {
			if x.State != models.BracketComplete || x.WinnerID == nil || *x.WinnerID != 1 || x.ScoresCSV != "1 - 0" {
				t.Errorf("forfeited match = %+v, want T1 wins 1 - 0", x)
			}
		}
	}
	// A second sweep is quiet and costs nothing.
	calls := len(prov.calls)
	if _, err := svc.ForfeitDisqualified(ctx); err != nil || len(prov.calls) != calls {
		t.Errorf("repeat sweep: err=%v, calls=%d->%d", err, calls, len(prov.calls))
	}
}

// Both sides of a match missing: the win passes through the empty team to
// the next live opponent, and the final becomes ready for the survivor.
func TestForfeitBothSidesCascades(t *testing.T) {
	svc, repo, _ := buildFour(t)
	ctx := context.Background()
	repo.teams[2].Status = models.TeamStatusDisqualified
	repo.teams[3].Status = models.TeamStatusDisqualified
	// T1 beats T4 on the other side, so the final has a live team waiting.
	m1, _ := svc.OpenMatchFor(ctx, 1)
	if _, err := svc.ReportResult(ctx, m1.ID, 1, 2, 0); err != nil {
		t.Fatal(err)
	}

	ready, err := svc.ForfeitDisqualified(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// After the cascade the final is T1 vs (forfeited T2 or T3) and completes
	// by walkover too, so nothing is "ready" — T1 has won the tournament.
	if len(ready) != 0 {
		t.Errorf("ready = %+v, want none: the final was walked over", ready)
	}
	ms, _ := svc.Matches(ctx)
	var final *models.BracketMatch
	for i := range ms {
		if ms[i].Round == 2 {
			final = &ms[i]
		}
	}
	if final == nil || final.State != models.BracketComplete || final.WinnerID == nil || *final.WinnerID != 1 {
		t.Errorf("final = %+v, want T1 the winner by walkover", final)
	}
}

func TestForfeitWaitsForPendingMatch(t *testing.T) {
	svc, repo, prov := buildFour(t)
	ctx := context.Background()
	// T1 wins round 1 and is then disqualified while the other semi is unplayed:
	// its final is pending (no opponent), so nothing can be reported yet.
	m1, _ := svc.OpenMatchFor(ctx, 1)
	if _, err := svc.ReportResult(ctx, m1.ID, 1, 2, 0); err != nil {
		t.Fatal(err)
	}
	repo.teams[1].Status = models.TeamStatusDisqualified
	calls := prov.count("ReportMatch")
	if _, err := svc.ForfeitDisqualified(ctx); err != nil {
		t.Fatal(err)
	}
	if prov.count("ReportMatch") != calls {
		t.Error("forfeit reported on a pending match")
	}
	// The other semi finishes; the next sweep hands the final to T3.
	m2, _ := svc.OpenMatchFor(ctx, 2)
	if _, err := svc.ReportResult(ctx, m2.ID, 3, 2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ForfeitDisqualified(ctx); err != nil {
		t.Fatal(err)
	}
	ms, _ := svc.Matches(ctx)
	for _, x := range ms {
		if x.Round == 2 && (x.State != models.BracketComplete || x.WinnerID == nil || *x.WinnerID != 3) {
			t.Errorf("final = %+v, want T3 by walkover", x)
		}
	}
}

func TestSetWinnerResetsBranchAndReportsIt(t *testing.T) {
	svc, _, prov := buildFour(t)
	ctx := context.Background()
	m1, _ := svc.OpenMatchFor(ctx, 1)
	m2, _ := svc.OpenMatchFor(ctx, 2)
	if _, err := svc.ReportResult(ctx, m1.ID, 1, 2, 0); err != nil {
		t.Fatal(err)
	}
	ready, err := svc.ReportResult(ctx, m2.ID, 2, 2, 0)
	if err != nil || len(ready) != 1 {
		t.Fatalf("final not ready: %v %v", ready, err)
	}
	final := ready[0]
	if _, err := svc.ReportResult(ctx, final.ID, 1, 2, 1); err != nil {
		t.Fatal(err)
	}

	// Admin: T4 actually won match #1.
	ch, err := svc.SetWinner(ctx, m1.PlayOrder, "T4", 2, 1)
	if err != nil {
		t.Fatalf("SetWinner: %v", err)
	}
	if prov.count("ReopenMatch") != 1 {
		t.Errorf("ReopenMatch called %d times, want 1", prov.count("ReopenMatch"))
	}
	// The final (played T1 vs T2) is reset and comes back ready as T4 vs T2.
	if len(ch.Reset) != 1 || ch.Reset[0].ID != final.ID || !ch.Reset[0].Has(1) || !ch.Reset[0].Has(2) {
		t.Errorf("Reset = %+v, want the old final T1 vs T2", ch.Reset)
	}
	if len(ch.Ready) != 1 || ch.Ready[0].ID != final.ID || !ch.Ready[0].Has(4) || !ch.Ready[0].Has(2) {
		t.Errorf("Ready = %+v, want the new final T4 vs T2", ch.Ready)
	}
	ms, _ := svc.Matches(ctx)
	for _, x := range ms {
		if x.ID == m1.ID && (x.WinnerID == nil || *x.WinnerID != 4 || x.ScoresCSV != "2 - 1") {
			t.Errorf("match #1 after SetWinner = %+v", x)
		}
	}
	// Errors: unknown match, stranger, pending match.
	if _, err := svc.SetWinner(ctx, 999, "T4", 1, 0); !errors.Is(err, ErrMatchNotFound) {
		t.Errorf("unknown play order = %v", err)
	}
	if _, err := svc.SetWinner(ctx, m1.PlayOrder, "T2", 1, 0); !errors.Is(err, ErrNotInMatch) {
		t.Errorf("stranger = %v", err)
	}
}

func TestSetWinnerOnOpenMatchNeedsNoReopen(t *testing.T) {
	svc, _, prov := buildFour(t)
	ctx := context.Background()
	m1, _ := svc.OpenMatchFor(ctx, 1)
	ch, err := svc.SetWinner(ctx, m1.PlayOrder, "T4", 1, 0)
	if err != nil || len(ch.Reset) != 0 || prov.count("ReopenMatch") != 0 {
		t.Errorf("SetWinner on open match: ch=%+v err=%v reopens=%d", ch, err, prov.count("ReopenMatch"))
	}
}

func TestReinstateReturnsTeamToItsMatch(t *testing.T) {
	svc, repo, _ := buildFour(t)
	ctx := context.Background()
	repo.teams[4].Status = models.TeamStatusDisqualified
	if _, err := svc.ForfeitDisqualified(ctx); err != nil {
		t.Fatal(err)
	}
	repo.teams[4].Status = models.TeamStatusActive // /reinstate did this

	ch, err := svc.Reinstate(ctx, "T4")
	if err != nil || ch == nil {
		t.Fatalf("Reinstate = %+v, %v", ch, err)
	}
	m, _ := svc.OpenMatchFor(ctx, 4)
	if m == nil || !m.Has(1) {
		t.Errorf("T4 has no open match against T1 after reinstate: %+v", m)
	}
	if len(ch.Ready) != 1 || ch.Ready[0].ID != m.ID {
		t.Errorf("Ready = %+v, want the reopened match", ch.Ready)
	}
	// Nothing to undo: a team that never lost.
	ch, err = svc.Reinstate(ctx, "T1")
	if err != nil || ch != nil {
		t.Errorf("Reinstate(T1) = %+v, %v; want nil, nil", ch, err)
	}
}

func TestFlushPendingReports(t *testing.T) {
	svc, repo, prov := buildFour(t)
	ctx := context.Background()
	m1, _ := svc.OpenMatchFor(ctx, 1)
	// A report that never reached Challonge (saved while it was down).
	rep := &models.TelegramMatchReport{ReporterTelegramID: 100, WinnerTeamID: 1, LoserTeamID: 4, Score: "2:1", PhotoFileIDs: []string{"f"}, BracketMatchID: &m1.ID}
	_ = repo.CreateMatchReport(ctx, rep)

	ready, err := svc.FlushPendingReports(ctx)
	if err != nil || len(ready) != 0 {
		t.Fatalf("Flush = %v, %v", ready, err)
	}
	if prov.count("ReportMatch") != 1 {
		t.Errorf("ReportMatch called %d times, want 1", prov.count("ReportMatch"))
	}
	if q, _ := repo.GetUnsyncedReports(ctx); len(q) != 0 {
		t.Errorf("report still queued: %+v", q)
	}
	ms, _ := svc.Matches(ctx)
	for _, x := range ms {
		if x.ID == m1.ID && x.ScoresCSV != "2 - 1" {
			t.Errorf("flushed score = %q, want 2 - 1", x.ScoresCSV)
		}
	}
	// A queued report for a match that is no longer open is dropped, not retried forever.
	rep2 := &models.TelegramMatchReport{ReporterTelegramID: 100, WinnerTeamID: 1, LoserTeamID: 4, Score: "2:0", PhotoFileIDs: []string{"f"}, BracketMatchID: &m1.ID}
	_ = repo.CreateMatchReport(ctx, rep2)
	if _, err := svc.FlushPendingReports(ctx); err != nil {
		t.Fatal(err)
	}
	if q, _ := repo.GetUnsyncedReports(ctx); len(q) != 0 {
		t.Errorf("stale report still queued: %+v", q)
	}
}
```

- [ ] **Step 2: Убедиться, что падает**

Run: `go test ./internal/application/ -run 'TestOpenMatch|TestReport|TestForfeit|TestSetWinner|TestReinstate|TestFlush' 2>&1 | head -5`
Expected: ошибки компиляции.

- [ ] **Step 3: Реализация**

Дописать в `internal/application/bracket.go`:

```go
// OpenMatchFor is the team's current playable match, nil when it has none
// (eliminated, waiting for an opponent, or the bracket is not built).
func (s *BracketService) OpenMatchFor(ctx context.Context, teamID int) (*models.BracketMatch, error) {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	return openMatchIn(ms, teamID), nil
}

func openMatchIn(ms []models.BracketMatch, teamID int) *models.BracketMatch {
	for i := range ms {
		if ms[i].Ready() && ms[i].Has(teamID) {
			m := ms[i]
			return &m
		}
	}
	return nil
}

func findMatch(ms []models.BracketMatch, id int) *models.BracketMatch {
	for i := range ms {
		if ms[i].ID == id {
			m := ms[i]
			return &m
		}
	}
	return nil
}

// participantIDs resolves both sides of a match to Challonge participant ids.
func (s *BracketService) participantIDs(ctx context.Context, winnerTeamID, loserTeamID int) (int64, int64, error) {
	w, err := s.repo.GetTeamByID(ctx, winnerTeamID)
	if err != nil || w == nil || w.ChallongeParticipantID == nil {
		return 0, 0, fmt.Errorf("bracket: team %d has no Challonge participant", winnerTeamID)
	}
	l, err := s.repo.GetTeamByID(ctx, loserTeamID)
	if err != nil || l == nil || l.ChallongeParticipantID == nil {
		return 0, 0, fmt.Errorf("bracket: team %d has no Challonge participant", loserTeamID)
	}
	return *w.ChallongeParticipantID, *l.ChallongeParticipantID, nil
}

// report is the PUT for one match; the caller holds mu and syncs afterwards.
func (s *BracketService) report(ctx context.Context, tID int64, m *models.BracketMatch, winnerTeamID, winnerScore, loserScore int) error {
	if !m.Ready() {
		return ErrMatchNotOpen
	}
	if !m.Has(winnerTeamID) {
		return ErrNotInMatch
	}
	loserID := *m.Opponent(winnerTeamID)
	wPID, lPID, err := s.participantIDs(ctx, winnerTeamID, loserID)
	if err != nil {
		return err
	}
	return s.provider.ReportMatch(ctx, tID, m.ChallongeMatchID, wPID, lPID, winnerScore, loserScore)
}

// ReportResult records a played match and returns the matches that became
// ready because of it.
func (s *BracketService) ReportResult(ctx context.Context, matchID, winnerTeamID, winnerScore, loserScore int) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, ErrBracketNotBuilt
	}
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	m := findMatch(ms, matchID)
	if m == nil {
		return nil, ErrMatchNotFound
	}
	if err := s.report(ctx, tID, m, winnerTeamID, winnerScore, loserScore); err != nil {
		return nil, err
	}
	return s.sync(ctx, tID)
}

// ForfeitDisqualified hands every open match of a disqualified team to its
// opponent. A pass reports every such match, then syncs once; a double
// no-show opens the next match for the "winner", so passes repeat until
// nothing changes. Idempotent: a team with no open match costs no request.
func (s *BracketService) ForfeitDisqualified(ctx context.Context) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, nil
	}
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	var out []models.BracketMatch
	const maxPasses = 8 // deeper than any bracket this bot runs
	for pass := 0; pass < maxPasses; pass++ {
		ms, err := s.repo.GetBracketMatches(ctx)
		if err != nil {
			return nil, err
		}
		reported := 0
		done := map[int]bool{} // match ids reported this pass
		for _, t := range teams {
			if t.Status != models.TeamStatusDisqualified {
				continue
			}
			m := openMatchIn(ms, t.ID)
			if m == nil || done[m.ID] {
				continue
			}
			opp := *m.Opponent(t.ID)
			if err := s.report(ctx, tID, m, opp, s.walkoverWin, s.walkoverLose); err != nil {
				return out, err
			}
			done[m.ID] = true
			reported++
		}
		if reported == 0 {
			break
		}
		ready, err := s.sync(ctx, tID)
		if err != nil {
			return s.stillReady(ctx, out), err
		}
		out = append(out, ready...)
	}
	return s.stillReady(ctx, out), nil
}

// stillReady drops matches that a later pass of the cascade closed again
// (a double no-show opens the next match and walks it over in one sweep),
// so nobody is pinged about a match that no longer exists to be played.
func (s *BracketService) stillReady(ctx context.Context, ms []models.BracketMatch) []models.BracketMatch {
	if len(ms) == 0 {
		return nil
	}
	cur, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return ms
	}
	var out []models.BracketMatch
	for _, m := range ms {
		if c := findMatch(cur, m.ID); c != nil && c.Ready() {
			out = append(out, *c)
		}
	}
	return out
}

// BracketChange is what an admin override did to the bracket. Reset holds
// matches as they were before: played or ready, now cleared by Challonge's
// branch reset. Ready holds matches that became playable.
type BracketChange struct {
	Reset []models.BracketMatch
	Ready []models.BracketMatch
}

// SetWinner forces the outcome of a match. A finished match is reopened
// first; Challonge then resets everything downstream, and the diff of the
// cache before and after says whom to tell.
func (s *BracketService) SetWinner(ctx context.Context, playOrder int, teamName string, winnerScore, loserScore int) (*BracketChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, ErrBracketNotBuilt
	}
	team, err := s.repo.GetTeamByName(ctx, teamName)
	if err != nil || team == nil {
		return nil, fmt.Errorf("команда '%s' не найдена", teamName)
	}
	before, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	var m *models.BracketMatch
	for i := range before {
		if before[i].PlayOrder == playOrder {
			m = &before[i]
		}
	}
	if m == nil {
		return nil, ErrMatchNotFound
	}
	if m.Team1ID == nil || m.Team2ID == nil {
		return nil, ErrMatchNotOpen
	}
	if !m.Has(team.ID) {
		return nil, ErrNotInMatch
	}
	return s.override(ctx, tID, before, m, team.ID, winnerScore, loserScore)
}

func (s *BracketService) override(ctx context.Context, tID int64, before []models.BracketMatch, m *models.BracketMatch, winnerTeamID, winnerScore, loserScore int) (*BracketChange, error) {
	if m.State == models.BracketComplete {
		if err := s.provider.ReopenMatch(ctx, tID, m.ChallongeMatchID); err != nil {
			return nil, err
		}
		m.State = models.BracketOpen
		m.WinnerID = nil
	}
	if err := s.report(ctx, tID, m, winnerTeamID, winnerScore, loserScore); err != nil {
		return nil, err
	}
	ready, err := s.sync(ctx, tID)
	if err != nil {
		return nil, err
	}
	after, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	afterByID := make(map[int]models.BracketMatch, len(after))
	for _, a := range after {
		afterByID[a.ID] = a
	}
	ch := &BracketChange{Ready: ready}
	for _, b := range before {
		if b.ID == m.ID {
			continue
		}
		wasLive := b.State == models.BracketComplete || b.Ready()
		if !wasLive {
			continue
		}
		a, ok := afterByID[b.ID]
		stillSame := ok && a.State == b.State && samePairIDs(a, b)
		if !stillSame {
			ch.Reset = append(ch.Reset, b)
		}
	}
	return ch, nil
}

func samePairIDs(a, b models.BracketMatch) bool {
	eq := func(x, y *int) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	return eq(a.Team1ID, b.Team1ID) && eq(a.Team2ID, b.Team2ID)
}

// Reinstate undoes a technical defeat: the team's lost match is reopened and
// handed back to it. Nil change when the team has no lost match.
func (s *BracketService) Reinstate(ctx context.Context, teamName string) (*BracketChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, nil
	}
	team, err := s.repo.GetTeamByName(ctx, teamName)
	if err != nil || team == nil {
		return nil, fmt.Errorf("команда '%s' не найдена", teamName)
	}
	teamID := team.ID
	before, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	var lost *models.BracketMatch
	for i := range before {
		b := &before[i]
		if b.State == models.BracketComplete && b.Has(teamID) && b.WinnerID != nil && *b.WinnerID != teamID {
			lost = b // ordered by round: the last one is the elimination
		}
	}
	if lost == nil {
		return nil, nil
	}
	if err := s.provider.ReopenMatch(ctx, tID, lost.ChallongeMatchID); err != nil {
		return nil, err
	}
	ready, err := s.sync(ctx, tID)
	if err != nil {
		return nil, err
	}
	ch := &BracketChange{Ready: ready}
	after, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	afterByID := make(map[int]models.BracketMatch, len(after))
	for _, a := range after {
		afterByID[a.ID] = a
	}
	// The reopened match keeps its pair, so both_notified survived the sync
	// and it is not in ready; the captains still need to hear it is back on.
	if a, ok := afterByID[lost.ID]; ok && a.Ready() {
		already := false
		for _, r := range ready {
			already = already || r.ID == a.ID
		}
		if !already {
			ch.Ready = append(ch.Ready, a)
		}
	}
	for _, b := range before {
		if b.ID == lost.ID || !(b.State == models.BracketComplete || b.Ready()) {
			continue
		}
		if a, ok := afterByID[b.ID]; !ok || a.State != b.State || !samePairIDs(a, b) {
			ch.Reset = append(ch.Reset, b)
		}
	}
	return ch, nil
}

// FlushPendingReports pushes reports saved while Challonge was unreachable.
// A report whose match is no longer open (someone else's result got there
// first, or an admin override) is marked synced and dropped: retrying it
// forever would burn the quota.
func (s *BracketService) FlushPendingReports(ctx context.Context) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, nil
	}
	queued, err := s.repo.GetUnsyncedReports(ctx)
	if err != nil {
		return nil, err
	}
	var out []models.BracketMatch
	for _, rep := range queued {
		ms, err := s.repo.GetBracketMatches(ctx)
		if err != nil {
			return out, err
		}
		m := findMatch(ms, *rep.BracketMatchID)
		if m == nil || !m.Ready() || !m.Has(rep.WinnerTeamID) {
			s.logger.Warn("bracket: dropping queued report %d: match no longer open", rep.ID)
			s.logWrite("SetReportSynced", s.repo.SetReportSynced(ctx, rep.ID))
			continue
		}
		w, l, _, ok := parseScore(rep.Score)
		if !ok {
			s.logger.Warn("bracket: dropping queued report %d: bad score %q", rep.ID, rep.Score)
			s.logWrite("SetReportSynced", s.repo.SetReportSynced(ctx, rep.ID))
			continue
		}
		if err := s.report(ctx, tID, m, rep.WinnerTeamID, w, l); err != nil {
			return out, err // Challonge still down: keep the queue, try next tick
		}
		s.logWrite("SetReportSynced", s.repo.SetReportSynced(ctx, rep.ID))
		ready, err := s.sync(ctx, tID)
		if err != nil {
			return out, err
		}
		out = append(out, ready...)
	}
	return out, nil
}
```

`parseScore` уже есть в `telegram_report.go` (возвращает `myScore, oppScore, formatted, ok`).

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/application/ -run 'TestOpenMatch|TestReport|TestForfeit|TestSetWinner|TestReinstate|TestFlush' -v`
Expected: PASS.

Если `TestForfeitBothSidesCascades` возвращает готовый финал вместо пустого списка: значит, второй проход не сработал — проверь, что после `sync` цикл читает **свежий** кэш (`GetBracketMatches` в начале каждого прохода) и что `openMatchIn` находит матч 2-го раунда для дисквалифицированной команды.

- [ ] **Step 5: `go vet ./... && go test ./internal/...` — зелёное. Коммит**

```bash
git add internal/application/bracket.go internal/application/bracket_test.go
git commit -m "feat(telegram): результат матча, тех. поражение по сетке, откат админом, очередь отчётов"
```

---

### Task 6: `/report` по матчу из сетки

**Files:**
- Modify: `internal/application/telegram_service.go` (поле `bracket`, `WithBracket`, интерфейс `TelegramService`)
- Modify: `internal/application/telegram_report.go`
- Modify: `internal/application/service.go`
- Modify: `internal/delivery/telegram/report.go` (новая сигнатура `SubmitReport`)
- Test: `internal/application/telegram_report_test.go`

**Interfaces:**
- Consumes: `BracketService.OpenMatchFor`, `ReportResult` (Task 5), `challonge.New` (Task 3), `cfg.BracketEnabled()`/`ParseWalkoverScore` (Task 1).
- Produces:
  - `func (s *TelegramServiceImpl) WithBracket(b *BracketService) *TelegramServiceImpl`
  - `MatchReportDraft.BracketMatchID int`, `.PlayOrder int`, `.WinnerScore int`, `.LoserScore int`
  - `SubmitReport(ctx, tgID) (string, string, *models.TelegramMatchReport, []models.BracketMatch)` — четвёртое значение: матчи, ставшие готовыми (для пинга капитанов).
  - `application.Service.Bracket *BracketService` (nil, если сетка выключена)
  - `func NewService(..., bracket BracketProvider, walkoverWin, walkoverLose int, ...)` — см. шаг 5 (точная сигнатура).

- [ ] **Step 1: Тесты**

В `internal/application/telegram_report_test.go`:

```go
// With a bracket, the captain does not pick an opponent: the open match
// decides it, and the report goes straight to the score step.
func TestReportWithBracketSkipsOpponentStep(t *testing.T) {
	bsvc, repo, _ := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	ctx := context.Background()
	captain := int64(100) // T1's captain, see addBracketTeam
	repo.players[captain].FSMState = models.StateIdle

	resp, kb := svc.StartReport(ctx, captain)
	if !strings.Contains(resp, "T4") || !strings.Contains(resp, "#") || kb != KbReportScore {
		t.Fatalf("StartReport = %q, %q; want the bracket match vs T4 and the score keyboard", resp, kb)
	}
	if st := repo.players[captain].FSMState; st != models.StateReportScore {
		t.Errorf("state = %q, want report_score", st)
	}
	d := svc.GetReportDraft(captain)
	if d == nil || d.LoserTeamID != 4 || d.BracketMatchID == 0 {
		t.Fatalf("draft = %+v, want loser T4 and a bracket match id", d)
	}

	resp, kb = svc.SetReportScore(ctx, captain, "2:1")
	if kb != KbReportPhotos+":0" || d.WinnerScore != 2 || d.LoserScore != 1 {
		t.Fatalf("SetReportScore = %q, %q; draft scores %d:%d", resp, kb, d.WinnerScore, d.LoserScore)
	}
	svc.AddReportPhoto(ctx, captain, "photo1")
	msg, kb, rep, ready := svc.SubmitReport(ctx, captain)
	if rep == nil || kb != "main_menu" || !strings.Contains(msg, "отправлен") {
		t.Fatalf("SubmitReport = %q, %q, %+v", msg, kb, rep)
	}
	if rep.BracketMatchID == nil || *rep.BracketMatchID != d.BracketMatchID || rep.SyncedAt == nil {
		t.Errorf("saved report = %+v, want bracket match id and synced_at set", rep)
	}
	if len(ready) != 0 {
		t.Errorf("ready = %+v, want none (other semi unplayed)", ready)
	}
	m, _ := bsvc.OpenMatchFor(ctx, 1)
	if m != nil {
		t.Errorf("T1 still has an open match after the report: %+v", m)
	}
	// Second report by the same captain: no open match.
	resp, kb = svc.StartReport(ctx, captain)
	if !strings.Contains(resp, "нет открытого матча") || kb != KbNone {
		t.Errorf("StartReport with nothing to play = %q, %q", resp, kb)
	}
}

// Challonge down at submit time: the report is saved and queued, the
// captain is told, and nothing is lost.
func TestReportWithBracketQueuesWhenChallongeDown(t *testing.T) {
	bsvc, repo, prov := buildFour(t)
	svc := NewTelegramServiceImpl(repo, nopLogger{}).WithBracket(bsvc)
	ctx := context.Background()
	captain := int64(100)
	repo.players[captain].FSMState = models.StateIdle

	svc.StartReport(ctx, captain)
	svc.SetReportScore(ctx, captain, "2:0")
	svc.AddReportPhoto(ctx, captain, "photo1")
	prov.fail["ReportMatch"] = errors.New("503")

	msg, kb, rep, _ := svc.SubmitReport(ctx, captain)
	if rep == nil || kb != "main_menu" || !strings.Contains(msg, "сетка обновится") {
		t.Fatalf("SubmitReport while down = %q, %q, %+v", msg, kb, rep)
	}
	if q, _ := repo.GetUnsyncedReports(ctx); len(q) != 1 || q[0].ID != rep.ID {
		t.Errorf("queue = %+v, want the report", q)
	}
	delete(prov.fail, "ReportMatch")
	if _, err := bsvc.FlushPendingReports(ctx); err != nil {
		t.Fatal(err)
	}
	if q, _ := repo.GetUnsyncedReports(ctx); len(q) != 0 {
		t.Errorf("queue after flush = %+v", q)
	}
}

// Without a bracket nothing changes: the opponent picker is still there.
func TestReportWithoutBracketKeepsOpponentPicker(t *testing.T) {
	svc, repo := newTelegramSvc()
	t1, t2 := 1, 2
	repo.teams[t1] = &models.TelegramTeam{ID: t1, Name: "Navi", Status: models.TeamStatusActive}
	repo.teams[t2] = &models.TelegramTeam{ID: t2, Name: "VP", Status: models.TeamStatusActive}
	repo.addPlayer(101, &t1, true, models.StateIdle)
	_, kb := svc.StartReport(context.Background(), 101)
	if kb != KbReportOpponent {
		t.Errorf("kb = %q, want opponent picker", kb)
	}
}
```

Добавить `"errors"` в импорты теста. Существующий `TestReportSubmitAndCancel` использует `SubmitReport` с тремя значениями — поправить на четыре (`msg, kb, rep, _ :=`).

- [ ] **Step 2: Убедиться, что падает**

Run: `go test ./internal/application/ -run TestReportWith 2>&1 | head -5`
Expected: ошибки компиляции (`WithBracket`, число возвращаемых значений).

- [ ] **Step 3: Сервис — поле и `WithBracket`**

В `telegram_service.go`, в `TelegramServiceImpl` после `profiles ProfileLookup`:

```go
	// bracket is optional: with it, /report is bound to the team's open match
	// in the Challonge bracket instead of a free-form opponent pick.
	bracket *BracketService
```

После `WithProfileLookup`:

```go
// WithBracket binds /report to the tournament bracket.
func (s *TelegramServiceImpl) WithBracket(b *BracketService) *TelegramServiceImpl {
	s.bracket = b
	return s
}
```

В интерфейсе `TelegramService` заменить строку `SubmitReport(...)` на:

```go
	SubmitReport(ctx context.Context, tgID int64) (string, string, *models.TelegramMatchReport, []models.BracketMatch)
```

- [ ] **Step 4: `telegram_report.go`**

В `MatchReportDraft` после `Score string`:

```go
	// WinnerScore/LoserScore are Score split, for Challonge.
	WinnerScore int
	LoserScore  int
	// BracketMatchID/PlayOrder are set when the bracket picked the opponent.
	BracketMatchID int
	PlayOrder      int
```

В `StartReport` заменить блок от `opponents, err := s.GetEligibleOpponents(...)` до `return msg, KbReportOpponent` на:

```go
	draft := &MatchReportDraft{
		ReporterTgID:   tgID,
		WinnerTeamID:   myTeam.ID,
		WinnerTeamName: myTeam.Name,
		UpdatedAt:      time.Now(),
	}

	if s.bracket != nil {
		m, err := s.bracket.OpenMatchFor(ctx, myTeam.ID)
		if err != nil {
			s.logger.Error("telegram: OpenMatchFor %d: %v", myTeam.ID, err)
			return "Не удалось прочитать сетку. Попробуйте позже.", KbNone
		}
		if m == nil {
			return "У вашей команды сейчас нет открытого матча в сетке. Если это ошибка — напишите администратору.", KbNone
		}
		oppID := *m.Opponent(myTeam.ID)
		opp, err := s.repo.GetTeamByID(ctx, oppID)
		if err != nil || opp == nil {
			return "Команда соперника не найдена.", KbNone
		}
		draft.LoserTeamID = opp.ID
		draft.LoserTeamName = opp.Name
		draft.BracketMatchID = m.ID
		draft.PlayOrder = m.PlayOrder
		s.setReportDraft(tgID, draft)
		s.setState(ctx, tgID, models.StateReportScore)
		msg := fmt.Sprintf("🏆 Отчет о результате матча\nМатч #%d (раунд %d): %s vs %s\n\nУкажите счет матча в пользу вашей команды:\n(Выберите кнопку или отправьте счет сообщением, например 2:0)",
			m.PlayOrder, m.Round, myTeam.Name, opp.Name)
		return msg, KbReportScore
	}

	opponents, err := s.GetEligibleOpponents(ctx, tgID)
	if err != nil || len(opponents) == 0 {
		return "Нет доступных команд-соперников для отправки отчета.", KbNone
	}
	s.setReportDraft(tgID, draft)
	s.setState(ctx, tgID, models.StateReportOpponent)

	msg := fmt.Sprintf("🏆 Отчет о результате матча\nВаша команда: %s (Победитель)\n\nВыберите команду соперника, против которой вы играли:", myTeam.Name)
	return msg, KbReportOpponent
```

В `SetReportScore` после `draft.Score = formatted`:

```go
	draft.WinnerScore, draft.LoserScore = x, y
```

`SubmitReport` целиком:

```go
func (s *TelegramServiceImpl) SubmitReport(ctx context.Context, tgID int64) (string, string, *models.TelegramMatchReport, []models.BracketMatch) {
	draft := s.GetReportDraft(tgID)
	if draft == nil {
		return "Сессия отчета истекла. Начните заново: /report", KbNone, nil, nil
	}

	if len(draft.PhotoFileIDs) == 0 {
		return "Сначала загрузите хотя бы один скриншот матча.", KbReportPhotos + ":0", nil, nil
	}

	report := &models.TelegramMatchReport{
		ReporterTelegramID: tgID,
		WinnerTeamID:       draft.WinnerTeamID,
		WinnerTeamName:     draft.WinnerTeamName,
		LoserTeamID:        draft.LoserTeamID,
		LoserTeamName:      draft.LoserTeamName,
		Score:              draft.Score,
		PhotoFileIDs:       draft.PhotoFileIDs,
	}
	if draft.BracketMatchID != 0 {
		id := draft.BracketMatchID
		report.BracketMatchID = &id
	}

	if err := s.repo.CreateMatchReport(ctx, report); err != nil {
		s.logger.Error("telegram: failed to save match report: %v", err)
		return "Ошибка при сохранении отчета в базу данных: " + err.Error(), KbReportPhotos + ":" + strconv.Itoa(len(draft.PhotoFileIDs)), nil, nil
	}

	s.setState(ctx, tgID, models.StateIdle)
	s.clearReportDraft(tgID)

	successMsg := fmt.Sprintf("✅ Отчет о матче %s %s %s успешно отправлен судьям!",
		draft.WinnerTeamName, draft.Score, draft.LoserTeamName)

	var ready []models.BracketMatch
	if s.bracket != nil && draft.BracketMatchID != 0 {
		var err error
		ready, err = s.bracket.ReportResult(ctx, draft.BracketMatchID, draft.WinnerTeamID, draft.WinnerScore, draft.LoserScore)
		if err != nil {
			// Saved with synced_at NULL: the background worker pushes it later.
			s.logger.Error("telegram: bracket report for match %d failed, queued: %v", draft.BracketMatchID, err)
			successMsg += "\n\nСетка сейчас недоступна — результат принят, сетка обновится в течение нескольких минут."
		} else {
			s.logWrite("SetReportSynced", s.repo.SetReportSynced(ctx, report.ID))
			now := time.Now()
			report.SyncedAt = &now
			successMsg += fmt.Sprintf("\nМатч #%d закрыт, победитель проходит дальше.", draft.PlayOrder)
		}
	}
	return successMsg, "main_menu", report, ready
}
```

- [ ] **Step 5: Сборка в `service.go`**

В структуру `Service` добавить `Bracket *BracketService` (nil = выключено). Сигнатуру `NewService` расширить:

```go
func NewService(repos *repository.Repository, ai AIProvider, sheetsClient sheets.Client, ownerEmail, spreadsheetID string, httpTimeoutSec int, bracket BracketProvider, walkoverWin, walkoverLose int, logger Logger) *Service {
```

и в теле перед `return`:

```go
	tg := NewTelegramServiceImpl(repos.Telegram, logger).WithProfileLookup(repos.ProfileLink)
	var bracketSvc *BracketService
	if bracket != nil {
		bracketSvc = NewBracketService(repos.Telegram, bracket, walkoverWin, walkoverLose, logger)
		tg.WithBracket(bracketSvc)
	}
```

заменить `TelegramService: NewTelegramServiceImpl(...)...` на `TelegramService: tg,` и добавить `Bracket: bracketSvc,`.

В `cmd/app/main.go` — вызов `NewService(...)`: найти его (`grep -n "NewService(" cmd/app/main.go`) и передать провайдер:

```go
	var bracketProvider application.BracketProvider
	if cfg.BracketEnabled() {
		bracketProvider = challonge.New(cfg.ChallongeAPIKey, cfg.ChallongeSubdomain, &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second})
		log.Info("Challonge bracket enabled")
	}
	walkoverWin, walkoverLose, _ := config.ParseWalkoverScore(cfg.BracketWalkoverScore)
	services := application.NewService(..., bracketProvider, walkoverWin, walkoverLose, log)
```

`bracketProvider` — интерфейсная переменная: **не** присваивать ей `*challonge.Client` равный nil (типизированный nil ≠ nil). Импорты: `blackwatch/internal/challonge`, `net/http`, `time` (проверь, какие уже есть). Другие вызовы `NewService` (тесты, если есть: `grep -rn "NewService(" --include=*.go`) — дополнить `nil, 1, 0`.

- [ ] **Step 6: Обработчик сабмита**

В `internal/delivery/telegram/report.go`, `case "submit"`:

```go
	case "submit":
		resp, kb, report, ready := b.service.SubmitReport(ctx, chatID)
		if report == nil {
			b.apiRespond(callback, resp, true)
			return
		}
		b.apiRespond(callback, "Отчет успешно отправлен!", false)
		b.sendMessage(chatID, resp, kb)
		b.forwardReportMedia(ctx, report, callback.From)
		b.notifyMatchesReady(ctx, ready)
```

`notifyMatchesReady` появится в Task 7; чтобы эта задача собиралась сама по себе, добавь в `report.go` временную заглушку:

```go
// notifyMatchesReady is filled in with the bracket delivery layer.
func (b *Bot) notifyMatchesReady(ctx context.Context, ms []models.BracketMatch) {}
```

(Task 7 её заменяет.)

- [ ] **Step 7: Прогнать всё**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: зелёное, включая старые тесты отчётов (`TestReportSubmitAndCancel` с четырьмя значениями).

- [ ] **Step 8: Коммит**

```bash
git add internal/application/ internal/delivery/telegram/report.go cmd/app/main.go
git commit -m "feat(telegram): /report по открытому матчу сетки, результат уходит в Challonge"
```

---

### Task 7: Telegram — команды, рассылки, планировщик, wiring

**Files:**
- Create: `internal/delivery/telegram/bracket.go`
- Test: `internal/delivery/telegram/bracket_test.go`
- Modify: `internal/application/bracket.go` (два вспомогательных метода)
- Modify: `internal/delivery/telegram/bot.go` (поле, `NewBot`, воркер, ТП)
- Modify: `internal/delivery/telegram/handlers.go` (диспетчеризация, `/admin`, `/reinstate`)
- Modify: `internal/delivery/telegram/report.go` (убрать заглушку `notifyMatchesReady`)
- Modify: `cmd/app/main.go` (`NewBot` получает `services.Bracket`)

**Interfaces:**
- Consumes: `BracketService` (Tasks 4–5), `application.RegistrationCloseLead`, `application.KbRegCheckin`, `technicalDefeatGrace`, `scheduleCatchUpWindow`, `b.shouldFire`.
- Produces:
  - `func (s *BracketService) CaptainChatIDs(ctx, teamID int) []int64`, `func (s *BracketService) Walkover() (win, lose int)`
  - `Bot.bracket *application.BracketService`; `NewBot(..., bracket *application.BracketService, ...)` — параметр добавляется **после** `bets BetSettings`.
  - чистые функции: `roundLabel(round, total int) string`, `formatBracketRound(url string, ms []models.BracketMatch, round int) []string`, `defaultBracketRound(ms []models.BracketMatch) int`, `parseSetWinner(args string, defWin, defLose int) (playOrder int, team string, win, lose int, err error)`, `bracketBuildDue(tournament, now time.Time) bool`.

- [ ] **Step 1: Вспомогательные методы сервиса**

В `internal/application/bracket.go`:

```go
// CaptainChatIDs is who to message about a team's match.
func (s *BracketService) CaptainChatIDs(ctx context.Context, teamID int) []int64 {
	members, err := s.repo.GetTeamMembers(ctx, teamID)
	if err != nil {
		s.logger.Error("bracket: GetTeamMembers %d: %v", teamID, err)
		return nil
	}
	var ids []int64
	for _, p := range members {
		if p.IsCaptain && p.TelegramID != nil {
			ids = append(ids, *p.TelegramID)
		}
	}
	return ids
}

// Walkover is the configured technical-defeat score.
func (s *BracketService) Walkover() (win, lose int) {
	return s.walkoverWin, s.walkoverLose
}
```

Проверь, что `fakeTelegramRepo.GetTeamMembers` возвращает игроков команды (`grep -n "GetTeamMembers" internal/application/telegram_service_test.go`); если он возвращает `nil`, дополни: перебор `r.teams[teamID].Players`.

- [ ] **Step 2: Тесты чистых функций**

`internal/delivery/telegram/bracket_test.go`:

```go
package telegram

import (
	"strings"
	"testing"
	"time"

	"blackwatch/internal/models"
)

func ip(v int) *int { return &v }

func TestRoundLabel(t *testing.T) {
	cases := []struct {
		round, total int
		want         string
	}{
		{3, 3, "Раунд 3 — финал"},
		{2, 3, "Раунд 2 — полуфинал"},
		{1, 3, "Раунд 1 — 1/4"},
		{1, 6, "Раунд 1 — 1/32"},
	}
	for _, c := range cases {
		if got := roundLabel(c.round, c.total); got != c.want {
			t.Errorf("roundLabel(%d,%d) = %q, want %q", c.round, c.total, got, c.want)
		}
	}
}

func TestFormatBracketRound(t *testing.T) {
	ms := []models.BracketMatch{
		{PlayOrder: 1, Round: 1, Team1ID: ip(1), Team2ID: ip(4), Team1Name: "T1", Team2Name: "T4", State: models.BracketComplete, WinnerID: ip(1), ScoresCSV: "2 - 0"},
		{PlayOrder: 2, Round: 1, Team1ID: ip(2), Team2ID: ip(3), Team1Name: "T2", Team2Name: "T3", State: models.BracketOpen},
		{PlayOrder: 3, Round: 1, Team1ID: ip(5), Team1Name: "T5", State: models.BracketComplete, WinnerID: ip(5)},
		{PlayOrder: 4, Round: 2, Team1ID: ip(1), Team1Name: "T1", State: models.BracketPending},
	}
	parts := formatBracketRound("https://challonge.com/x", ms, 1)
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	text := parts[0]
	for _, want := range []string{"https://challonge.com/x", "Раунд 1 — полуфинал", "#1 T1 vs T4 — 2 - 0, победа T1", "#2 T2 vs T3 — идёт", "#3 T5 — автопроход"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "#4") {
		t.Error("round 2 leaked into round 1")
	}
	r2 := formatBracketRound("u", ms, 2)[0]
	if !strings.Contains(r2, "#4 T1 vs ? — ожидает соперника") {
		t.Errorf("pending line wrong:\n%s", r2)
	}
	if got := formatBracketRound("u", ms, 9); len(got) != 1 || !strings.Contains(got[0], "нет матчей") {
		t.Errorf("unknown round = %v", got)
	}
}

func TestFormatBracketRoundSplitsLongOutput(t *testing.T) {
	var ms []models.BracketMatch
	for i := 1; i <= 64; i++ {
		ms = append(ms, models.BracketMatch{PlayOrder: i, Round: 1, Team1ID: ip(i), Team2ID: ip(i + 100),
			Team1Name: strings.Repeat("A", 30), Team2Name: strings.Repeat("B", 30), State: models.BracketOpen})
	}
	parts := formatBracketRound("u", ms, 1)
	if len(parts) < 2 {
		t.Fatalf("64 long lines fit in one message: %d parts", len(parts))
	}
	for i, p := range parts {
		if len(p) > telegramMessageLimit {
			t.Errorf("part %d is %d chars, over the limit", i, len(p))
		}
	}
}

func TestDefaultBracketRound(t *testing.T) {
	ms := []models.BracketMatch{
		{Round: 1, State: models.BracketComplete},
		{Round: 2, State: models.BracketOpen},
		{Round: 3, State: models.BracketPending},
	}
	if got := defaultBracketRound(ms); got != 2 {
		t.Errorf("default round = %d, want 2 (first with unplayed matches)", got)
	}
	all := []models.BracketMatch{{Round: 1, State: models.BracketComplete}, {Round: 2, State: models.BracketComplete}}
	if got := defaultBracketRound(all); got != 2 {
		t.Errorf("all played: default round = %d, want last", got)
	}
	if got := defaultBracketRound(nil); got != 1 {
		t.Errorf("empty: default round = %d, want 1", got)
	}
}

func TestParseSetWinner(t *testing.T) {
	po, team, w, l, err := parseSetWinner("7 Team Liquid", 1, 0)
	if err != nil || po != 7 || team != "Team Liquid" || w != 1 || l != 0 {
		t.Errorf("default score: %d %q %d:%d %v", po, team, w, l, err)
	}
	po, team, w, l, err = parseSetWinner("12 NaVi 2:1", 1, 0)
	if err != nil || po != 12 || team != "NaVi" || w != 2 || l != 1 {
		t.Errorf("explicit score: %d %q %d:%d %v", po, team, w, l, err)
	}
	for _, bad := range []string{"", "x Team", "7", "7 Team 1:2", "7 Team 2-2"} {
		if _, _, _, _, err := parseSetWinner(bad, 1, 0); err == nil {
			t.Errorf("parseSetWinner(%q) accepted", bad)
		}
	}
}

func TestBracketBuildDue(t *testing.T) {
	tourney := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	cases := []struct {
		now  time.Time
		want bool
	}{
		{tourney.Add(-90 * time.Minute), false}, // too early
		{tourney.Add(-60 * time.Minute), true},  // registration just closed
		{tourney.Add(-5 * time.Minute), true},   // still before start: retrying
		{tourney.Add(1 * time.Minute), false},   // tournament started: admin's job now
	}
	for _, c := range cases {
		if got := bracketBuildDue(tourney, c.now); got != c.want {
			t.Errorf("bracketBuildDue(now=%s) = %v, want %v", c.now.Format("15:04"), got, c.want)
		}
	}
	if bracketBuildDue(time.Time{}, tourney) {
		t.Error("due with no tournament time")
	}
}
```

- [ ] **Step 3: Убедиться, что падает**

Run: `go test ./internal/delivery/telegram/ -run 'TestRoundLabel|TestFormatBracket|TestDefaultBracket|TestParseSetWinner|TestBracketBuildDue' 2>&1 | head -5`
Expected: ошибки компиляции.

- [ ] **Step 4: `bracket.go` — чистые функции**

`internal/delivery/telegram/bracket.go`:

```go
package telegram

import (
	"blackwatch/internal/application"
	"blackwatch/internal/challonge"
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// telegramMessageLimit is Telegram's cap on a text message; a round of 64
// matches with long names does not fit, so /bracket is split.
const telegramMessageLimit = 4000

// bracketBuildFailureAlert is after how many failed automatic builds the
// admins are told.
const bracketBuildFailureAlert = 5

func roundLabel(round, total int) string {
	switch d := total - round; d {
	case 0:
		return fmt.Sprintf("Раунд %d — финал", round)
	case 1:
		return fmt.Sprintf("Раунд %d — полуфинал", round)
	default:
		return fmt.Sprintf("Раунд %d — 1/%d", round, 1<<uint(d))
	}
}

func totalRounds(ms []models.BracketMatch) int {
	total := 0
	for _, m := range ms {
		if m.Round > total {
			total = m.Round
		}
	}
	return total
}

func matchLine(m models.BracketMatch) string {
	name := func(id *int, n string) string {
		if id == nil {
			return "?"
		}
		return n
	}
	switch {
	case m.State == models.BracketComplete && (m.Team1ID == nil || m.Team2ID == nil):
		// A bye: one team, no game.
		if m.Team1ID != nil {
			return fmt.Sprintf("#%d %s — автопроход", m.PlayOrder, m.Team1Name)
		}
		return fmt.Sprintf("#%d %s — автопроход", m.PlayOrder, m.Team2Name)
	case m.State == models.BracketComplete:
		winner := m.Team1Name
		if m.WinnerID != nil && m.Team2ID != nil && *m.WinnerID == *m.Team2ID {
			winner = m.Team2Name
		}
		return fmt.Sprintf("#%d %s vs %s — %s, победа %s", m.PlayOrder, m.Team1Name, m.Team2Name, m.ScoresCSV, winner)
	case m.Ready():
		return fmt.Sprintf("#%d %s vs %s — идёт", m.PlayOrder, m.Team1Name, m.Team2Name)
	default:
		return fmt.Sprintf("#%d %s vs %s — ожидает соперника", m.PlayOrder, name(m.Team1ID, m.Team1Name), name(m.Team2ID, m.Team2Name))
	}
}

// formatBracketRound renders one round as one or more messages under the
// Telegram limit. The first message carries the link.
func formatBracketRound(url string, ms []models.BracketMatch, round int) []string {
	total := totalRounds(ms)
	head := fmt.Sprintf("🏆 Сетка: %s\n\n%s\n", url, roundLabel(round, total))
	var lines []string
	for _, m := range ms {
		if m.Round == round {
			lines = append(lines, matchLine(m))
		}
	}
	if len(lines) == 0 {
		return []string{fmt.Sprintf("🏆 Сетка: %s\n\nВ раунде %d нет матчей.", url, round)}
	}
	var parts []string
	cur := head
	for _, l := range lines {
		if len(cur)+len(l)+1 > telegramMessageLimit {
			parts = append(parts, strings.TrimRight(cur, "\n"))
			cur = ""
		}
		cur += l + "\n"
	}
	return append(parts, strings.TrimRight(cur, "\n"))
}

// defaultBracketRound is the first round with something left to play, or
// the last round when everything is done.
func defaultBracketRound(ms []models.BracketMatch) int {
	if len(ms) == 0 {
		return 1
	}
	best := 0
	for _, m := range ms {
		if m.State != models.BracketComplete && (best == 0 || m.Round < best) {
			best = m.Round
		}
	}
	if best == 0 {
		return totalRounds(ms)
	}
	return best
}

// parseSetWinner reads "/set_winner <#match> <team name> [W:L]". The team
// name may contain spaces; a trailing token that parses as a score is one.
func parseSetWinner(args string, defWin, defLose int) (playOrder int, team string, win, lose int, err error) {
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return 0, "", 0, 0, errors.New("формат: /set_winner <№ матча> <название команды> [счёт, например 2:1]")
	}
	playOrder, err = strconv.Atoi(strings.TrimPrefix(fields[0], "#"))
	if err != nil || playOrder <= 0 {
		return 0, "", 0, 0, errors.New("номер матча должен быть числом, например 7")
	}
	win, lose = defWin, defLose
	rest := fields[1:]
	if len(rest) >= 2 {
		if w, l, ok := parseScorePair(rest[len(rest)-1]); ok {
			if w <= l {
				return 0, "", 0, 0, errors.New("счёт должен быть в пользу победителя, например 2:1")
			}
			win, lose = w, l
			rest = rest[:len(rest)-1]
		}
	}
	return playOrder, strings.Join(rest, " "), win, lose, nil
}

// parseScorePair reads "2:1" or "2-1".
func parseScorePair(s string) (int, int, bool) {
	sep := ":"
	if !strings.Contains(s, sep) {
		sep = "-"
	}
	parts := strings.Split(s, sep)
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || a < 0 || b < 0 {
		return 0, 0, false
	}
	return a, b, true
}

// bracketBuildDue: from registration close (an hour before) until the start.
// After the start a missing bracket is the admin's call (/build_bracket).
func bracketBuildDue(tournament, now time.Time) bool {
	if tournament.IsZero() {
		return false
	}
	closeAt := tournament.Add(-application.RegistrationCloseLead)
	return !now.Before(closeAt) && now.Before(tournament)
}
```

- [ ] **Step 5: Прогнать тесты чистых функций**

Run: `go test ./internal/delivery/telegram/ -run 'TestRoundLabel|TestFormatBracket|TestDefaultBracket|TestParseSetWinner|TestBracketBuildDue' -v`
Expected: PASS.

- [ ] **Step 6: `bracket.go` — команды и рассылки**

Дописать в `internal/delivery/telegram/bracket.go`:

```go
// ---- notifications --------------------------------------------------------

// reportBracketError logs a bracket failure and tells the admins once about
// the one that matters to them: the quota.
func (b *Bot) reportBracketError(what string, err error) {
	if err == nil {
		return
	}
	b.logger.Error("telegram: bracket %s: %v", what, err)
	if errors.Is(err, challonge.ErrQuotaExceeded) && !b.quotaAlerted {
		b.quotaAlerted = true
		b.notifyAdmins("⚠️ Challonge: исчерпана месячная квота запросов (500). Сетка не обновляется — нужен платный план. Отчёты капитанов сохраняются и уйдут в сетку, когда квота откроется.")
	}
}

func (b *Bot) notifyAdmins(text string) {
	for id := range b.adminIDs {
		b.sendMessage(id, text, "main_menu")
	}
}

func (b *Bot) notifyTeam(ctx context.Context, teamID int, text, kb string) {
	for _, chatID := range b.bracket.CaptainChatIDs(ctx, teamID) {
		b.sendMessage(chatID, text, kb)
	}
}

// notifyMatchesReady tells both captains their next match is set.
func (b *Bot) notifyMatchesReady(ctx context.Context, ms []models.BracketMatch) {
	if b.bracket == nil {
		return
	}
	for _, m := range ms {
		text := fmt.Sprintf("⚔️ Раунд %d, матч #%d: %s vs %s.\n\nПосле игры капитан победителя отправляет /report.",
			m.Round, m.PlayOrder, m.Team1Name, m.Team2Name)
		b.notifyTeam(ctx, *m.Team1ID, text, "main_menu")
		b.notifyTeam(ctx, *m.Team2ID, text, "main_menu")
	}
}

// notifyMatchesReset tells the captains of matches an admin override undid.
func (b *Bot) notifyMatchesReset(ctx context.Context, ms []models.BracketMatch) {
	for _, m := range ms {
		text := fmt.Sprintf("⚠️ Результат матча #%d (%s vs %s) отменён администратором. Бот пришлёт нового соперника или попросит переиграть матч.",
			m.PlayOrder, m.Team1Name, m.Team2Name)
		if m.Team1ID != nil {
			b.notifyTeam(ctx, *m.Team1ID, text, "main_menu")
		}
		if m.Team2ID != nil {
			b.notifyTeam(ctx, *m.Team2ID, text, "main_menu")
		}
	}
}

func (b *Bot) applyBracketChange(ctx context.Context, ch *application.BracketChange) {
	if ch == nil {
		return
	}
	b.notifyMatchesReset(ctx, ch.Reset)
	b.notifyMatchesReady(ctx, ch.Ready)
}

// announceBracket posts the bracket to the tournament chat and every
// captain: their round-1 pairing, or their bye, plus the check-in button.
func (b *Bot) announceBracket(ctx context.Context, built *application.BracketBuilt) {
	tTime := b.service.GetTournamentTime(ctx)
	deadline := tTime.In(b.location).Add(technicalDefeatGrace).Format("15:04")

	ms, _ := b.bracket.Matches(ctx)
	for _, part := range formatBracketRound(built.URL, ms, 1) {
		b.notifyTournamentChat("СЕТКА ГОТОВА\n\n" + part)
	}
	if len(built.Byes) > 0 {
		names := make([]string, 0, len(built.Byes))
		for _, t := range built.Byes {
			names = append(names, t.Name)
		}
		b.notifyTournamentChat("Автопроход во 2-й раунд: " + strings.Join(names, ", "))
	}

	for _, m := range built.Round1 {
		text := fmt.Sprintf("⚔️ Сетка готова! Раунд 1, матч #%d: %s vs %s.\nСетка: %s\n\nПодтвердите участие кнопкой ниже до %s, иначе — техническое поражение.",
			m.PlayOrder, m.Team1Name, m.Team2Name, built.URL, deadline)
		b.notifyTeam(ctx, *m.Team1ID, text, application.KbRegCheckin)
		b.notifyTeam(ctx, *m.Team2ID, text, application.KbRegCheckin)
	}
	for _, t := range built.Byes {
		text := fmt.Sprintf("🏆 Сетка готова! В 1-м раунде у команды '%s' автопроход.\nСетка: %s\n\nЧек-ин обязателен: подтвердите участие кнопкой ниже до %s, иначе — техническое поражение.",
			t.Name, built.URL, deadline)
		b.notifyTeam(ctx, t.ID, text, application.KbRegCheckin)
	}
}

// ---- scheduler hooks --------------------------------------------------------

// runBracketChecks is the bracket's share of the minute tick: build when
// registration closes, sweep forfeits, push queued reports.
func (b *Bot) runBracketChecks(ctx context.Context, tTime, now time.Time) {
	if b.bracket == nil {
		return
	}
	if bracketBuildDue(tTime, now) && !b.bracket.IsBuiltFor(ctx, tTime) {
		b.buildBracket(ctx, tTime, 0)
	}
	if b.bracket.IsBuiltFor(ctx, tTime) {
		ready, err := b.bracket.ForfeitDisqualified(ctx)
		b.reportBracketError("forfeit sweep", err)
		b.notifyMatchesReady(ctx, ready)

		ready, err = b.bracket.FlushPendingReports(ctx)
		b.reportBracketError("flush reports", err)
		b.notifyMatchesReady(ctx, ready)
	}
}

// buildBracket builds and announces; adminChat, when non-zero, gets the
// outcome. Automatic builds alert the admins on the Nth consecutive failure.
func (b *Bot) buildBracket(ctx context.Context, tTime time.Time, adminChat int64) {
	built, err := b.bracket.Build(ctx, tTime)
	if err != nil {
		b.reportBracketError("build", err)
		if adminChat != 0 {
			b.sendMessage(adminChat, "Не удалось построить сетку: "+err.Error(), "main_menu")
		} else if b.bracket.BuildFailures(ctx) == bracketBuildFailureAlert {
			b.notifyAdmins(fmt.Sprintf("⚠️ Не удалось построить сетку в Challonge (%d попыток подряд): %v\nБот продолжает пробовать раз в минуту до старта. Вручную: /build_bracket", bracketBuildFailureAlert, err))
		}
		return
	}
	b.announceBracket(ctx, built)
	if adminChat != 0 {
		b.sendMessage(adminChat, fmt.Sprintf("Сетка построена: %s\nМатчей в 1-м раунде: %d, автопроходов: %d.", built.URL, len(built.Round1), len(built.Byes)), "main_menu")
	}
}

// ---- commands ----------------------------------------------------------------

func (b *Bot) handleBracketCommand(ctx context.Context, chatID int64, text string) bool {
	if b.bracket == nil {
		return false
	}
	switch {
	case text == "/bracket" || strings.HasPrefix(text, "/bracket "):
		ms, err := b.bracket.Matches(ctx)
		if err != nil {
			b.sendMessage(chatID, "Не удалось прочитать сетку: "+err.Error(), "main_menu")
			return true
		}
		url := b.bracket.URL(ctx)
		if url == "" || len(ms) == 0 {
			b.sendMessage(chatID, "Сетка ещё не построена. Она появится за час до старта турнира.", "main_menu")
			return true
		}
		round := defaultBracketRound(ms)
		if arg := strings.TrimSpace(strings.TrimPrefix(text, "/bracket")); arg != "" {
			if n, err := strconv.Atoi(arg); err == nil && n > 0 {
				round = n
			}
		}
		for _, part := range formatBracketRound(url, ms, round) {
			b.sendMessage(chatID, part, "main_menu")
		}
		return true

	case text == "/build_bracket" && b.isAdmin(chatID):
		tTime := b.service.GetTournamentTime(ctx)
		if tTime.IsZero() {
			b.sendMessage(chatID, "Сначала задайте время турнира: /set_tourney", "main_menu")
			return true
		}
		if has, _ := b.bracket.HasResults(ctx); has {
			b.sendMessage(chatID, "В сетке уже есть сыгранные матчи — пересобрать нельзя. Исправляйте результаты через /set_winner.", "main_menu")
			return true
		}
		b.sendMessage(chatID, "Строю сетку...", "main_menu")
		b.buildBracket(ctx, tTime, chatID)
		return true

	case strings.HasPrefix(text, "/set_winner") && b.isAdmin(chatID):
		defWin, defLose := b.bracket.Walkover()
		po, team, w, l, err := parseSetWinner(strings.TrimPrefix(text, "/set_winner"), defWin, defLose)
		if err != nil {
			b.sendMessage(chatID, err.Error(), "main_menu")
			return true
		}
		ch, err := b.bracket.SetWinner(ctx, po, team, w, l)
		if err != nil {
			b.reportBracketError("set_winner", err)
			b.sendMessage(chatID, "Не удалось: "+err.Error(), "main_menu")
			return true
		}
		b.applyBracketChange(ctx, ch)
		b.sendMessage(chatID, fmt.Sprintf("Матч #%d: победа '%s' %d:%d. Сброшено матчей дальше по сетке: %d.", po, team, w, l, len(ch.Reset)), "main_menu")
		b.notifyTournamentChat(fmt.Sprintf("РЕШЕНИЕ АДМИНА\n\nМатч #%d: победа '%s' %d:%d.", po, team, w, l))
		return true
	}
	return false
}
```

- [ ] **Step 7: `bot.go` — поле, конструктор, воркер, ТП**

В `Bot` после `bettingBot *BettingBot`:

```go
	// bracket is the Challonge bracket; nil when CHALLONGE_API_KEY is unset.
	bracket *application.BracketService
	// quotaAlerted: the admins hear about an exhausted Challonge quota once.
	quotaAlerted bool
```

`NewBot`: добавить параметр `bracket *application.BracketService` после `bets BetSettings`; в литерале `b := &Bot{...}` — `bracket: bracket,`; после логов про betting:

```go
	if bracket != nil {
		logger.Info("Telegram bracket commands enabled (Challonge)")
	}
```

В `runScheduledChecks` в конец (после проверки ТП):

```go
	b.runBracketChecks(ctx, tTime, now)
```

В `processTechnicalDefeat` после того, как отчёт по ТП сформирован и разослан (в самом конце функции):

```go
	if b.bracket != nil {
		ready, err := b.bracket.ForfeitDisqualified(ctx)
		b.reportBracketError("forfeit after technical defeat", err)
		b.notifyMatchesReady(ctx, ready)
	}
```

- [ ] **Step 8: `handlers.go`**

В `handleUpdate` (bot.go) — перед блоком `if b.isAdmin(chatID) && (...)` добавить:

```go
	if b.handleBracketCommand(ctx, chatID, text) {
		return
	}
```

(`/bracket` доступен всем; `/build_bracket` и `/set_winner` внутри проверяют `isAdmin`, для не-админа падают в обычный обработчик как неизвестная команда.)

В тексте `/admin` после строки `/reinstate ...`:

```go
			"/build_bracket - Пересобрать сетку (пока нет результатов)\n" +
			"/set_winner [№] [команда] [счёт] - Результат матча вручную\n" +
			"/bracket [раунд] - Показать сетку\n" +
```

`/reinstate` — после `b.notifyTeamReinstated(resp)`:

```go
		if b.bracket != nil && strings.Contains(resp, "возвращена в турнир") {
			ch, err := b.bracket.Reinstate(ctx, name)
			b.reportBracketError("reinstate", err)
			b.applyBracketChange(ctx, ch)
			if ch != nil {
				b.sendMessage(chatID, "Команда возвращена в свой матч сетки.", "main_menu")
			}
		}
```

В `report.go` удалить заглушку `notifyMatchesReady` из Task 6.

В главном меню (`helpers.go`, `case "main_menu"`) добавить кнопку `/bracket` в ряд с `/checkin` и `/report`:

```go
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("/checkin"),
				tgbotapi.NewKeyboardButton("/report"),
				tgbotapi.NewKeyboardButton("/bracket"),
			),
```

- [ ] **Step 9: `main.go`**

Вызов `telegram.NewBot(...)`: после `telegram.BetSettings{...}` передать `services.Bracket`.

- [ ] **Step 10: Всё зелёное**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS везде. Затем `golangci-lint run ./...`, если линтер настроен в Makefile (`grep -n lint Makefile`).

- [ ] **Step 11: Ручная проверка без Challonge**

Запустить бота без `CHALLONGE_API_KEY` (как раньше): `/report` показывает выбор соперника, `/bracket` отвечает как неизвестная команда (падает в `HandleUserInput`), фоновый воркер не трогает сетку. Это подтверждает Global Constraint «без ключа ничего не меняется».

- [ ] **Step 12: Коммит**

```bash
git add internal/ cmd/app/main.go
git commit -m "feat(telegram): сетка в Challonge — /bracket, /build_bracket, /set_winner, автопостроение за час, ТП по матчу"
```

---

### Task 8: Проверка от начала до конца с реальным Challonge

**Files:** нет новых.

Это приёмка, не код. Нужны: `CHALLONGE_API_KEY` в `.env`, тестовый Telegram-бот, 3–4 аккаунта-капитана (или один аккаунт и правка `telegram_players` руками).

- [ ] **Step 1:** Зарегистрировать 3 команды, `/set_tourney` на +65 минут. Через ~5 минут в турнирном чате — «СЕТКА ГОТОВА» со ссылкой; в Challonge три участника, сеяные по звёздам; капитанам пришли пары/автопроход с кнопкой чек-ина. Квота: 4 запроса.
- [ ] **Step 2:** Двум капитанам нажать чек-ин, третьему — нет. `/set_tourney` на «сейчас − 9 минут» (чтобы ТП сработало на ближайшем тике). Ожидание: ТП третьей команде, её соперник проходит в финал без игры, в Challonge матч закрыт счётом walkover. Если сеяные так легли, что третья команда была с автопроходом — её ТП сработает, когда откроется финал.
- [ ] **Step 3:** Капитан оставшейся пары: `/report` → сразу счёт (без выбора соперника) → скриншот → отправить. В Challonge матч закрыт, капитанам финала пришёл пинг.
- [ ] **Step 4:** Админ: `/set_winner <№ сыгранного матча> <проигравшая команда> 2:1`. Финал сброшен, капитанам ушло «отменено администратором», новому финалисту — пинг. `/bracket 2` показывает новый финал.
- [ ] **Step 5:** `/reinstate <команда с ТП>` — команда вернулась в свой матч, `/bracket 1` показывает его как «идёт».
- [ ] **Step 6:** Выключить сеть/подменить ключ на неверный, отправить `/report`: капитану — «сетка обновится», в `telegram_match_reports.synced_at` — NULL. Вернуть ключ: в течение минуты отчёт ушёл, `synced_at` заполнен.
- [ ] **Step 7:** Записать результат в PR/сообщение: что работало, что нет, сколько запросов ушло (Developer Portal Challonge).

---

## Самопроверка плана

**Покрытие спека:**
- Жизненный цикл T−1ч → Build: Task 7 (`bracketBuildDue`, `runBracketChecks`). Чек-ин — существующий. T+10 → ТП → walkover: Task 5 (`ForfeitDisqualified`) + Task 7 (`processTechnicalDefeat`). Обе стороны сняты — каскад в Task 5. Bye-команды без матча — воркер (`runBracketChecks`) повторяет `ForfeitDisqualified` каждую минуту.
- `/report` без соперника, привязка к матчу, пинг следующей пары: Task 6 + `notifyMatchesReady` в Task 7.
- `/set_winner`, `/reinstate`, `/build_bracket`, `/bracket`: Task 7 на методах Task 5.
- Посев по среднему звёзд основы, tie-break по id, исключение disqualified: Task 4 (`SeedTeams`).
- Данные: миграция, `challonge_participant_id`, `bracket_match_id`, `synced_at`, настройки: Task 2 + Task 4 (константы ключей).
- Конфиг: Task 1.
- Клиент, `ErrQuotaExceeded`: Task 3. Квота → одно сообщение админам: Task 7 (`reportBracketError`).
- Отказ Challonge при Build: счётчик и алерт на 5-й — Task 4 (`Build`) + Task 7 (`buildBracket`); частично созданный турнир удаляется на следующей попытке — Task 4 (`build`).
- Отказ при `/report` → очередь → досылка: Task 5 (`FlushPendingReports`), Task 6, Task 7 (воркер).
- Spike против реального API: Task 3 `TestLive`.
- Тесты: посев (Task 4), сервис на фейке (Tasks 4–5), клиент на httptest (Task 3), интеграционный тест кэша (Task 2).

**Отличие от спека, принятое в плане:** `/set_winner` принимает необязательный третий аргумент — счёт (по умолчанию walkover-счёт из конфига): без него откат «команда реально выиграла 2:1» записал бы 1:0. Не меняет поведения, описанного в спеке, только расширяет.

**Согласованность имён:** `ReplaceBracketMatches/GetBracketMatches/MarkBracketNotified/SetTeamParticipantID/ClearTeamParticipantIDs/SetReportSynced/GetUnsyncedReports` — одинаковы в Task 2 (интерфейс, фейк, Postgres) и Tasks 4–5. `SubmitReport` с четырьмя значениями — Task 6 (интерфейс, реализация, тест) и `report.go`. `NewBot` получает `bracket` после `bets` — Task 7 и `main.go`. `Reinstate(ctx, teamName string)` — Task 5 и Task 7.
