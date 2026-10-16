//go:build loadtest

package application

// A load test of the match desk against a real Postgres, as the Mini App
// drives it during a tournament: every player polls the active match every
// six seconds, an SSE broadcast makes every open app fetch at once, the bot
// worker delivers the outbox, and the teams press ready.
//
//	BW_LOAD_DSN="host=127.0.0.1 port=55432 user=postgres password=bwtest dbname=bwload sslmode=disable" \
//	  go test -tags loadtest -run TestDeskLoad -v -timeout 10m ./internal/application/
//
// LOAD_TEAMS (32), LOAD_SECONDS (40), LOAD_MAXCONNS (0: unlimited; the bot
// caps its pool with REPO_DB_MAX_OPEN_CONNS, 20 by default) tune it. The
// database is wiped and seeded.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"blackwatch/internal/repository"
	"blackwatch/migrations"

	_ "github.com/lib/pq"
)

const (
	loadPlayersPerTeam = 5
	loadPollEvery      = 6 * time.Second  // the Mini App's polling interval
	loadHerdEvery      = 10 * time.Second // a bracket_update reaching every app
)

func loadEnvInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func loadTg(team, i int) int64 { return int64(100000 + team*10 + i) }

func seedLoad(t *testing.T, db *sql.DB, teams int, start time.Time) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	exec(`TRUNCATE telegram_match_desks, telegram_match_games, telegram_match_reports, telegram_bracket_matches,
		telegram_tournament_teams, telegram_tournaments, telegram_players, telegram_teams RESTART IDENTITY CASCADE`)
	var tid int
	if err := db.QueryRow(`INSERT INTO telegram_tournaments (name, status, is_active, tournament_time, challonge_id, challonge_for, format_rounds)
		VALUES ('Load', 'active', TRUE, $1, 777, $1, 3) RETURNING id`, start).Scan(&tid); err != nil {
		t.Fatalf("seed tournament: %v", err)
	}
	exec(`DELETE FROM telegram_settings WHERE key IN ('challonge_tournament_id','challonge_tournament_for')`)
	exec(`INSERT INTO telegram_settings (key, value) VALUES ('challonge_tournament_id','777'), ('challonge_tournament_for',$1)`,
		start.UTC().Format(time.RFC3339))
	ids := make([]int, teams+1)
	for team := 1; team <= teams; team++ {
		if err := db.QueryRow(`INSERT INTO telegram_teams (name, is_checked_in, status) VALUES ($1, TRUE, 'active') RETURNING id`,
			fmt.Sprintf("Load %02d", team)).Scan(&ids[team]); err != nil {
			t.Fatalf("seed team: %v", err)
		}
		exec(`INSERT INTO telegram_tournament_teams (tournament_id, team_id, is_checked_in, status) VALUES ($1, $2, TRUE, 'checked_in')`, tid, ids[team])
		for i := 0; i < loadPlayersPerTeam; i++ {
			exec(`INSERT INTO telegram_players (telegram_id, telegram_username, game_nickname, game_id, zone_id, is_captain, team_id)
				VALUES ($1, $2, $2, $3, '1001', $4, $5)`, loadTg(team, i), fmt.Sprintf("p%d_%d", team, i), fmt.Sprintf("%d", 5000000+team*10+i), i == 0, ids[team])
		}
	}
	// Round 1 open, the rest of a single-elimination bracket pending.
	order, cm := 0, int64(900000)
	for round, n := 1, teams/2; n >= 1; round, n = round+1, n/2 {
		for k := 0; k < n; k++ {
			order++
			cm++
			if round == 1 {
				exec(`INSERT INTO telegram_bracket_matches (tournament_id, challonge_match_id, round, play_order, team1_id, team2_id, state)
					VALUES ($1, $2, 1, $3, $4, $5, 'open')`, tid, cm, order, ids[2*k+1], ids[2*k+2])
			} else {
				exec(`INSERT INTO telegram_bracket_matches (tournament_id, challonge_match_id, round, play_order, state)
					VALUES ($1, $2, $3, $4, 'pending')`, tid, cm, round, order)
			}
		}
	}
}

// loadStats collects latencies of one kind of call.
type loadStats struct {
	mu     sync.Mutex
	name   string
	lat    []time.Duration
	errors map[string]int
}

func newLoadStats(name string) *loadStats {
	return &loadStats{name: name, errors: map[string]int{}}
}

func (s *loadStats) observe(start time.Time, err error) {
	d := time.Since(start)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		msg := err.Error()
		if len(msg) > 80 {
			msg = msg[:80]
		}
		s.errors[msg]++
		return
	}
	s.lat = append(s.lat, d)
}

