package core

import "time"

// ChatMessage is the unified structure written to SQLite (and usable for NDJSON).
type ChatMessage struct {
	ID         string    // platform-native message ID (or composed)
	Ts         time.Time // message timestamp
	Username   string
	Platform   string // "Twitch" | "YouTube"
	Text       string
	EmotesJSON string // optional: JSON-encoded emote list
	RawJSON    string // optional: raw source payload for debugging/exports
	BadgesJSON string // optional
	Colour     string // optional (e.g., Twitch)
}
