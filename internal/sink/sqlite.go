package sink

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/pkg/errors"

	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/httpapi"
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

const defaultListLimit = 100

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
	ApplySQLitePragmas(context.Background(), db)
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

func (s *SQLiteSink) String() string {
	return fmt.Sprintf("SQLiteSink{%p}", s.db)
}

func (s *SQLiteSink) CountMessages(ctx context.Context, filters httpapi.Filters) (int64, error) {
	query, args := buildMessageQuery(filters, true)
	var n int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, errors.Wrap(err, "count")
	}
	return n, nil
}

func (s *SQLiteSink) ListMessages(ctx context.Context, filters httpapi.Filters) ([]core.ChatMessage, error) {
	query, args := buildMessageQuery(filters, false)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrap(err, "list messages")
	}
	defer rows.Close()

	var out []core.ChatMessage
	for rows.Next() {
		var (
			msg core.ChatMessage
			ts  string
		)
		if err := rows.Scan(&msg.ID, &ts, &msg.Username, &msg.Platform, &msg.Text, &msg.EmotesJSON, &msg.RawJSON, &msg.BadgesJSON, &msg.Colour); err != nil {
			return nil, errors.Wrap(err, "scan message")
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			msg.Ts = t
		}
		out = append(out, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate messages")
	}
	return out, nil
}

func buildMessageQuery(filters httpapi.Filters, count bool) (string, []any) {
	var builder strings.Builder
	if count {
		builder.WriteString("SELECT COUNT(*) FROM messages")
	} else {
		builder.WriteString("SELECT id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour FROM messages")
	}

	var (
		conditions []string
		args       []any
	)

	if len(filters.Platforms) > 0 {
		placeholders := make([]string, 0, len(filters.Platforms))
		for _, p := range filters.Platforms {
			placeholders = append(placeholders, "?")
			args = append(args, p)
		}
		conditions = append(conditions, fmt.Sprintf("platform IN (%s)", strings.Join(placeholders, ",")))
	}

	if len(filters.Usernames) > 0 {
		ors := make([]string, 0, len(filters.Usernames))
		for _, u := range filters.Usernames {
			ors = append(ors, "LOWER(username) LIKE '%' || ? || '%'")
			args = append(args, u)
		}
		conditions = append(conditions, fmt.Sprintf("(%s)", strings.Join(ors, " OR ")))
	}

	if filters.Since != nil {
		conditions = append(conditions, "ts >= ?")
		args = append(args, filters.Since.UTC().Format(time.RFC3339Nano))
	}

	if len(conditions) > 0 {
		builder.WriteString(" WHERE ")
		builder.WriteString(strings.Join(conditions, " AND "))
	}

	if !count {
		order := "DESC"
		if filters.Order == httpapi.OrderAsc {
			order = "ASC"
		}
		builder.WriteString(" ORDER BY ts ")
		builder.WriteString(order)
		limit := filters.Limit
		if limit <= 0 {
			limit = defaultListLimit
		}
		builder.WriteString(" LIMIT ?")
		args = append(args, limit)
	}

	builder.WriteString(";")
	return builder.String(), args
}
