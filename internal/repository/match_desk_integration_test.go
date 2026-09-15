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
	if _, err := db.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`
 CREATE TABLE telegram_settings (key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE telegram_teams (id INT PRIMARY KEY,name TEXT NOT NULL,status TEXT NOT NULL);
 CREATE TABLE telegram_players (id INT PRIMARY KEY,telegram_id BIGINT,telegram_username TEXT,game_nickname TEXT,game_id TEXT,zone_id TEXT,team_id INT,is_captain BOOLEAN);
 CREATE TABLE telegram_bracket_matches (id INT PRIMARY KEY,challonge_match_id BIGINT,play_order INT,team1_id INT,team2_id INT,state TEXT);
 INSERT INTO telegram_settings VALUES ('challonge_tournament_id','123'),('challonge_tournament_for','2026-09-16T12:00:00Z');
 INSERT INTO telegram_teams VALUES (1,'Alpha','active'),(2,'Beta','active');
 INSERT INTO telegram_players (id,telegram_id,team_id,is_captain) VALUES (1,100,1,true),(2,200,2,true);
 INSERT INTO telegram_bracket_matches VALUES (1,111,12,1,2,'open');
 `)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/000028_match_desk.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	repo := NewTelegramPostgres(db)
	ctx := context.Background()
	err = repo.UpdateMatchDesk(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		if len(c.Matches) != 1 || len(c.Captains) != 2 {
			t.Fatalf("snapshot: %+v", c)
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
	err = repo.UpdateMatchDesk(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		if d.NextGeneration != 42 || len(d.Outbox) != 1 {
			t.Fatalf("rollback/persistence: %+v", d)
		}
		if c.Revisions[1] != 2 {
			t.Fatalf("reopen epoch: %d", c.Revisions[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/000028_match_desk.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(down)); err != nil {
		t.Fatal(err)
	}
}
