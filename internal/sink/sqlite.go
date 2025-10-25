package sink

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/pkg/errors"

	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/httpapi"
)

const schema = `CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  platform TEXT NOT NULL,
  platform_msg_id TEXT,
  ts INTEGER NOT NULL,
  username TEXT NOT NULL,
  text TEXT NOT NULL,
  emotes_json TEXT NOT NULL DEFAULT '[]',
  raw_json TEXT NOT NULL DEFAULT '',
  badges_json TEXT NOT NULL DEFAULT '[]',
  colour TEXT NOT NULL DEFAULT ''
);`

type SQLiteSink struct {
	db *sql.DB
}

const defaultListLimit = 100

func OpenSQLite(path string) (*SQLiteSink, error) {
	dsn := path
	if strings.Contains(path, "?") {
		dsn = path + "&_busy_timeout=5000&_journal_mode=wal"
	} else {
		dsn = path + "?_busy_timeout=5000&_journal_mode=wal"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, errors.Wrap(err, "open sqlite")
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, errors.Wrap(err, "apply schema")
	}
	if err := migrateLegacyMessagesTable(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureIndices(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=wal;`); err != nil {
		_ = db.Close()
		return nil, errors.Wrap(err, "set WAL")
	}
	ApplySQLitePragmas(context.Background(), db)
	return &SQLiteSink{db: db}, nil
}

func ensureIndices(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS messages_platform_msg_id
           ON messages(platform, platform_msg_id)
           WHERE platform_msg_id IS NOT NULL AND platform_msg_id != '';`,
		`CREATE UNIQUE INDEX IF NOT EXISTS messages_upsert_key
           ON messages(platform, ts, username, text);`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return errors.Wrap(err, "ensure indices")
		}
	}
	return nil
}

func (s *SQLiteSink) Close() error { return s.db.Close() }

func migrateLegacyMessagesTable(ctx context.Context, db *sql.DB) error {
	columns, err := inspectMessagesColumns(ctx, db)
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}

	idType, okID := columns["id"]
	tsType, okTS := columns["ts"]
	if !okID || !okTS {
		return nil
	}
	if strings.EqualFold(idType, "INTEGER") && strings.EqualFold(tsType, "INTEGER") {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin legacy messages migration")
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `CREATE TABLE messages_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  platform TEXT NOT NULL,
  platform_msg_id TEXT,
  ts INTEGER NOT NULL,
  username TEXT NOT NULL,
  text TEXT NOT NULL,
  emotes_json TEXT NOT NULL DEFAULT '[]',
  raw_json TEXT NOT NULL DEFAULT '',
  badges_json TEXT NOT NULL DEFAULT '[]',
  colour TEXT NOT NULL DEFAULT ''
);`); err != nil {
		return errors.Wrap(err, "create migrated messages table")
	}

	rows, qErr := tx.QueryContext(ctx, `SELECT id, platform, platform_msg_id, ts, username, text, emotes_json, raw_json, badges_json, colour FROM messages`)
	if qErr != nil {
		err = errors.Wrap(qErr, "select legacy messages")
		return err
	}
	defer rows.Close()

	insertStmt, prepErr := tx.PrepareContext(ctx, `INSERT INTO messages_new (
        platform, platform_msg_id, ts, username, text, emotes_json, raw_json, badges_json, colour
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`)
	if prepErr != nil {
		err = errors.Wrap(prepErr, "prepare migrated insert")
		return err
	}
	defer insertStmt.Close()

	for rows.Next() {
		var (
			legacyID      sql.NullString
			platform      string
			platformMsgID sql.NullString
			legacyTS      sql.NullString
			username      string
			text          string
			emotesJSON    string
			rawJSON       string
			badgesJSON    string
			colour        string
		)
		if scanErr := rows.Scan(&legacyID, &platform, &platformMsgID, &legacyTS, &username, &text, &emotesJSON, &rawJSON, &badgesJSON, &colour); scanErr != nil {
			err = errors.Wrap(scanErr, "scan legacy message")
			return err
		}

		tsMillis, convErr := legacyTimestampToMillis(legacyTS.String)
		if convErr != nil {
			err = errors.Wrap(convErr, "convert legacy timestamp")
			return err
		}

		pmid := strings.TrimSpace(platformMsgID.String)
		if pmid == "" {
			pmid = strings.TrimSpace(legacyID.String)
		}
		var platformMsgArg any
		if pmid != "" {
			platformMsgArg = pmid
		} else {
			platformMsgArg = nil
		}

		if _, execErr := insertStmt.ExecContext(ctx,
			strings.TrimSpace(platform),
			platformMsgArg,
			tsMillis,
			strings.TrimSpace(username),
			text,
			emotesJSON,
			rawJSON,
			badgesJSON,
			colour,
		); execErr != nil {
			err = errors.Wrap(execErr, "insert migrated message")
			return err
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		err = errors.Wrap(rowsErr, "iterate legacy messages")
		return err
	}

	if _, err = tx.ExecContext(ctx, `DROP TABLE messages;`); err != nil {
		err = errors.Wrap(err, "drop legacy messages table")
		return err
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE messages_new RENAME TO messages;`); err != nil {
		err = errors.Wrap(err, "rename migrated messages table")
		return err
	}

	if err = tx.Commit(); err != nil {
		return errors.Wrap(err, "commit legacy messages migration")
	}

	return nil
}

func inspectMessagesColumns(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(messages);`)
	if err != nil {
		return nil, errors.Wrap(err, "inspect messages table info")
	}
	defer rows.Close()

	columns := make(map[string]string)
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return nil, errors.Wrap(err, "scan messages table info")
		}
		columns[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(colType)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate messages table info")
	}
	return columns, nil
}

