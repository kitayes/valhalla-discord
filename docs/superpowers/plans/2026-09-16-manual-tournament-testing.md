# Tournament Manual Test Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to run this checklist in order and record evidence after every scenario.

**Goal:** Вручную подтвердить полный турнирный цикл Telegram-бота: регистрация, check-in, посев, Challonge, Match Desk, отчёты, исправление результатов, восстановление после сбоев и итоговая целостность PostgreSQL.

**Architecture:** Проверка проводится на отдельной PostgreSQL-базе, отдельном Telegram-боте и тестовом турнире Challonge. Пользовательские действия выполняются в Telegram, а после каждого критического перехода состояние сверяется с локальной БД и Challonge.

**Tech Stack:** Go 1.24+, Telegram Bot API, PostgreSQL 16, Challonge API, `psql`, Docker.

**Spec:** `docs/superpowers/specs/2026-09-15-challonge-bracket-design.md`

## Global Constraints

- Не использовать production-токен Telegram, production-чат или рабочую БД.
- Использовать отдельный Challonge-турнир из четырёх команд, чтобы не расходовать месячную квоту на большую сетку.
- Не запускать два экземпляра бота с одним Telegram-токеном одновременно.
- Все команды боту отправлять в личном чате; групповые сообщения бот намеренно не обслуживает.
- Часовой пояс теста: `Asia/Almaty`.
- После каждого сценария сохранять сообщение бота, SQL-снимок и релевантные строки лога.

---

## 1. Подготовка стенда

### Участники

- Администратор: Telegram ID внесён в `TELEGRAM_ADMIN_IDS`.
- Капитан A: реальный тестовый Telegram-аккаунт.
- Капитан B: второй реальный тестовый Telegram-аккаунт.
- Посторонний пользователь: аккаунт без команды и административных прав.
- Две дополнительные полные команды можно добавить SQL-фикстурой; их капитанам разрешено не иметь `telegram_id`.

### Окружение

- [ ] Создать отдельного бота через BotFather и отдельный тестовый Telegram-чат.
- [ ] Создать отдельную БД `bw_telegram_manual`; не использовать `bw_telegram_fixture`, потому что её фейковые Telegram ID не принадлежат реальным аккаунтам.
- [ ] Применить все миграции запуском приложения или миграционным integration-тестом.

```bash
docker exec bw_telegram_fixture_db createdb -U postgres bw_telegram_manual
BW_MIGRATION_TEST_DSN='host=127.0.0.1 port=55432 user=postgres password=bwtest dbname=bw_telegram_manual sslmode=disable' \
  go test -tags integration ./internal/repository \
  -run TestIntegrationMigrationsFromScratch -count=1 -v
```

- [ ] Настроить переменные:

```text
REPO_DB_HOST=127.0.0.1
REPO_DB_PORT=55432
REPO_DB_USERNAME=postgres
REPO_DB_PASSWORD=bwtest
REPO_DB_NAME=bw_telegram_manual
REPO_DB_SSLMODE=disable
TELEGRAM_TOKEN=<test-bot-token>
TELEGRAM_ADMIN_IDS=<admin-telegram-id>
TELEGRAM_TOURNAMENT_CHAT_ID=<test-chat-id>
TOURNAMENT_TZ=Asia/Almaty
CHALLONGE_API_KEY=<test-key>
CHALLONGE_SUBDOMAIN=<test-subdomain-or-empty>
BRACKET_WALKOVER_SCORE=1-0
```

- [ ] Запустить `go run ./cmd/app` и убедиться, что в логе есть `Telegram bot started` и `Challonge bracket service initialized`.
- [ ] Проверить `/start` всеми аккаунтами; администратор должен увидеть ссылку на `/admin`, обычные пользователи — только главное меню.
- [ ] Отправить `/admin` посторонним пользователем; административные команды не должны выполниться.

### Базовый SQL-снимок

