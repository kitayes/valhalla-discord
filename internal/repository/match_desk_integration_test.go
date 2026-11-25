//go:build integration

package repository

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMatchDeskPostgresPersistenceRollbackAndReopen(t *testing.T) {
	db := testDB(t)
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("desk_test_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	if _, err := db.Exec(`SET search_path TO ` + schema + `, public`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/000001_init.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
 DELETE FROM telegram_tournaments;
 INSERT INTO telegram_settings VALUES ('challonge_tournament_id','123'),('challonge_tournament_for','2026-09-16T12:00:00Z');
 INSERT INTO telegram_tournaments (id,name,status,is_active,format_final) VALUES (1,'Old','completed',false,3),(2,'Current','active',true,5);
 INSERT INTO telegram_teams (id,name,status) VALUES (1,'Alpha','active'),(2,'Beta','active');
 INSERT INTO telegram_players (id,telegram_id,team_id,is_captain) VALUES (1,100,1,true),(2,200,2,true);
 INSERT INTO telegram_bracket_matches (id,tournament_id,challonge_match_id,round,play_order,team1_id,team2_id,state) VALUES (1,2,111,1,12,1,2,'open'),(2,1,99,1,3,1,2,'open');
 `)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewTelegramPostgres(db)
	ctx := context.Background()
	err = repo.UpdateMatchDesk(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		// Only the active tournament's match; the old one's open row stays out.
		if len(c.Matches) != 1 || c.Matches[0].ID != 1 || len(c.Captains) != 2 {
			t.Fatalf("snapshot: %+v", c)
		}
		if c.Tournament == nil || c.Tournament.ID != 2 || c.Tournament.FormatFinal != 5 {
			t.Fatalf("snapshot tournament: %+v", c.Tournament)
		}
		if c.Captains[1].GameID != "" {
			t.Fatal("NULL fields were not read as empty")
		}
		d.NextGeneration = 42
		d.Outbox = []models.DeskNotice{{ID: 7, ChatID: 100, Text: "hello"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	abort := errors.New("abort")
	err = repo.UpdateMatchDesk(ctx, func(d *models.MatchDesk, _ models.DeskContext) error { d.NextGeneration = 99; return abort })
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE telegram_bracket_matches SET state='complete'; UPDATE telegram_bracket_matches SET state='open'`); err != nil {
		t.Fatal(err)
	}
	// Games of the reopened match: one from before the reopen (revision 1)
	// that no longer counts, a confirmed game 1 and a disputed game 2.
	disputedAt := time.Date(2026, 9, 16, 12, 40, 0, 0, time.UTC)
	if _, err = db.Exec(`INSERT INTO telegram_match_games (bracket_match_id,match_revision,game_no,winner_team_id,reporter_telegram_id,status,created_at,resolved_at) VALUES
   (1,1,1,1,100,'confirmed','2026-09-16T13:30:00Z','2026-09-16T13:31:00Z'),
   (1,2,1,1,100,'confirmed','2026-09-16T12:20:00Z','2026-09-16T12:22:00Z'),
   (1,2,2,2,200,'disputed','2026-09-16T12:38:00Z',$1)`, disputedAt); err != nil {
		t.Fatal(err)
	}
	err = repo.UpdateMatchDesk(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		if d.NextGeneration != 42 || len(d.Outbox) != 1 {
			t.Fatalf("rollback/persistence: %+v", d)
		}
		if c.Revisions[1] != 2 {
			t.Fatalf("reopen epoch: %d", c.Revisions[1])
		}
		st := c.Series[1]
		if st.DisputedGame != 2 || !st.DisputedAt.Equal(disputedAt) || !st.LastActivity.Equal(disputedAt) {
			t.Fatalf("series snapshot = %+v, want game 2 disputed at %v", st, disputedAt)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A view reads the same desk and context and saves nothing.
	err = repo.ViewMatchDesk(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		if d.NextGeneration != 42 || len(c.Matches) != 1 || c.Series[1].DisputedGame != 2 {
			t.Fatalf("view: desk %+v, context %+v", d, c)
		}
		d.NextGeneration = 7
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = repo.UpdateMatchDesk(ctx, func(d *models.MatchDesk, _ models.DeskContext) error {
		if d.NextGeneration != 42 {
			t.Fatalf("a view saved its change: %d", d.NextGeneration)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/000001_init.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(down)); err != nil {
		t.Fatal(err)
	}
}
