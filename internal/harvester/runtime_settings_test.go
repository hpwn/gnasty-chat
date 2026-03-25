package harvester

import (
	"testing"

	"github.com/you/gnasty-chat/internal/config"
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
		Kick: KickRuntimeSettings{
			Enabled: true,
			Channels: []string{"alpha"},
			BackoffMinMS: 1000,
			BackoffMaxMS: 2000,
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
		Kick: KickRuntimeSettings{Enabled: false, BackoffMinMS: 1000, BackoffMaxMS: 60_000},
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
	if next.Kick.Enabled != current.Kick.Enabled {
		t.Fatalf("expected no kick settings changes")
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
func TestNewRuntimeSettingsFromConfigKickDefaults(t *testing.T) {
	settings := NewRuntimeSettingsFromConfig(config.Config{})
	if settings.Kick.Enabled {
		t.Fatalf("expected kick disabled by default")
	}
	if settings.Kick.BackoffMinMS != 1000 || settings.Kick.BackoffMaxMS != 60_000 {
		t.Fatalf("unexpected default kick backoff: min=%d max=%d", settings.Kick.BackoffMinMS, settings.Kick.BackoffMaxMS)
	}
}

func TestNewRuntimeSettingsFromConfigKickExplicit(t *testing.T) {
	settings := NewRuntimeSettingsFromConfig(config.Config{
		Kick: config.KickConfig{
			Enabled: true,
			Channels: []string{"Alpha", "beta"},
			Nick: "kickbot",
			TLS: false,
			BackoffMinMS: 1500,
			BackoffMaxMS: 45000,
		},
	})
	if !settings.Kick.Enabled || len(settings.Kick.Channels) != 2 {
		t.Fatalf("unexpected Kick runtime settings: %+v", settings.Kick)
	}
	if settings.Kick.Nick != "kickbot" || settings.Kick.TLS {
		t.Fatalf("unexpected Kick runtime identity/tls: %+v", settings.Kick)
	}
	if settings.Kick.BackoffMinMS != 1500 || settings.Kick.BackoffMaxMS != 45000 {
		t.Fatalf("unexpected Kick runtime backoff: %+v", settings.Kick)
	}
}

func intPtr(v int) *int { return &v }
