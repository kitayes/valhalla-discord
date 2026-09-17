//go:build integration

package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/challonge"
	"blackwatch/internal/repository"
	"blackwatch/pkg/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/lib/pq"
)

type mockTelegramServer struct {
	mu           sync.Mutex
	messagesSent []tgbotapi.MessageConfig
	photosSent   []tgbotapi.PhotoConfig
	callbacks    []tgbotapi.CallbackConfig
	updates      []tgbotapi.Update
}

func newMockTelegramServer(t *testing.T) (*httptest.Server, *mockTelegramServer) {
	mock := &mockTelegramServer{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/getMe"):
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"id":12345678,"is_bot":true,"first_name":"ValhallaBot","username":"valhalla_test_bot"}}`)
		case strings.Contains(r.URL.Path, "/sendMessage"):
			var msg tgbotapi.MessageConfig
			_ = json.NewDecoder(r.Body).Decode(&msg)
			mock.messagesSent = append(mock.messagesSent, msg)
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":100,"type":"private"}}}`)
		case strings.Contains(r.URL.Path, "/sendPhoto"):
			var photo tgbotapi.PhotoConfig
			_ = json.NewDecoder(r.Body).Decode(&photo)
			mock.photosSent = append(mock.photosSent, photo)
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":2,"chat":{"id":100,"type":"private"}}}`)
		case strings.Contains(r.URL.Path, "/sendMediaGroup"):
			_, _ = fmt.Fprint(w, `{"ok":true,"result":[{"message_id":3,"chat":{"id":100,"type":"private"}}]}`)
		case strings.Contains(r.URL.Path, "/answerCallbackQuery"):
			var cb tgbotapi.CallbackConfig
			_ = json.NewDecoder(r.Body).Decode(&cb)
			mock.callbacks = append(mock.callbacks, cb)
			_, _ = fmt.Fprint(w, `{"ok":true,"result":true}`)
		case strings.Contains(r.URL.Path, "/getChat"):
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"id":-1001234567890,"title":"Tournament Chat","type":"supergroup"}}`)
		case strings.Contains(r.URL.Path, "/getUpdates"):
			_, _ = fmt.Fprint(w, `{"ok":true,"result":[]}`)
		default:
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{}}`)
		}
	})
	server := httptest.NewServer(handler)
	return server, mock
}

type syntheticChallonge struct {
	mu           sync.Mutex
	nextID       int64
	participants []challonge.Participant
	matches      []challonge.Match
	reported     map[int64]string
	failErr      error
}

func newSyntheticChallonge() *syntheticChallonge {
	return &syntheticChallonge{
		nextID:   100,
		reported: make(map[int64]string),
	}
}

func (s *syntheticChallonge) CreateTournament(_ context.Context, name, slug string) (challonge.Tournament, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return challonge.Tournament{}, s.failErr
	}
	s.nextID++
	return challonge.Tournament{ID: s.nextID, Slug: slug, URL: "https://challonge.com/" + slug}, nil
}

func (s *syntheticChallonge) BulkAddParticipants(_ context.Context, tournamentID int64, ps []challonge.NewParticipant) ([]challonge.Participant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return nil, s.failErr
	}
	var res []challonge.Participant
	for _, p := range ps {
		s.nextID++
		part := challonge.Participant{ID: s.nextID, Name: p.Name, Seed: p.Seed}
		s.participants = append(s.participants, part)
		res = append(res, part)
	}
	return res, nil
}

func (s *syntheticChallonge) Start(_ context.Context, tournamentID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return s.failErr
	}
	if len(s.participants) >= 2 {
		p1 := s.participants[0].ID
		p2 := s.participants[1].ID
		s.matches = []challonge.Match{
			{
				ID:        s.nextID + 1,
				Round:     1,
				PlayOrder: 1,
				State:     "open",
				Player1ID: p1,
				Player2ID: p2,
			},
		}
	}
	return nil
}

func (s *syntheticChallonge) ListMatches(_ context.Context, tournamentID int64) ([]challonge.Match, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return nil, s.failErr
	}
	return s.matches, nil
}

func (s *syntheticChallonge) ListOpenMatches(_ context.Context, tournamentID int64) ([]challonge.Match, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return nil, s.failErr
	}
	var open []challonge.Match
	for _, m := range s.matches {
		if m.State == "open" {
			open = append(open, m)
		}
	}
	return open, nil
}