```sql
SELECT 'teams' entity, count(*) FROM telegram_teams
UNION ALL SELECT 'players', count(*) FROM telegram_players
UNION ALL SELECT 'matches', count(*) FROM telegram_bracket_matches
UNION ALL SELECT 'reports', count(*) FROM telegram_match_reports;
```

Ожидается четыре нуля.

---

## 2. Регистрация и составы

### MT-REG-01 — Полная команда капитана A

- [ ] Капитан A отправляет `/reg_team` и имя `Manual Alpha`.
- [ ] Для каждого из пяти игроков отправить блок:

```text
Alpha1
61000001 (1001)
80
```

- [ ] Для следующих игроков использовать уникальные GameID `61000002`…`61000005`; выбрать роли Gold, Exp, Mid, Roam и Jungle.
- [ ] На итоговой карточке проверить пять игроков и подтвердить регистрацию.
- [ ] Выполнить `/my_team`; команда должна иметь пять основных игроков, капитана и статус «Check-in: не пройден».

### MT-REG-02 — Вторая полная команда

- [ ] Капитан B регистрирует `Manual Beta` по той же схеме с GameID `62000001`…`62000005` и 40–50 звёздами.
- [ ] Проверить `/my_team` и редактирование одного игрока через кнопку его слота.
- [ ] Попробовать повторно использовать `61000002`; бот должен сообщить, что GameID уже заявлен другой командой.

### MT-REG-03 — Неполная команда-призрак

- [ ] Посторонний пользователь начинает `/reg_team`, создаёт `Manual Ghost`, вводит только капитана и одного игрока, затем нажимает отмену.
- [ ] Выполнить `/my_team`; должно отображаться `2/5`, команда должна сохраниться.
- [ ] Выполнить `/checkin`; бот должен отказать из-за неполного состава.

### MT-REG-04 — Две SQL-команды для сетки

- [ ] Добавить `Manual Gamma` и `Manual Delta`, по пять основных игроков в каждой:

```sql
WITH new_teams AS (
  INSERT INTO telegram_teams(name, status, is_checked_in)
  VALUES ('Manual Gamma', 'active', false),
         ('Manual Delta', 'active', false)
  RETURNING id, name
)
INSERT INTO telegram_players
  (telegram_id, telegram_username, first_name, game_nickname,
   game_id, zone_id, stars, main_role, is_captain,
   is_substitute, fsm_state, team_id)
SELECT NULL, NULL, 'Fixture',
       CASE WHEN t.name='Manual Gamma' THEN 'Gamma' ELSE 'Delta' END || s,
       CASE WHEN t.name='Manual Gamma' THEN '6300000' ELSE '6400000' END || s,
       CASE WHEN t.name='Manual Gamma' THEN '1003' ELSE '1004' END,
       CASE WHEN t.name='Manual Gamma' THEN 30+s ELSE 20+s END,
       (ARRAY['Gold','Exp','Mid','Roam','Jungle'])[s],
       s=1, false, '', t.id
FROM new_teams t
CROSS JOIN generate_series(1,5) AS s;
```

- [ ] Не назначать им вымышленные `telegram_id`: использовать `NULL`, чтобы бот не пытался писать случайным людям.
- [ ] Администратор выполняет `/list_teams`, `/check_team Manual Alpha` и `/checkin_status`.

Проверка БД:

```sql
SELECT t.id, t.name, t.status, t.is_checked_in,
       count(p.id) FILTER (WHERE NOT coalesce(p.is_substitute, false)) AS main_players,
       count(p.id) FILTER (WHERE p.is_captain) AS captains
FROM telegram_teams t
LEFT JOIN telegram_players p ON p.team_id=t.id
GROUP BY t.id
ORDER BY t.id;
```

Ожидается: четыре команды с `main_players=5`, `Manual Ghost` с `main_players=2`, ровно один капитан в каждой команде.

---

## 3. Регистрация, check-in и автоматическое расписание

### MT-SCH-01 — Закрытие регистрации и построение сетки

