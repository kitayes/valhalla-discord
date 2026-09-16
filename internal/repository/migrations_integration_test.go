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
		"telegram_teams",
		"telegram_players",
		"telegram_match_reports",
		"telegram_bracket_matches",
		"telegram_match_desks",
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
		"telegram_teams":           {"status", "challonge_participant_id"},
		"telegram_match_reports":   {"bracket_match_id", "synced_at"},
		"telegram_bracket_matches": {"challonge_match_id", "both_notified"},
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
