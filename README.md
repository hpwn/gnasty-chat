# gnasty-chat

Portable chat harvester for Twitch (IRC) and YouTube Live (Innertube polling).
- Single Go binary (portable; suitable for Electron bundling later).
- Writes directly to SQLite using Elora-style schema.
- Can also evolve to emit NDJSON for pipes/tools.

## Quick build
```bash
go mod tidy
go build ./...
./harvester -h
```

## Next steps (planned)

* Implement Twitch IRC receive loop with reconnect/backoff.
* Implement YouTube Innertube polling with continuations & backoff.
* Exactly-once inserts via UNIQUE(platform,id).
* Config via flags/env; sidecar or library mode for elora-chat.
