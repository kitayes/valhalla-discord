package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/challonge"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"blackwatch/migrations"

	_ "github.com/lib/pq"
)

type mockLogger struct{}

func (m mockLogger) Info(msg string, args ...interface{})  { log.Printf("[INFO] "+msg, args...) }
func (m mockLogger) Error(msg string, args ...interface{}) { log.Printf("[ERROR] "+msg, args...) }
func (m mockLogger) Debug(msg string, args ...interface{}) { log.Printf("[DEBUG] "+msg, args...) }
func (m mockLogger) Warn(msg string, args ...interface{})  { log.Printf("[WARN] "+msg, args...) }

// Mock Sheets Client
type mockSheetsClient struct {
	updatedValues map[string][][]interface{}
}

func newMockSheetsClient() *mockSheetsClient {
	return &mockSheetsClient{updatedValues: make(map[string][][]interface{})}
}

func (m *mockSheetsClient) CreateSpreadsheet(title string) (string, string, error) {
	return "mock_sheet_id", "https://mock.sheet", nil
}
func (m *mockSheetsClient) AddPermission(id, email, role string) error { return nil }
func (m *mockSheetsClient) MakePublic(id string) error                 { return nil }
func (m *mockSheetsClient) EnsureSheet(id, title string) error         { return nil }
func (m *mockSheetsClient) ClearRange(id, rangeStr string) error       { return nil }
func (m *mockSheetsClient) UpdateValues(id, rangeStr string, vals [][]interface{}) error {
	m.updatedValues[rangeStr] = vals
	return nil
}

// In-memory bracket provider satisfying application.BracketProvider
type memBracketProvider struct {
	tournaments map[int64]challonge.Tournament
	matches     map[int64][]challonge.Match
	nextID      int64
	reopened    []int64
}

func newMemBracketProvider() *memBracketProvider {
	return &memBracketProvider{
		tournaments: make(map[int64]challonge.Tournament),
		matches:     make(map[int64][]challonge.Match),
		nextID:      100,
	}
}

func (p *memBracketProvider) CreateTournament(ctx context.Context, name, slug string) (challonge.Tournament, error) {
	p.nextID++
	t := challonge.Tournament{
		ID:   p.nextID,
		Slug: slug,
		URL:  "https://challonge.com/" + slug,
	}
	p.tournaments[t.ID] = t
	return t, nil
}

func (p *memBracketProvider) BulkAddParticipants(ctx context.Context, tID int64, ps []challonge.NewParticipant) ([]challonge.Participant, error) {
	var out []challonge.Participant
	for i, np := range ps {
		pid := int64(1000 + i + 1)
		out = append(out, challonge.Participant{ID: pid, Name: np.Name, Seed: np.Seed})
	}
	return out, nil
}

func (p *memBracketProvider) Start(ctx context.Context, tID int64) error {
	return nil
}

func (p *memBracketProvider) ListMatches(ctx context.Context, tID int64) ([]challonge.Match, error) {
	return p.matches[tID], nil
}

func (p *memBracketProvider) ListOpenMatches(ctx context.Context, tID int64) ([]challonge.Match, error) {
	var open []challonge.Match
	for _, m := range p.matches[tID] {
		if m.State == "open" {
			open = append(open, m)
		}
	}
	return open, nil
}

func (p *memBracketProvider) GetMatch(ctx context.Context, tID, matchID int64) (challonge.Match, error) {
	for _, m := range p.matches[tID] {
		if m.ID == matchID {
			return m, nil
		}
	}
	return challonge.Match{}, fmt.Errorf("match %d not found", matchID)
}

func (p *memBracketProvider) ReportMatch(ctx context.Context, tID, matchID, winnerPID, loserPID int64, wScore, lScore int) error {
	ms := p.matches[tID]
	for i := range ms {
		if ms[i].ID == matchID {
			ms[i].State = "complete"
			ms[i].WinnerID = winnerPID
			ms[i].Scores = fmt.Sprintf("%d-%d", wScore, lScore)
			p.matches[tID] = ms
			return nil
		}
	}
	return fmt.Errorf("match %d not found", matchID)
}

func (p *memBracketProvider) ReopenMatch(ctx context.Context, tID, matchID int64) error {
	p.reopened = append(p.reopened, matchID)
	ms := p.matches[tID]
	for i := range ms {
		if ms[i].ID == matchID {
			ms[i].State = "open"
			ms[i].WinnerID = 0
			p.matches[tID] = ms
			return nil
		}
	}
	return fmt.Errorf("match %d not found", matchID)
}

