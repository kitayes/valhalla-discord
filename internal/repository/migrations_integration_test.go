//go:build integration

package repository

import (
	"database/sql"
	"os"
	"testing"

	"blackwatch/migrations"
)

// This test fails when a clean database can no longer reach the schema the
// tournament runtime expects (for example, an invalid migration or a missing
// pgvector extension).
func TestIntegrationMigrationsFromScratch(t *testing.T) {
	dsn := os.Getenv("BW_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("BW_MIGRATION_TEST_DSN is not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	var existing sql.NullString
	if err := db.QueryRow(`SELECT to_regclass('public.schema_migrations')`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing.Valid {
		t.Fatalf("migration test database is not empty: %s already exists", existing.String)
	}
	if err := RunMigrations(db, migrations.FS); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"players",
		"player_aliases",
		"matches",
		"player_results",
		"lobby_matches",
		"match_bets",
		"currency_ledger",
		"shop_items",
		"shop_orders",
		"mmr_history",
		"queue_bans",
		"discord_servers",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !exists {
			t.Errorf("migration did not create %s", table)
		}
	}

	for table, columns := range map[string][]string{
		"players":        {"discord_id", "current_mmr", "points", "xp", "level", "profile_title", "profile_color", "bet_insurance_count"},
		"lobby_matches":  {"betting_open", "betting_closes_at", "discord_channel_id", "discord_message_id"},
		"match_bets":     {"player_id", "insured", "settled_at", "payout"},
		"matches":        {"lobby_match_id", "mvp_player_id", "svp_player_id"},
		"player_aliases": {"player_id", "alias", "source"},
		"mmr_history":    {"base_delta", "kda_mod", "medal_bonus"},
	} {
		for _, column := range columns {
			var exists bool
			if err := db.QueryRow(`SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
			)`, table, column).Scan(&exists); err != nil {
				t.Fatalf("check %s.%s: %v", table, column, err)
			}
			if !exists {
				t.Errorf("migration did not create %s.%s", table, column)
			}
		}
	}
}
