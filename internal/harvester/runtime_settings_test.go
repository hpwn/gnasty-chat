package harvester

import (
	"testing"

	"github.com/you/gnasty-chat/internal/twitchauth"
)

func TestRuntimeSettingsApplyValidation(t *testing.T) {
	current := RuntimeSettings{
		Sinks: SinkRuntimeSettings{Enabled: []string{"sqlite"}, BatchSize: 1, FlushMaxMS: 0},
		Twitch: TwitchRuntimeSettings{
			Channel:             "elora",
			Nick:                "bot",
			TLS:                 true,
			BackoffMinMS:        1000,
			BackoffMaxMS:        2000,
			RefreshBackoffMinMS: 1000,
			RefreshBackoffMaxMS: 2000,
		},
		YouTube: YouTubeRuntimeSettings{RetrySeconds: 30},
	}

	_, _, err := current.ApplyConfig(RuntimeConfigPatch{
		YouTube: YouTubeRuntimePatch{RetrySeconds: intPtr(0)},
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestRuntimeSettingsApplyNoopChangedFalse(t *testing.T) {
	current := RuntimeSettings{
		Sinks: SinkRuntimeSettings{Enabled: []string{"sqlite"}, BatchSize: 1, FlushMaxMS: 0},
		Twitch: TwitchRuntimeSettings{
			Channel:             "elora",
			Nick:                "bot",
			TLS:                 true,
			BackoffMinMS:        1000,
			BackoffMaxMS:        60_000,
			RefreshBackoffMinMS: 1000,
			RefreshBackoffMaxMS: 60_000,
		},
		YouTube: YouTubeRuntimeSettings{URL: "https://www.youtube.com/@elora/live", RetrySeconds: 30},
	}

	next, result, err := current.ApplyConfig(RuntimeConfigPatch{
		Sinks: SinkRuntimePatch{BatchSize: intPtr(1)},
	})
	if err != nil {
		t.Fatalf("apply config: %v", err)
	}
	if result.Changed {
		t.Fatalf("expected changed=false")
	}
	if next.Sinks.BatchSize != current.Sinks.BatchSize || next.YouTube.URL != current.YouTube.URL || next.Twitch.Channel != current.Twitch.Channel {
		t.Fatalf("expected no settings changes")
	}
}

func TestHarvesterApplyRuntimeConfigInvokesApplier(t *testing.T) {
	h := New(twitchauth.TokenFiles{}, nil, nil, RuntimeSettings{
		Sinks: SinkRuntimeSettings{Enabled: []string{"sqlite"}, BatchSize: 1, FlushMaxMS: 0},
	})

	var seen SinkRuntimeSettings
	h.SetSinkRuntimeApplier(func(next SinkRuntimeSettings) (bool, error) {
		seen = next
		return true, nil
	})

	result, _, err := h.ApplyRuntimeConfig(RuntimeConfigPatch{
		Sinks: SinkRuntimePatch{BatchSize: intPtr(5)},
	})
	if err != nil {
		t.Fatalf("apply runtime config: %v", err)
	}
	if !result.Changed || !result.Sinks {
		t.Fatalf("expected sink runtime change, got %+v", result)
	}
	if seen.BatchSize != 5 {
		t.Fatalf("sink applier saw batch_size=%d, want 5", seen.BatchSize)
	}
}

func intPtr(v int) *int { return &v }
