package ytlive

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestNewNormalizesTimingDefaults(t *testing.T) {
	cfg := Config{}
	client := New(cfg, nil)

	if client.pollTimeout != defaultPollTimeout {
		t.Fatalf("expected default poll timeout %v, got %v", defaultPollTimeout, client.pollTimeout)
	}
	if client.http.Timeout != defaultPollTimeout {
		t.Fatalf("expected http timeout %v, got %v", defaultPollTimeout, client.http.Timeout)
	}
	if client.pollDelay != defaultLivePollDelay {
		t.Fatalf("expected default poll delay %v, got %v", defaultLivePollDelay, client.pollDelay)
	}
}

func TestNewAppliesTimingOverrides(t *testing.T) {
	cfg := Config{PollTimeoutSecs: 5, PollIntervalMS: 4500}
	client := New(cfg, nil)

	if client.pollTimeout != 5*time.Second {
		t.Fatalf("expected poll timeout 5s, got %v", client.pollTimeout)
	}
	if client.http.Timeout != 5*time.Second {
		t.Fatalf("expected http timeout 5s, got %v", client.http.Timeout)
	}
	if client.pollDelay != 4500*time.Millisecond {
		t.Fatalf("expected poll delay 4500ms, got %v", client.pollDelay)
	}
}

func TestExtractContinuationTimeout(t *testing.T) {
	payload := map[string]any{
		"continuationContents": map[string]any{
			"liveChatContinuation": map[string]any{
				"continuations": []any{
					map[string]any{
						"timedContinuationData": map[string]any{
							"continuation": "abc123",
							"timeoutMs":    "2500",
						},
					},
				},
			},
		},
	}

	cont, timeout, hasTimeout := extractContinuation(payload)
	if cont != "abc123" {
		t.Fatalf("expected continuation abc123, got %q", cont)
	}
	if !hasTimeout {
		t.Fatalf("expected to detect timeout")
	}
	delay, used := nextLivePollDelay(timeout, hasTimeout, defaultLivePollDelay)
	if !used {
		t.Fatalf("expected delay to come from continuation")
	}
	if delay != 2500*time.Millisecond {
		t.Fatalf("expected 2500ms delay, got %v", delay)
	}
}

func TestExtractContinuationTimeoutFallback(t *testing.T) {
	payload := map[string]any{
		"continuationContents": map[string]any{
			"liveChatContinuation": map[string]any{
				"continuations": []any{
					map[string]any{
						"timedContinuationData": map[string]any{
							"continuation": "def456",
						},
					},
				},
			},
		},
	}

	cont, timeout, hasTimeout := extractContinuation(payload)
	if cont != "def456" {
		t.Fatalf("expected continuation def456, got %q", cont)
	}
	if hasTimeout {
		t.Fatalf("expected no timeout value")
	}
	delay, used := nextLivePollDelay(timeout, hasTimeout, defaultLivePollDelay)
	if used {
		t.Fatalf("expected to fall back to default delay")
	}
	if delay != defaultLivePollDelay {
		t.Fatalf("expected fallback delay %v, got %v", defaultLivePollDelay, delay)
	}
}