func (s *loadStats) line() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	lat := append([]time.Duration(nil), s.lat...)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	q := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(p*float64(len(lat)-1))].Round(time.Millisecond)
	}
	nerr := 0
	for _, n := range s.errors {
		nerr += n
	}
	out := fmt.Sprintf("%-22s ok %6d  err %5d  p50 %7v  p95 %7v  p99 %7v  max %7v", s.name, len(lat), nerr, q(0.5), q(0.95), q(0.99), q(1))
	for msg, n := range s.errors {
		out += fmt.Sprintf("\n    %4d x %s", n, msg)
	}
	return out
}

func TestDeskLoad(t *testing.T) {
	dsn := os.Getenv("BW_LOAD_DSN")
	if dsn == "" {
		t.Skip("BW_LOAD_DSN is not set")
	}
	teams := loadEnvInt("LOAD_TEAMS", 32)
	duration := time.Duration(loadEnvInt("LOAD_SECONDS", 40)) * time.Second
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := repository.RunMigrations(db, migrations.FS); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	start := time.Now().Add(-time.Minute)
	seedLoad(t, db, teams, start)
	if n := loadEnvInt("LOAD_MAXCONNS", 0); n > 0 {
		db.SetMaxOpenConns(n)
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	desk := NewMatchDeskService(repository.NewTelegramPostgres(db), []int64{1}, time.UTC)
	if err := desk.Tick(ctx); err != nil {
		t.Fatalf("first tick: %v", err)
	}

	poll := newLoadStats("poll /api/match/active")
	herd := newLoadStats("poll after SSE herd")
	ready := newLoadStats("press ready")
	worker := newLoadStats("worker per notice")
	var wg sync.WaitGroup

	var players []int64
	for team := 1; team <= teams; team++ {
		for i := 0; i < loadPlayersPerTeam; i++ {
			players = append(players, loadTg(team, i))
		}
	}

	// Every open Mini App polls on its own phase.
	for _, tg := range players {
		wg.Add(1)
		go func(tg int64) {
			defer wg.Done()
			timer := time.NewTimer(time.Duration(rand.Int63n(int64(loadPollEvery))))
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				t0 := time.Now()
				_, err := desk.GetMatchDeskDetail(ctx, tg)
				if ctx.Err() == nil {
					poll.observe(t0, err)
				}
				timer.Reset(loadPollEvery)
			}
		}(tg)
	}

	// A bracket update reaches every app at once.
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(loadHerdEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			var herdWG sync.WaitGroup
			for _, tg := range players {
				herdWG.Add(1)
				go func(tg int64) {
					defer herdWG.Done()
					t0 := time.Now()
					_, err := desk.GetMatchDeskDetail(ctx, tg)
					if ctx.Err() == nil {
						herd.observe(t0, err)
					}
				}(tg)
			}
			herdWG.Wait()
		}
	}()

	// The bot worker delivers the outbox the way flushMatchDesk does: three
	// desk transactions per notice and a pause between sends.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			notices, err := desk.Pending(ctx)
			if err != nil {
				continue
			}
			for _, n := range notices {
				if ctx.Err() != nil {
					return
				}
				t0 := time.Now()
				_, err := desk.NoticeCurrent(ctx, n)
				if err == nil {
					err = desk.Acknowledge(ctx, n)
				}
				if ctx.Err() == nil {
					worker.observe(t0, err)
				}
				time.Sleep(50 * time.Millisecond)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}()

	// Every team presses ready once, from a random player at a random time.
	for team := 1; team <= teams; team++ {
		wg.Add(1)
		go func(team int) {
			defer wg.Done()
			at := time.Duration(rand.Int63n(int64(duration * 8 / 10)))
			select {
			case <-ctx.Done():
				return
			case <-time.After(at):
			}
			t0 := time.Now()
			err := desk.ReadyCaptain(ctx, loadTg(team, rand.Intn(loadPlayersPerTeam)))
			if ctx.Err() == nil {
				ready.observe(t0, err)
			}
		}(team)
	}

	wg.Wait()
	var open int
	_ = db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()`).Scan(&open)
	var size int
	_ = db.QueryRow(`SELECT octet_length(data::text) FROM telegram_match_desks LIMIT 1`).Scan(&size)
	t.Logf("teams %d, players %d, %v, max open conns %d, connections at the end %d, desk json %d bytes",
		teams, len(players), duration, db.Stats().MaxOpenConnections, open, size)
	t.Logf("db pool: waits for a connection %d, wait %v", db.Stats().WaitCount, db.Stats().WaitDuration.Round(time.Millisecond))
	for _, s := range []*loadStats{poll, herd, ready, worker} {
		t.Log(s.line())
	}
	if strings.Contains(poll.line()+herd.line()+ready.line(), "too many clients") {
		t.Error("Postgres ran out of connections")
	}
}
