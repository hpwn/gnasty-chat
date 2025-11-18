# gnasty-chat

Portable chat harvester for Twitch (IRC) and YouTube Live (Innertube polling).
- Single Go binary (portable; suitable for Electron bundling later).
- Writes directly to SQLite using the Elora-style schema.
- Streams live messages over HTTP (REST, SSE, and WebSocket).

## Build

The repository targets Go 1.23+ and pins `modernc.org/sqlite` to **v1.38.2** to keep
CGO-free builds working consistently. To compile:

```bash
# fetch deps (tidy keeps the sqlite pin in place)
go mod tidy

# standard build
go build ./...

# optional: omit SQLite load_extension support if your environment forbids it
go build -tags 'sqlite_omit_load_extension' ./cmd/harvester
```

### Build `harvester` in Docker (pinned Go)

```bash
# from repo root
docker buildx build --platform=linux/amd64 \
  --build-arg VERSION=$(git describe --tags --always 2>/dev/null || echo dev) \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_TIME=$(date -u +%FT%TZ) \
  -f Dockerfile.pr21 -t gnasty-chat:pr21 --load .

# Optionally tag as latest for local compose stacks that reference :latest
docker tag gnasty-chat:pr21 gnasty-chat:latest
```

**Hot-swap into elora-chat**

```bash
cd ~/Documents/dev/elora-chat
docker compose up -d --force-recreate --no-deps gnasty-harvester

# sanity checks
docker logs -n 200 elora-gnasty-harvester-1
curl -fsS http://localhost:9400/healthz
```

## Running

The harvester ingests Twitch IRC and/or YouTube Live chat and optionally serves the
built-in HTTP API. A typical invocation:

Twitch authentication accepts either a static token or a file that rotates in place:

- Static flag: `-twitch-token oauth:<access_token>` (the `oauth:` prefix is optional).
- Token file: `-twitch-token-file /run/secrets/twitch_token` (the file may contain the
  raw token or the `oauth:` form; the harvester polls every ~10s and reconnects when it
  changes).

Required IRC scopes:

- `chat:read`
- `chat:edit`

Helix scopes are unused.

### Automatic Twitch token refresh

Provide a Twitch refresh token plus app credentials to let gnasty-chat fetch new IRC
tokens automatically. On startup the harvester exchanges the refresh token for a fresh
`oauth:<access_token>`, writes it to the token file (mode `0600`), and reconnects the IRC
session whenever a new token lands.

| Flag | Environment variable | Required with refresh | Description |
| --- | --- | --- | --- |
| `-twitch-client-id` | `TWITCH_CLIENT_ID` | ✅ | Twitch application client ID. |
| `-twitch-client-secret` | `TWITCH_CLIENT_SECRET` | ✅ | Twitch application client secret. |
| `-twitch-refresh-token` | `TWITCH_REFRESH_TOKEN` | ✅* | Refresh token. Use `-twitch-refresh-token-file` to load from disk instead. |
| `-twitch-refresh-token-file` | `TWITCH_REFRESH_TOKEN_FILE` | Optional | Path to a file containing the refresh token. Takes precedence over the inline flag/env. |
| `-twitch-token-file` | `TWITCH_TOKEN_FILE` | ✅ | Location for the IRC token (`oauth:<access_token>`) written after every refresh. |

"Required" means the values must be supplied via flag or env when enabling refresh. You may
still combine the refresh flow with manual rotations - gnasty will watch the token file
and reconnect when it changes. For production cutovers where you want the new token to
apply immediately, call `POST /admin/twitch/reload` right after writing the updated
credentials.

Security notes:

- Secrets are never logged. Refresh failures only report generic error messages.
- The token file is rewritten with permissions `0600` and fsync'd on every refresh.
- On authentication failures Twitch IRC forces an immediate refresh with bounded
  backoff; no manual restart is required.

### Bring-up with Authorization Code

Use the standard Authorization Code grant to provision both the IRC access token and
its refresh token:

1. Direct a browser to the Twitch authorize endpoint (replace the placeholders and keep
   the redirect URL exactly as registered with Twitch):

   ```
   https://id.twitch.tv/oauth2/authorize?client_id=<client_id>&redirect_uri=<urlencoded_redirect_uri>&response_type=code&scope=chat:read+chat:edit&state=<random_nonce>
   ```

2. After Twitch redirects back with `?code=...`, exchange it for tokens:

   ```bash
   curl -sS -X POST 'https://id.twitch.tv/oauth2/token' \
     -d client_id="$TWITCH_CLIENT_ID" \
     -d client_secret="$TWITCH_CLIENT_SECRET" \
     -d code="$AUTH_CODE" \
     -d grant_type=authorization_code \
     -d redirect_uri="$TWITCH_REDIRECT_URI" \
     | tee twitch-oauth.json
   ```

3. Populate the files that gnasty-chat watches for IRC and refresh credentials (the
   `oauth:` prefix is required for IRC connections):

   ```bash
   mkdir -p /data
   jq -r '.access_token' twitch-oauth.json | sed 's/^/oauth:/' > /data/twitch_irc.pass
   jq -r '.refresh_token' twitch-oauth.json > /data/twitch_refresh.pass
   chmod 600 /data/twitch_irc.pass /data/twitch_refresh.pass
   ```

