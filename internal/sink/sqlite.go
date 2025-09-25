package sink

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/pkg/errors"

	"github.com/you/gnasty-chat/internal/core"
)

const schema = `CREATE TABLE IF NOT EXISTS messages (
  id TEXT NOT NULL,
  ts TEXT NOT NULL,
  username TEXT NOT NULL,
  platform TEXT NOT NULL,
  text TEXT NOT NULL,
  emotes_json TEXT NOT NULL DEFAULT '[]',
  raw_json TEXT NOT NULL DEFAULT '',
  badges_json TEXT NOT NULL DEFAULT '[]',
  colour TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (platform, id)
);`

type SQLiteSink struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteSink, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, errors.Wrap(err, "open sqlite")
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, errors.Wrap(err, "apply schema")
	}
	if _, err := db.Exec(`PRAGMA journal_mode=wal;`); err != nil {
		_ = db.Close()
		return nil, errors.Wrap(err, "set WAL")
	}
	return &SQLiteSink{db: db}, nil
}

func (s *SQLiteSink) Close() error { return s.db.Close() }

func (s *SQLiteSink) Write(msg core.ChatMessage) error {
	const q = `INSERT INTO messages (id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, id) DO NOTHING;`
	ts := msg.Ts.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(q, msg.ID, ts, msg.Username, msg.Platform, msg.Text,
		nz(msg.EmotesJSON, "[]"), nz(msg.RawJSON, ""), nz(msg.BadgesJSON, "[]"), nz(msg.Colour, ""))
	return errors.Wrap(err, "insert message")
}

func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (s *SQLiteSink) Ping() error {
	return s.db.Ping()
}

func (s *SQLiteSink) Count() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages;`).Scan(&n); err != nil {
		return 0, errors.Wrap(err, "count")
	}
	return n, nil
}

// ListRecent returns up to limit messages ordered by timestamp descending.
func (s *SQLiteSink) ListRecent(limit int) ([]core.ChatMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour
FROM messages
ORDER BY ts DESC
LIMIT ?;
`, limit)
	if err != nil {
		return nil, errors.Wrap(err, "list recent")
	}
	defer rows.Close()

	var out []core.ChatMessage
	for rows.Next() {
		var (
			msg core.ChatMessage
			ts  string
		)
		if err := rows.Scan(&msg.ID, &ts, &msg.Username, &msg.Platform, &msg.Text, &msg.EmotesJSON, &msg.RawJSON, &msg.BadgesJSON, &msg.Colour); err != nil {
			return nil, errors.Wrap(err, "scan recent")
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			msg.Ts = t
		}
		out = append(out, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate recent")
	}
	return out, nil
}

func (s *SQLiteSink) String() string {
	return fmt.Sprintf("SQLiteSink{%p}", s.db)
}
