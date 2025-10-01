package sink

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
)

// ApplySQLitePragmas applies optional SQLite tuning statements when enabled via the
// GN_SQLITE_TUNING environment variable. Each pragma result is logged at info
// level.
func ApplySQLitePragmas(ctx context.Context, db *sql.DB) {
	if os.Getenv("GN_SQLITE_TUNING") != "1" {
		return
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA wal_autocheckpoint=1000;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA mmap_size=268435456;",
	}

	for _, pragma := range pragmas {
		if value, err := applyPragma(ctx, db, pragma); err != nil {
			log.Printf("sqlite: pragma %s failed: %v", pragma, err)
		} else {
			log.Printf("sqlite: pragma %s => %v", pragma, value)
		}
	}
}

func applyPragma(ctx context.Context, db *sql.DB, pragma string) (any, error) {
	row := db.QueryRowContext(ctx, pragma)
	var value any
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if _, execErr := db.ExecContext(ctx, pragma); execErr != nil {
				return nil, execErr
			}
			return "ok", nil
		}
		return nil, err
	}
	return value, nil
}
