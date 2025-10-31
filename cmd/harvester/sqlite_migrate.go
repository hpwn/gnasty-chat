package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
)

type sqliteColumn struct {
	Name        string
	Type        string
	NotNull     bool
	DefaultText string
}

func migrateSQLite(ctx context.Context, db *sql.DB) error {
	path := sqlitePath(ctx, db)
	userVersion, err := sqliteUserVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("sqlite: user_version: %w", err)
	}

	log.Printf("harvester: sqlite: path=%s user_version=%d", path, userVersion)

	columns, err := sqliteTableInfo(ctx, db, "messages")
	if err != nil {
		return fmt.Errorf("sqlite: describe messages: %w", err)
	}
	if len(columns) == 0 {
		log.Printf("harvester: sqlite: messages table missing; skipping migration")
		return nil
	}

	if _, ok := columns["colour"]; !ok {
		if _, err := db.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN colour TEXT NOT NULL DEFAULT '';`); err != nil {
			return fmt.Errorf("sqlite: ensure colour column: %w", err)
		}
		log.Printf("harvester: sqlite: added colour column to messages")
	}

	normalize := []struct {
		query string
		label string
	}{
		{`UPDATE messages SET raw_json='' WHERE raw_json IS NULL;`, "raw_json"},
		{`UPDATE messages SET emotes_json='[]' WHERE emotes_json IS NULL;`, "emotes_json"},
		{`UPDATE messages SET badges_json='[]' WHERE badges_json IS NULL;`, "badges_json"},
	}
	for _, step := range normalize {
		res, execErr := db.ExecContext(ctx, step.query)
		if execErr != nil {
			return fmt.Errorf("sqlite: normalize %s: %w", step.label, execErr)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			log.Printf("harvester: sqlite: normalized %s nulls=%d", step.label, n)
		}
	}

    dedupeSQL := `DELETE FROM messages
WHERE platform_msg_id IS NOT NULL
  AND TRIM(platform_msg_id) != ''
  AND rowid NOT IN (
    SELECT MIN(rowid)
    FROM messages
    WHERE platform_msg_id IS NOT NULL
      AND TRIM(platform_msg_id) != ''
    GROUP BY platform, platform_msg_id
);`
	if res, execErr := db.ExecContext(ctx, dedupeSQL); execErr != nil {
		return fmt.Errorf("sqlite: dedupe platform/platform_msg_id: %w", execErr)
	} else if n, err := res.RowsAffected(); err == nil && n > 0 {
		log.Printf("harvester: sqlite: removed %d duplicate messages", n)
	}

	if _, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS messages_uq_platform_msg
        ON messages(platform, platform_msg_id);`); err != nil {
		return fmt.Errorf("sqlite: ensure messages_uq_platform_msg: %w", err)
	}

	columns, err = sqliteTableInfo(ctx, db, "messages")
	if err != nil {
		return fmt.Errorf("sqlite: refresh messages schema: %w", err)
	}

	hasColour := false
	if _, ok := columns["colour"]; ok {
		hasColour = true
	}

	hasIndex, err := sqliteHasIndex(ctx, db, "messages", "messages_uq_platform_msg")
	if err != nil {
		return fmt.Errorf("sqlite: inspect indices: %w", err)
	}

	nullCounts := make(map[string]int64)
	for _, field := range []string{"raw_json", "emotes_json", "badges_json"} {
		var count int64
		if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM messages WHERE %s IS NULL;", field)).Scan(&count); err != nil {
			return fmt.Errorf("sqlite: count null %s: %w", field, err)
		}
		nullCounts[field] = count
	}

	log.Printf("harvester: sqlite: colour_column=%v messages_uq_platform_msg=%v raw_json_nulls=%d emotes_json_nulls=%d badges_json_nulls=%d",
		hasColour,
		hasIndex,
		nullCounts["raw_json"],
		nullCounts["emotes_json"],
		nullCounts["badges_json"],
	)

	return nil
}

func sqlitePath(ctx context.Context, db *sql.DB) string {
	rows, err := db.QueryContext(ctx, `PRAGMA database_list;`)
	if err != nil {
		return "(unknown)"
	}
	defer rows.Close()

	for rows.Next() {
		var (
			seq  int
			name string
			file sql.NullString
		)
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "(unknown)"
		}
		if strings.EqualFold(strings.TrimSpace(name), "main") {
			if file.Valid && strings.TrimSpace(file.String) != "" {
				return file.String
			}
			return "(memory)"
		}
	}
	if err := rows.Err(); err != nil {
		return "(unknown)"
	}
	return "(unknown)"
}

func sqliteUserVersion(ctx context.Context, db *sql.DB) (int, error) {
	var userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version;`).Scan(&userVersion); err != nil {
		return 0, err
	}
	return userVersion, nil
}

func sqliteTableInfo(ctx context.Context, db *sql.DB, table string) (map[string]sqliteColumn, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s);`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]sqliteColumn)
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
			return nil, err
		}
		lower := strings.ToLower(strings.TrimSpace(name))
		out[lower] = sqliteColumn{
			Name:        name,
			Type:        strings.TrimSpace(colType),
			NotNull:     notNull == 1,
			DefaultText: strings.TrimSpace(defaultVal.String),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sqliteHasIndex(ctx context.Context, db *sql.DB, table, index string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA index_list('%s');`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			seq     int
			name    string
			unique  int
			origin  string
			partial int
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if strings.EqualFold(strings.TrimSpace(name), index) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}