func (s *syntheticChallonge) GetMatch(_ context.Context, tournamentID, matchID int64) (challonge.Match, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return challonge.Match{}, s.failErr
	}
	for _, m := range s.matches {
		if m.ID == matchID {
			return m, nil
		}
	}
	return challonge.Match{}, errors.New("match not found")
}

func (s *syntheticChallonge) ReportMatch(_ context.Context, tournamentID, matchID, winnerPID, loserPID int64, winnerScore, loserScore int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return s.failErr
	}
	for i, m := range s.matches {
		if m.ID == matchID {
			s.matches[i].State = "complete"
			s.matches[i].WinnerID = winnerPID
			s.matches[i].Scores = fmt.Sprintf("%d-%d", winnerScore, loserScore)
			s.reported[matchID] = s.matches[i].Scores
			return nil
		}
	}
	return errors.New("match not found")
}

func (s *syntheticChallonge) ReopenMatch(_ context.Context, tournamentID, matchID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.matches {
		if m.ID == matchID {
			s.matches[i].State = "open"
			s.matches[i].WinnerID = 0
			s.matches[i].Scores = ""
			delete(s.reported, matchID)
			return nil
		}
	}
	return errors.New("match not found")
}

func (s *syntheticChallonge) DeleteTournament(_ context.Context, tournamentID int64) error {
	return nil
}

func testPostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BW_TEST_DSN")
	if dsn == "" {
		t.Skip("BW_TEST_DSN not set; skipping tournament synthetic integration test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("failed to ping postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestSyntheticTournamentBotStartup validates that the bot startup, config,
// database wiring, background workers, bracket checks, match desk, and shutdown
// work cleanly without panics, nil pointer dereferences or silent deadlocks.
func TestSyntheticTournamentBotStartup(t *testing.T) {
	db := testPostgresDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Clean up tournament test tables in a clean namespace
	_, _ = db.Exec(`DELETE FROM telegram_match_desks`)
	_, _ = db.Exec(`DELETE FROM telegram_bracket_matches`)
	_, _ = db.Exec(`DELETE FROM telegram_match_reports`)
	_, _ = db.Exec(`DELETE FROM telegram_players
		WHERE team_id IN (SELECT id FROM telegram_teams WHERE name IN ('Alpha Team', 'Beta Team', 'Gamma Team'))
		   OR telegram_id IN (1001, 1002, 1003, 1004, 1005, 2001, 2002, 2003, 2004, 2005, 3001, 3002, 3003, 3004, 3005, 9999)`)
	_, _ = db.Exec(`DELETE FROM telegram_teams WHERE name IN ('Alpha Team', 'Beta Team', 'Gamma Team')`)
	_, _ = db.Exec(`DELETE FROM telegram_settings WHERE key IN ('tournament_time', 'challonge_tournament_id', 'challonge_tournament_url', 'challonge_tournament_for', 'challonge_quota_blocked_until')`)
	var leftovers int
	if err := db.QueryRow(`SELECT count(*) FROM telegram_teams WHERE name IN ('Alpha Team', 'Beta Team', 'Gamma Team')`).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Fatalf("synthetic cleanup left %d tournament teams", leftovers)
	}

	server, _ := newMockTelegramServer(t)
	defer server.Close()

	api, err := tgbotapi.NewBotAPIWithClient("synthetic-token", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatalf("failed to create BotAPI: %v", err)
	}

	logger := &captureLogger{}
	telegramRepo := repository.NewTelegramPostgres(db)
	profileLinkRepo := repository.NewProfileLinkPostgres(db)

	telegramService := application.NewTelegramServiceImpl(telegramRepo, logger).WithProfileLookup(profileLinkRepo)
	challongeMock := newSyntheticChallonge()
	bracketService := application.NewBracketService(telegramRepo, challongeMock, 1, 0, logger)
	telegramService.WithBracket(bracketService)

	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		loc = time.UTC
	}
	adminIDs := []int64{9999}
	deskService := application.NewMatchDeskService(telegramRepo, adminIDs, loc)

	adminMap := map[int64]struct{}{9999: {}}
	bot := &Bot{
		bot:              api,
		service:          telegramService,
		bracket:          bracketService,
		logger:           logger,
		adminIDs:         adminMap,
		tournamentChatID: "-1001234567890",
		location:         loc,
		photoTimers:      make(map[int64]*time.Timer),
	}
	bot.WithMatchDesk(deskService)

	t.Run("0. Validate Tournament Config Bounds and Defaults", func(t *testing.T) {
		cfg := config.Config{
			DiscordToken:    "dummy-dc",
			GuildID:         "dummy-guild",
			GeminiKey:       "dummy-gemini",
			AdminUserIDs:    []string{"admin1"},
			TelegramToken:   "dummy-tg",
			TournamentTZ:    "Invalid/Timezone",
			BetAmounts:      []int{10, 20},
			BetMax:          100,
			HTTPTimeoutSec:  10,
			PlayerCacheSize: 1000,
		}
		if err := cfg.Validate(); err == nil {
			t.Error("expected config validation to fail on invalid TOURNAMENT_TZ")
		}

		cfg.TournamentTZ = "Asia/Almaty"
		cfg.BracketWalkoverScore = "invalid-score"
		if err := cfg.Validate(); err == nil {
			t.Error("expected config validation to fail on invalid BRACKET_WALKOVER_SCORE")
		}

		cfg.BracketWalkoverScore = "1-0"
		cfg.BetAmounts = []int{150}
		cfg.BetMax = 100
		if err := cfg.Validate(); err == nil {
			t.Error("expected config validation to fail when BetAmounts > BetMax")
		}

		cfg.BetAmounts = []int{10, 50}
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected valid config to pass, got: %v", err)
		}
	})

	t.Run("1. Validate Tournament Date Parsing", func(t *testing.T) {
		parsed, summary, err := bot.parseTournamentTime("25.12.2026 18:00")
		if err != nil {
			t.Fatalf("failed to parse tournament time: %v", err)
		}
		if parsed.Hour() != 18 || parsed.Day() != 25 {
			t.Errorf("unexpected parsed time: %v", parsed)
		}
		if !strings.Contains(summary, "25.12.2026 18:00") {
			t.Errorf("summary missing expected date: %s", summary)
		}

		// Invalid format must return error, not panic
		_, _, err = bot.parseTournamentTime("invalid-date")
		if err == nil {
			t.Error("expected error for invalid date, got nil")
		}
	})

	t.Run("2. Scheduled Checks and MatchDesk on Clean State", func(t *testing.T) {
		// Run scheduled checks when no tournament is set: must not panic or error
		bot.runScheduledChecks(ctx)

		// Run flushMatchDesk when bracket is not yet built: must gracefully return
		bot.flushMatchDesk(ctx)
	})

	t.Run("3. Admin Sets Tournament Time", func(t *testing.T) {
		bot.handleAdminCommand(ctx, 9999, "/set_tourney 25.12.2026 18:00")
		tTime := telegramService.GetTournamentTime(ctx)
		if tTime.IsZero() {
			t.Fatal("tournament time was not stored in settings")
		}
	})

	t.Run("4. Teams Registration Lifecycle", func(t *testing.T) {
		// Register Captain A (1001) and Team Alpha
		_ = telegramService.RegisterUser(ctx, 1001, "captain_alpha", "Alice")
		resp, _ := telegramService.StartTeamRegistration(ctx, 1001)
		if !strings.Contains(resp, "название") {
			t.Fatalf("unexpected StartTeamRegistration response: %s", resp)
		}
		resp, _ = telegramService.HandleUserInput(ctx, 1001, "Alpha Team")
		if !strings.Contains(resp, "создана") {
			t.Fatalf("expected team created response, got: %s", resp)
		}
		_, _ = telegramService.HandleUserInput(ctx, 1001, "AliceNick 123456 (1001) 50")
		_, _ = telegramService.HandleRegAction(ctx, 1001, "role", "Mid")

		// Add 4 teammates to Team Alpha (slots 2..5)
		for slot := 2; slot <= 5; slot++ {
			pID := int64(1000 + slot)
			_, _ = telegramService.HandleUserInput(ctx, 1001, fmt.Sprintf("AliceMate%d %d (100%d) 25 @alicemate%d", slot, 110000+slot, slot, slot))
			_, _ = telegramService.HandleRegAction(ctx, 1001, "role", "Roam")
			// simulate linking teammate telegram ID
			p, _ := telegramRepo.GetPlayerByTelegramID(ctx, 1001)
			if p != nil && p.TeamID != nil {
				members, _ := telegramRepo.GetTeamMembers(ctx, *p.TeamID)
				for _, m := range members {
					if m.GameNickname == fmt.Sprintf("AliceMate%d", slot) {
						_ = telegramRepo.UpdatePlayerFieldByID(ctx, m.ID, "telegram_id", pID)
					}
				}
			}
		}
		// Confirm registration
		resp, _ = telegramService.HandleRegAction(ctx, 1001, "confirm", "")
		if !strings.Contains(resp, "зарегистрирована") {
			t.Fatalf("unexpected team confirmation response: %s", resp)
		}

		// Register Captain B (2001) and Team Beta
		_ = telegramService.RegisterUser(ctx, 2001, "captain_beta", "Bob")
		_, _ = telegramService.StartTeamRegistration(ctx, 2001)
		_, _ = telegramService.HandleUserInput(ctx, 2001, "Beta Team")
		_, _ = telegramService.HandleUserInput(ctx, 2001, "BobNick 654321 (2001) 60")
		_, _ = telegramService.HandleRegAction(ctx, 2001, "role", "Exp")

		// Add 4 teammates to Team Beta (slots 2..5)
		for slot := 2; slot <= 5; slot++ {
			pID := int64(2000 + slot)
			_, _ = telegramService.HandleUserInput(ctx, 2001, fmt.Sprintf("BobMate%d %d (200%d) 30 @bobmate%d", slot, 220000+slot, slot, slot))
			_, _ = telegramService.HandleRegAction(ctx, 2001, "role", "Gold")
			p, _ := telegramRepo.GetPlayerByTelegramID(ctx, 2001)
			if p != nil && p.TeamID != nil {
				members, _ := telegramRepo.GetTeamMembers(ctx, *p.TeamID)
				for _, m := range members {
					if m.GameNickname == fmt.Sprintf("BobMate%d", slot) {
						_ = telegramRepo.UpdatePlayerFieldByID(ctx, m.ID, "telegram_id", pID)
					}
				}
			}
		}
		resp, _ = telegramService.HandleRegAction(ctx, 2001, "confirm", "")
		if !strings.Contains(resp, "зарегистрирована") {
			t.Fatalf("unexpected team confirmation response for Beta: %s", resp)
		}
	})

	t.Run("5. Check-in and Debtors Workflow", func(t *testing.T) {
		// Team Alpha confirms checkin
		bot.handleUserCommand(ctx, 1001, "/checkin", "captain_alpha")

		// Checkin status should show 1 checked in, 1 pending
		status := telegramService.GetCheckInStatus(ctx)
		if !strings.Contains(status, "Alpha Team") {
			t.Errorf("checkin status missing Alpha Team: %s", status)
		}

		// Admin pings debtors (Team Beta hasn't checked in)
		bot.handleAdminCommand(ctx, 9999, "/ping_debtors Срочно чекин!")
	})

	t.Run("6. Build Bracket and MatchDesk Routing", func(t *testing.T) {
		// Team Beta also checks in so bracket can be built
		bot.handleUserCommand(ctx, 2001, "/checkin", "captain_beta")

		// Admin builds bracket
		tTime := telegramService.GetTournamentTime(ctx)
		bot.buildBracket(ctx, tTime, 9999)

		ms, err := bracketService.Matches(ctx)
		if err != nil || len(ms) == 0 {
			t.Fatalf("bracket matches empty after build: %v", err)
		}

		// MatchDesk should pick up matches and create cards
		bot.flushMatchDesk(ctx)

		// Captain Alpha checks /match
		bot.handleDeskCommand(ctx, 1001, "/match")

		// Captain Alpha signals Ready
		mID := ms[0].ID
		gen := int64(1)
		bot.handleCallbackQuery(ctx, &tgbotapi.CallbackQuery{
			ID:   "cb-ready",
			From: &tgbotapi.User{ID: 1001},
			Data: fmt.Sprintf("desk:ready:%d:%d", mID, gen),
		})

		// Captain Beta calls referee
		bot.handleCallbackQuery(ctx, &tgbotapi.CallbackQuery{
			ID:   "cb-judge",
			From: &tgbotapi.User{ID: 2001},
			Data: fmt.Sprintf("desk:judge_lobby:%d:%d", mID, gen),
		})

		// Admin checks /attention
		bot.handleDeskCommand(ctx, 9999, "/attention")

		// Admin pauses match
		bot.handleDeskCommand(ctx, 9999, fmt.Sprintf("/pause_match %d", ms[0].PlayOrder))

		// Admin resumes match
		bot.handleDeskCommand(ctx, 9999, fmt.Sprintf("/resume_match %d", ms[0].PlayOrder))
	})

	t.Run("7. Match Report Submission and Result Sync", func(t *testing.T) {
		// Captain Alpha starts report
		bot.handleUserCommand(ctx, 1001, "/report", "captain_alpha")

		// Selects opponent Beta Team
		pBeta, _ := telegramRepo.GetPlayerByTelegramID(ctx, 2001)
		if pBeta != nil && pBeta.TeamID != nil {
			bot.handleReportCallback(ctx, &tgbotapi.CallbackQuery{
				ID:   "rep-opp",
				From: &tgbotapi.User{ID: 1001},
				Data: fmt.Sprintf("rep:opp:%d", *pBeta.TeamID),
			})
		}

		// Selects score 2:0
		bot.handleReportCallback(ctx, &tgbotapi.CallbackQuery{
			ID:   "rep-score",
			From: &tgbotapi.User{ID: 1001},
			Data: "rep:score:2:0",
		})

		// Adds photo
		_, _, _ = telegramService.AddReportPhoto(ctx, 1001, "mock-photo-file-id")

		// Submits report
		bot.handleReportCallback(ctx, &tgbotapi.CallbackQuery{
			ID:   "rep-submit",
			From: &tgbotapi.User{ID: 1001},
			Data: "rep:submit",
		})
	})

	t.Run("8. Fault Injection and Error Recovery", func(t *testing.T) {
		// 1. Technical Defeat Sweep for Unchecked Teams
		_ = telegramService.RegisterUser(ctx, 3001, "captain_gamma", "Charlie")
		_, _ = telegramService.StartTeamRegistration(ctx, 3001)
		_, _ = telegramService.HandleUserInput(ctx, 3001, "Gamma Team")
		_, _ = telegramService.HandleUserInput(ctx, 3001, "CharlieNick 777777 (3001) 40")
		_, _ = telegramService.HandleRegAction(ctx, 3001, "role", "Mid")
		for slot := 2; slot <= 5; slot++ {
			_, _ = telegramService.HandleUserInput(ctx, 3001, fmt.Sprintf("GammaMate%d %d (300%d) 20", slot, 330000+slot, slot))
			_, _ = telegramService.HandleRegAction(ctx, 3001, "role", "Roam")
		}
		_, _ = telegramService.HandleRegAction(ctx, 3001, "confirm", "")

		// Team Gamma never checks in -> processTechnicalDefeat
		bot.processTechnicalDefeat(ctx)

		pGamma, _ := telegramRepo.GetPlayerByTelegramID(ctx, 3001)
		if pGamma != nil && pGamma.TeamID != nil {
			tGamma, _ := telegramRepo.GetTeamByID(ctx, *pGamma.TeamID)
			if tGamma == nil || tGamma.Status != "disqualified" {
				t.Errorf("expected Gamma Team to be disqualified, got status: %v", tGamma)
			}
		}

		// 2. Challonge Quota Limit (429) Handling
		bracketService.RecordAPIError(ctx, challonge.ErrQuotaExceeded)

		tTime := telegramService.GetTournamentTime(ctx)
		bot.quotaAlerted = false
		bot.runBracketChecks(ctx, tTime, time.Now())
		if !bot.quotaAlerted {
			t.Error("expected quotaAlerted to be true after 429 quota error")
		}

		// 3. Robustness against malformed callbacks & commands
		malformedQueries := []string{
			"desk:bogus",
			"desk:ready:invalid:invalid",
			"desk:ready:1:999999", // stale generation
			"rep:bogus",
			"reg:bogus",
			"reg:role:NonExistentRole",
			"admin_ping_debtors",
		}
		for _, q := range malformedQueries {
			bot.handleCallbackQuery(ctx, &tgbotapi.CallbackQuery{
				ID:   "cb-err",
				From: &tgbotapi.User{ID: 1001},
				Data: q,
			})
		}

		malformedCommands := []string{
			"/reset_user notanumber",
			"/pause_match notanumber",
			"/match_history notanumber",
			"/del_team NonExistentTeam",
			"/edit_player",
			"/edit_player 99",
			"/set_winner",
			"/set_winner 999 NonExistentTeam 1:2",
			"/unknown_slash_command",
		}
		for _, cmd := range malformedCommands {
			bot.handleAdminCommand(ctx, 9999, cmd)
			bot.handleUserCommand(ctx, 1001, cmd, "captain_alpha")
		}
	})

	t.Run("9. Background Workers and Graceful Shutdown", func(t *testing.T) {
		workerCtx, workerCancel := context.WithCancel(context.Background())

		// Start both workers in goroutines
		go bot.startBackgroundWorker(workerCtx)
		go bot.startMatchDeskWorker(workerCtx)

		// Let workers tick once
		time.Sleep(100 * time.Millisecond)

		// Cancel context and stop bot
		workerCancel()
		bot.Stop()
	})
}
