package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateSQLite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	schema := `CREATE TABLE messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  platform TEXT NOT NULL,
  platform_msg_id TEXT,
  ts INTEGER NOT NULL,
  username TEXT NOT NULL,
  text TEXT NOT NULL,
  emotes_json TEXT,
  raw_json TEXT,
  badges_json TEXT
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	seed := `INSERT INTO messages (platform, platform_msg_id, ts, username, text, emotes_json, raw_json, badges_json)
VALUES
  ('twitch', 'abc', 1, 'alice', 'hello', NULL, NULL, NULL),
  ('twitch', 'abc', 1, 'alice', 'hello again', NULL, NULL, NULL),
  ('youtube', NULL, 2, 'bob', 'hi', NULL, NULL, NULL);
`
	if _, err := db.Exec(seed); err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	if err := migrateSQLite(context.Background(), db); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	// colour column exists and has default
	cols, err := sqliteTableInfo(context.Background(), db, "messages")
	if err != nil {
		t.Fatalf("inspect columns: %v", err)
	}
	colour, ok := cols["colour"]
	if !ok {
		t.Fatalf("expected colour column to exist")
	}
	if !colour.NotNull || colour.DefaultText == "" {
		t.Fatalf("expected colour column to be NOT NULL with default, got %+v", colour)
	}

	// ensure duplicates trimmed to single row
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE platform='twitch' AND platform_msg_id='abc';`).Scan(&count); err != nil {
		t.Fatalf("count duplicates: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 twitch row after dedupe, got %d", count)
	}

	// ensure NULLs replaced
	var nulls int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE raw_json IS NULL OR emotes_json IS NULL OR badges_json IS NULL;`).Scan(&nulls); err != nil {
		t.Fatalf("count nulls: %v", err)
	}
	if nulls != 0 {
		t.Fatalf("expected no NULL json columns, got %d", nulls)
	}

	// index enforces uniqueness
	if _, err := db.Exec(`INSERT INTO messages (platform, platform_msg_id, ts, username, text, emotes_json, raw_json, badges_json, colour)
VALUES ('twitch', 'abc', 3, 'carol', 'later', '[]', '', '[]', '');`); err == nil {
		t.Fatalf("expected unique index to prevent duplicate insert")
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db file: %v", err)
	}
}