func legacyTimestampToMillis(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if strings.ContainsAny(raw, "-:T") {
		layouts := []string{time.RFC3339Nano, time.RFC3339}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed.UTC().UnixMilli(), nil
			}
		}
	}
	if numeric, err := strconv.ParseInt(raw, 10, 64); err == nil {
		const (
			nanosThreshold  = 1_000_000_000_000_000_000
			microsThreshold = 1_000_000_000_000_000
			millisThreshold = 1_000_000_000_000
		)
		switch {
		case numeric >= nanosThreshold:
			return numeric / 1_000_000, nil
		case numeric >= microsThreshold:
			return numeric / 1_000, nil
		case numeric >= millisThreshold:
			return numeric, nil
		default:
			return numeric * 1_000, nil
		}
	}
	return 0, fmt.Errorf("unrecognised legacy timestamp %q", raw)
}

func (s *SQLiteSink) Write(msg core.ChatMessage) error {
	tsMS := msg.TimestampMS
	if tsMS == 0 {
		if !msg.Ts.IsZero() {
			tsMS = msg.Ts.UTC().UnixMilli()
		} else {
			tsMS = time.Now().UTC().UnixMilli()
		}
	}

	platform := strings.TrimSpace(msg.Platform)
	username := strings.TrimSpace(msg.Username)
	text := msg.Text

	platformMsgID := strings.TrimSpace(msg.PlatformMsgID)
	if platformMsgID == "" {
		platformMsgID = strings.TrimSpace(msg.ID)
	}

	emotesJSON := jsonText(msg.EmotesJSON, msg.Emotes, "[]")
	badgesJSON := jsonText(msg.BadgesJSON, msg.Badges, "[]")
	rawJSON := jsonText(msg.RawJSON, msg.Raw, "")

	conflict := `ON CONFLICT(platform, ts, username, text) DO NOTHING`
	var platformMsgArg any
	if platformMsgID != "" {
		conflict = `ON CONFLICT(platform, platform_msg_id) DO NOTHING`
		platformMsgArg = platformMsgID
	} else {
		platformMsgArg = nil
	}

	query := fmt.Sprintf(`INSERT INTO messages (
        platform, platform_msg_id, ts, username, text, emotes_json, raw_json, badges_json, colour
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) %s;`, conflict)

	err := withRetry(func() error {
		_, execErr := s.db.Exec(query,
			platform,
			platformMsgArg,
			tsMS,
			username,
			text,
			emotesJSON,
			rawJSON,
			badgesJSON,
			msg.Colour,
		)
		return execErr
	})
	return errors.Wrap(err, "insert message")
}

func jsonText(encoded string, value any, empty string) string {
	if encoded != "" {
		return encoded
	}
	if value == nil {
		return empty
	}
	b, err := json.Marshal(value)
	if err != nil {
		return empty
	}
	return string(b)
}

func (s *SQLiteSink) Ping() error {
	return s.db.Ping()
}

func withRetry(fn func() error) error {
	const max = 5
	for i := 0; i < max; i++ {
		if err := fn(); err != nil {
			if strings.Contains(err.Error(), "SQLITE_BUSY") {
				time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("exhausted retries (SQLITE_BUSY)")
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
			msg           core.ChatMessage
			rowID         int64
			platformMsgID sql.NullString
			tsMS          int64
			emotesJSON    string
			rawJSON       string
			badgesJSON    string
			colour        string
		)
		if err := rows.Scan(
			&rowID,
			&platformMsgID,
			&tsMS,
			&msg.Username,
			&msg.Platform,
			&msg.Text,
			&emotesJSON,
			&rawJSON,
			&badgesJSON,
			&colour,
		); err != nil {
			return nil, errors.Wrap(err, "scan message")
		}
		msg.TimestampMS = tsMS
		if tsMS > 0 {
			msg.Ts = time.UnixMilli(tsMS).UTC()
		}
		if platformMsgID.Valid {
			msg.PlatformMsgID = platformMsgID.String
		}
		if msg.PlatformMsgID != "" {
			msg.ID = msg.PlatformMsgID
		} else {
			msg.ID = fmt.Sprintf("%d", rowID)
		}
		msg.EmotesJSON = emotesJSON
		msg.RawJSON = rawJSON
		msg.BadgesJSON = badgesJSON
		msg.Colour = colour
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
		builder.WriteString("SELECT id, platform_msg_id, ts, username, platform, text, emotes_json, raw_json, badges_json, colour FROM messages")
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
		args = append(args, filters.Since.UTC().UnixMilli())
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
