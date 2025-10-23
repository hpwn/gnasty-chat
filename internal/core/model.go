package core

import "time"

// ChatMessage is the unified structure written to SQLite (and usable for NDJSON).
type ChatMessage struct {
	ID            string    // platform-native message ID (or composed)
	PlatformMsgID string    // optional: dedicated platform message ID when ID is rewritten
	Ts            time.Time // message timestamp
	TimestampMS   int64     // optional: timestamp in epoch milliseconds
	Username      string
	Platform      string // "Twitch" | "YouTube"
	Text          string
	EmotesJSON    string // optional: JSON-encoded emote list
	Emotes        any    // optional: structured emote payload
	RawJSON       string // optional: raw source payload for debugging/exports
	Raw           any    // optional: structured raw payload
	BadgesJSON    string // optional
	Badges        any    // optional: structured badge payload
	Colour        string // optional (e.g., Twitch)
}
