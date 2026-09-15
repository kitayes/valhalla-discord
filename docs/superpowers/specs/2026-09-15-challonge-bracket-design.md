# Турнирная сетка через Challonge: сетка до чек-ина, ТП по матчу, `/report` без выбора соперника

Дата: 2026-09-15
Статус: дизайн, к реализации не приступали

## Задача

На турнир регистрируется произвольное число команд — 47, 73, 115 — и точное
N известно только к закрытию регистрации. Сетку на 100+ команд админы сейчас
сводят руками, а `/report` — свободная форма: капитан сам выбирает
соперника из списка всех команд, бот пересылает скриншоты «судьям» и на
этом всё. Кто с кем играет, кто прошёл дальше и кто завис — живёт в чате.

Что меняем:

1. **Сетка строится автоматически при закрытии регистрации** (за час до
   старта, уже есть автозакрытие) — в Challonge, по всем активным командам,
   с посевом по силе состава. Ссылка на сетку уходит в турнирный чат,
   капитанам в ЛС — их пара первого раунда и кнопка чек-ина.
2. **Неявка на чек-ин = техническое поражение в матче сетки.** Sweep ТП
   (уже есть) докладывает в Challonge победу сопернику; тот проходит дальше
   без игры.
3. **`/report` привязан к матчу.** Бот сам знает соперника по сетке; капитан
   вводит только счёт и скриншоты. Победитель продвигается сразу по отчёту
   капитана (без подтверждения админом); когда следующий матч заполнен обеими
   командами — оба капитана получают пинг.
4. **Откат — одна команда админа.** `/set_winner` меняет победителя; Challonge
   сам сбрасывает все матчи, ветвящиеся от изменённого. `/reinstate`
   использует тот же путь.

## Почему Challonge, а не своя сетка

Решение принято осознанно, с известной ценой:

- Плюсы: генерация пар с byes и посевом, публичная страница с нарисованной
  сеткой (`challonge.com/<url>`), каскадный откат результата — всё готовое.
- Цена: с июля 2026 бесплатно **500 запросов в месяц**, дальше платный план
  или `429`. Турнир на 100 команд стоит ≈200 запросов (см. «Бюджет
  запросов»). Один турнир в месяц влезает, два — впритык, три — платный план.
- Цена: внешняя зависимость за час до старта. Закрывается ретраями,
  очередью недосланных отчётов и ручным `/build_bracket`.

## Источник правды и кэш

**Challonge — источник правды. Наша БД — кэш для чтения.**

- Пишем только в Challonge: создать турнир, залить участников, стартовать,
  доложить счёт.
- После каждой записи один раз вызываем `ListMatches` и перезаписываем кэш
  `telegram_bracket_matches`.
- Читаем («кто мой соперник», `/bracket`, «пара готова») только из кэша —
  ноль запросов к API.

Кэш перезаписывается целиком (`DELETE` + `INSERT` в одной транзакции).
Единственное поле, которого нет в Challonge, — `both_notified`; при
перезаписи оно переносится по `challonge_match_id`, чтобы пинг «пара готова»
не ушёл дважды.

## Жизненный цикл турнира

```
T−1ч   регистрация закрывается сама (есть)
       └► BuildBracket
            посев → CreateTournament → BulkAddParticipants(seed) → Start → Sync
            турнирный чат:  «СЕТКА ГОТОВА» + ссылка + пары раунда 1
            капитану:       «Раунд 1, матч #7: вы vs X. Подтвердите участие» [Подтвердить участие]
            bye-капитану:   «Раунд 1 пропускаете (bye). Чек-ин обязателен»   [Подтвердить участие]

T−1ч…T+10м   чек-ин (есть)

T+10м  sweep ТП (есть) → для каждой снятой команды Forfeit(team)
       └► матч open, соперник есть   → ReportMatch(winner=соперник, score=WALKOVER) → Sync
          матч open, обе сняты        → победа первой по порядку, затем её Forfeit в следующем матче
                                        (пустота проваливается по дереву до живой команды)
          команда ещё без матча (bye) → Forfeit откладывается: воркер повторит, когда матч откроется

Игра   капитан-победитель /report
       └► OpenMatchFor(team) из кэша → «Матч #7 vs X. Счёт?» → скриншоты → SubmitReport
            CreateMatchReport (журнал, как сейчас) → ReportMatch → Sync
            пересылка скриншотов админам и в чат (есть)
            если следующий матч заполнился обеими → пинг обоим капитанам, both_notified = true

Админ  /set_winner <#матч> <команда>  → ReportMatch с новым победителем → Sync
                                        Challonge сбрасывает ветку; капитанам сброшенных
                                        матчей — «Матч #N переигрывается: <причина>»
       /reinstate <команда>            → как сейчас (status=active, checked_in) + /set_winner
                                        в её матче в её пользу, если матч был закрыт ТП
       /build_bracket                  → пока ни одного complete-матча: DeleteTournament
                                        старого, BuildBracket заново. Иначе отказ.
       /bracket [раунд]                → ссылка + текстом матчи раунда (по умолчанию —
                                        первый раунд с open-матчами)
```