- [ ] Завершить создание всех команд до изменения времени.
- [ ] Администратор задаёт турнир примерно через 31 минуту: `/set_tourney ДД.ММ.ГГГГ ЧЧ:ММ`.
- [ ] Проверить, что ответ явно показывает зону `Asia/Almaty`, время закрытия, напоминания и техпоражения.
- [ ] Подождать не более двух минут работы минутного scheduler.
- [ ] Новый пользователь отправляет `/reg_team`; регистрация должна быть закрыта, потому что до старта меньше часа.
- [ ] Администратор и капитаны выполняют `/bracket`.

Ожидается:

- Создан один Challonge-турнир.
- В нём ровно четыре полные команды.
- `Manual Ghost` отсутствует и в Challonge, и среди `challonge_participant_id`.
- Администратор получил предупреждение о неполном основном составе.
- Повторный scheduler tick не создаёт второй турнир.

```sql
SELECT name, status, challonge_participant_id
FROM telegram_teams
ORDER BY id;

SELECT key, value
FROM telegram_settings
WHERE key LIKE 'challonge_%' OR key='bracket_build_failures'
ORDER BY key;
```

### MT-SCH-02 — Напоминание и check-in

- [ ] Дождаться ближайшего scheduler tick после момента T−30 минут.
- [ ] Капитаны A и B должны получить напоминание с кнопкой подтверждения.
- [ ] Нажать подтверждение A два раза: результат должен остаться `true`, а не переключиться обратно.
- [ ] B выполняет `/checkin`, затем повторяет `/checkin`; команда сначала подтверждается, затем снимает подтверждение. Ещё раз выполнить `/checkin`, чтобы вернуть `true`.
- [ ] Выполнить `/checkin_status`: A и B должны быть в подтверждённых; Ghost — в неполных.

### MT-SCH-03 — Техническое поражение

- [ ] Оставить `Manual Gamma` без check-in, а `Manual Delta` подтвердить тестовой SQL-командой, поскольку у её фикстурного капитана нет Telegram ID:

```sql
UPDATE telegram_teams SET is_checked_in=true WHERE name='Manual Delta';
```

- [ ] Для ускорения задать время турнира на девять минут в прошлом.
- [ ] Подождать до двух минут, чтобы наступил T+10 scheduler tick.
- [ ] Проверить сообщение администратора и турнирного чата о техпоражении Gamma.
- [ ] В Challonge открытый матч Gamma должен завершиться победой соперника `1-0`.
- [ ] Повторный scheduler tick не должен создавать второе техпоражение или повторно отправлять результат.
- [ ] Выполнить `/reinstate Manual Gamma`; статус должен стать `active`, check-in — `true`, а сетка должна согласованно восстановиться.

```sql
SELECT name, status, is_checked_in
FROM telegram_teams
WHERE name IN ('Manual Gamma','Manual Delta');
```

---

## 4. Match Desk

### MT-DESK-01 — Карточки матча и готовность

- [ ] Капитан открытой пары выполняет `/match`.
- [ ] Карточка содержит номер матча, обе команды, контакт и GameID капитана соперника.
- [ ] Первый капитан нажимает «Готовы»; второй получает обновлённую карточку.
- [ ] Второй капитан нажимает «Готовы»; статус становится «Обе команды готовы».
- [ ] Повторное нажатие старой кнопки возвращает сообщение об устаревшей карточке и не меняет состояние.

### MT-DESK-02 — Судья, пауза и история

- [ ] Капитан выполняет `/judge` и выбирает «Проблема с лобби».
- [ ] Администратор выполняет `/attention` и видит обращение ровно один раз.
- [ ] Проверить `/pause_match <номер>`, `/resume_match <номер>` и `/match_history <номер>`.
- [ ] Проверить `/pause_matches` и `/resume_matches`; локальная пауза не должна исчезать из-за снятия глобальной.
- [ ] Администратор закрывает обращение; `/attention` больше не показывает этот матч.

```sql
SELECT tournament_id,
       (SELECT count(*) FROM jsonb_object_keys(data->'Matches')) AS matches,
       jsonb_array_length(data->'Outbox') AS pending_notices,
       updated_at
FROM telegram_match_desks;
```

