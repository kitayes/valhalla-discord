//go:build integration

// Package repository integration tests run the real SQL against a real
// Postgres. Everything in this layer is plain strings until it reaches a
// database — a wrong column name, a Scan with the wrong number of targets or a
// join that does not resolve compiles and vets cleanly and only fails when a
// user presses the button.
//
// Run with:
//
//	BW_TEST_DSN='host=127.0.0.1 port=55432 user=postgres password=... dbname=bw sslmode=disable' \
//	    go test -tags integration ./internal/repository/... -count=1
//
// The database must already have the migrations applied. Each test works in its
// own transaction-free namespace and cleans up after itself.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	_ "github.com/lib/pq"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BW_TEST_DSN")
	if dsn == "" {
		t.Skip("BW_TEST_DSN is not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// uniqueName keeps parallel runs and reruns from colliding on players.name.
func uniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano()%1e9, os.Getpid())
}

func newMatchRepo(t *testing.T, db *sql.DB) *MatchPostgres {
	t.Helper()
	repo, err := NewMatchPostgres(context.Background(), db, 1000)
	if err != nil {
		t.Fatalf("NewMatchPostgres: %v", err)
	}
	return repo
}

func TestIntegrationIdentityBinding(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := newMatchRepo(t, db)

	name := uniqueName(t, "ident")
	playerID, err := repo.EnsurePlayerExists(ctx, name)
	if err != nil {
		t.Fatalf("EnsurePlayerExists: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM players WHERE id = $1`, playerID)
	})

	discordID := fmt.Sprintf("dc-%d", playerID)

	t.Run("discord round trip", func(t *testing.T) {
		if err := repo.SetDiscordID(ctx, playerID, discordID); err != nil {
			t.Fatalf("SetDiscordID: %v", err)
		}
		gotID, gotName, err := repo.GetPlayerByDiscordID(ctx, discordID)
		if err != nil {
			t.Fatalf("GetPlayerByDiscordID: %v", err)
		}
		if gotID != playerID || gotName != name {
			t.Errorf("got (%d, %q), want (%d, %q)", gotID, gotName, playerID, name)
		}
	})

	t.Run("second profile cannot take the same discord account", func(t *testing.T) {
		otherID, err := repo.EnsurePlayerExists(ctx, uniqueName(t, "ident-other"))
		if err != nil {
			t.Fatalf("EnsurePlayerExists: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM players WHERE id = $1`, otherID) })

		err = repo.SetDiscordID(ctx, otherID, discordID)
		if !errors.Is(err, ErrDiscordIDTaken) {
			t.Errorf("SetDiscordID on a taken account = %v, want ErrDiscordIDTaken", err)
		}
	})

	t.Run("clearing releases the account", func(t *testing.T) {
		if err := repo.ClearDiscordID(ctx, playerID); err != nil {
			t.Fatalf("ClearDiscordID: %v", err)
		}
		if _, _, err := repo.GetPlayerByDiscordID(ctx, discordID); !errors.Is(err, domain.ErrPlayerNotFound) {
			t.Errorf("lookup after clear = %v, want ErrPlayerNotFound", err)
		}
	})
}

// seedPlayer creates a player with the given points balance.
func seedPlayer(t *testing.T, db *sql.DB, repo *MatchPostgres, points int) int {
	t.Helper()
	ctx := context.Background()
	id, err := repo.EnsurePlayerExists(ctx, uniqueName(t, "bettor"))
	if err != nil {
		t.Fatalf("EnsurePlayerExists: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM players WHERE id = $1`, id) })

	if _, err := db.Exec(`UPDATE players SET points = $1 WHERE id = $2`, points, id); err != nil {
		t.Fatalf("seed points: %v", err)
	}
	return id
}

func TestIntegrationBettingLifecycle(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)

	// The bettors are deliberately not on the roster: staking on a match you
	// are playing in is refused.
	rosterA := seedPlayer(t, db, matchRepo, 0)
	rosterB := seedPlayer(t, db, matchRepo, 0)
	winnerID := seedPlayer(t, db, matchRepo, 100)
	loserID := seedPlayer(t, db, matchRepo, 100)

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID:    "itest",
		CaptainAID: rosterA,
		CaptainBID: rosterB,
		TeamAIDs:   []int{rosterA},
		TeamBIDs:   []int{rosterB},
	})
	if err != nil {
		t.Fatalf("Create lobby match: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	t.Run("bets are refused while the window is shut", func(t *testing.T) {
		_, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
			MatchID: matchID, PlayerID: winnerID, TeamChosen: domain.TeamA, Amount: 10,
		})
		if !errors.Is(err, domain.ErrBettingClosed) {
			t.Errorf("PlaceDiscordBet before OpenBetting = %v, want ErrBettingClosed", err)
		}
	})

	if err := lobby.OpenBetting(ctx, matchID, time.Hour); err != nil {
		t.Fatalf("OpenBetting: %v", err)
	}

	t.Run("bets land once the window is open", func(t *testing.T) {
		for _, b := range []struct {
			pid  int
			team string
			amt  int
		}{
			{winnerID, domain.TeamA, 40},
			{loserID, domain.TeamB, 60},
		} {
			if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
				MatchID: matchID, PlayerID: b.pid, TeamChosen: b.team, Amount: b.amt,
			}); err != nil {
				t.Fatalf("PlaceDiscordBet(%d): %v", b.pid, err)
			}
		}
	})

	t.Run("one bet per user per match", func(t *testing.T) {
		_, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
			MatchID: matchID, PlayerID: winnerID, TeamChosen: domain.TeamB, Amount: 5,
		})
		if !errors.Is(err, domain.ErrAlreadyBet) {
			t.Errorf("second bet = %v, want ErrAlreadyBet", err)
		}
	})

	t.Run("stake is deducted", func(t *testing.T) {
		got, err := bets.GetPlayerPointsByID(ctx, winnerID)
		if err != nil {
			t.Fatalf("GetPlayerPointsByID: %v", err)
		}
		if got != 60 {
			t.Errorf("points after a 40 stake = %d, want 60", got)
		}
	})

	t.Run("closing the match shuts the window", func(t *testing.T) {
		ok, err := lobby.AtomicSetWinner(ctx, matchID, domain.TeamA)
		if err != nil {
			t.Fatalf("AtomicSetWinner: %v", err)
		}
		if !ok {
			t.Fatal("AtomicSetWinner reported no transition on an ACTIVE match")
		}
		again, err := lobby.AtomicSetWinner(ctx, matchID, domain.TeamB)
		if err != nil {
			t.Fatalf("AtomicSetWinner (replay): %v", err)
		}
		if again {
			t.Error("a finished match accepted a second winner")
		}
	})

	t.Run("the pool goes to the winner", func(t *testing.T) {
		summary, err := bets.PayoutWinnersDetailed(ctx, matchID, domain.TeamA)
		if err != nil {
			t.Fatalf("PayoutWinnersDetailed: %v", err)
		}
		if got, want := summary.PlayerPayouts[winnerID], 100; got != want {
			t.Errorf("winner was paid %d, want the whole %d pool", got, want)
		}
		points, err := bets.GetPlayerPointsByID(ctx, winnerID)
		if err != nil {
			t.Fatalf("GetPlayerPointsByID: %v", err)
		}
		if points != 160 {
			t.Errorf("winner holds %d points, want 160 (60 left + 100 pool)", points)
		}
	})

	t.Run("payout is idempotent", func(t *testing.T) {
		summary, err := bets.PayoutWinnersDetailed(ctx, matchID, domain.TeamA)
		if err != nil {
			t.Fatalf("PayoutWinnersDetailed (replay): %v", err)
		}
		if len(summary.PlayerPayouts) != 0 {
			t.Errorf("a settled match paid out again: %v", summary.PlayerPayouts)
		}
		points, err := bets.GetPlayerPointsByID(ctx, winnerID)
		if err != nil {
			t.Fatalf("GetPlayerPointsByID: %v", err)
		}
		if points != 160 {
			t.Errorf("replay moved points to %d, want 160", points)
		}
	})

	t.Run("nothing is left pending", func(t *testing.T) {
		pending, err := bets.PendingSettlements(ctx)
		if err != nil {
			t.Fatalf("PendingSettlements: %v", err)
		}
		for _, p := range pending {
			if p.MatchID == matchID {
				t.Errorf("match %d is still listed as pending after payout", matchID)
			}
		}
	})
}

