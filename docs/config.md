# gnasty-chat runtime configuration

This service reads configuration from environment variables during startup and logs a redacted
`config_summary` JSON line before joining any receivers. The same redacted snapshot is available
via `GET /configz`. Secrets such as tokens and client secrets are never logged or returned in
plain text; instead they are replaced with `***REDACTED*** (len=N)` to show the size of the value.

## Environment variables

| Name | Type | Default | Example | Redaction |
| --- | --- | --- | --- | --- |
| `GNASTY_SINKS` | string list (comma/space separated) | `sqlite` | `sqlite` | Logged verbatim |
| `GNASTY_RECEIVERS` | string list | _(alias for `GNASTY_SINKS` when that variable is unset)_ | `sqlite` | Logged verbatim |
| `GNASTY_SINK_SQLITE_PATH` | filesystem path | `chat.db` | `/data/elora.db` | Logged verbatim |
| `GNASTY_SINK_BATCH_SIZE` | integer (>0) | `1` | `50` | Logged verbatim |
| `GNASTY_SINK_FLUSH_MAX_MS` | integer milliseconds (>=0) | `0` | `250` | Logged verbatim |
| `GNASTY_TWITCH_ENABLED` | boolean | `false` (auto-enabled when channels configured) | `true` | Logged verbatim |
| `GNASTY_TWITCH_CHANNELS` | string list | _(empty)_ | `elora` | Logged verbatim |
| `GNASTY_TWITCH_NICK` | string | _(empty)_ | `elora_bot` | Logged verbatim |
| `GNASTY_TWITCH_TOKEN` | string | _(empty)_ | `oauth:xxxx` | Redacted |
| `GNASTY_TWITCH_TOKEN_FILE` | filesystem path | _(empty)_ | `/secrets/twitch_access` | Logged verbatim |
| `GNASTY_TWITCH_CLIENT_ID` | string | _(empty)_ | `abcd1234` | Redacted |
| `GNASTY_TWITCH_CLIENT_SECRET` | string | _(empty)_ | `super-secret` | Redacted |
| `GNASTY_TWITCH_REFRESH_TOKEN` | string | _(empty)_ | `refresh-xxxx` | Redacted |
| `GNASTY_TWITCH_REFRESH_TOKEN_FILE` | filesystem path | _(empty)_ | `/secrets/twitch_refresh` | Logged verbatim |
| `GNASTY_TWITCH_TLS` | boolean | `true` | `false` | Logged verbatim |
| `TWITCH_CHANNEL` | string | _(empty)_ | `elora` | Logged verbatim |
| `TWITCH_NICK` | string | _(empty)_ | `elora_bot` | Logged verbatim |
| `TWITCH_TOKEN` | string | _(empty)_ | `oauth:xxxx` | Redacted |
| `TWITCH_TOKEN_FILE` | filesystem path | _(empty)_ | `/secrets/twitch_access` | Logged verbatim |
| `TWITCH_CLIENT_ID` | string | _(empty)_ | `abcd1234` | Redacted |
| `TWITCH_CLIENT_SECRET` | string | _(empty)_ | `super-secret` | Redacted |
| `TWITCH_REFRESH_TOKEN` | string | _(empty)_ | `refresh-xxxx` | Redacted |
| `TWITCH_REFRESH_TOKEN_FILE` | filesystem path | _(empty)_ | `/secrets/twitch_refresh` | Logged verbatim |
| `TWITCH_TLS` | boolean | Inherits `true` | `false` | Logged verbatim |
| `GNASTY_YT_URL` | string URL | _(empty)_ | `https://www.youtube.com/watch?v=jfKfPfyJRdk` | Logged verbatim |
| `YOUTUBE_URL` | string URL | _(empty)_ | `https://www.youtube.com/watch?v=jfKfPfyJRdk` | Logged verbatim |

Values listed as "Redacted" are replaced with `***REDACTED*** (len=N)` in logs and the `/configz`
endpoint. All other values are emitted verbatim.

## SQLite storage

When the SQLite sink is enabled (`sqlite` listed in `GNASTY_SINKS`), gnasty-chat writes to the path
configured via `GNASTY_SINK_SQLITE_PATH`. In containerised deployments it must share the same named
volume as `elora-chat` (for example `elora_data:/data`) so that both services interact with the same
`elora.db` file.

Batching behaviour is controlled via `GNASTY_SINK_BATCH_SIZE` and `GNASTY_SINK_FLUSH_MAX_MS`. The
process writes immediately when the batch size is reached or when the flush interval elapses,
whichever happens first. Set the batch size to `1` or the flush interval to `0` to write each
message synchronously.
