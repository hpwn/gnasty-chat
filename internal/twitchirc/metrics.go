package twitchirc

import "sync/atomic"

// twitchMetrics tracks basic ingest counters for Twitch IRC handling.
type twitchMetricsState struct {
	seenFromProvider atomic.Int64
	dropped          atomic.Int64
}

var twitchMetrics twitchMetricsState

func (m *twitchMetricsState) incSeenFromProvider() int64 {
	if m == nil {
		return 0
	}
	return m.seenFromProvider.Add(1)
}

func (m *twitchMetricsState) incDropped(reason string) int64 {
	if m == nil {
		return 0
	}
	_ = reason // reserved for future per-reason counters
	return m.dropped.Add(1)
}