func TestIntegrationCancelRefundsEveryStake(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)

	rosterA := seedPlayer(t, db, matchRepo, 0)
	rosterB := seedPlayer(t, db, matchRepo, 0)
	aID := seedPlayer(t, db, matchRepo, 100)
	bID := seedPlayer(t, db, matchRepo, 100)

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest", CaptainAID: rosterA, CaptainBID: rosterB,
		TeamAIDs: []int{rosterA}, TeamBIDs: []int{rosterB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	if err := lobby.OpenBetting(ctx, matchID, time.Hour); err != nil {
		t.Fatalf("OpenBetting: %v", err)
	}
	for _, b := range []struct {
		pid int
		amt int
	}{{aID, 25}, {bID, 35}} {
		if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
			MatchID: matchID, PlayerID: b.pid, TeamChosen: domain.TeamA, Amount: b.amt,
		}); err != nil {
			t.Fatalf("PlaceDiscordBet: %v", err)
		}
	}

	players, err := lobby.CancelMatch(ctx, matchID)
	if err != nil {
		t.Fatalf("CancelMatch: %v", err)
	}
	if len(players) != 2 {
		t.Errorf("CancelMatch returned %d players, want 2", len(players))
	}

	// A cancelled match is exactly what the settlement sweep exists to catch:
	// the row is FINISHED with no winner while the stakes are still deducted.
	pending, err := bets.PendingSettlements(ctx)
	if err != nil {
		t.Fatalf("PendingSettlements: %v", err)
	}
	found := false
	for _, p := range pending {
		if p.MatchID == matchID {
			found = true
			if !p.Cancelled {
				t.Error("a cancelled match is not reported as cancelled, so the sweep would pay it out")
			}
		}
	}
	if !found {
		t.Fatal("a cancelled match with live bets is not listed as pending")
	}

	n, err := bets.RefundAllBets(ctx, matchID)
	if err != nil {
		t.Fatalf("RefundAllBets: %v", err)
	}
	if n != 2 {
		t.Errorf("refunded %d bets, want 2", n)
	}
	for pid, want := range map[int]int{aID: 100, bID: 100} {
		got, err := bets.GetPlayerPointsByID(ctx, pid)
		if err != nil {
			t.Fatalf("GetPlayerPointsByID(%d): %v", pid, err)
		}
		if got != want {
			t.Errorf("player %d holds %d points after refund, want %d", pid, got, want)
		}
	}

	if again, err := bets.RefundAllBets(ctx, matchID); err != nil || again != 0 {
		t.Errorf("second refund = (%d, %v), want (0, nil)", again, err)
	}
}

func TestIntegrationBettingDeadlineSweep(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)

	pID := seedPlayer(t, db, matchRepo, 100)
	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest", CaptainAID: pID, CaptainBID: pID,
		TeamAIDs: []int{pID}, TeamBIDs: []int{pID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	// A window that closed a second ago: the in-memory timer is what normally
	// shuts it, and a restart loses that timer.
	if err := lobby.OpenBetting(ctx, matchID, -time.Second); err != nil {
		t.Fatalf("OpenBetting: %v", err)
	}

	_, _, err = bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: pID, TeamChosen: domain.TeamA, Amount: 10,
	})
	if !errors.Is(err, domain.ErrBettingClosed) {
		t.Errorf("bet past the deadline = %v, want ErrBettingClosed", err)
	}

	if _, err := lobby.CloseExpiredBetting(ctx); err != nil {
		t.Fatalf("CloseExpiredBetting: %v", err)
	}
	m, err := lobby.GetByID(ctx, matchID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if m.BettingOpen {
		t.Error("the sweep left an expired window open")
	}
}