func TestExtractMessagesAndLogging(t *testing.T) {
	chatRenderer := func(id, author, text string) map[string]any {
		return map[string]any{
			"id":            id,
			"timestampUsec": "1234567890",
			"authorName": map[string]any{
				"simpleText": author,
			},
			"message": map[string]any{
				"simpleText": text,
			},
		}
	}

	payload := map[string]any{
		"actions": []any{
			map[string]any{
				"addChatItemAction": map[string]any{
					"item": map[string]any{
						"liveChatTextMessageRenderer": chatRenderer("chat-1", "User1", "Hello world"),
					},
				},
			},
			map[string]any{
				"addChatItemAction": map[string]any{
					"item": map[string]any{
						"liveChatTextMessageRenderer": chatRenderer("chat-2", "User2", "Second line"),
					},
				},
			},
			map[string]any{
				"appendContinuationItemsAction": map[string]any{
					"continuationItems": []any{
						map[string]any{
							"liveChatLegacyTextMessageRenderer": chatRenderer("chat-3", "User3", "Legacy line"),
						},
					},
				},
			},
			map[string]any{
				"addChatItemAction": map[string]any{
					"item": map[string]any{
						"liveChatPaidMessageRenderer": map[string]any{
							"id": "nonchat-1",
						},
					},
				},
			},
			map[string]any{
				"showLiveChatActionPanelAction": map[string]any{
					"panelToShow": map[string]any{
						"liveChatMembershipItemRenderer": map[string]any{
							"id": "nonchat-2",
						},
					},
				},
			},
		},
	}

	messages, summary, failures, nonChats := extractMessages(payload)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if summary.actions != 5 {
		t.Fatalf("expected 5 actions, got %d", summary.actions)
	}
	if summary.chatMessages != 3 {
		t.Fatalf("expected 3 chat messages, got %d", summary.chatMessages)
	}
	if summary.stored != 3 {
		t.Fatalf("expected 3 stored messages, got %d", summary.stored)
	}
	if summary.skipped != 2 {
		t.Fatalf("expected 2 skipped actions, got %d", summary.skipped)
	}
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %d", len(failures))
	}
	if len(nonChats) != 2 {
		t.Fatalf("expected 2 non-chat actions, got %d", len(nonChats))
	}

	var buf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalWriter)

	logPollResults(summary, failures, nonChats, false)
	output := buf.String()
	if !strings.Contains(output, "ytlive: poll summary actions=5 chat_messages=3 stored=3 skipped=2") {
		t.Fatalf("missing poll summary log, got %q", output)
	}
	if strings.Contains(output, "unhandled action dump") {
		t.Fatalf("unexpected dump without env set: %q", output)
	}
	if count := strings.Count(output, "ytlive: skipped non-chat action"); count != 2 {
		t.Fatalf("expected 2 skip logs, got %d in %q", count, output)
	}

	buf.Reset()
	logPollResults(summary, failures, nonChats, true)
	output = buf.String()
	if !strings.Contains(output, "unhandled action dump") {
		t.Fatalf("expected dump with env set, got %q", output)
	}
}

func TestParseYouTubeBadges(t *testing.T) {
	renderer := map[string]any{
		"authorExternalChannelId": "channel-123",
		"authorBadges": []any{
			map[string]any{
				"liveChatAuthorBadgeRenderer": map[string]any{
					"icon":    map[string]any{"iconType": "OWNER"},
					"tooltip": "Channel owner",
				},
			},
			map[string]any{
				"liveChatAuthorBadgeRenderer": map[string]any{
					"tooltip": "Member (12 months)",
					"accessibility": map[string]any{
						"accessibilityData": map[string]any{"label": "Member (12 months)"},
					},
				},
			},
		},
		"authorBadgesWithMetadata": []any{
			map[string]any{
				"metadataBadgeRenderer": map[string]any{
					"label": "Verified",
					"icon":  map[string]any{"iconType": "CHECK"},
				},
			},
		},
	}

	badges, raw := parseYouTubeBadges(renderer)
	expected := map[string]string{"owner": "", "member": "12 months", "verified": ""}

	if len(badges) != len(expected) {
		t.Fatalf("expected %d badges, got %d", len(expected), len(badges))
	}

	for _, badge := range badges {
		wantVersion, ok := expected[badge.ID]
		if !ok {
			t.Fatalf("unexpected badge id %q", badge.ID)
		}
		if badge.Platform != "youtube" {
			t.Fatalf("expected youtube platform, got %q", badge.Platform)
		}
		if badge.Version != wantVersion {
			t.Fatalf("badge %s expected version %q, got %q", badge.ID, wantVersion, badge.Version)
		}
		delete(expected, badge.ID)
	}

	if len(expected) != 0 {
		t.Fatalf("missing expected badges: %#v", expected)
	}

	ytRaw, ok := raw["youtube"].(map[string]any)
	if !ok {
		t.Fatalf("expected youtube raw payload, got %#v", raw)
	}
	if _, ok := ytRaw["authorBadges"]; !ok {
		t.Fatalf("missing authorBadges in raw payload: %#v", ytRaw)
	}
	if _, ok := ytRaw["authorBadgesWithMetadata"]; !ok {
		t.Fatalf("missing authorBadgesWithMetadata in raw payload: %#v", ytRaw)
	}
	if ytRaw["authorExternalChannelId"] != "channel-123" {
		t.Fatalf("expected authorExternalChannelId to be preserved, got %#v", ytRaw["authorExternalChannelId"])
	}
}