Future refreshes can reuse `/data/twitch_refresh.pass`; gnasty-chat will automatically
rotate `/data/twitch_irc.pass` as long as the refresh flow is configured.

```bash
./harvester \
  -sqlite /data/gnasty.db \
  -twitch-channel rifftrax -twitch-nick hp_az -twitch-token-file /run/secrets/twitch_token -twitch-tls=true \
  -youtube-url 'https://youtube.com/@yourchannel/live' \
  -http-addr :8765 \
  -http-cors-origins 'http://localhost:5173' \
  -http-rate-rps 20 -http-rate-burst 40 \
  -http-metrics=true -http-access-log=true
```

In the docker-compose setup we bind-mount `./data:/data` for both services. gnasty-chat writes
`/data/gnasty.db` by default while elora-chat continues to write `/data/elora.db`. If you prefer the
legacy single-database flow, point gnasty at `/data/elora.db` instead so both services share the same
file.

When `GNASTY_YT_URL` (or `-youtube-url`) is set the resolver accepts both the
legacy direct watch link (`https://www.youtube.com/watch?v=...`) and the modern
channel handle form (`https://youtube.com/@creator/live`). Handles are
automatically normalized to their live endpoint before the resolver polls the
page, which keeps links short and resilient to vanity domain changes.

The YouTube poller continuously re-resolves the configured URL every
`GNASTY_YT_RETRY_SECS` (default 30 seconds). When the channel is offline gnasty
logs the backoff and waits for the next retry; when a new broadcast starts the
harvester tears down the old poller (if any) and attaches to the latest live
watch URL automatically. Typical log lines look like:

```
ytlive: resolved watch=https://www.youtube.com/watch?v=abc123 chat=https://www.youtube.com/live_chat?v=abc123 live=true
ytlive: live stream changed to https://www.youtube.com/watch?v=abc123
ytlive: channel https://youtube.com/@yourchannel/live not live, backing off 30s
```

Additional HTTP controls:

| Flag | Default | Description |
| --- | --- | --- |
| `-http-cors-origins` | `""` | Comma-separated list of allowed origins (empty disables CORS). |
| `-http-rate-rps` | `20` | Requests-per-second token bucket per client IP. |
| `-http-rate-burst` | `40` | Burst size for the rate limiter. |
| `-http-metrics` | `true` | Expose Prometheus metrics on `/metrics`. |
| `-http-access-log` | `true` | Emit structured access logs. |
| `-http-pprof` | `false` | Enable Go `pprof` handlers under `/debug/pprof/*`. |

## Message schema

All transports return the same JSON payload:

```json
{
  "ID": "...",
  "PlatformMsgID": "...",
  "Ts": "RFC3339",
  "TimestampMS": 1700176192345,
  "Username": "...",
  "Platform": "Twitch|YouTube",
  "Text": "...",
  "EmotesJSON": "...",
  "RawJSON": "...",
  "badges": [
    { "platform": "Twitch", "id": "broadcaster", "version": "1" }
  ],
  "badges_raw": { "twitch": { "badges": "...", "badge_info": "..." } },
  "BadgesJSON": "...",
  "Colour": "..."
}
```

`badges` is an optional structured list of normalized badges (platform/id/version)
and `badges_raw` (also optional) carries the underlying platform payload used to
compute the normalized list. gnasty-chat does not emit custom badge art or
fallback images.

### Badge metadata passthrough

- `badges` contains normalized entries with `platform`, `id`, and `version` so
  downstream renderers can look up official art and metadata.
- `badges_raw` mirrors the source platform payload for debugging and parity
  checks; its shape matches the platform responses and may change without a
  schema bump.
- `EmotesJSON`, `RawJSON`, and `BadgesJSON` contain the raw strings stored in
  SQLite and forwarded over REST/SSE/WS. The structured `Emotes`/`Raw` fields in
  `core.ChatMessage` are currently unused and not serialized by the transports.
- Badge image resolution happens downstream (e.g., overlay/UI) using the
  platform's official Twitch or YouTube endpoints. gnasty-chat intentionally
  avoids embedding image URLs or shipping custom fallbacks.

`Ts` is always UTC (RFC3339 / RFC3339Nano depending on precision stored in SQLite).

## HTTP API

### REST endpoints

| Endpoint | Description |
| --- | --- |
| `GET /messages` | Returns recent messages (defaults to 100, newest first). |
| `GET /count` | Returns `{"count": N}` for the current filters. |
| `GET /info` | Build metadata (`version`, `rev`, `built_at`, `go`). |
| `GET /metrics` | Prometheus metrics (if enabled). |
| `GET /healthz` | JSON liveness probe with sink reachability. |
| `GET /configz` | Effective configuration snapshot (secrets redacted). |
| `POST /admin/twitch/reload` | Forces the Twitch IRC client to reload the token file (if present) and reconnect immediately. |

