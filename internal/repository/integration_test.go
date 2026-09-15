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
	// nil embedding client: the constructor only warms the name cache, and the
	// tests below never take the fuzzy-match path that would use it.
	repo, err := NewMatchPostgres(context.Background(), db, 1000, nil)
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
	telegramID := int64(900000000 + playerID)

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

	t.Run("telegram round trip", func(t *testing.T) {
		// The column the betting code resolves a bettor through. Nothing wrote
		// to it before, so this path has never run.
		if err := repo.SetTelegramID(ctx, playerID, telegramID); err != nil {
			t.Fatalf("SetTelegramID: %v", err)
		}
		gotID, gotName, err := repo.GetPlayerByTelegramID(ctx, telegramID)
		if err != nil {
			t.Fatalf("GetPlayerByTelegramID: %v", err)
		}
		if gotID != playerID || gotName != name {
			t.Errorf("got (%d, %q), want (%d, %q)", gotID, gotName, playerID, name)
		}
	})

	t.Run("second profile cannot take the same telegram account", func(t *testing.T) {
		otherID, err := repo.EnsurePlayerExists(ctx, uniqueName(t, "ident-other"))
		if err != nil {
			t.Fatalf("EnsurePlayerExists: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM players WHERE id = $1`, otherID) })

		err = repo.SetTelegramID(ctx, otherID, telegramID)
		if !errors.Is(err, ErrTelegramIDTaken) {
			t.Errorf("SetTelegramID on a taken account = %v, want ErrTelegramIDTaken", err)
		}
	})

	t.Run("clearing releases the account", func(t *testing.T) {
		if err := repo.ClearTelegramID(ctx, playerID); err != nil {
			t.Fatalf("ClearTelegramID: %v", err)
		}
		if _, _, err := repo.GetPlayerByTelegramID(ctx, telegramID); !errors.Is(err, domain.ErrPlayerNotFound) {
			t.Errorf("lookup after clear = %v, want ErrPlayerNotFound", err)
		}
	})
}

// seedBettor creates a player with points and a Telegram binding.
func seedBettor(t *testing.T, db *sql.DB, repo *MatchPostgres, points int) (playerID int, tgID int64) {
	t.Helper()
	ctx := context.Background()
	id, err := repo.EnsurePlayerExists(ctx, uniqueName(t, "bettor"))
	if err != nil {
		t.Fatalf("EnsurePlayerExists: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM players WHERE id = $1`, id) })

	tg := int64(910000000 + id)
	if err := repo.SetTelegramID(ctx, id, tg); err != nil {
		t.Fatalf("SetTelegramID: %v", err)
	}
	if _, err := db.Exec(`UPDATE players SET points = $1 WHERE id = $2`, points, id); err != nil {
		t.Fatalf("seed points: %v", err)
	}
	return id, tg
}

func TestIntegrationBettingLifecycle(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)

	// The bettors are deliberately not on the roster: staking on a match you
	// are playing in is refused, so a test that seeds one of the players as the
	// bettor is testing a bet that can no longer be placed.
	rosterA, _ := seedBettor(t, db, matchRepo, 0)
	rosterB, _ := seedBettor(t, db, matchRepo, 0)
	_, winnerTG := seedBettor(t, db, matchRepo, 100)
	_, loserTG := seedBettor(t, db, matchRepo, 100)

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
		err := bets.PlaceBet(ctx, models.PlaceBetRequest{
			MatchID: matchID, TgUserID: winnerTG, TeamChosen: domain.TeamA, Amount: 10,
		})
		if !errors.Is(err, domain.ErrBettingClosed) {
			t.Errorf("PlaceBet before OpenBetting = %v, want ErrBettingClosed", err)
		}
	})

	if err := lobby.OpenBetting(ctx, matchID, time.Hour); err != nil {
		t.Fatalf("OpenBetting: %v", err)
	}

	t.Run("bets land once the window is open", func(t *testing.T) {
		for _, b := range []struct {
			tg   int64
			team string
			amt  int
		}{
			{winnerTG, domain.TeamA, 40},
			{loserTG, domain.TeamB, 60},
		} {
			if err := bets.PlaceBet(ctx, models.PlaceBetRequest{
				MatchID: matchID, TgUserID: b.tg, TeamChosen: b.team, Amount: b.amt,
			}); err != nil {
				t.Fatalf("PlaceBet(%d): %v", b.tg, err)
			}
		}
	})

	t.Run("one bet per user per match", func(t *testing.T) {
		err := bets.PlaceBet(ctx, models.PlaceBetRequest{
			MatchID: matchID, TgUserID: winnerTG, TeamChosen: domain.TeamB, Amount: 5,
		})
		if !errors.Is(err, domain.ErrAlreadyBet) {
			t.Errorf("second bet = %v, want ErrAlreadyBet", err)
		}
	})

	t.Run("stake is deducted", func(t *testing.T) {
		got, err := bets.GetPlayerPoints(ctx, winnerTG)
		if err != nil {
			t.Fatalf("GetPlayerPoints: %v", err)
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
		payouts, err := bets.PayoutWinners(ctx, matchID, domain.TeamA)
		if err != nil {
			t.Fatalf("PayoutWinners: %v", err)
		}
		if got, want := payouts[winnerTG], 100; got != want {
			t.Errorf("winner was paid %d, want the whole %d pool", got, want)
		}
		points, err := bets.GetPlayerPoints(ctx, winnerTG)
		if err != nil {
			t.Fatalf("GetPlayerPoints: %v", err)
		}
		if points != 160 {
			t.Errorf("winner holds %d points, want 160 (60 left + 100 pool)", points)
		}
	})

	t.Run("payout is idempotent", func(t *testing.T) {
		payouts, err := bets.PayoutWinners(ctx, matchID, domain.TeamA)
		if err != nil {
			t.Fatalf("PayoutWinners (replay): %v", err)
		}
		if len(payouts) != 0 {
			t.Errorf("a settled match paid out again: %v", payouts)
		}
		points, err := bets.GetPlayerPoints(ctx, winnerTG)
		if err != nil {
			t.Fatalf("GetPlayerPoints: %v", err)
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

	// Bettors sit outside the roster — see TestIntegrationBettingLifecycle.
	rosterA, _ := seedBettor(t, db, matchRepo, 0)
	rosterB, _ := seedBettor(t, db, matchRepo, 0)
	_, aTG := seedBettor(t, db, matchRepo, 100)
	_, bTG := seedBettor(t, db, matchRepo, 100)

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
		tg  int64
		amt int
	}{{aTG, 25}, {bTG, 35}} {
		if err := bets.PlaceBet(ctx, models.PlaceBetRequest{
			MatchID: matchID, TgUserID: b.tg, TeamChosen: domain.TeamA, Amount: b.amt,
		}); err != nil {
			t.Fatalf("PlaceBet: %v", err)
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
	for tg, want := range map[int64]int{aTG: 100, bTG: 100} {
		got, err := bets.GetPlayerPoints(ctx, tg)
		if err != nil {
			t.Fatalf("GetPlayerPoints(%d): %v", tg, err)
		}
		if got != want {
			t.Errorf("telegram %d holds %d points after refund, want %d", tg, got, want)
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

	pID, pTG := seedBettor(t, db, matchRepo, 100)
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

	err = bets.PlaceBet(ctx, models.PlaceBetRequest{
		MatchID: matchID, TgUserID: pTG, TeamChosen: domain.TeamA, Amount: 10,
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

	aID, _ := seedBettor(t, db, matchRepo, 0)
	bID, _ := seedBettor(t, db, matchRepo, 0)

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

func TestIntegrationProfileLinkFlow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	links := NewProfileLinkPostgres(db)

	name := uniqueName(t, "link")
	playerID, err := matchRepo.EnsurePlayerExists(ctx, name)
	if err != nil {
		t.Fatalf("EnsurePlayerExists: %v", err)
	}
	tgID := int64(920000000 + playerID)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM profile_links WHERE discord_player_id = $1`, playerID)
		_, _ = db.Exec(`DELETE FROM players WHERE id = $1`, playerID)
	})

	code, err := links.CreateLinkCode(ctx, playerID)
	if err != nil {
		t.Fatalf("CreateLinkCode: %v", err)
	}
	if code == "" {
		t.Fatal("CreateLinkCode returned an empty code")
	}

	gotID, err := links.ValidateLinkCode(ctx, code)
	if err != nil {
		t.Fatalf("ValidateLinkCode: %v", err)
	}
	if gotID != playerID {
		t.Fatalf("code resolved to player %d, want %d", gotID, playerID)
	}

	if err := links.CreateProfileLink(ctx, &models.ProfileLink{
		DiscordPlayerID:  playerID,
		TelegramID:       &tgID,
		TelegramUsername: "tester",
	}); err != nil {
		t.Fatalf("CreateProfileLink: %v", err)
	}

	t.Run("both lookup directions agree", func(t *testing.T) {
		byPlayer, err := links.GetLinkByDiscordPlayer(ctx, playerID)
		if err != nil || byPlayer == nil {
			t.Fatalf("GetLinkByDiscordPlayer = (%v, %v)", byPlayer, err)
		}
		byTG, err := links.GetLinkByTelegramID(ctx, tgID)
		if err != nil || byTG == nil {
			t.Fatalf("GetLinkByTelegramID = (%v, %v)", byTG, err)
		}
		if byPlayer.DiscordPlayerID != byTG.DiscordPlayerID {
			t.Errorf("the two directions disagree: %d vs %d", byPlayer.DiscordPlayerID, byTG.DiscordPlayerID)
		}
	})

	t.Run("telegram profile data updates", func(t *testing.T) {
		if err := links.UpdateTelegramProfile(ctx, tgID, "ник", "gid", "zid", 5, "mid"); err != nil {
			t.Fatalf("UpdateTelegramProfile: %v", err)
		}
		got, err := links.GetLinkByTelegramID(ctx, tgID)
		if err != nil {
			t.Fatalf("GetLinkByTelegramID: %v", err)
		}
		if got.GameNickname != "ник" || got.Stars != 5 {
			t.Errorf("profile = (%q, %d stars), want (ник, 5)", got.GameNickname, got.Stars)
		}
	})

	t.Run("unlink clears the row", func(t *testing.T) {
		if err := links.DeleteLinkByDiscordPlayer(ctx, playerID); err != nil {
			t.Fatalf("DeleteLinkByDiscordPlayer: %v", err)
		}
		got, err := links.GetLinkByDiscordPlayer(ctx, playerID)
		if err != nil {
			t.Fatalf("GetLinkByDiscordPlayer after delete: %v", err)
		}
		if got != nil {
			t.Error("the link survived deletion")
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

	playerID, playerTG := seedBettor(t, db, matchRepo, 100)
	outsiderID, outsiderTG := seedBettor(t, db, matchRepo, 100)

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
		err := bets.PlaceBet(ctx, models.PlaceBetRequest{
			MatchID: matchID, TgUserID: playerTG, TeamChosen: domain.TeamB, Amount: 10,
		})
		if !errors.Is(err, domain.ErrBettingOnOwnMatch) {
			t.Errorf("PlaceBet by a participant = %v, want ErrBettingOnOwnMatch", err)
		}
		// The refusal must not have moved anything.
		points, err := bets.GetPlayerPoints(ctx, playerTG)
		if err != nil {
			t.Fatalf("GetPlayerPoints: %v", err)
		}
		if points != 100 {
			t.Errorf("a refused bet changed the balance to %d, want 100", points)
		}
	})

	t.Run("somebody outside the match may bet", func(t *testing.T) {
		if err := bets.PlaceBet(ctx, models.PlaceBetRequest{
			MatchID: matchID, TgUserID: outsiderTG, TeamChosen: domain.TeamA, Amount: 10,
		}); err != nil {
			t.Errorf("PlaceBet by a non-participant: %v", err)
		}
		_ = outsiderID
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

// The coefficients shown in Telegram are computed from BetPool, and the money
// is moved by PayoutWinners. This pins the two together: whatever BetPool
// reports must be the pool settlement actually distributes.
func TestIntegrationBetPoolTracksTheLiveMarket(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)

	// Bettors sit outside the roster — see TestIntegrationBettingLifecycle.
	rosterA, _ := seedBettor(t, db, matchRepo, 0)
	rosterB, _ := seedBettor(t, db, matchRepo, 0)
	_, backerA := seedBettor(t, db, matchRepo, 100)
	_, backerB := seedBettor(t, db, matchRepo, 100)

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
		tg     int64
		team   string
		amount int
	}{
		{backerA, domain.TeamA, 40},
		{backerB, domain.TeamB, 10},
	} {
		if err := bets.PlaceBet(ctx, models.PlaceBetRequest{
			MatchID: matchID, TgUserID: b.tg, TeamChosen: b.team, Amount: b.amount,
		}); err != nil {
			t.Fatalf("PlaceBet %d on %s: %v", b.amount, b.team, err)
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

		payouts, err := bets.PayoutWinners(ctx, matchID, domain.TeamA)
		if err != nil {
			t.Fatalf("PayoutWinners: %v", err)
		}
		if payouts[backerA] != quoted {
			t.Errorf("paid %d but the post advertised %d", payouts[backerA], quoted)
		}
	})

	// closePost relies on this: a settled match must stop reporting a market, or
	// the post would keep quoting odds on money that has already moved.
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

// The betting post is edited after every bet and closed out at settlement, which
// can land after a restart — so where it lives has to outlive the process.
func TestIntegrationBetPostReferenceSurvives(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	matchRepo := newMatchRepo(t, db)
	lobby := NewLobbyMatchPostgres(db)

	rosterA, _ := seedBettor(t, db, matchRepo, 0)
	rosterB, _ := seedBettor(t, db, matchRepo, 0)

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "itest", CaptainAID: rosterA, CaptainBID: rosterB,
		TeamAIDs: []int{rosterA}, TeamBIDs: []int{rosterB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM lobby_matches WHERE id = $1`, matchID) })

	t.Run("a match with no post is not an error", func(t *testing.T) {
		_, _, ok, err := lobby.GetBetPost(ctx, matchID)
		if err != nil {
			t.Fatalf("GetBetPost before publishing: %v", err)
		}
		if ok {
			t.Error("reported a post for a match that has none")
		}
	})

	if err := lobby.SaveBetPost(ctx, matchID, -1001234567890, 4242); err != nil {
		t.Fatalf("SaveBetPost: %v", err)
	}

	t.Run("the reference round-trips", func(t *testing.T) {
		chatID, messageID, ok, err := lobby.GetBetPost(ctx, matchID)
		if err != nil {
			t.Fatalf("GetBetPost: %v", err)
		}
		if !ok {
			t.Fatal("saved post not reported")
		}
		// Channel ids are negative and outside int32 — a narrower column would
		// silently mangle them.
		if chatID != -1001234567890 || messageID != 4242 {
			t.Errorf("GetBetPost = (%d, %d), want (-1001234567890, 4242)", chatID, messageID)
		}
	})

	t.Run("an unknown match is refused, not answered with zeros", func(t *testing.T) {
		_, _, _, err := lobby.GetBetPost(ctx, -1)
		if !errors.Is(err, domain.ErrMatchNotFound) {
			t.Errorf("GetBetPost on a missing match = %v, want ErrMatchNotFound", err)
		}
	})
}

// Disbanding a team must leave nothing behind: the captain used to keep
// is_captain = TRUE (and so kept receiving captain broadcasts), their FSM state
// was left mid-registration, and the roster rows — which have no telegram_id —
// were merely detached and then surfaced in the solo-players list.
func TestIntegrationDisbandTeamCleansUpRoster(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewTelegramPostgres(db)

	captainTG := int64(930000000 + os.Getpid()%100000)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM telegram_players WHERE telegram_id = $1`, captainTG)
	})

	team, err := repo.CreateTeam(ctx, uniqueName(t, "disband"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM telegram_players WHERE team_id = $1`, team.ID)
		_, _ = db.Exec(`DELETE FROM telegram_teams WHERE id = $1`, team.ID)
	})

	if err := repo.CreateOrUpdatePlayer(ctx, &models.TelegramPlayer{TelegramID: &captainTG, TelegramUsername: "cap", FirstName: "Cap"}); err != nil {
		t.Fatalf("CreateOrUpdatePlayer: %v", err)
	}
	for col, val := range map[string]interface{}{"team_id": team.ID, "is_captain": true, "fsm_state": "team_reg_nick_3", "main_role": "Mid"} {
		if err := repo.UpdatePlayerField(ctx, captainTG, col, val); err != nil {
			t.Fatalf("UpdatePlayerField(%s): %v", col, err)
		}
	}
	mateNick := uniqueName(t, "mate")
	for i := 0; i < 2; i++ {
		if err := repo.CreateTeammate(ctx, &models.TelegramPlayer{TeamID: &team.ID, GameNickname: mateNick, MainRole: "Gold"}); err != nil {
			t.Fatalf("CreateTeammate: %v", err)
		}
	}

	if err := repo.ReleaseTeamMembers(ctx, team.ID); err != nil {
		t.Fatalf("ReleaseTeamMembers: %v", err)
	}
	if err := repo.DeleteTeam(ctx, team.ID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}

	cap, err := repo.GetPlayerByTelegramID(ctx, captainTG)
	if err != nil || cap == nil {
		t.Fatalf("GetPlayerByTelegramID = (%v, %v)", cap, err)
	}
	if cap.TeamID != nil || cap.IsCaptain || cap.FSMState != models.StateIdle {
		t.Errorf("captain after disband: team=%v captain=%v state=%q, want detached/idle", cap.TeamID, cap.IsCaptain, cap.FSMState)
	}

	captains, err := repo.GetAllCaptains(ctx)
	if err != nil {
		t.Fatalf("GetAllCaptains: %v", err)
	}
	for _, c := range captains {
		if c.TelegramID != nil && *c.TelegramID == captainTG {
			t.Error("former captain still listed in the broadcast list")
		}
	}

	solo, err := repo.GetSoloPlayers(ctx)
	if err != nil {
		t.Fatalf("GetSoloPlayers: %v", err)
	}
	for _, p := range solo {
		if p.GameNickname == mateNick {
			t.Error("orphaned roster row surfaced as a solo player")
		}
	}

	var orphans int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telegram_players WHERE game_nickname = $1`, mateNick).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d roster rows without a telegram account survived the disband, want 0", orphans)
	}
}

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

func TestIntegrationFindByGameID(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewTelegramPostgres(db)

	team, err := repo.CreateTeam(ctx, uniqueName(t, "dup"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM telegram_players WHERE team_id = $1`, team.ID)
		_, _ = db.Exec(`DELETE FROM telegram_teams WHERE id = $1`, team.ID)
	})
	gameID := uniqueName(t, "9")[:12]
	if err := repo.CreateTeammate(ctx, &models.TelegramPlayer{TeamID: &team.ID, GameNickname: "A", GameID: gameID}); err != nil {
		t.Fatalf("CreateTeammate: %v", err)
	}
	if err := repo.CreateTeammate(ctx, &models.TelegramPlayer{TeamID: &team.ID, GameNickname: "B", GameID: gameID + "x"}); err != nil {
		t.Fatalf("CreateTeammate: %v", err)
	}

	got, err := repo.FindByGameID(ctx, gameID)
	if err != nil {
		t.Fatalf("FindByGameID: %v", err)
	}
	if len(got) != 1 || got[0].GameNickname != "A" || got[0].TeamID == nil || *got[0].TeamID != team.ID {
		t.Errorf("FindByGameID = %+v, want exactly the row A in team %d", got, team.ID)
	}
	if none, err := repo.FindByGameID(ctx, "no-such-id"); err != nil || len(none) != 0 {
		t.Errorf("unknown id = (%v, %v), want empty", none, err)
	}
}

func TestIntegrationTeamStatusRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewTelegramPostgres(db)

	team, err := repo.CreateTeam(ctx, uniqueName(t, "status"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM telegram_teams WHERE id = $1`, team.ID) })

	got, err := repo.GetTeamByID(ctx, team.ID)
	if err != nil || got.Status != models.TeamStatusActive {
		t.Fatalf("fresh team status = (%q, %v), want active", got.Status, err)
	}
	if err := repo.SetTeamStatus(ctx, team.ID, models.TeamStatusDisqualified); err != nil {
		t.Fatalf("SetTeamStatus: %v", err)
	}
	byName, err := repo.GetTeamByName(ctx, team.Name)
	if err != nil || byName.Status != models.TeamStatusDisqualified {
		t.Errorf("GetTeamByName status = (%q, %v)", byName.Status, err)
	}
	all, err := repo.GetAllTeams(ctx)
	if err != nil {
		t.Fatalf("GetAllTeams: %v", err)
	}
	found := false
	for _, tm := range all {
		if tm.ID == team.ID {
			found = true
			if tm.Status != models.TeamStatusDisqualified {
				t.Errorf("GetAllTeams status = %q", tm.Status)
			}
		}
	}
	if !found {
		t.Error("team missing from GetAllTeams")
	}
}

func TestIntegrationNullableTelegramPlayer(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewTelegramPostgres(db)

	tgID := int64(999888777)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM telegram_players WHERE telegram_id = $1`, tgID)
	})

	// Create fresh player just like /start does - game_nickname, game_id, zone_id, etc. are NULL
	p := &models.TelegramPlayer{
		TelegramID:       &tgID,
		TelegramUsername: "test_null_scan",
		FirstName:        "TestNull",
	}
	if err := repo.CreateOrUpdatePlayer(ctx, p); err != nil {
		t.Fatalf("CreateOrUpdatePlayer: %v", err)
	}

	got, err := repo.GetPlayerByTelegramID(ctx, tgID)
	if err != nil {
		t.Fatalf("GetPlayerByTelegramID with NULL fields failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetPlayerByTelegramID returned nil player")
	}
	if got.GameNickname != "" || got.GameID != "" || got.ZoneID != "" || got.Stars != 0 || got.MainRole != "" {
		t.Errorf("expected zero values for unset fields, got nick=%q id=%q zone=%q stars=%d role=%q",
			got.GameNickname, got.GameID, got.ZoneID, got.Stars, got.MainRole)
	}
}