func (p *memBracketProvider) DeleteTournament(ctx context.Context, tID int64) error {
	delete(p.tournaments, tID)
	delete(p.matches, tID)
	return nil
}

type bettingRepoAdapter struct {
	*repository.BetPostgres
	*repository.LobbyMatchPostgres
}

func main() {
	ctx := context.Background()
	dsn := "host=127.0.0.1 port=5435 user=postgres password=valhalla dbname=valhalla_db sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("db.Ping: %v", err)
	}
	log.Println("[PASS] Connected to PostgreSQL on localhost:5435 (valhalla_db)")

	// 1. Run migrations
	if err := repository.RunMigrations(db, migrations.FS); err != nil {
		log.Fatalf("RunMigrations: %v", err)
	}
	log.Println("[PASS] Database migrations applied successfully")

	// 2. Clean previous test data
	cleanQuery := `
		TRUNCATE TABLE
			telegram_match_desks,
			telegram_match_reports,
			telegram_bracket_matches,
			telegram_players,
			telegram_teams,
			telegram_settings,
			match_bets,
			lobby_matches,
			players
		CASCADE;
	`
	if _, err := db.ExecContext(ctx, cleanQuery); err != nil {
		log.Fatalf("Clean database: %v", err)
	}
	log.Println("[PASS] Cleaned tables for test run")

	logger := mockLogger{}
	tgRepo := repository.NewTelegramPostgres(db)
	betRepo := repository.NewBetPostgres(db)
	lobbyRepo := repository.NewLobbyMatchPostgres(db)

	// 3. Seed tournament time (2 hours in the future)
	tourneyTime := time.Now().Add(2 * time.Hour).Truncate(time.Minute)
	if err := tgRepo.SetSetting(ctx, "tournament_time", tourneyTime.UTC().Format(time.RFC3339)); err != nil {
		log.Fatalf("Set tournament_time: %v", err)
	}
	log.Printf("[PASS] Seeded tournament_time = %s", tourneyTime.Format("2006-01-02 15:04"))

	// 4. Seed 6 Teams
	type teamSeed struct {
		name      string
		checkedIn bool
		status    string
		starList  []int
	}

	teamsToSeed := []teamSeed{
		{name: "Team Spirit", checkedIn: true, status: "active", starList: []int{50, 45, 40, 35, 30}},   // avg 40
		{name: "Natus Vincere", checkedIn: true, status: "active", starList: []int{45, 40, 35, 30, 25}}, // avg 35
		{name: "Virtus.pro", checkedIn: false, status: "active", starList: []int{40, 35, 30, 25, 20}},   // avg 30 (unchecked)
		{name: "Team Liquid", checkedIn: true, status: "active", starList: []int{35, 30, 25, 20, 15}},   // avg 25
		{name: "Incomplete Squad", checkedIn: false, status: "active", starList: []int{20, 20, 20}},     // 3 players (< 5)
		{name: "Disqualified Five", checkedIn: false, status: "disqualified", starList: []int{30, 30, 30, 30, 30}},
	}

	roles := []string{"Gold", "Exp", "Mid", "Roam", "Jungle"}
	var captainTgIDs []int64

	for tIdx, ts := range teamsToSeed {
		team, err := tgRepo.CreateTeam(ctx, ts.name)
		if err != nil {
			log.Fatalf("CreateTeam %s: %v", ts.name, err)
		}
		teamID := team.ID

		if ts.status != "active" {
			_ = tgRepo.SetTeamStatus(ctx, teamID, ts.status)
		}
		if ts.checkedIn {
			_ = tgRepo.SetCheckIn(ctx, teamID, true)
		}

		// Insert players
		for pIdx, stars := range ts.starList {
			tgID := int64(100000 + (tIdx+1)*100 + pIdx + 1)
			isCap := (pIdx == 0)
			if isCap {
				captainTgIDs = append(captainTgIDs, tgID)
			}
			role := roles[pIdx%len(roles)]
			nick := fmt.Sprintf("%s_P%d", strings.ReplaceAll(ts.name, " ", ""), pIdx+1)
			gameID := fmt.Sprintf("%d%04d", tIdx+1, pIdx+1)
			zoneID := fmt.Sprintf("%04d", 1000+tIdx)

			p := models.TelegramPlayer{
				TelegramID:       &tgID,
				TelegramUsername: fmt.Sprintf("tg_user_%d", tgID),
				GameNickname:     nick,
				GameID:           gameID,
				ZoneID:           zoneID,
				MainRole:         role,
				Stars:            stars,
				IsCaptain:        isCap,
				TeamID:           &teamID,
			}
			if isCap {
				_, err := db.ExecContext(ctx, `
					INSERT INTO telegram_players (telegram_id, telegram_username, game_nickname, game_id, zone_id, stars, main_role, is_captain, is_substitute, team_id)
					VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, FALSE, $8)
				`, tgID, p.TelegramUsername, p.GameNickname, p.GameID, p.ZoneID, p.Stars, p.MainRole, teamID)
				if err != nil {
					log.Fatalf("Insert captain %s: %v", nick, err)
				}
			} else {
				if err := tgRepo.CreateTeammate(ctx, &p); err != nil {
					log.Fatalf("CreateTeammate %s: %v", nick, err)
				}
			}
		}
	}
	log.Printf("[PASS] Seeded %d teams with rosters and captains into valhalla_db", len(teamsToSeed))

	// 5. Initialize application service
	tgService := application.NewTelegramServiceImpl(tgRepo, logger)

	// =========================================================================
	// TEST CASE 1: Check-in dashboard & Debtors
	// =========================================================================
	dash := tgService.GetCheckInStatus(ctx)
	if !strings.Contains(dash, "Team Spirit") || !strings.Contains(dash, "Virtus.pro") {
		log.Fatalf("[FAIL TC1] Dashboard incorrect:\n%s", dash)
	}
	unchecked, err := tgService.GetUncheckedTeams(ctx)
	if err != nil || len(unchecked) == 0 {
		log.Fatalf("[FAIL TC1] Expected unchecked teams, got %d (err: %v)", len(unchecked), err)
	}
	log.Printf("[PASS TC1] Check-in dashboard verified: %d unchecked teams found", len(unchecked))

	// =========================================================================
	// TEST CASE 2: Team Roster Verification & Seeding
	// =========================================================================
	allTeams, err := tgRepo.GetAllTeams(ctx)
	if err != nil {
		log.Fatalf("[FAIL TC2] GetAllTeams: %v", err)
	}
	seeded := application.SeedTeams(allTeams)
	if len(seeded) != 4 {
		log.Fatalf("[FAIL TC2] Expected 4 seeded teams, got %d", len(seeded))
	}
	if seeded[0].Team.Name != "Team Spirit" || seeded[3].Team.Name != "Team Liquid" {
		log.Fatalf("[FAIL TC2] Seeding order incorrect: 1st=%s, 4th=%s", seeded[0].Team.Name, seeded[3].Team.Name)
	}
	log.Printf("[PASS TC2] Team seeding correctly ordered by avg stars (top seed: %s, 4th: %s)", seeded[0].Team.Name, seeded[3].Team.Name)

	// =========================================================================
	// TEST CASE 3: Bracket Build & Challonge Synchronization
	// =========================================================================
	provider := newMemBracketProvider()
	bracketSvc := application.NewBracketService(tgRepo, provider, 1, 0, logger)

	p1 := int64(1001)
	p2 := int64(1004)
	p3 := int64(1002)
	p4 := int64(1003)
	provider.matches[101] = []challonge.Match{
		{ID: 101, Round: 1, PlayOrder: 1, State: "open", Player1ID: p1, Player2ID: p2},
		{ID: 102, Round: 1, PlayOrder: 2, State: "open", Player1ID: p3, Player2ID: p4},
		{ID: 103, Round: 2, PlayOrder: 3, State: "pending"},
	}

	built, err := bracketSvc.Build(ctx, tourneyTime)
	if err != nil {
		log.Fatalf("[FAIL TC3] Bracket Build failed: %v", err)
	}
	if len(built.Round1) != 2 {
		log.Fatalf("[FAIL TC3] Expected 2 matches in round 1, got %d", len(built.Round1))
	}

	cachedMatches, err := tgRepo.GetBracketMatches(ctx)
	if err != nil || len(cachedMatches) != 3 {
		log.Fatalf("[FAIL TC3] Expected 3 cached bracket matches in DB, got %d", len(cachedMatches))
	}
	if cachedMatches[0].Team1Name == "" || cachedMatches[0].Team2Name == "" {
		log.Fatalf("[FAIL TC3] Team names not populated via JOIN in cached match: %+v", cachedMatches[0])
	}
	log.Printf("[PASS TC3] Bracket built and cached in PostgreSQL (Match 1: %s vs %s)", cachedMatches[0].Team1Name, cachedMatches[0].Team2Name)

	// =========================================================================
	// TEST CASE 4: Match Desk Operations (Readiness, Referee, Pause)
	// =========================================================================
	adminIDs := []int64{8150393380}
	deskSvc := application.NewMatchDeskService(tgRepo, adminIDs, time.UTC)

	notices, err := deskSvc.Pending(ctx)
	if err != nil || len(notices) == 0 {
		log.Fatalf("[FAIL TC4] MatchDesk Pending: %v (len=%d)", err, len(notices))
	}

	cards, err := deskSvc.Cards(ctx, captainTgIDs[0])
	if err != nil || len(cards) == 0 {
		log.Fatalf("[FAIL TC4] No cards found for captain: %v", err)
	}
	deskMatchID := cards[0].MatchID
	deskGen := cards[0].Generation

	spiritCapTg := captainTgIDs[0]
	err = deskSvc.CaptainAction(ctx, spiritCapTg, deskMatchID, deskGen, "ready")
	if err != nil {
		log.Fatalf("[FAIL TC4] Captain ready action failed: %v", err)
	}

	liquidCards, _ := deskSvc.Cards(ctx, captainTgIDs[3])
	if len(liquidCards) > 0 {
		deskMatchID = liquidCards[0].MatchID
		deskGen = liquidCards[0].Generation
	}
	liquidCapTg := captainTgIDs[3]
	err = deskSvc.CaptainAction(ctx, liquidCapTg, deskMatchID, deskGen, "judge_lobby")
	if err != nil {
		log.Fatalf("[FAIL TC4] Captain judge call failed: %v", err)
	}

	attention, err := deskSvc.Attention(ctx, adminIDs[0])
	if err != nil || len(attention) == 0 {
		log.Fatalf("[FAIL TC4] Expected attention card for admin, got %v (err: %v)", attention, err)
	}
	if !strings.Contains(attention[0].Text, "Проблема с лобби") {
		log.Fatalf("[FAIL TC4] Attention card missing issue text: %s", attention[0].Text)
	}

	err = deskSvc.AdminAction(ctx, adminIDs[0], deskMatchID, deskGen, "pause")
	if err != nil {
		log.Fatalf("[FAIL TC4] Admin pause failed: %v", err)
	}
	err = deskSvc.AdminAction(ctx, adminIDs[0], deskMatchID, deskGen, "resolve")
	if err != nil {
		log.Fatalf("[FAIL TC4] Admin resolve failed: %v", err)
	}
	err = deskSvc.AdminAction(ctx, adminIDs[0], deskMatchID, deskGen, "resume")
	if err != nil {
		log.Fatalf("[FAIL TC4] Admin resume failed: %v", err)
	}
	log.Println("[PASS TC4] Match Desk lifecycle (ready, referee call, admin pause/resolve/resume) verified")

	// =========================================================================
	// TEST CASE 5: Match Report Submission & Bracket Advancement
	// =========================================================================
	tgService.WithBracket(bracketSvc)

	repText, kb := tgService.StartReport(ctx, spiritCapTg)
	if !strings.Contains(repText, "Отчет о результате матча") {
		log.Fatalf("[FAIL TC5] StartReport failed: %s, kb: %s", repText, kb)
	}

	scoreText, _ := tgService.SetReportScore(ctx, spiritCapTg, "2:0")
	if !strings.Contains(scoreText, "Отправьте скриншоты") {
		log.Fatalf("[FAIL TC5] SetReportScore failed: %s", scoreText)
	}

	_, _, count := tgService.AddReportPhoto(ctx, spiritCapTg, "file_id_mock_screenshot_1")
	if count != 1 {
		log.Fatalf("[FAIL TC5] AddReportPhoto count = %d, want 1", count)
	}

	submitMsg, _, reportRecord, _ := tgService.SubmitReport(ctx, spiritCapTg)
	if reportRecord == nil || !strings.Contains(submitMsg, "успешно отправлен") {
		log.Fatalf("[FAIL TC5] SubmitReport failed: msg=%s, report=%v", submitMsg, reportRecord)
	}

	recentReports, err := tgRepo.GetRecentMatchReports(ctx, 1)
	if err != nil || len(recentReports) == 0 {
		log.Fatalf("[FAIL TC5] Reports not found in DB: %v", err)
	}
	if recentReports[0].Score != "2:0" || recentReports[0].WinnerTeamName != "Team Spirit" {
		log.Fatalf("[FAIL TC5] DB report data mismatch: %+v", recentReports[0])
	}

	updatedMatches, _ := tgRepo.GetBracketMatches(ctx)
	if updatedMatches[0].State != models.BracketComplete || updatedMatches[0].WinnerID == nil {
		log.Fatalf("[FAIL TC5] Bracket match not marked complete: %+v", updatedMatches[0])
	}
	log.Printf("[PASS TC5] Match report submitted and match #%d marked complete in PostgreSQL", updatedMatches[0].PlayOrder)

	// =========================================================================
	// TEST CASE 6: Admin Override (/set_winner)
	// =========================================================================
	change, err := bracketSvc.SetWinner(ctx, 1, "Team Liquid", 2, 1)
	if err != nil {
		log.Fatalf("[FAIL TC6] SetWinner failed: %v", err)
	}
	if change == nil {
		log.Fatalf("[FAIL TC6] Expected BracketChange from SetWinner, got nil")
	}
	afterOverride, _ := tgRepo.GetBracketMatches(ctx)
	liquidTeam, _ := tgRepo.GetTeamByName(ctx, "Team Liquid")
	if *afterOverride[0].WinnerID != liquidTeam.ID {
		log.Fatalf("[FAIL TC6] SetWinner did not update winner in DB: winner=%v, want %d", afterOverride[0].WinnerID, liquidTeam.ID)
	}
	log.Printf("[PASS TC6] Admin /set_winner successfully changed outcome to %s", liquidTeam.Name)

	// =========================================================================
	// TEST CASE 7: Technical Defeat & Admin Reinstate
	// =========================================================================
	dqTeams, err := tgService.DisqualifyUnchecked(ctx)
	if err != nil {
		log.Fatalf("[FAIL TC7] DisqualifyUnchecked failed: %v", err)
	}
	vpFound := false
	for _, t := range dqTeams {
		if t.Name == "Virtus.pro" {
			vpFound = true
		}
	}
	if !vpFound {
		log.Fatalf("[FAIL TC7] Virtus.pro was not disqualified for missing check-in")
	}

	forfeitMatches, err := bracketSvc.ForfeitDisqualified(ctx)
	if err != nil {
		log.Fatalf("[FAIL TC7] ForfeitDisqualified failed: %v", err)
	}
	log.Printf("[PASS TC7] Virtus.pro disqualified; forfeit processed (ready matches: %d)", len(forfeitMatches))

	reinstateResp := tgService.AdminReinstateTeam(ctx, "Virtus.pro")
	if !strings.Contains(reinstateResp, "возвращена в турнир") {
		log.Fatalf("[FAIL TC7] AdminReinstateTeam unexpected response: %s", reinstateResp)
	}
	reinstateChange, err := bracketSvc.Reinstate(ctx, "Virtus.pro")
	if err != nil {
		log.Fatalf("[FAIL TC7] Bracket Reinstate failed: %v", err)
	}
	if reinstateChange != nil {
		log.Printf("[PASS TC7] Bracket state reopened on reinstate (reset: %d, ready: %d)", len(reinstateChange.Reset), len(reinstateChange.Ready))
	}

	// =========================================================================
	// TEST CASE 8: Google Sheets Export
	// =========================================================================
	mockSheets := newMockSheetsClient()
	tgService.WithSheets(mockSheets, "test_spreadsheet_123")
	sheetURL, err := tgService.ExportTeamsToSheet(ctx)
	if err != nil {
		log.Fatalf("[FAIL TC8] ExportTeamsToSheet failed: %v", err)
	}
	if !strings.Contains(sheetURL, "test_spreadsheet_123") {
		log.Fatalf("[FAIL TC8] Export URL invalid: %s", sheetURL)
	}
	if len(mockSheets.updatedValues) < 2 {
		log.Fatalf("[FAIL TC8] Expected 2 tabs updated (Teams & Matches), got %d", len(mockSheets.updatedValues))
	}
	log.Printf("[PASS TC8] Google Sheets export generated and verified: %s", sheetURL)

	// =========================================================================
	// TEST CASE 9: Betting System Integrity & Payout Calculations
	// =========================================================================
	bettingSvc := application.NewBettingService(logger, &bettingRepoAdapter{BetPostgres: betRepo, LobbyMatchPostgres: lobbyRepo})

	var pA, pB int
	err = db.QueryRowContext(ctx, `INSERT INTO players (name, points) VALUES ('LobbyPlayerA', 0) RETURNING id`).Scan(&pA)
	if err != nil {
		log.Fatalf("[FAIL TC9] Insert player A: %v", err)
	}
	err = db.QueryRowContext(ctx, `INSERT INTO players (name, points) VALUES ('LobbyPlayerB', 0) RETURNING id`).Scan(&pB)
	if err != nil {
		log.Fatalf("[FAIL TC9] Insert player B: %v", err)
	}

	u1Tg := int64(9001)
	u2Tg := int64(9002)
	_, err = db.ExecContext(ctx, `INSERT INTO players (name, tg_id, points) VALUES ('Bettor1', $1, 100), ('Bettor2', $2, 100)`, u1Tg, u2Tg)
	if err != nil {
		log.Fatalf("[FAIL TC9] Insert bettors: %v", err)
	}

	lmID, err := lobbyRepo.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID:    "guild_test_1",
		CaptainAID: pA,
		CaptainBID: pB,
		TeamAIDs:   []int{pA},
		TeamBIDs:   []int{pB},
	})
	if err != nil {
		log.Fatalf("[FAIL TC9] Create LobbyMatch: %v", err)
	}
	if err := lobbyRepo.OpenBetting(ctx, lmID, time.Hour); err != nil {
		log.Fatalf("[FAIL TC9] OpenBetting: %v", err)
	}

	err = bettingSvc.PlaceBet(ctx, models.PlaceBetRequest{
		MatchID:    lmID,
		TgUserID:   u1Tg,
		TeamChosen: domain.TeamA,
		Amount:     50,
	})
	if err != nil {
		log.Fatalf("[FAIL TC9] PlaceBet 1 failed: %v", err)
	}
	err = bettingSvc.PlaceBet(ctx, models.PlaceBetRequest{
		MatchID:    lmID,
		TgUserID:   u2Tg,
		TeamChosen: domain.TeamB,
		Amount:     50,
	})
	if err != nil {
		log.Fatalf("[FAIL TC9] PlaceBet 2 failed: %v", err)
	}

	pool, err := betRepo.BetPool(ctx, lmID)
	if err != nil || pool.Total() != 100 {
		log.Fatalf("[FAIL TC9] Bet pool total = %d, want 100 (err: %v)", pool.Total(), err)
	}

	payoutCalled := false
	bettingSvc.SetPayoutCallback(func(res application.PayoutResult) {
		payoutCalled = true
		if res.MatchID != lmID || res.WinningTeam != domain.TeamA || res.Payouts[u1Tg] != 100 {
			log.Fatalf("[FAIL TC9] Payout mismatch: %+v", res)
		}
	})
	payouts, err := bettingSvc.ProcessPayout(ctx, lmID, domain.TeamA)
	if err != nil || !payoutCalled || payouts[u1Tg] != 100 {
		log.Fatalf("[FAIL TC9] ProcessPayout failed: err=%v, called=%v, payouts=%v", err, payoutCalled, payouts)
	}
	log.Println("[PASS TC9] Betting pool and payout calculation verified")

	// =========================================================================
	// TEST CASE 10: Robustness Against Stale Callbacks & Boundary Conditions
	// =========================================================================
	_, _, _, err = application.ParseDeskCallback("desk:bogus")
	if err == nil {
		log.Fatalf("[FAIL TC10] Expected error on bogus callback")
	}

	err = deskSvc.CaptainAction(ctx, spiritCapTg, deskMatchID, 99999, "ready")
	if err == nil || !strings.Contains(err.Error(), "устарела") {
		log.Fatalf("[FAIL TC10] Expected ErrDeskStale on bad generation, got: %v", err)
	}
	log.Println("[PASS TC10] Boundary conditions and error guards verified")

	log.Println("=========================================================================")
	log.Println("ALL 10 SCENARIO TEST CASES PASSED SUCCESSFULLY ON POSTGRESQL!")
	log.Println("=========================================================================")
}