func TestParseYouTubeBadgesPrefersMetadataVersion(t *testing.T) {
	renderer := map[string]any{
		"authorBadgesWithMetadata": []any{
			map[string]any{
				"metadataBadgeRenderer": map[string]any{
					"label": "Moderator",
					"style": "LIVE_CHAT_MODERATOR",
				},
			},
			map[string]any{
				"metadataBadgeRenderer": map[string]any{
					"label":   "Level 3",
					"style":   "MEMBER",
					"tooltip": "Member Level 3",
				},
			},
		},
	}

	badges, _ := parseYouTubeBadges(renderer)
	expected := map[string]string{"moderator": "", "member": "Level 3"}

	if len(badges) != len(expected) {
		t.Fatalf("expected %d badges, got %d", len(expected), len(badges))
	}

	for _, badge := range badges {
		wantVersion, ok := expected[badge.ID]
		if !ok {
			t.Fatalf("unexpected badge id %q", badge.ID)
		}
		if badge.Version != wantVersion {
			t.Fatalf("badge %s expected version %q, got %q", badge.ID, wantVersion, badge.Version)
		}
		delete(expected, badge.ID)
	}

	if len(expected) != 0 {
		t.Fatalf("missing expected badges: %#v", expected)
	}
}

func TestBuildMessageBadges(t *testing.T) {
	cases := []struct {
		name     string
		renderer map[string]any
		expected map[string]string
	}{
		{
			name: "owner badge from icon",
			renderer: map[string]any{
				"id":            "msg-owner",
				"message":       map[string]any{"simpleText": "hello"},
				"authorName":    map[string]any{"simpleText": "Owner"},
				"authorBadges":  []any{map[string]any{"liveChatAuthorBadgeRenderer": map[string]any{"icon": map[string]any{"iconType": "OWNER"}}}},
				"timestampUsec": "1700000000000000",
			},
			expected: map[string]string{"owner": ""},
		},
		{
			name: "moderator badge from style",
			renderer: map[string]any{
				"id":         "msg-mod",
				"message":    map[string]any{"simpleText": "hello"},
				"authorName": map[string]any{"simpleText": "Moderator"},
				"authorBadgesWithMetadata": []any{
					map[string]any{"metadataBadgeRenderer": map[string]any{"style": "LIVE_CHAT_MODERATOR", "label": "Moderator"}},
				},
				"timestampUsec": "1700000000000000",
			},
			expected: map[string]string{"moderator": ""},
		},
		{
			name: "member badge with tenure",
			renderer: map[string]any{
				"id":         "msg-member",
				"message":    map[string]any{"simpleText": "hello"},
				"authorName": map[string]any{"simpleText": "Member"},
				"authorBadges": []any{
					map[string]any{"liveChatAuthorBadgeRenderer": map[string]any{"tooltip": "Member (6 months)"}},
				},
				"timestampUsec": "1700000000000000",
			},
			expected: map[string]string{"member": "6 months"},
		},
		{
			name: "verified badge from check icon",
			renderer: map[string]any{
				"id":         "msg-verified",
				"message":    map[string]any{"simpleText": "hello"},
				"authorName": map[string]any{"simpleText": "Verified"},
				"authorBadgesWithMetadata": []any{
					map[string]any{"metadataBadgeRenderer": map[string]any{"icon": map[string]any{"iconType": "CHECK"}}},
				},
				"timestampUsec": "1700000000000000",
			},
			expected: map[string]string{"verified": ""},
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, reason := buildMessage(tt.renderer)
			if !ok {
				t.Fatalf("expected buildMessage to succeed, got reason %q", reason)
			}
			if len(msg.Badges) != len(tt.expected) {
				t.Fatalf("expected %d badges, got %d", len(tt.expected), len(msg.Badges))
			}
			for _, badge := range msg.Badges {
				want, ok := tt.expected[badge.ID]
				if !ok {
					t.Fatalf("unexpected badge id %q", badge.ID)
				}
				if badge.Platform != "youtube" {
					t.Fatalf("expected youtube platform, got %q", badge.Platform)
				}
				if badge.Version != want {
					t.Fatalf("badge %s expected version %q, got %q", badge.ID, want, badge.Version)
				}
				delete(tt.expected, badge.ID)
			}
			if len(tt.expected) != 0 {
				t.Fatalf("missing expected badges: %#v", tt.expected)
			}
			if msg.BadgesRaw == nil || msg.BadgesRaw["youtube"] == nil {
				t.Fatalf("expected youtube badges raw payload to be set")
			}
		})
	}
}
