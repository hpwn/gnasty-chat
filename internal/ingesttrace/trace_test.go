package ingesttrace

import "testing"

func TestTraceIDDeterminism(t *testing.T) {
	first := NewTraceFromProviderMessage("twitch", "channel-a", "user1", "hello world")
	second := NewTraceFromProviderMessage("twitch", "channel-a", "user1", "hello world")
	if first.TraceID != second.TraceID {
		t.Fatalf("expected deterministic trace id, got %q and %q", first.TraceID, second.TraceID)
	}

	different := NewTraceFromProviderMessage("twitch", "channel-a", "user1", "hello mars")
	if first.TraceID == different.TraceID {
		t.Fatalf("expected different trace id when snippet changes")
	}
}

func TestCounterIncrements(t *testing.T) {
	trace := NewTraceFromProviderMessage("youtube", "channel-b", "user2", "hi there")

	if count := trace.IncCounter(StageNormalizedOK); count != 1 {
		t.Fatalf("expected normalized_ok to be 1, got %d", count)
	}

	if count := trace.IncCounter(StageDropped("filter")); count != 1 {
		t.Fatalf("expected dropped_filter to be 1, got %d", count)
	}

	if count := trace.IncCounter(StageDropped("filter")); count != 2 {
		t.Fatalf("expected dropped_filter to be 2 after increment, got %d", count)
	}

	if count := trace.IncCounter(StageWrittenToDB); count != 1 {
		t.Fatalf("expected written_to_db to be 1, got %d", count)
	}
}