func TestIntegrationQueueBans(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewQueueBanPostgres(db)

	discordID := fmt.Sprintf("ban-%d", time.Now().UnixNano()%1e9)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM queue_bans WHERE discord_id = $1`, discordID) })

	if banned, err := repo.IsBanned(ctx, discordID); err != nil || banned {
		t.Fatalf("IsBanned on a clean account = (%v, %v), want (false, nil)", banned, err)
	}

	until := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := repo.BanPlayer(ctx, discordID, "тест", until); err != nil {
		t.Fatalf("BanPlayer: %v", err)
	}
	banned, err := repo.IsBanned(ctx, discordID)
	if err != nil || !banned {
		t.Fatalf("IsBanned after ban = (%v, %v), want (true, nil)", banned, err)
	}

	reason, gotUntil, err := repo.GetBanInfo(ctx, discordID)
	if err != nil {
		t.Fatalf("GetBanInfo: %v", err)
	}
	if reason != "тест" {
		t.Errorf("reason = %q, want %q", reason, "тест")
	}
	if !gotUntil.Truncate(time.Second).Equal(until.UTC().Truncate(time.Second)) &&
		gotUntil.Sub(until).Abs() > time.Second {
		t.Errorf("until = %v, want %v", gotUntil, until)
	}

	// Re-banning the same account must update, not fail on the unique index.
	if err := repo.BanPlayer(ctx, discordID, "второй", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("BanPlayer (repeat): %v", err)
	}

	// An expired ban stops applying and gets purged.
	if _, err := db.Exec(`UPDATE queue_bans SET banned_until = NOW() - INTERVAL '1 hour' WHERE discord_id = $1`, discordID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if banned, err := repo.IsBanned(ctx, discordID); err != nil || banned {
		t.Errorf("IsBanned on an expired ban = (%v, %v), want (false, nil)", banned, err)
	}
	if _, err := repo.PurgeExpired(ctx); err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
}

func TestIntegrationLobbyMatchReads(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)

	aID := seedPlayer(t, db, matchRepo, 0)
	bID := seedPlayer(t, db, matchRepo, 0)

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest-reads", CaptainAID: aID, CaptainBID: bID,
		TeamAIDs: []int{aID}, TeamBIDs: []int{bID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	t.Run("a running match has no winner and scans anyway", func(t *testing.T) {
		// winner is NULL until a referee closes the match; scanning it into a
		// plain string failed for every match still in progress.
		m, err := lobby.GetByID(ctx, matchID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if m.Winner != "" {
			t.Errorf("winner = %q, want empty on a running match", m.Winner)
		}
		if len(m.TeamAIDs) != 1 || m.TeamAIDs[0] != aID {
			t.Errorf("TeamAIDs = %v, want [%d]", m.TeamAIDs, aID)
		}
	})

	t.Run("thread id round trip", func(t *testing.T) {
		threadID := fmt.Sprintf("th-%d", matchID)
		if err := lobby.SaveThreadID(ctx, matchID, threadID); err != nil {
			t.Fatalf("SaveThreadID: %v", err)
		}
		m, err := lobby.GetByThreadID(ctx, threadID)
		if err != nil {
			t.Fatalf("GetByThreadID: %v", err)
		}
		if m.ID != matchID {
			t.Errorf("GetByThreadID returned match %d, want %d", m.ID, matchID)
		}
	})

	t.Run("medals are stored and empty names do not erase them", func(t *testing.T) {
		if err := lobby.SaveMedals(ctx, matchID, "MVPGuy", "SVPGuy"); err != nil {
			t.Fatalf("SaveMedals: %v", err)
		}
		if err := lobby.SaveMedals(ctx, matchID, "", ""); err != nil {
			t.Fatalf("SaveMedals (empty): %v", err)
		}
		m, err := lobby.GetByID(ctx, matchID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if m.MVP != "MVPGuy" || m.SVP != "SVPGuy" {
			t.Errorf("medals = (%q, %q), want (MVPGuy, SVPGuy)", m.MVP, m.SVP)
		}
	})

	t.Run("roster names resolve", func(t *testing.T) {
		names, err := lobby.GetPlayerNamesByMatchID(ctx, matchID)
		if err != nil {
			t.Fatalf("GetPlayerNamesByMatchID: %v", err)
		}
		if len(names) != 2 {
			t.Errorf("got %d names, want 2: %v", len(names), names)
		}
	})

	t.Run("mmr batch and update", func(t *testing.T) {
		if err := lobby.UpdatePlayerMMR(ctx, aID, 1234, matchID); err != nil {
			t.Fatalf("UpdatePlayerMMR: %v", err)
		}
		mmrs, err := lobby.GetPlayerMMRsBatch(ctx, []int{aID, bID})
		if err != nil {
			t.Fatalf("GetPlayerMMRsBatch: %v", err)
		}
		if mmrs[aID] != 1234 {
			t.Errorf("mmr = %d, want 1234", mmrs[aID])
		}
	})

	t.Run("missing match is a typed error", func(t *testing.T) {
		if _, err := lobby.GetByID(ctx, 0); !errors.Is(err, domain.ErrMatchNotFound) {
			t.Errorf("GetByID(0) = %v, want ErrMatchNotFound", err)
		}
		if _, err := lobby.CancelMatch(ctx, 0); !errors.Is(err, domain.ErrMatchNotFound) {
			t.Errorf("CancelMatch(0) = %v, want ErrMatchNotFound", err)
		}
	})
}

func TestIntegrationMatchRecording(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := newMatchRepo(t, db)

	winner := uniqueName(t, "rec-win")
	loser := uniqueName(t, "rec-lose")
	hash := fmt.Sprintf("hash-%d", time.Now().UnixNano())
	sig := fmt.Sprintf("sig-%d", time.Now().UnixNano())

	match := models.Match{
		FileHash:       hash,
		MatchSignature: sig,
		MVP:            winner,
		SVP:            loser,
		Players: []models.PlayerResult{
			{PlayerName: winner, Result: "WIN", Kills: 10, Deaths: 2, Assists: 7, Champion: "A"},
			{PlayerName: loser, Result: "LOSE", Kills: 3, Deaths: 9, Assists: 1, Champion: "B"},
		},
	}

	matchID, err := repo.Create(ctx, match)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM matches WHERE id = $1`, matchID)
		_, _ = db.Exec(`DELETE FROM players WHERE name IN ($1, $2)`, winner, loser)
	})

	t.Run("the same screenshot is not recorded twice", func(t *testing.T) {
		exists, err := repo.Exists(ctx, hash, sig)
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !exists {
			t.Error("a recorded match reports as absent, so duplicates would pile up")
		}
		absent, err := repo.Exists(ctx, "no-such-hash", "no-such-sig")
		if err != nil {
			t.Fatalf("Exists (absent): %v", err)
		}
		if absent {
			t.Error("an unrecorded match reports as present, so screenshots would be dropped")
		}
	})

	t.Run("medals reach the players who earned them", func(t *testing.T) {
		// The AI reads MVP/SVPG off the scoreboard as names; Create has to turn
		// them into player ids on the match row.
		counts, err := repo.GetMedalCountsAfter(ctx, time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("GetMedalCountsAfter: %v", err)
		}
		winnerID, err := repo.EnsurePlayerExists(ctx, winner)
		if err != nil {
			t.Fatalf("EnsurePlayerExists: %v", err)
		}
		if counts[winnerID].MVP < 1 {
			t.Errorf("MVP count for the awarded player = %d, want at least 1", counts[winnerID].MVP)
		}
		lifetime, err := repo.GetLifetimeMedals(ctx, winnerID)
		if err != nil {
			t.Fatalf("GetLifetimeMedals: %v", err)
		}
		if lifetime.MVP < 1 {
			t.Errorf("lifetime MVP = %d, want at least 1", lifetime.MVP)
		}
	})

	t.Run("the match is readable back", func(t *testing.T) {
		all, err := repo.GetAllAfter(ctx, time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("GetAllAfter: %v", err)
		}
		var found *models.Match
		for i := range all {
			if all[i].ID == matchID {
				found = &all[i]
			}
		}
		if found == nil {
			t.Fatalf("match %d is missing from GetAllAfter", matchID)
		}
		if len(found.Players) != 2 {
			t.Errorf("match carries %d player rows, want 2", len(found.Players))
		}
	})

	t.Run("player history", func(t *testing.T) {
		winnerID, err := repo.EnsurePlayerExists(ctx, winner)
		if err != nil {
			t.Fatalf("EnsurePlayerExists: %v", err)
		}
		hist, err := repo.GetHistory(ctx, winnerID, 10)
		if err != nil {
			t.Fatalf("GetHistory: %v", err)
		}
		if len(hist) == 0 {
			t.Fatal("history is empty right after recording a match")
		}
		if len(hist[0].Players) == 0 {
			t.Error("a history entry carries no player row, so the handler renders nothing")
		}
	})
}