Правило «отчёт отправляет победитель» сохраняется. Проигравший отчёт не
шлёт; спор — через `/set_winner`.

## Посев

Сила команды — среднее `Stars` по основному составу (слоты 1–5, запасные не
считаются). Сортировка по убыванию; при равенстве — раньше
зарегистрированная выше (`telegram_teams.id`). Порядковый номер в этом
списке — `seed` участника в Challonge. Стандартный посев Challonge даёт
1 vs N, 2 vs N−1 и раздаёт byes верхним сеянным.

Команды со `status = disqualified` на момент построения в сетку не попадают.

## Данные

Миграция `000027_bracket_matches`:

```sql
CREATE TABLE telegram_bracket_matches (
    id                 SERIAL PRIMARY KEY,
    challonge_match_id BIGINT NOT NULL UNIQUE,
    round              INT NOT NULL,                 -- 1..k
    play_order         INT NOT NULL,                 -- «#N» для капитанов (suggested_play_order)
    team1_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    team2_id           INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    winner_id          INT REFERENCES telegram_teams(id) ON DELETE SET NULL,
    state              VARCHAR(16) NOT NULL,         -- pending | open | complete
    scores_csv         VARCHAR(32) NOT NULL DEFAULT '',
    both_notified      BOOLEAN NOT NULL DEFAULT FALSE,
    synced_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_bracket_matches_team1 ON telegram_bracket_matches(team1_id);
CREATE INDEX idx_bracket_matches_team2 ON telegram_bracket_matches(team2_id);

ALTER TABLE telegram_teams ADD COLUMN challonge_participant_id BIGINT;

ALTER TABLE telegram_match_reports
    ADD COLUMN bracket_match_id INT REFERENCES telegram_bracket_matches(id) ON DELETE SET NULL,
    ADD COLUMN synced_at TIMESTAMPTZ;   -- NULL = в Challonge ещё не доложен
```

`telegram_match_reports` остаётся журналом со скриншотами; `synced_at`
делает его же очередью недосланных отчётов.

Настройки в `telegram_settings` (рядом с `tournament_time`):
`challonge_tournament_id`, `challonge_tournament_url`,
`bracket_build_failures` (счётчик неудачных попыток построения).

Конфиг (`pkg/config`):

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `CHALLONGE_API_KEY` | пусто | Пусто — сетка выключена, `/report` работает как сейчас (выбор соперника из списка). |
| `CHALLONGE_SUBDOMAIN` | пусто | Организация в Challonge, если турниры ведутся под ней. |
| `BRACKET_WALKOVER_SCORE` | `1-0` | Счёт, которым докладывается ТП. |

## Компоненты

**`internal/challonge`** — тонкий HTTP-клиент API v2.1 (`Authorization-Type: v1`,
`Authorization: <key>`, `Content-Type: application/vnd.api+json`). Методы:
`CreateTournament(name, url)`, `BulkAddParticipants([]{Name, Seed})`,
`Start(tournamentID)`, `ListMatches(tournamentID)`,
`ReportMatch(tournamentID, matchID, scoresCSV, winnerParticipantID)`,
`DeleteTournament(tournamentID)`. Ретраев нет — ими занимается сервис.
`429` возвращается отдельной ошибкой `ErrQuotaExceeded`.

Клиент скрыт за интерфейсом `application.BracketProvider`; в тестах — фейк.

**`internal/application/bracket.go`** — `BracketService`:

- `Build(ctx)` — посев, создание, заливка, старт, sync, возвращает
  `BracketBuilt{URL, Round1 []Pairing, Byes []Team}` для рассылки.
- `Sync(ctx)` — `ListMatches` → перезапись кэша с переносом `both_notified`;
  возвращает матчи, которые только что стали заполненными (`open`, обе
  команды, `both_notified = false`) — для пинга.
