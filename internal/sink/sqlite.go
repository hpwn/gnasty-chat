package sink

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
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
  platform_msg_id TEXT NOT NULL DEFAULT '',
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
	db               *sql.DB
	upsertSQL        string
	usePlatformMsgID bool
	hasPlatformMsgID bool
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
	hasPlatformMsgID, err := ensureInteropBits(context.Background(), db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	upsertSQL, usePlatformMsgID, err := pickUpsertSQL(context.Background(), db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteSink{db: db, upsertSQL: upsertSQL, usePlatformMsgID: usePlatformMsgID, hasPlatformMsgID: hasPlatformMsgID}, nil
}

func ensureInteropBits(ctx context.Context, db *sql.DB) (bool, error) {
	hasPMID, err := hasColumn(ctx, db, "messages", "platform_msg_id")
	if err != nil {
		return false, errors.Wrap(err, "inspect messages table")
	}
	if !hasPMID {
		if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN platform_msg_id TEXT`); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				return false, errors.Wrap(err, "add platform_msg_id column")
			}
		} else {
			hasPMID = true
		}
	}
	if hasPMID {
		if _, err := db.Exec(`UPDATE messages SET platform_msg_id = id WHERE (platform_msg_id IS NULL OR platform_msg_id = '') AND id IS NOT NULL AND id != '';`); err != nil {
			return false, errors.Wrap(err, "backfill platform_msg_id")
		}
		if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS messages_platform_msgid ON messages(platform, platform_msg_id);`); err != nil {
			return false, errors.Wrap(err, "ensure platform_msg_id index")
		}
	}
	return hasPMID, nil
}

func pickUpsertSQL(ctx context.Context, db *sql.DB) (string, bool, error) {
	hasPMID, err := hasColumn(ctx, db, "messages", "platform_msg_id")
	if err != nil {
		return "", false, errors.Wrap(err, "inspect messages table for platform_msg_id")
	}
	if hasPMID {
		if hasUniqueIndex(ctx, db, "messages", []string{"platform", "platform_msg_id"}) {
			return `INSERT INTO messages (id, platform_msg_id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, platform_msg_id) DO NOTHING;`, true, nil
		}
		if hasUniqueIndex(ctx, db, "messages", []string{"platform", "id"}) {
			return `INSERT INTO messages (id, platform_msg_id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, id) DO NOTHING;`, true, nil
		}
		return `INSERT INTO messages (id, platform_msg_id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING;`, true, nil
	}
	if hasUniqueIndex(ctx, db, "messages", []string{"platform", "id"}) {
		return `INSERT INTO messages (id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, id) DO NOTHING;`, false, nil
	}
	return `INSERT INTO messages (id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING;`, false, nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`);`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func hasUniqueIndex(ctx context.Context, db *sql.DB, table string, columns []string) bool {
	rows, err := db.QueryContext(ctx, `PRAGMA index_list(`+table+`);`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			seq     int
			name    string
			unique  int
			origin  string
			partial sql.NullInt64
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			continue
		}
		if unique == 0 {
			continue
		}
		ci, err := db.QueryContext(ctx, `PRAGMA index_info(`+name+`);`)
		if err != nil {
			continue
		}
		ok := true
		i := 0
		for ci.Next() {
			var (
				seqno int
				cid   int
				cname string
			)
			if err := ci.Scan(&seqno, &cid, &cname); err != nil {
				ok = false
				break
			}
			if i >= len(columns) || columns[i] != cname {
				ok = false
				break
			}
			i++
		}
		if err := ci.Err(); err != nil {
			ok = false
		}
		ci.Close()
		if ok && i == len(columns) {
			return true
		}
	}
	return false
}

func (s *SQLiteSink) Close() error { return s.db.Close() }

func (s *SQLiteSink) Write(msg core.ChatMessage) error {
	const fallback = `INSERT INTO messages (id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, id) DO NOTHING;`
	ts := msg.Ts
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	tsStr := ts.UTC().Format(time.RFC3339Nano)

	id := msg.ID
	if id == "" {
		data := fmt.Sprintf("%s|%s|%s|%s", msg.Platform, tsStr, msg.Username, msg.Text)
		sum := sha1.Sum([]byte(data))
		id = "fallback_" + hex.EncodeToString(sum[:8])
	}

	platformMsgID := msg.PlatformMsgID
	if platformMsgID == "" {
		platformMsgID = id
	}

	args := []any{id, tsStr, msg.Username, msg.Platform, msg.Text,
		nz(msg.EmotesJSON, "[]"), nz(msg.RawJSON, ""), nz(msg.BadgesJSON, "[]"), nz(msg.Colour, "")}
	query := fallback
	if s.upsertSQL != "" {
		query = s.upsertSQL
	}
	if s.usePlatformMsgID {
		args = append(args, platformMsgID)
	}

	_, err := s.db.Exec(query, args...)
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
	query, args := buildMessageQuery(filters, true, false)
	var n int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, errors.Wrap(err, "count")
	}
	return n, nil
}

func (s *SQLiteSink) ListMessages(ctx context.Context, filters httpapi.Filters) ([]core.ChatMessage, error) {
	query, args := buildMessageQuery(filters, false, s.hasPlatformMsgID)
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
		if s.hasPlatformMsgID {
			var platformMsgID sql.NullString
			if err := rows.Scan(&msg.ID, &platformMsgID, &ts, &msg.Username, &msg.Platform, &msg.Text, &msg.EmotesJSON, &msg.RawJSON, &msg.BadgesJSON, &msg.Colour); err != nil {
				return nil, errors.Wrap(err, "scan message")
			}
			if platformMsgID.Valid {
				msg.PlatformMsgID = platformMsgID.String
			}
		} else {
			if err := rows.Scan(&msg.ID, &ts, &msg.Username, &msg.Platform, &msg.Text, &msg.EmotesJSON, &msg.RawJSON, &msg.BadgesJSON, &msg.Colour); err != nil {
				return nil, errors.Wrap(err, "scan message")
			}
		}
		if msg.PlatformMsgID == "" {
			msg.PlatformMsgID = msg.ID
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

func buildMessageQuery(filters httpapi.Filters, count bool, includePlatformMsgID bool) (string, []any) {
	var builder strings.Builder
	if count {
		builder.WriteString("SELECT COUNT(*) FROM messages")
	} else {
		if includePlatformMsgID {
			builder.WriteString("SELECT id, platform_msg_id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour FROM messages")
		} else {
			builder.WriteString("SELECT id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour FROM messages")
		}
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