func TestIntegrationRegisterByNickname(t *testing.T) {
	// A newcomer could never register: a players row was only ever created by
	// parsing a match screenshot, joining the lobby needed a bound profile, and
	// the profile needed a match they could not join.
	db := testDB(t)
	ctx := context.Background()
	repo := newMatchRepo(t, db)

	nick := uniqueName(t, "newbie")
	discordID := fmt.Sprintf("dc-new-%d", time.Now().UnixNano()%1e9)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM players WHERE name = $1`, nick) })

	t.Run("an unknown nickname is not found rather than invented", func(t *testing.T) {
		if _, _, err := repo.FindPlayerByExactName(ctx, nick); !errors.Is(err, domain.ErrPlayerNotFound) {
			t.Errorf("FindPlayerByExactName on an unknown nick = %v, want ErrPlayerNotFound", err)
		}
	})

	t.Run("create and bind in one step", func(t *testing.T) {
		id, err := repo.CreatePlayerWithDiscord(ctx, nick, discordID)
		if err != nil {
			t.Fatalf("CreatePlayerWithDiscord: %v", err)
		}
		gotID, gotName, err := repo.GetPlayerByDiscordID(ctx, discordID)
		if err != nil {
			t.Fatalf("GetPlayerByDiscordID: %v", err)
		}
		if gotID != id || gotName != nick {
			t.Errorf("got (%d, %q), want (%d, %q)", gotID, gotName, id, nick)
		}
	})

	t.Run("the same Discord account cannot register twice", func(t *testing.T) {
		second := uniqueName(t, "newbie2")
		t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM players WHERE name = $1`, second) })
		if _, err := repo.CreatePlayerWithDiscord(ctx, second, discordID); !errors.Is(err, ErrDiscordIDTaken) {
			t.Errorf("second registration = %v, want ErrDiscordIDTaken", err)
		}
		// The refusal must not leave the half-made profile behind.
		if _, _, err := repo.FindPlayerByExactName(ctx, second); !errors.Is(err, domain.ErrPlayerNotFound) {
			t.Errorf("a rejected registration left a profile behind: %v", err)
		}
	})

	t.Run("lookup is exact, not fuzzy", func(t *testing.T) {
		// EnsurePlayerExists falls back to embedding similarity, which is right
		// for OCR and wrong for a self-service claim: a near miss would hand the
		// caller somebody else's profile.
		if _, _, err := repo.FindPlayerByExactName(ctx, nick+"x"); !errors.Is(err, domain.ErrPlayerNotFound) {
			t.Errorf("a near-miss nickname resolved to %v, want ErrPlayerNotFound", err)
		}
	})

	t.Run("case and padding do not create a second profile", func(t *testing.T) {
		id, _, err := repo.FindPlayerByExactName(ctx, "  "+strings.ToUpper(nick)+"  ")
		if err != nil {
			t.Fatalf("FindPlayerByExactName with different case: %v", err)
		}
		if id == 0 {
			t.Error("case-insensitive lookup missed the profile")
		}
	})
}

