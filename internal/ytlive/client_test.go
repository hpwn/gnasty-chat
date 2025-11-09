package ytlive

import (
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
