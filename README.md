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

## Running

The harvester ingests Twitch IRC and/or YouTube Live chat and optionally serves the
built-in HTTP API. A typical invocation:

```bash
./harvester \ 
  -sqlite ./elora.db \ 
  -twitch-channel rifftrax -twitch-nick hp_az -twitch-token 'oauth:…' -twitch-tls=true \ 
  -youtube-url 'https://www.youtube.com/watch?v=jfKfPfyJRdk' \ 
  -http-addr :8765 \ 
  -http-cors-origins 'http://localhost:5173' \ 
  -http-rate-rps 20 -http-rate-burst 40 \ 
  -http-metrics=true -http-access-log=true
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
{ "ID": "...", "Ts": "RFC3339", "Username": "...", "Platform": "Twitch|YouTube", "Text": "...", "EmotesJSON": "...", "RawJSON": "...", "BadgesJSON": "...", "Colour": "..." }
```

`Ts` is always UTC (RFC3339 / RFC3339Nano depending on precision stored in SQLite).

## HTTP API

### REST endpoints

| Endpoint | Description |
| --- | --- |
| `GET /messages` | Returns recent messages (defaults to 100, newest first). |
| `GET /count` | Returns `{"count": N}` for the current filters. |
| `GET /info` | Build metadata (`version`, `rev`, `built_at`, `go`). |
| `GET /metrics` | Prometheus metrics (if enabled). |
| `GET /healthz` | Liveness probe (200/"ok"). |

Responses from `/messages` and `/count` are gzip-compressed when the client sends
`Accept-Encoding: gzip`.

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

These counters are useful while hammer-testing with `hey`, validating filters with `curl`,
and monitoring production deployments.

## Docker quick start

```bash
cp .env.example .env   # then edit TWITCH_TOKEN
docker compose up --build
curl -s localhost:8765/healthz
```

## SQLite tuning (optional)

Set `GN_SQLITE_TUNING=1` to apply WAL mode, adjust synchronous and busy timeout, and
increase mmap/temp_store settings on startup. The defaults remain unchanged when the
variable is unset. Compose users can flip this via `.env`.

## Integrating with elora-chat

When running under Compose, other services can connect to gnasty via
`http://gnasty:8765` on the shared network.
