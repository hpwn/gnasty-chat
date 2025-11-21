package ingesttrace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
)

// Stage represents a pipeline stage used for tracking message processing.
type Stage string

const (
	StageSeenFromProvider Stage = "seen_from_provider"
	StageNormalizedOK     Stage = "normalized_ok"
	StageWrittenToDB      Stage = "written_to_db"

	StageDroppedPrefix = "dropped_"
)

// StageDropped creates a Stage for a dropped message with the given reason.
func StageDropped(reason string) Stage {
	return Stage(fmt.Sprintf("%s%s", StageDroppedPrefix, reason))
}

// MessageTrace captures trace metadata for a message throughout the ingest pipeline.
type MessageTrace struct {
	Platform string
	Channel  string
	User     string
	Snippet  string
	TraceID  string

	mu       sync.Mutex
	counters map[Stage]int64
}

// NewTraceFromProviderMessage constructs a trace from provider metadata and seeds the
// seen_from_provider counter.
func NewTraceFromProviderMessage(platform, channel, user, snippet string) *MessageTrace {
	trace := &MessageTrace{
		Platform: platform,
		Channel:  channel,
		User:     user,
		Snippet:  snippet,
		TraceID:  computeTraceID(platform, channel, user, snippet),
		counters: make(map[Stage]int64),
	}

	trace.counters[StageSeenFromProvider] = 1
	return trace
}

// IncCounter increments the counter for the provided stage and returns the updated value.
func (t *MessageTrace) IncCounter(stage Stage) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.counters[stage]++
	return t.counters[stage]
}

// LogTrace logs the trace metadata and counters using structured logging.
func (t *MessageTrace) LogTrace(logger *slog.Logger, msg string) {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info(msg,
		"trace_id", t.TraceID,
		"platform", t.Platform,
		"channel", t.Channel,
		"user", t.User,
		"snippet", t.Snippet,
		"counters", t.snapshotCounters(),
	)
}

func (t *MessageTrace) snapshotCounters() map[Stage]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	copy := make(map[Stage]int64, len(t.counters))
	for stage, count := range t.counters {
		copy[stage] = count
	}

	return copy
}

func computeTraceID(platform, channel, user, snippet string) string {
	digest := sha256.Sum256([]byte(platform + "\x1f" + channel + "\x1f" + user + "\x1f" + snippet))
	return hex.EncodeToString(digest[:])
}
