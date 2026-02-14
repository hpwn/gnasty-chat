package twitchirc

import (
	"strings"
	"testing"
)

func TestSummarizeIRC(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		command string
		channel string
		sample  string
	}{
		{
			name:    "ping keeps trailing",
			raw:     "PING :tmi.twitch.tv",
			command: "PING",
			sample:  "tmi.twitch.tv",
		},
		{
			name:    "roomstate includes channel and sample",
			raw:     "@emote-only=0;followers-only=-1;room-id=123 :tmi.twitch.tv ROOMSTATE #chan",
			command: "ROOMSTATE",
			channel: "#chan",
			sample:  "#chan",
		},
		{
			name:    "userstate channel and trailing",
			raw:     "@badges=;display-name=bot :tmi.twitch.tv USERSTATE #chan",
			command: "USERSTATE",
			channel: "#chan",
			sample:  "#chan",
		},
		{
			name:    "notice trailing text",
			raw:     "@msg-id=msg_channel_suspended :tmi.twitch.tv NOTICE #chan :This channel has been suspended.",
			command: "NOTICE",
			channel: "#chan",
			sample:  "This channel has been suspended.",
		},
		{
			name:    "usernotice prefers msg-id tag",
			raw:     "@badge-info=subscriber/6;msg-id=resub :tmi.twitch.tv USERNOTICE #chan :great stream",
			command: "USERNOTICE",
			channel: "#chan",
			sample:  "msg-id=resub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeIRC(tt.raw)
			if got.command != tt.command {
				t.Fatalf("command mismatch: want %q got %q", tt.command, got.command)
			}
			if got.channel != tt.channel {
				t.Fatalf("channel mismatch: want %q got %q", tt.channel, got.channel)
			}
			if got.sample != tt.sample {
				t.Fatalf("sample mismatch: want %q got %q", tt.sample, got.sample)
			}
		})
	}
}

func TestSanitizeAndTruncateRedactsSecrets(t *testing.T) {
	raw := "oauth:abcdefghijklmnopqrstuvwxyz123456 token=QWxhZGRpbjpPcGVuU2VzYW1lMTIzNDU2Nzg5MA=="
	got := sanitizeAndTruncate(raw, 300)
	if strings.Contains(strings.ToLower(got), "oauth:abcdefghijkl") {
		t.Fatalf("expected oauth token redaction, got %q", got)
	}
	if strings.Contains(got, "QWxhZGRpbjpPcGVuU2VzYW1lMTIzNDU2Nzg5MA==") {
		t.Fatalf("expected long token redaction, got %q", got)
	}
	if !strings.Contains(got, "oauth:[REDACTED]") {
		t.Fatalf("expected oauth redaction marker, got %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected generic redaction marker, got %q", got)
	}
}

func TestSanitizeAndTruncateRedactsPassLine(t *testing.T) {
	got := sanitizeAndTruncate("PASS oauth:supersecrettokenvalue", 200)
	if got != "PASS [REDACTED]" {
		t.Fatalf("expected PASS redaction, got %q", got)
	}
}
