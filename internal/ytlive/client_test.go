package ytlive

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

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
	delay, used := nextLivePollDelay(timeout, hasTimeout)
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
	delay, used := nextLivePollDelay(timeout, hasTimeout)
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

	prevEnv := os.Getenv("GNASTY_YT_DUMP_UNHANDLED")
	defer os.Setenv("GNASTY_YT_DUMP_UNHANDLED", prevEnv)

	var buf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalWriter)

	os.Unsetenv("GNASTY_YT_DUMP_UNHANDLED")
	logPollResults(summary, failures, nonChats, os.Getenv("GNASTY_YT_DUMP_UNHANDLED") != "")
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
	os.Setenv("GNASTY_YT_DUMP_UNHANDLED", "1")
	logPollResults(summary, failures, nonChats, os.Getenv("GNASTY_YT_DUMP_UNHANDLED") != "")
	output = buf.String()
	if !strings.Contains(output, "unhandled action dump") {
		t.Fatalf("expected dump with env set, got %q", output)
	}
}