func TestIntegrationBettingOnOwnMatchIsRefused(t *testing.T) {
	// A participant backing the other side and then throwing the game is the
	// whole of match fixing in one move, and the same people play and bet here.
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)

	playerID := seedPlayer(t, db, matchRepo, 100)
	outsiderID := seedPlayer(t, db, matchRepo, 100)

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest", CaptainAID: playerID, CaptainBID: playerID,
		TeamAIDs: []int{playerID}, TeamBIDs: []int{},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	if err := lobby.OpenBetting(ctx, matchID, time.Hour); err != nil {
		t.Fatalf("OpenBetting: %v", err)
	}

	t.Run("a player in the match is refused", func(t *testing.T) {
		_, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
			MatchID: matchID, PlayerID: playerID, TeamChosen: domain.TeamB, Amount: 10,
		})
		if !errors.Is(err, domain.ErrBettingOnOwnMatch) {
			t.Errorf("PlaceDiscordBet by a participant = %v, want ErrBettingOnOwnMatch", err)
		}
		// The refusal must not have moved anything.
		points, err := bets.GetPlayerPointsByID(ctx, playerID)
		if err != nil {
			t.Fatalf("GetPlayerPointsByID: %v", err)
		}
		if points != 100 {
			t.Errorf("a refused bet changed the balance to %d, want 100", points)
		}
	})

	t.Run("somebody outside the match may bet", func(t *testing.T) {
		if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
			MatchID: matchID, PlayerID: outsiderID, TeamChosen: domain.TeamA, Amount: 10,
		}); err != nil {
			t.Errorf("PlaceDiscordBet by a non-participant: %v", err)
		}
	})
}

func TestIntegrationMMRHistory(t *testing.T) {
	// current_mmr is one overwritten column, so before this a rating had no
	// past: no "+18" in the profile, no graph, and no way to settle a dispute
	// over a drop.
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)

	playerID, err := matchRepo.EnsurePlayerExists(ctx, uniqueName(t, "mmr"))
	if err != nil {
		t.Fatalf("EnsurePlayerExists: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM players WHERE id = $1`, playerID) })

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest", CaptainAID: playerID, CaptainBID: playerID,
		TeamAIDs: []int{playerID}, TeamBIDs: []int{},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	t.Run("a change is recorded with both endpoints", func(t *testing.T) {
		if err := lobby.UpdatePlayerMMR(ctx, playerID, 1018, matchID); err != nil {
			t.Fatalf("UpdatePlayerMMR: %v", err)
		}
		history, err := lobby.GetMMRHistory(ctx, playerID, 10)
		if err != nil {
			t.Fatalf("GetMMRHistory: %v", err)
		}
		if len(history) != 1 {
			t.Fatalf("got %d history rows, want 1", len(history))
		}
		got := history[0]
		if got.Before != 1000 || got.After != 1018 || got.Delta != 18 {
			t.Errorf("change = (%d → %d, %+d), want (1000 → 1018, +18)", got.Before, got.After, got.Delta)
		}
		if got.MatchID != matchID {
			t.Errorf("change is attached to match %d, want %d", got.MatchID, matchID)
		}
	})

	t.Run("the rating itself moved", func(t *testing.T) {
		mmrs, err := lobby.GetPlayerMMRsBatch(ctx, []int{playerID})
		if err != nil {
			t.Fatalf("GetPlayerMMRsBatch: %v", err)
		}
		if mmrs[playerID] != 1018 {
			t.Errorf("current_mmr = %d, want 1018", mmrs[playerID])
		}
	})

	t.Run("replaying a settlement does not double the history", func(t *testing.T) {
		if err := lobby.UpdatePlayerMMR(ctx, playerID, 1018, matchID); err != nil {
			t.Fatalf("UpdatePlayerMMR (replay): %v", err)
		}
		history, err := lobby.GetMMRHistory(ctx, playerID, 10)
		if err != nil {
			t.Fatalf("GetMMRHistory: %v", err)
		}
		if len(history) != 1 {
			t.Errorf("a replay appended a second row: %d rows", len(history))
		}
	})

	t.Run("a change with no match behind it is allowed", func(t *testing.T) {
		// An admin correction or a season reset moves a rating without a match.
		if err := lobby.UpdatePlayerMMR(ctx, playerID, 1000, 0); err != nil {
			t.Fatalf("UpdatePlayerMMR without a match: %v", err)
		}
		history, err := lobby.GetMMRHistory(ctx, playerID, 10)
		if err != nil {
			t.Fatalf("GetMMRHistory: %v", err)
		}
		if len(history) != 2 {
			t.Fatalf("got %d rows, want 2", len(history))
		}
		if history[0].Delta != -18 || history[0].MatchID != 0 {
			t.Errorf("newest change = (%+d, match %d), want (-18, no match)", history[0].Delta, history[0].MatchID)
		}
	})
}

// The coefficients shown in Discord are computed from BetPool, and the money
// is moved by PayoutWinnersDetailed. This pins the two together: whatever
// BetPool reports must be the pool settlement actually distributes.
func TestIntegrationBetPoolTracksTheLiveMarket(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)

	rosterA := seedPlayer(t, db, matchRepo, 0)
	rosterB := seedPlayer(t, db, matchRepo, 0)
	backerA := seedPlayer(t, db, matchRepo, 100)
	backerB := seedPlayer(t, db, matchRepo, 100)

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest", CaptainAID: rosterA, CaptainBID: rosterB,
		TeamAIDs: []int{rosterA}, TeamBIDs: []int{rosterB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	t.Run("an untouched match has no market", func(t *testing.T) {
		pool, err := bets.BetPool(ctx, matchID)
		if err != nil {
			t.Fatalf("BetPool: %v", err)
		}
		if pool.Total() != 0 || pool.Odds(domain.TeamA) != 0 {
			t.Errorf("BetPool on a match with no bets = %+v, want empty", pool)
		}
	})

	if err := lobby.OpenBetting(ctx, matchID, time.Hour); err != nil {
		t.Fatalf("OpenBetting: %v", err)
	}
	for _, b := range []struct {
		pid    int
		team   string
		amount int
	}{
		{backerA, domain.TeamA, 40},
		{backerB, domain.TeamB, 10},
	} {
		if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
			MatchID: matchID, PlayerID: b.pid, TeamChosen: b.team, Amount: b.amount,
		}); err != nil {
			t.Fatalf("PlaceDiscordBet %d on %s: %v", b.amount, b.team, err)
		}
	}

	t.Run("sides are summed and counted separately", func(t *testing.T) {
		pool, err := bets.BetPool(ctx, matchID)
		if err != nil {
			t.Fatalf("BetPool: %v", err)
		}
		if pool.AmountA != 40 || pool.CountA != 1 {
			t.Errorf("Team A = %d over %d bets, want 40 over 1", pool.AmountA, pool.CountA)
		}
		if pool.AmountB != 10 || pool.CountB != 1 {
			t.Errorf("Team B = %d over %d bets, want 10 over 1", pool.AmountB, pool.CountB)
		}
		if pool.Total() != 50 {
			t.Errorf("Total() = %d, want 50", pool.Total())
		}
	})

	t.Run("the quoted coefficient is the one that gets paid", func(t *testing.T) {
		pool, err := bets.BetPool(ctx, matchID)
		if err != nil {
			t.Fatalf("BetPool: %v", err)
		}
		quoted := int(40 * pool.Odds(domain.TeamA))

		summary, err := bets.PayoutWinnersDetailed(ctx, matchID, domain.TeamA)
		if err != nil {
			t.Fatalf("PayoutWinnersDetailed: %v", err)
		}
		if summary.PlayerPayouts[backerA] != quoted {
			t.Errorf("paid %d but the post advertised %d", summary.PlayerPayouts[backerA], quoted)
		}
	})

	t.Run("settled bets leave the market", func(t *testing.T) {
		pool, err := bets.BetPool(ctx, matchID)
		if err != nil {
			t.Fatalf("BetPool: %v", err)
		}
		if pool.Total() != 0 {
			t.Errorf("BetPool after settlement = %+v, want empty", pool)
		}
	})
}

