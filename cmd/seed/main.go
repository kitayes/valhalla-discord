package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"blackwatch/migrations"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type PlayerSeed struct {
	TgID         int64
	Username     string
	FirstName    string
	GameNick     string
	GameID       string
	ZoneID       string
	Stars        int
	Role         string
	IsCaptain    bool
	IsSubstitute bool
}

type TeamSeed struct {
	Name      string
	CheckedIn bool
	Status    string
	Players   []PlayerSeed
}

func main() {
	_ = godotenv.Load()

	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		host := os.Getenv("REPO_DB_HOST")
		if host == "" {
			host = "127.0.0.1"
		}
		port := os.Getenv("REPO_DB_PORT")
		if port == "" {
			port = "55432"
		}
		user := os.Getenv("REPO_DB_USERNAME")
		if user == "" {
			user = "postgres"
		}
		pass := os.Getenv("REPO_DB_PASSWORD")
		if pass == "" {
			pass = "bwtest"
		}
		dbname := os.Getenv("REPO_DB_NAME")
		if dbname == "" {
			dbname = "bw_telegram_fixture"
		}
		sslmode := os.Getenv("REPO_DB_SSLMODE")
		if sslmode == "" {
			sslmode = "disable"
		}
		dsn = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s", host, port, user, pass, dbname, sslmode)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("sql.Open failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("db.PingContext failed: %v", err)
	}
	log.Printf("[SEED] Connected to PostgreSQL: %s", dsn)

	// Ensure migrations are run
	if err := repository.RunMigrations(db, migrations.FS); err != nil {
		log.Fatalf("RunMigrations failed: %v", err)
	}
	log.Println("[SEED] Database migrations verified.")

	// Clean existing tournament data
	cleanQuery := `
		TRUNCATE TABLE
			telegram_match_desks,
			telegram_match_reports,
			telegram_bracket_matches,
			telegram_players,
			telegram_teams,
			telegram_settings
		CASCADE;
	`
	if _, err := db.ExecContext(ctx, cleanQuery); err != nil {
		log.Fatalf("Clean database failed: %v", err)
	}
	log.Println("[SEED] Previous tournament data cleared.")

	tgRepo := repository.NewTelegramPostgres(db)

	// Tournament started 1 minute ago, so active matches have ~9 minutes remaining
	tourneyTime := time.Now().Add(-1 * time.Minute).Truncate(time.Minute)
	settings := map[string]string{
		"tournament_time":               tourneyTime.UTC().Format(time.RFC3339),
		"registration_open":             "closed",
		"challonge_tournament_id":       "101",
		"challonge_tournament_url":      "https://challonge.com/valhalla_championship_128",
		"challonge_tournament_for":      tourneyTime.UTC().Format(time.RFC3339),
		"bracket_build_failures":        "0",
		"challonge_quota_blocked_until": "",
	}
	for k, v := range settings {
		if err := tgRepo.SetSetting(ctx, k, v); err != nil {
			log.Fatalf("SetSetting %s failed: %v", k, err)
		}
	}
	log.Printf("[SEED] Tournament settings initialized (start: %s, registration: closed).", tourneyTime.Format("2006-01-02 15:04:05 UTC"))

	// 128 Team Names
	teamNames := []string{
		"Team Spirit", "BetBoom Team", "Natus Vincere", "Virtus.pro",
		"OG Esports", "Team Liquid", "Gaimin Gladiators", "Cloud9",
		"Team Falcons", "Tundra Esports", "PARIVISION", "Aurora Gaming",
		"Team Aster", "PSG Quest", "HEROIC", "MOUZ",
		"Entity", "Team Secret", "Nigma Galaxy", "BOOM Esports",
		"Talon Esports", "Blacklist Int", "Geek Fam", "Bleed Esports",
		"Evil Geniuses", "Shopify Rebellion", "nouns esports", "beastcoast",
		"Thunder Awaken", "Invictus Gaming", "Xtreme Gaming", "Azure Ray",
		"LGD Gaming", "Vici Gaming", "Team Zero", "Team Bright",
		"Alliance", "Fnatic", "Fnatic Rising", "Ninjas in Pyjamas",
		"Astralis", "Vitality", "FaZe Clan", "Complexity Gaming",
		"ENCE", "BIG Clan", "Sprout", "Apeks",
		"Eternal Fire", "SAW Gaming", "Into The Breach", "Monte",
		"9Pandas", "1win Team", "Nemiga Gaming", "Cybercats",
		"Matreshka", "StoRm", "SIBE Team", "Klim Sani4",
		"Yellow Submarine", "Night Pulse", "Kalmychata", "B8 Esports",
		"Team Empire", "HellRaisers", "Vega Squadron", "Gambit Esports",
		"ForZe Esports", "Winstrike Team", "Unique Team", "Pro100",
		"FlyToMoon", "Magic Hands", "Cyber Legacy", "PuckChamp",
		"One Move", "HYDRA", "Level UP", "Team Tickles",
		"IVY", "D2 Hustlers", "Ancient Tribe", "Luna Galaxy",
		"ex-Monaspa", "KZ Team", "MarsBet Team", "Rest Farmers",
		"Just Error", "NoTechies", "4 Zoomers", "Wildcard Gaming",
		"felt", "The Cut", "DogChamp", "Ravens",
		"Infinity", "Hokori", "Lava Esports", "Mad Kings",
		"AcatSuki", "Estar_backs", "Polaris Esports", "Army Geniuses",
		"Execration", "Neon Esports", "Team SMG", "IHC Esports",
		"Bleed Academy", "MAG.Nirvana", "Invictus Junior", "CDEC Gaming",
		"EHOME", "Newbee", "Wings Gaming", "Speed Gaming",
		"RoX.KIS", "Moscow Five", "DTS Gaming", "Darer",
		"DTS.Chatrix", "MeetYourMakers", "SK Gaming", "MYM INT",
		"Zenith", "Orange Esports", "TongFu", "MUFC",
	}

	if len(teamNames) != 128 {
		log.Fatalf("Expected 128 team names, got %d", len(teamNames))
	}

	roles := []string{"Gold", "Exp", "Mid", "Roam", "Jungle"}
	roleInitials := []string{"G", "E", "M", "R", "J"}

	// Build 128 teams
	teams := make([]TeamSeed, 128)
	for i := 0; i < 128; i++ {
		tName := teamNames[i]
		// Teams 1..120 checked in, 121..128 debtors (CheckedIn: false)
		checkedIn := i < 120

		var players []PlayerSeed
		if i == 0 {
			// Team Spirit - Admin team (user 5227889205 is Captain)
			players = []PlayerSeed{
				{TgID: 5227889205, Username: "admin_spirit", FirstName: "Yatoro (Admin/Cap)", GameNick: "TS.Yatoro", GameID: "10001", ZoneID: "1000", Stars: 125, Role: "Gold", IsCaptain: true},
				{TgID: 10102, Username: "collapse_god", FirstName: "Collapse", GameNick: "TS.Collapse", GameID: "10002", ZoneID: "1000", Stars: 110, Role: "Exp"},
				{TgID: 10103, Username: "larl_mid", FirstName: "Larl", GameNick: "TS.Larl", GameID: "10003", ZoneID: "1000", Stars: 105, Role: "Mid"},
				{TgID: 10104, Username: "mira_sup", FirstName: "Mira", GameNick: "TS.Mira", GameID: "10004", ZoneID: "1000", Stars: 95, Role: "Roam"},
				{TgID: 10105, Username: "miposhka_lead", FirstName: "Miposhka", GameNick: "TS.Miposhka", GameID: "10005", ZoneID: "1000", Stars: 90, Role: "Jungle"},
				{TgID: 10106, Username: "silent_coach", FirstName: "Silent", GameNick: "TS.Silent", GameID: "10006", ZoneID: "1000", Stars: 80, Role: "Jungle", IsSubstitute: true},
			}
		} else if i == 1 {
			// BetBoom Team
			players = []PlayerSeed{
				{TgID: 100201, Username: "pure_bb", FirstName: "Pure", GameNick: "BB.Pure", GameID: "20001", ZoneID: "1001", Stars: 120, Role: "Gold", IsCaptain: true},
				{TgID: 100202, Username: "nightfall_bb", FirstName: "Nightfall", GameNick: "BB.Nightfall", GameID: "20002", ZoneID: "1001", Stars: 105, Role: "Exp"},
				{TgID: 100203, Username: "gpk_bb", FirstName: "gpk~", GameNick: "BB.gpk", GameID: "20003", ZoneID: "1001", Stars: 115, Role: "Mid"},
				{TgID: 100204, Username: "save_bb", FirstName: "Save-", GameNick: "BB.Save", GameID: "20004", ZoneID: "1001", Stars: 95, Role: "Roam"},
				{TgID: 100205, Username: "toronto_bb", FirstName: "TORONTOTOKYO", GameNick: "BB.TORONTO", GameID: "20005", ZoneID: "1001", Stars: 90, Role: "Jungle"},
			}
		} else {
			// Procedural players for teams 3..128
			tag := strings.ToUpper(strings.ReplaceAll(tName, " ", ""))
			if len(tag) > 4 {
				tag = tag[:4]
			}
			for p := 0; p < 5; p++ {
				tgID := int64(1000000 + (i+1)*10 + p + 1)
				isCap := (p == 0)
				role := roles[p]
				stars := 40 + ((i*11 + p*17) % 85)
				gameNick := fmt.Sprintf("%s.%s%d", tag, roleInitials[p], p+1)
				firstName := fmt.Sprintf("%s Player %d", tName, p+1)
				username := fmt.Sprintf("p_%d_%d", i+1, p+1)

				players = append(players, PlayerSeed{
					TgID:      tgID,
					Username:  username,
					FirstName: firstName,
					GameNick:  gameNick,
					GameID:    fmt.Sprintf("%d", 10000+(i+1)*10+p+1),
					ZoneID:    fmt.Sprintf("%d", 1000+(i%50)),
					Stars:     stars,
					Role:      role,
					IsCaptain: isCap,
				})
			}
		}

		teams[i] = TeamSeed{
			Name:      tName,
			CheckedIn: checkedIn,
			Status:    "active",
			Players:   players,
		}
	}

	teamIDByName := make(map[string]int)
	captainTgByTeamName := make(map[string]int64)

	for _, ts := range teams {
		t, err := tgRepo.CreateTeam(ctx, ts.Name)
		if err != nil {
			log.Fatalf("CreateTeam %s failed: %v", ts.Name, err)
		}
		teamIDByName[ts.Name] = t.ID

		if ts.CheckedIn {
			_ = tgRepo.SetCheckIn(ctx, t.ID, true)
		}
		if ts.Status != "active" {
			_ = tgRepo.SetTeamStatus(ctx, t.ID, ts.Status)
		}

		for _, p := range ts.Players {
			if p.IsCaptain {
				captainTgByTeamName[ts.Name] = p.TgID
			}
			_, err := db.ExecContext(ctx, `
				INSERT INTO telegram_players (
					telegram_id, telegram_username, first_name, game_nickname, game_id, zone_id, stars, main_role, is_captain, is_substitute, fsm_state, team_id
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			`, p.TgID, p.Username, p.FirstName, p.GameNick, p.GameID, p.ZoneID, p.Stars, p.Role, p.IsCaptain, p.IsSubstitute, models.StateIdle, t.ID)
			if err != nil {
				log.Fatalf("Insert player %s failed: %v", p.GameNick, err)
			}
		}
	}
	log.Printf("[SEED] 128 Teams with 641 total players created (120 checked-in, 8 debtors).")

	// 3. Seed Bracket Matches
	// 7 Rounds:
	// Round 1 (1/64): 64 matches (PlayOrder 1..64)
	// Round 2 (1/32): 32 matches (PlayOrder 65..96)
	// Round 3 (1/16): 16 matches (PlayOrder 97..112)
	// Round 4 (1/8):   8 matches (PlayOrder 113..120)
	// Round 5 (1/4):   4 matches (PlayOrder 121..124)
	// Round 6 (SF):    2 matches (PlayOrder 125..126)
	// Round 7 (Final): 1 match   (PlayOrder 127)
	var bracketMatches []models.BracketMatch

	// Round 1 (64 matches)
	// Matches 1 & 2: Open
	// Matches 3..16 (14 matches): Complete
	// Matches 17..64 (48 matches): Open
	round1Winners := make(map[int]int) // playOrder -> winner team ID

	for k := 1; k <= 64; k++ {
		t1Name := teamNames[k-1]
		t2Name := teamNames[128-k]
		t1ID := teamIDByName[t1Name]
		t2ID := teamIDByName[t2Name]

		match := models.BracketMatch{
			ChallongeMatchID: int64(1000 + k),
			Round:            1,
			PlayOrder:        k,
			Team1ID:          &t1ID,
			Team2ID:          &t2ID,
			BothNotified:     true,
		}

		if k >= 3 && k <= 16 {
			// Complete match
			match.State = "complete"
			match.WinnerID = &t1ID
			round1Winners[k] = t1ID
			if k%2 == 0 {
				match.ScoresCSV = "2-1"
			} else {
				match.ScoresCSV = "2-0"
			}
		} else {
			// Open live match
			match.State = "open"
		}

		bracketMatches = append(bracketMatches, match)
	}

	// Round 2 (32 matches, PlayOrder 65..96)
	// Match 65: Pair from M1 & M2 (pending, as M1 and M2 are open)
	// Matches 66..72 (7 matches): Pair from M3..M16 winners (open!)
	// Matches 73..96 (24 matches): pending
	for m := 1; m <= 32; m++ {
		playOrder := 64 + m
		match := models.BracketMatch{
			ChallongeMatchID: int64(2000 + m),
			Round:            2,
			PlayOrder:        playOrder,
			State:            "pending",
		}

		// Check if feed matches are complete
		feed1 := 2*m - 1
		feed2 := 2 * m
		w1, hasW1 := round1Winners[feed1]
		w2, hasW2 := round1Winners[feed2]

		if hasW1 && hasW2 {
			match.Team1ID = &w1
			match.Team2ID = &w2
			match.State = "open"
			match.BothNotified = true
		}

		bracketMatches = append(bracketMatches, match)
	}

	// Round 3 (16 matches, PlayOrder 97..112)
	for m := 1; m <= 16; m++ {
		bracketMatches = append(bracketMatches, models.BracketMatch{
			ChallongeMatchID: int64(3000 + m),
			Round:            3,
			PlayOrder:        96 + m,
			State:            "pending",
		})
	}

	// Round 4 (8 matches, PlayOrder 113..120)
	for m := 1; m <= 8; m++ {
		bracketMatches = append(bracketMatches, models.BracketMatch{
			ChallongeMatchID: int64(4000 + m),
			Round:            4,
			PlayOrder:        112 + m,
			State:            "pending",
		})
	}

	// Round 5 (4 matches, PlayOrder 121..124)
	for m := 1; m <= 4; m++ {
		bracketMatches = append(bracketMatches, models.BracketMatch{
			ChallongeMatchID: int64(5000 + m),
			Round:            5,
			PlayOrder:        120 + m,
			State:            "pending",
		})
	}

	// Round 6 (2 matches, PlayOrder 125..126)
	for m := 1; m <= 2; m++ {
		bracketMatches = append(bracketMatches, models.BracketMatch{
			ChallongeMatchID: int64(6000 + m),
			Round:            6,
			PlayOrder:        124 + m,
			State:            "pending",
		})
	}

	// Round 7 (1 match, PlayOrder 127)
	bracketMatches = append(bracketMatches, models.BracketMatch{
		ChallongeMatchID: 7001,
		Round:            7,
		PlayOrder:        127,
		State:            "pending",
	})

	if len(bracketMatches) != 127 {
		log.Fatalf("Expected 127 bracket matches, got %d", len(bracketMatches))
	}

	if err := tgRepo.ReplaceBracketMatches(ctx, bracketMatches); err != nil {
		log.Fatalf("ReplaceBracketMatches failed: %v", err)
	}

	// Set both_notified = true on all open and complete matches
	_, err = db.ExecContext(ctx, `UPDATE telegram_bracket_matches SET both_notified = TRUE WHERE state != 'pending'`)
	if err != nil {
		log.Fatalf("Update both_notified failed: %v", err)
	}
	log.Printf("[SEED] 127 Bracket matches inserted (14 complete, 57 open, 56 pending across 7 rounds).")

	// Query saved matches to obtain their database IDs
	savedMatches, err := tgRepo.GetBracketMatches(ctx)
	if err != nil {
		log.Fatalf("GetBracketMatches failed: %v", err)
	}
	matchByID := make(map[int]models.BracketMatch)
	for _, sm := range savedMatches {
		matchByID[sm.PlayOrder] = sm
	}

	// 4. Seed Match Reports for the 14 completed matches (PlayOrders 3..16)
	for k := 3; k <= 16; k++ {
		sm, ok := matchByID[k]
		if !ok || sm.WinnerID == nil || sm.Team1ID == nil || sm.Team2ID == nil {
			continue
		}
		winnerID := *sm.WinnerID
		var loserID int
		if *sm.Team1ID == winnerID {
			loserID = *sm.Team2ID
		} else {
			loserID = *sm.Team1ID
		}

		t1Name := teamNames[k-1]
		capTg := captainTgByTeamName[t1Name]
		if capTg == 0 {
			capTg = int64(1000000 + k*10 + 1)
		}

		bmID := sm.ID
		report := &models.TelegramMatchReport{
			ReporterTelegramID: capTg,
			WinnerTeamID:       winnerID,
			LoserTeamID:        loserID,
			Score:              sm.ScoresCSV,
			PhotoFileIDs:       []string{fmt.Sprintf("AgACAgIAAxkBAAIFRGbV_report_demo_%d", k)},
			BracketMatchID:     &bmID,
		}
		if err := tgRepo.CreateMatchReport(ctx, report); err != nil {
			log.Printf("[WARN] CreateMatchReport for match #%d failed: %v", k, err)
		}
	}
	log.Println("[SEED] 14 Match reports seeded for completed matches (Round 1).")

	// 5. Initialize MatchDeskService and sync desk
	adminIDs := []int64{5227889205, 8150393380}
	deskSvc := application.NewMatchDeskService(tgRepo, adminIDs, time.UTC)
	if err := deskSvc.Tick(ctx); err != nil {
		log.Fatalf("MatchDeskService.Tick failed: %v", err)
	}

	// 6. Simulate Actions & States
	// Match #2: BetBoom Team captain calls judge (creates active dispute)
	bbCaptainTg := captainTgByTeamName["BetBoom Team"]
	if err := deskSvc.CallJudgeCaptain(ctx, bbCaptainTg, "Соперник не заходит в лобби более 10 минут"); err != nil {
		log.Printf("[WARN] BetBoom call judge action: %v", err)
	}

	// Match #66: NAVI captain confirms ready
	naviCaptainTg := captainTgByTeamName["Natus Vincere"]
	if err := deskSvc.ReadyCaptain(ctx, naviCaptainTg); err != nil {
		log.Printf("[WARN] NAVI ready action: %v", err)
	}

	// Match #67: OG captain confirms ready
	ogCaptainTg := captainTgByTeamName["OG Esports"]
	if err := deskSvc.ReadyCaptain(ctx, ogCaptainTg); err != nil {
		log.Printf("[WARN] OG ready action: %v", err)
	}

	// Match #17: Team 17 captain confirms ready
	t17CapTg := captainTgByTeamName[teamNames[16]]
	if err := deskSvc.ReadyCaptain(ctx, t17CapTg); err != nil {
		log.Printf("[WARN] Team 17 ready action: %v", err)
	}

	_ = deskSvc.Tick(ctx)

	log.Println("=================================================================")
	log.Println("           TOURNAMENT 128-TEAM SEEDING COMPLETE!                 ")
	log.Println("=================================================================")
	log.Printf("Tournament: 128 Teams | 641 Players | 7 Rounds | 127 Matches")
	log.Printf("Status: Live | Registration: Closed | Check-in: 120 Done, 8 Debtors")
	log.Printf("Admin & Team Spirit Captain: TG ID 5227889205 (@admin_spirit)")
	log.Printf("Active Desk: 57 Active matches (50 in R1, 7 in R2)")
	log.Printf("Disputes: 1 Active dispute on Match #2 (BetBoom called judge)")
	log.Printf("Archive: 14 Completed matches with reports in Round 1")
	log.Printf("Match #1 (1/64): Team Spirit vs %s [OPEN - YOUR ACTIVE MATCH]", teamNames[127])
	log.Println("=================================================================")
}