- `OpenMatchFor(ctx, teamID) (*BracketMatch, error)` — из кэша.
- `ReportResult(ctx, matchID, winnerTeamID, scoresCSV)` — PUT + Sync.
- `Forfeit(ctx, teamID)` — сценарии из жизненного цикла.
- `SetWinner(ctx, playOrder, teamName)` — для админа; возвращает список
  сброшенных матчей для уведомлений.
- `FlushPendingReports(ctx)` — досылает отчёты с `synced_at IS NULL`.

Маппинг счёта: капитан вводит `2:0` (парсер есть) → в Challonge уходит
`2-0`, где первое число — за `player1`. Если победитель — `player2`, счёт
переворачивается.

**`internal/application/telegram_report.go`** — при включённой сетке
`StartReport` пропускает `StateReportOpponent`: берёт `OpenMatchFor`,
кладёт соперника и `bracket_match_id` в черновик и сразу переходит в
`StateReportScore`. Если открытого матча нет — «У вашей команды сейчас нет
матча в сетке». `SubmitReport` после `CreateMatchReport` вызывает
`ReportResult`; при ошибке Challonge отчёт остаётся с `synced_at = NULL`,
капитану — «Отчёт принят, сетка обновится в течение минуты».

**`internal/delivery/telegram/bracket.go`** — команды `/bracket`,
`/build_bracket`, `/set_winner`; рассылки «сетка готова», «пара готова»,
«матч переигрывается». Хуки: автозакрытие регистрации → `Build`;
`processTechnicalDefeat` → `Forfeit` по каждой снятой команде; фоновый
воркер (тик в минуту) → `FlushPendingReports`, повтор `Build` при
`bracket_build_failures > 0`, отложенные `Forfeit`.

## Бюджет запросов

Турнир на N команд, Single Elimination, N−1 матчей:

| Действие | Запросов |
|---|---|
| Create + BulkAdd + Start + первый Sync | 4 |
| Отчёт (PUT) + Sync | 2 × (N−1) |
| ТП (входят в N−1 матчей, отдельный PUT + Sync) | — |
| `/set_winner` | 2 каждый |

N = 100 → ≈ 202. `/bracket`, `/report` без сабмита, пинги — 0.

## Ошибки

- **Challonge недоступен при `Build`.** Счётчик `bracket_build_failures++`,
  воркер повторяет раз в минуту. На 5-й неудаче — сообщение админам с
  подсказкой `/build_bracket`. Частично созданный турнир (есть
  `challonge_tournament_id`, но нет матчей) перед повтором удаляется.
- **Challonge недоступен при `/report`.** Отчёт сохранён, `synced_at = NULL`,
  воркер досылает. Капитан ничего не переделывает.
- **`429`.** Одно сообщение админам «квота Challonge исчерпана, нужен платный
  план», далее только лог. Отчёты копятся в очереди и уйдут, когда квота
  откроется.
- **Команда без `challonge_participant_id`** (зарегистрирована после
  построения — регистрация закрыта, так быть не должно) — `/report`
  отвечает «вашей команды нет в сетке», админу — лог.
- **`/set_winner` на матч в `pending`** — отказ: «матч ещё не сыгран».

## Тесты

- Посев: табличный тест на среднее звёзд, tie-break по id, исключение
  disqualified.
- `BracketService` на фейке `BracketProvider`: построение; отчёт и пинг
  следующей пары ровно один раз; ТП одной стороны; ТП обеих сторон
  проваливается до живой команды; `SetWinner` возвращает сброшенные матчи;
  очередь недосланных отчётов досылается.
- Клиент `internal/challonge` против `httptest.Server` с записанными
  ответами v2.1, включая `429 → ErrQuotaExceeded`.
- Репозиторий: `telegram_bracket_matches` в существующем
  `integration_test.go` — перезапись кэша сохраняет `both_notified`.
- Первая задача плана — spike: одним скриптом прогнать
  Create → BulkAdd → Start → ListMatches → Report → Delete против реального
  Challonge с тестовым ключом и зафиксировать формы ответов как фикстуры.

## Что не делаем

- Свой рендер сетки (картинка или веб-страница) — есть страница Challonge.
- Double Elimination, групповой этап, Bo3 по картам — только Single
  Elimination, счёт как строка.
- Подтверждение результата проигравшим или админом — победитель продвигается
  по отчёту, спор решает `/set_winner`.
- Несколько турниров одновременно — один активный, как и `tournament_time`.
