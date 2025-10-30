#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB_ARG="${1:-elora.db}"
SQLITE_IMAGE="${SQLITE_IMAGE:-nouchka/sqlite3}"

if ! command -v docker >/dev/null 2>&1; then
        echo "error: docker is required to run sqlite migrations" >&2
        exit 1
fi

if [[ "$DB_ARG" = /* ]]; then
        DB_TARGET="$DB_ARG"
else
        DB_TARGET="/data/$DB_ARG"
fi

if [[ -d "$ROOT/data" ]]; then
        echo "sqlite-migrate: using data directory $ROOT/data" >&2
        VOLUME=(-v "$ROOT/data:/data")
else
        container_id="$(docker compose ps -q gnasty-harvester 2>/dev/null || true)"
        if [[ -z "$container_id" ]]; then
                echo "error: ./data directory missing and gnasty-harvester container not running" >&2
                exit 1
        fi
        echo "sqlite-migrate: using volumes from gnasty-harvester container $container_id" >&2
        VOLUME=(--volumes-from "$container_id")
fi

table_count="$(docker run --rm "${VOLUME[@]}" "$SQLITE_IMAGE" "$DB_TARGET" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND lower(name)='messages';" | tr -d '\r')"
if [[ "${table_count:-0}" -eq 0 ]]; then
        echo "sqlite-migrate: messages table missing; nothing to do" >&2
        exit 0
fi

colour_count="$(docker run --rm "${VOLUME[@]}" "$SQLITE_IMAGE" "$DB_TARGET" "SELECT COUNT(*) FROM pragma_table_info('messages') WHERE lower(name)='colour';" | tr -d '\r')"
if [[ "${colour_count:-0}" -eq 0 ]]; then
        docker run --rm "${VOLUME[@]}" "$SQLITE_IMAGE" "$DB_TARGET" "ALTER TABLE messages ADD COLUMN colour TEXT NOT NULL DEFAULT '';"
        echo "sqlite-migrate: added colour column" >&2
fi

docker run --rm -i "${VOLUME[@]}" "$SQLITE_IMAGE" "$DB_TARGET" <<'SQL'
PRAGMA foreign_keys=OFF;
BEGIN;
UPDATE messages SET raw_json='' WHERE raw_json IS NULL;
UPDATE messages SET emotes_json='[]' WHERE emotes_json IS NULL;
UPDATE messages SET badges_json='[]' WHERE badges_json IS NULL;
DELETE FROM messages
WHERE rowid NOT IN (
  SELECT MIN(rowid)
  FROM messages
  GROUP BY platform, platform_msg_id
);
CREATE UNIQUE INDEX IF NOT EXISTS messages_uq_platform_msg
        ON messages(platform, platform_msg_id);
COMMIT;
SQL
