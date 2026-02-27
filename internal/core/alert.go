package core

import "time"

// AlertEvent is a normalized non-chat event emitted by providers.
type AlertEvent struct {
	ID              string    `json:"id"`
	Platform        string    `json:"platform"` // "Twitch" | "YouTube"
	Type            string    `json:"type"`     // e.g. "twitch.subs"
	Ts              time.Time `json:"ts"`
	TimestampMS     int64     `json:"timestamp_ms,omitempty"`
	PlatformEventID string    `json:"platform_event_id,omitempty"`
	Username        string    `json:"username,omitempty"`
	Text            string    `json:"text,omitempty"`
	Amount          float64   `json:"amount,omitempty"`
	Currency        string    `json:"currency,omitempty"`
	Count           int       `json:"count,omitempty"`
	RawJSON         string    `json:"raw_json,omitempty"`
	MetaJSON        string    `json:"meta_json,omitempty"`
}