Responses from `/messages` and `/count` are gzip-compressed when the client sends
`Accept-Encoding: gzip`.

#### `POST /admin/twitch/reload`

- **Method:** `POST`
- **Response:**

  ```json
  { "status": "ok", "reloaded": true, "login": "streamer" }
  ```

- **Usage:** Trigger a manual reconnect after rotating the Twitch IRC token on disk, or when testing new credentials in staging.
  The endpoint is intended for trusted operators and should be called from automation (e.g. deploy hooks) or secure shells.

When the harvester runs with `-twitch-token-file`, it already watches the file for changes and reconnects automatically. `POST
/admin/twitch/reload` lets you force the reload path immediately instead of waiting for the next poll.

Example queries:

```bash
# grab three recent Twitch messages from users containing "ochr" in the last 5 minutes
curl -s 'http://localhost:8765/messages?limit=3&platform=twitch&username=ochr&since=5m' | jq .

# count only YouTube messages
curl -s 'http://localhost:8765/count?platform=youtube' | jq .

# oldest message in the window
curl -s 'http://localhost:8765/messages?order=asc&limit=1' | jq '.[0].Ts'
```

### Live streaming

| Endpoint | Notes |
| --- | --- |
| `GET /stream` | Server-Sent Events (heartbeat every ~25s, drops slow clients). |
| `GET /ws` | WebSocket (JSON frames, ping every 30s). |

Both transports accept the same query filters as `/messages` (documented below), so
you can connect to a subset of the live firehose:

```bash
# WebSocket stream filtered to Twitch messages from usernames containing "foo"
npx wscat -c 'ws://localhost:8765/ws?platform=twitch&username=foo'

# SSE stream for only YouTube chat
curl -N 'http://localhost:8765/stream?platform=youtube'
```

### Query filters

| Parameter | Description |
| --- | --- |
| `platform` | Accepts `twitch`, `tw`, `youtube`, `yt`, or `all` (comma-separated or repeated). Maps to canonical `Twitch`/`YouTube`. |
| `username` | Case-insensitive substring match; may appear multiple times or comma-separated. |
| `since` | RFC3339 timestamp, RFC3339Nano, UNIX seconds, or duration (e.g. `5m`, `2h`). |
| `limit` | Max rows (default `100`, cap `1000`). |
| `order` | `desc` (default) or `asc` for chronological order. |

The same filters apply to `/messages`, `/count`, `/stream`, and `/ws`.

## Operations & observability

- **Rate limiting:** per-client-IP token bucket (defaults: 20 req/s, burst 40). Exceeding the
  budget yields HTTP 429 responses and increments the `gnasty_http_rate_limited_total` metric.
- **CORS:** enabled when `-http-cors-origins` is non-empty. Requests from disallowed origins
  receive HTTP 403. Preflight requests are answered automatically.
- **Access logging:** when enabled, every request logs method, path, status, duration,
  response bytes, remote IP, and user-agent.
- **Prometheus metrics:** exposed at `/metrics` when `-http-metrics=true`. Key series include
  `gnasty_http_requests_total`, `gnasty_http_request_duration_seconds`,
  `gnasty_ws_clients`, `gnasty_sse_clients`, `gnasty_messages_sent_total`,
  `gnasty_broadcast_drops_total`, and `gnasty_db_write_errors_total`.
- **pprof:** enable `-http-pprof` to serve `/debug/pprof/*` for live profiling.
- **Manual Twitch reloads:** `POST /admin/twitch/reload` forces the IRC client to reread the
  token file immediately. Use this in deployment hooks after rotating credentials when you
  cannot wait for the built-in file watcher (~10s poll) to detect the change.

These counters are useful while hammer-testing with `hey`, validating filters with `curl`,
and monitoring production deployments.

## Docker quick start

Need to mint fresh Twitch tokens? See [Bring-up with Authorization Code](#bring-up-with-authorization-code).

```bash
cp .env.example .env   # then edit Twitch credentials + sqlite path
docker compose up --build
curl -s localhost:8765/healthz | jq .
```

## SQLite tuning (optional)

Set `GN_SQLITE_TUNING=1` to apply WAL mode, adjust synchronous and busy timeout, and
increase mmap/temp_store settings on startup. The defaults remain unchanged when the
variable is unset. Compose users can flip this via `.env`.

## SQLite maintenance

The harvester runs a self-healing migration on startup that fills in missing
columns, normalises legacy `NULL` JSON blobs, and enforces
`UNIQUE(platform, platform_msg_id)` for reliable upserts. You can run the same
steps manually (for CI or offline maintenance) with:

```bash
./scripts/sqlite-migrate.sh             # defaults to ./data/elora.db
./scripts/sqlite-migrate.sh custom.db   # relative to ./data
```

The helper mounts `./data` when it exists so the migration touches the same
database that Compose uses. When `./data` is absent it falls back to the
currently running `gnasty-harvester` container volume.

## Integrating with elora-chat

When running under Compose, other services can connect to gnasty via
`http://gnasty:8765` on the shared network. See [`docs/config.md`](docs/config.md)
for environment variables and shared volume guidance.