// A player on either roster of an ACTIVE match may not queue again; once the
// match leaves ACTIVE they may.
func TestIntegrationIsInActiveMatch(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)

	aID := seedPlayer(t, db, matchRepo, 0)
	bID := seedPlayer(t, db, matchRepo, 0)
	freeID := seedPlayer(t, db, matchRepo, 0)

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest-active", CaptainAID: aID, CaptainBID: bID,
		TeamAIDs: []int{aID}, TeamBIDs: []int{bID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	for _, tc := range []struct {
		name     string
		playerID int
		want     bool
	}{
		{"team A player", aID, true},
		{"team B player", bID, true},
		{"player outside the match", freeID, false},
	} {
		got, err := lobby.IsInActiveMatch(ctx, tc.playerID)
		if err != nil {
			t.Fatalf("%s: IsInActiveMatch: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: IsInActiveMatch = %v, want %v", tc.name, got, tc.want)
		}
	}

	if _, err := db.Exec(`UPDATE lobby_matches SET status = 'FINISHED' WHERE id = $1`, matchID); err != nil {
		t.Fatalf("finish match: %v", err)
	}
	if got, err := lobby.IsInActiveMatch(ctx, aID); err != nil || got {
		t.Errorf("after the match finished: IsInActiveMatch = (%v, %v), want (false, nil)", got, err)
	}
}