---

## 5. Отчёт капитана и продвижение по сетке

### MT-REP-01 — Успешный отчёт

- [ ] Победивший капитан открывает `/report`.
- [ ] Бот должен предложить только фактического соперника из открытого матча.
- [ ] Выбрать счёт `2:0`, отправить два тестовых изображения и подтвердить отчёт.
- [ ] Ответ должен содержать: матч закрыт, победитель проходит дальше.
- [ ] `/reports` показывает команды, счёт и два скриншота.
- [ ] `/bracket` показывает матч завершённым; если следующая пара определилась, оба капитана получают новое уведомление.

```sql
SELECT r.id, w.name winner, l.name loser, r.score,
       r.bracket_match_id, r.synced_at, cardinality(r.photo_file_ids) screenshots
FROM telegram_match_reports r
LEFT JOIN telegram_teams w ON w.id=r.winner_team_id
LEFT JOIN telegram_teams l ON l.id=r.loser_team_id
ORDER BY r.id;
```

Ожидается: `bracket_match_id IS NOT NULL`, `synced_at IS NOT NULL`, `screenshots=2`.

### MT-REP-02 — Валидация и повторные действия

- [ ] Попробовать отправить отчёт без изображения — бот должен отказать.
- [ ] Попробовать счёт `0:0`, отрицательные числа, текст и ничью — бот должен отказать.
- [ ] После принятого отчёта проигравший капитан запускает `/report`; завершённый матч больше не должен предлагаться, и конфликтующий повторный результат не должен попасть в Challonge.
- [ ] Повторно нажать кнопку отправки старого отчёта — сетка и число отчётов не должны измениться.

---

## 6. Исправление результата и защита от пересборки

### MT-ADM-01 — Запрет опасной пересборки

- [ ] После первого завершённого матча выполнить `/build_bracket`.
- [ ] Бот должен отказать: сыгранные матчи уже существуют.
- [ ] В Challonge не должно появиться нового турнира.

### MT-ADM-02 — `/set_winner` и каскадный rollback

- [ ] Довести турнир минимум до открывшегося следующего раунда.
- [ ] Исправить победителя раннего матча: `/set_winner <номер> <точное имя команды> 2-1`.
- [ ] Проверить, что старые downstream-матчи сброшены, затронутые капитаны уведомлены, а новая пара открылась.
- [ ] Старые Match Desk-кнопки должны стать недействительными.
- [ ] Снова выполнить `/bracket` и сравнить локальные пары с Challonge.

---

## 7. Сбои и восстановление

### MT-FAIL-01 — Имитация исчерпанной квоты без расходования API

- [ ] Перед новым отчётом открыть circuit breaker:

```sql
INSERT INTO telegram_settings(key,value)
VALUES ('challonge_quota_blocked_until',
        to_char(now() + interval '5 minutes', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
ON CONFLICT(key) DO UPDATE SET value=excluded.value;
```

- [ ] Отправить валидный `/report`.
- [ ] Бот должен принять отчёт локально и сообщить, что сетка обновится позже.
- [ ] У отчёта `synced_at` должен быть `NULL`; локальный матч остаётся открытым.
- [ ] Администратор получает одно предупреждение о квоте, без спама каждый tick.
- [ ] Очистить блокировку:

```sql
UPDATE telegram_settings
SET value=''
WHERE key='challonge_quota_blocked_until';
```

- [ ] Подождать до двух минут. Очередь должна уйти в Challonge, `synced_at` заполниться, следующий матч — открыться.

### MT-FAIL-02 — Рестарт с незавершённой очередью

- [ ] Повторить постановку отчёта в очередь, остановить бот и запустить снова.
- [ ] Убедиться, что FSM регистрации, сетка, Match Desk и отчёт сохранились.
- [ ] Снять блокировку и дождаться автоматической синхронизации.
- [ ] В Challonge должен быть один результат, а в БД — одна запись отчёта.

