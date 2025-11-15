package core

import "time"

// ChatBadge represents a normalized badge awarded to a chat participant.
// Platform identifies the source (e.g., Twitch), ID is the badge slug, and
// Version corresponds to the platform's version tag (if any).
type ChatBadge struct {
	Platform string `json:"platform,omitempty"`
	ID       string `json:"id,omitempty"`
	Version  string `json:"version,omitempty"`
}

// BadgesRaw carries the raw platform-specific badge payload, when available.
type BadgesRaw map[string]any

// ChatMessage is the unified structure written to SQLite (and usable for NDJSON).
type ChatMessage struct {
	ID            string    // platform-native message ID (or composed)
	PlatformMsgID string    // optional: dedicated platform message ID when ID is rewritten
	Ts            time.Time // message timestamp
	TimestampMS   int64     // optional: timestamp in epoch milliseconds
	Username      string
	Platform      string // "Twitch" | "YouTube"
	Text          string
	EmotesJSON    string      // optional: JSON-encoded emote list
	Emotes        any         // optional: structured emote payload
	RawJSON       string      // optional: raw source payload for debugging/exports
	Raw           any         // optional: structured raw payload
	BadgesJSON    string      // optional
	Badges        []ChatBadge `json:"badges,omitempty"`
	BadgesRaw     BadgesRaw   `json:"badges_raw,omitempty"`
	Colour        string      // optional (e.g., Twitch)
}