func TestIntegrationRosterScoreboardAndAliases(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)

	pA := seedPlayer(t, db, matchRepo, 0)
	pB := seedPlayer(t, db, matchRepo, 0)

	pAName, err := matchRepo.GetPlayerNameByID(ctx, pA)
	if err != nil {
		t.Fatalf("GetPlayerNameByID(pA): %v", err)
	}
	pBName, err := matchRepo.GetPlayerNameByID(ctx, pB)
	if err != nil {
		t.Fatalf("GetPlayerNameByID(pB): %v", err)
	}

	lobbyMatchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID:    "itest-roster",
		CaptainAID: pA,
		CaptainBID: pB,
		TeamAIDs:   []int{pA},
		TeamBIDs:   []int{pB},
	})
	if err != nil {
		t.Fatalf("lobby.Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, lobbyMatchID) })

	smurfName := uniqueName(t, "smurf-ghost")
	hash1 := fmt.Sprintf("hash-r1-%d", time.Now().UnixNano())
	sig1 := fmt.Sprintf("sig-r1-%d", time.Now().UnixNano())

	firstMatchID, err := matchRepo.Create(ctx, models.Match{
		LobbyMatchID:   &lobbyMatchID,
		FileHash:       hash1,
		MatchSignature: sig1,
		MVP:            pAName,
		SVP:            pBName,
		Players: []models.PlayerResult{
			{PlayerID: pA, PlayerName: smurfName, Result: "WIN", Kills: 10, Deaths: 1, Assists: 4},
			{PlayerID: pB, PlayerName: pBName, Result: "LOSE", Kills: 1, Deaths: 10, Assists: 0},
		},
	})
	if err != nil {
		t.Fatalf("matchRepo.Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM matches WHERE id = $1`, firstMatchID) })

	var storedLobbyID sql.NullInt64
	if err := db.QueryRow(`SELECT lobby_match_id FROM matches WHERE id = $1`, firstMatchID).Scan(&storedLobbyID); err != nil {
		t.Fatalf("select lobby_match_id: %v", err)
	}
	if !storedLobbyID.Valid || int(storedLobbyID.Int64) != lobbyMatchID {
		t.Errorf("stored lobby_match_id = %+v, want %d", storedLobbyID, lobbyMatchID)
	}

	// Pre-resolved PlayerID must prevent EnsurePlayerExists from spawning a phantom player row.
	if _, _, err := matchRepo.FindPlayerByExactName(ctx, smurfName); !errors.Is(err, domain.ErrPlayerNotFound) {
		t.Errorf("FindPlayerByExactName(%q) = %v, want ErrPlayerNotFound (no phantom player)", smurfName, err)
	}

	// Unique partial index idx_matches_lobby_match_id blocks duplicate active scoreboards for the same lobby match.
	hash2 := fmt.Sprintf("hash-r2-%d", time.Now().UnixNano())
	sig2 := fmt.Sprintf("sig-r2-%d", time.Now().UnixNano())
	if dupID, err := matchRepo.Create(ctx, models.Match{
		LobbyMatchID:   &lobbyMatchID,
		FileHash:       hash2,
		MatchSignature: sig2,
		Players: []models.PlayerResult{
			{PlayerID: pA, PlayerName: pAName, Result: "WIN", Kills: 8, Deaths: 2, Assists: 3},
			{PlayerID: pB, PlayerName: pBName, Result: "LOSE", Kills: 2, Deaths: 8, Assists: 1},
		},
	}); err == nil {
		t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM matches WHERE id = $1`, dupID) })
		t.Fatal("duplicate Create with same active lobby_match_id succeeded, want unique constraint error")
	}

	// Soft-deleting the first match frees the partial unique index for a replacement scoreboard.
	if err := matchRepo.Delete(ctx, firstMatchID); err != nil {
		t.Fatalf("Delete(firstMatchID): %v", err)
	}
	hash3 := fmt.Sprintf("hash-r3-%d", time.Now().UnixNano())
	sig3 := fmt.Sprintf("sig-r3-%d", time.Now().UnixNano())
	secondMatchID, err := matchRepo.Create(ctx, models.Match{
		LobbyMatchID:   &lobbyMatchID,
		FileHash:       hash3,
		MatchSignature: sig3,
		Players: []models.PlayerResult{
			{PlayerID: pA, PlayerName: pAName, Result: "WIN", Kills: 9, Deaths: 1, Assists: 5},
			{PlayerID: pB, PlayerName: pBName, Result: "LOSE", Kills: 1, Deaths: 9, Assists: 0},
		},
	})
	if err != nil {
		t.Fatalf("replacement Create after soft delete failed: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM matches WHERE id = $1`, secondMatchID) })

	// Alias learning and poisoning protection.
	freeAlias := uniqueName(t, "free-alias")
	if err := matchRepo.LearnAlias(ctx, pA, freeAlias, "elimination"); err != nil {
		t.Fatalf("LearnAlias(freeAlias): %v", err)
	}
	// Poisoning guard 1: cannot learn pB's canonical players.name as an alias for pA (even with case/spaces).
	if err := matchRepo.LearnAlias(ctx, pA, "  "+strings.ToUpper(pBName)+"  ", "referee"); err != nil {
		t.Fatalf("LearnAlias(canonical of pB): %v", err)
	}
	// Poisoning guard 2: pB cannot steal pA's already-learned alias (case/whitespace normalized unique index).
	if err := matchRepo.LearnAlias(ctx, pB, "  "+strings.ToUpper(freeAlias)+"  ", "referee"); err != nil {
		t.Fatalf("LearnAlias(steal freeAlias): %v", err)
	}

	aliases, err := matchRepo.GetAliasesForPlayers(ctx, []int{pA, pB})
	if err != nil {
		t.Fatalf("GetAliasesForPlayers: %v", err)
	}
	if len(aliases[pA]) != 1 || aliases[pA][0] != freeAlias {
		t.Errorf("aliases[pA] = %v, want [%q]", aliases[pA], freeAlias)
	}
	if len(aliases[pB]) != 0 {
		t.Errorf("aliases[pB] = %v, want empty (stolen alias must be ignored)", aliases[pB])
	}
}

func TestIntegrationMMRBreakdownAliasesAndMergePlayers(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	matchRepo := newMatchRepo(t, db)
	lobbyRepo := NewLobbyMatchPostgres(db)

	canonName := uniqueName(t, "canon")
	phantomName := uniqueName(t, "phantom")
	otherName := uniqueName(t, "other")

	canonID, err := matchRepo.EnsurePlayerExists(ctx, canonName)
	if err != nil {
		t.Fatalf("EnsurePlayerExists(canon): %v", err)
	}
	phantomID, err := matchRepo.EnsurePlayerExists(ctx, phantomName)
	if err != nil {
		t.Fatalf("EnsurePlayerExists(phantom): %v", err)
	}
	otherID, err := matchRepo.EnsurePlayerExists(ctx, otherName)
	if err != nil {
		t.Fatalf("EnsurePlayerExists(other): %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM players WHERE id IN ($1, $2, $3)`, canonID, phantomID, otherID)
	})

	// 1. MMR breakdown persistence + read-back.
	lmID, err := lobbyRepo.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID:    "test-guild-merge",
		CaptainAID: phantomID,
		CaptainBID: otherID,
		TeamAIDs:   []int{phantomID},
		TeamBIDs:   []int{otherID},
	})
	if err != nil {
		t.Fatalf("lobbyRepo.Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, lmID) })

	if err := lobbyRepo.UpdatePlayerMMRWithBreakdown(ctx, phantomID, 1032, lmID, 24, 5, 3); err != nil {
		t.Fatalf("UpdatePlayerMMRWithBreakdown: %v", err)
	}
	hist, err := lobbyRepo.GetMMRHistory(ctx, phantomID, 5)
	if err != nil {
		t.Fatalf("GetMMRHistory: %v", err)
	}
	if len(hist) != 1 || hist[0].BaseDelta != 24 || hist[0].KDAMod != 5 || hist[0].MedalBonus != 3 || hist[0].After != 1032 {
		t.Fatalf("GetMMRHistory(phantom) = %+v, want base=24 kda=5 medal=3 after=1032", hist)
	}

	// 2. Manual aliases CRUD & conflict rejection.
	manualAlias := uniqueName(t, "smurf-nick")
	if err := matchRepo.AddManualAlias(ctx, phantomID, manualAlias); err != nil {
		t.Fatalf("AddManualAlias: %v", err)
	}
	if err := matchRepo.AddManualAlias(ctx, canonID, manualAlias); err == nil {
		t.Error("AddManualAlias should reject alias already owned by another player")
	}
	if err := matchRepo.AddManualAlias(ctx, canonID, otherName); err == nil {
		t.Error("AddManualAlias should reject another active player's canonical name")
	}
	list, err := matchRepo.ListAliases(ctx, phantomID)
	if err != nil || len(list) != 1 || list[0].Alias != manualAlias || list[0].Source != "admin" {
		t.Fatalf("ListAliases(phantom) = %+v, err=%v", list, err)
	}
	// Delete & re-add to verify DeleteAlias.
	deleted, err := matchRepo.DeleteAlias(ctx, phantomID, "  "+strings.ToUpper(manualAlias)+"  ")
	if err != nil || !deleted {
		t.Fatalf("DeleteAlias = (%v, %v), want (true, nil)", deleted, err)
	}
	if err := matchRepo.AddManualAlias(ctx, phantomID, manualAlias); err != nil {
		t.Fatalf("re-AddManualAlias: %v", err)
	}

	// 3. Record a match on phantomID with MVP medal, then merge phantomID -> canonID.
	mID, err := matchRepo.Create(ctx, models.Match{
		FileHash:       fmt.Sprintf("hash-merge-%d", time.Now().UnixNano()),
		MatchSignature: fmt.Sprintf("sig-merge-%d", time.Now().UnixNano()),
		MVP:            phantomName,
		Players: []models.PlayerResult{
			{PlayerID: phantomID, PlayerName: phantomName, Result: "WIN", Kills: 10, Deaths: 1, Assists: 5},
			{PlayerID: otherID, PlayerName: otherName, Result: "LOSE", Kills: 1, Deaths: 10, Assists: 0},
		},
	})
	if err != nil {
		t.Fatalf("matchRepo.Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM matches WHERE id = $1`, mID) })

	// Bind conflicting Discord IDs first -> merge must refuse!
	d1 := fmt.Sprintf("disc-1-%d", time.Now().UnixNano())
	d2 := fmt.Sprintf("disc-2-%d", time.Now().UnixNano())
	if err := matchRepo.SetDiscordID(ctx, phantomID, d1); err != nil {
		t.Fatalf("SetDiscordID(phantom): %v", err)
	}
	if err := matchRepo.SetDiscordID(ctx, canonID, d2); err != nil {
		t.Fatalf("SetDiscordID(canon): %v", err)
	}
	if _, err := matchRepo.MergePlayers(ctx, phantomID, canonID); err == nil {
		t.Fatal("MergePlayers with two different discord_ids must fail")
	}
	if err := matchRepo.ClearDiscordID(ctx, canonID); err != nil {
		t.Fatalf("ClearDiscordID(canon): %v", err)
	}

	// Now merge phantomID (has discord_id d1) -> canonID (no discord_id): must move everything!
	summary, err := matchRepo.MergePlayers(ctx, phantomID, canonID)
	if err != nil {
		t.Fatalf("MergePlayers: %v", err)
	}
	if summary.MovedResults != 1 || summary.MovedMedals != 1 || summary.MovedHistory != 1 || summary.LearnedAlias != phantomName {
		t.Errorf("unexpected merge summary: %+v", summary)
	}

	// Canon inherited discord_id d1, MMR history with breakdown, both aliases, and MVP medal.
	gotDiscord, err := matchRepo.GetDiscordIDByPlayerID(ctx, canonID)
	if err != nil || gotDiscord != d1 {
		t.Errorf("canon discord_id = (%q, %v), want (%q, nil)", gotDiscord, err, d1)
	}
	canonHist, err := lobbyRepo.GetMMRHistory(ctx, canonID, 5)
	if err != nil || len(canonHist) != 1 || canonHist[0].KDAMod != 5 {
		t.Errorf("canon MMR history = %+v, err=%v", canonHist, err)
	}
	canonAliases, err := matchRepo.ListAliases(ctx, canonID)
	if err != nil || len(canonAliases) != 2 {
		t.Errorf("canon aliases = %+v (want 2: %q + %q), err=%v", canonAliases, manualAlias, phantomName, err)
	}
	medals, err := matchRepo.GetLifetimeMedals(ctx, canonID)
	if err != nil || medals.MVP != 1 {
		t.Errorf("canon lifetime medals = %+v, err=%v, want MVP=1", medals, err)
	}
	// Lobby match captain and team_a_ids now point to canonID.
	updatedLM, err := lobbyRepo.GetByID(ctx, lmID)
	if err != nil || updatedLM.CaptainAID != canonID || len(updatedLM.TeamAIDs) != 1 || updatedLM.TeamAIDs[0] != canonID {
		t.Errorf("updated lobby match = %+v, err=%v, want CaptainAID & TeamAIDs=%d", updatedLM, err, canonID)
	}
}