### MT-FAIL-03 — Модель split-brain

- [ ] После успешно записанного в Challonge результата остановить бот.
- [ ] Только на тестовой БД искусственно вернуть локальный матч в `open`, очистить `winner_id/scores_csv` и выставить связанному отчёту `synced_at=NULL`.
- [ ] Запустить бот и дождаться фоновой сверки.
- [ ] Бот должен прочитать уже завершённый remote match, не делать второй конфликтующий PUT, восстановить локальный cache и заполнить `synced_at`.
- [ ] Если remote winner отличается от отчёта, администратор должен получить конфликт, а автоматические повторы должны прекратиться.

---

## 8. Экспорт и итоговая целостность

### MT-EXP-01 — Выгрузки

- [ ] `/export` возвращает `teams.csv`; открыть файл и проверить UTF-8, заголовки, все составы, статусы и check-in.
- [ ] Если настроен Google Sheets, `/export_sheet` обновляет только листы `Teams` и `Matches`; остальные листы не меняются.
- [ ] `Manual Ghost` присутствует в списке регистраций, но не имеет этапа сетки.
- [ ] `/reports` совпадает с таблицей `telegram_match_reports`.

### MT-DB-01 — Финальные инварианты

```sql
SELECT
  (SELECT count(*) FROM telegram_players p
   LEFT JOIN telegram_teams t ON t.id=p.team_id
   WHERE p.team_id IS NOT NULL AND t.id IS NULL) AS orphan_players,
  (SELECT count(*) FROM telegram_match_reports r
   LEFT JOIN telegram_bracket_matches m ON m.id=r.bracket_match_id
   WHERE r.bracket_match_id IS NOT NULL AND m.id IS NULL) AS orphan_reports,
  (SELECT count(*)-count(DISTINCT telegram_id)
   FROM telegram_players WHERE telegram_id IS NOT NULL) AS duplicate_telegram_ids,
  (SELECT count(*) FROM telegram_match_reports
   WHERE bracket_match_id IS NOT NULL AND synced_at IS NULL) AS queued_reports;
```

Критерий: первые три значения равны нулю; `queued_reports=0` после восстановления связи.

```sql
SELECT m.play_order, m.round, m.state,
       a.name team1, b.name team2, w.name winner, m.scores_csv
FROM telegram_bracket_matches m
LEFT JOIN telegram_teams a ON a.id=m.team1_id
LEFT JOIN telegram_teams b ON b.id=m.team2_id
LEFT JOIN telegram_teams w ON w.id=m.winner_id
ORDER BY m.play_order;
```

Критерий завершённого турнира из четырёх команд: три матча `complete`, один чемпион, локальные победители и счёт совпадают с Challonge.

---

## 9. Критерии приёмки

- [ ] Неполная и дисквалифицированная команды не получают место в сетке.
- [ ] Повторные scheduler ticks, рестарты и старые кнопки не дублируют действия.
- [ ] Отчёт капитана закрывает матч и открывает следующую пару без `/set_winner`.
- [ ] Очередь отчётов переживает рестарт и синхронизируется после восстановления доступа.
- [ ] Техпоражение проводится один раз и согласованно отражается в Telegram, PostgreSQL и Challonge.
- [ ] Админское исправление сбрасывает зависимые матчи и инвалидирует старые карточки.
- [ ] В логах нет panic, SQL scan errors, бесконечных повторов и неожиданных HTTP 4xx/5xx.
- [ ] Финальные SQL-инварианты выполнены.

## 10. Форма протокола

Для каждого `MT-*` сохранить:

```text
Сценарий:
Время и зона:
Git commit:
Исполнитель/аккаунт:
Команда или кнопка:
Ожидалось:
Получено:
PASS/FAIL:
Telegram screenshot:
Challonge screenshot/link:
SQL snapshot:
Log excerpt:
Номер дефекта:
```

При первом FAIL остановить зависимые сценарии, сохранить БД и логи, затем продолжить только независимые проверки.
