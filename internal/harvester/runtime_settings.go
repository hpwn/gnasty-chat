package harvester

import (
	"fmt"
	"strings"

	"github.com/you/gnasty-chat/internal/config"
	"github.com/you/gnasty-chat/internal/twitch"
	"github.com/you/gnasty-chat/internal/ytlive"
)

const (
	defaultTwitchBackoffMinMS        = 1000
	defaultTwitchBackoffMaxMS        = 60_000
	defaultTwitchRefreshBackoffMinMS = 1000
	defaultTwitchRefreshBackoffMaxMS = 60_000
	defaultKickBackoffMinMS           = 1000
	defaultKickBackoffMaxMS           = 60_000
)

type RuntimeSettings struct {
	Sinks   SinkRuntimeSettings    `json:"sinks"`
	Twitch  TwitchRuntimeSettings  `json:"twitch"`
	Kick    KickRuntimeSettings    `json:"kick"`
	YouTube YouTubeRuntimeSettings `json:"youtube"`
}

type SinkRuntimeSettings struct {
	Enabled    []string `json:"enabled"`
	BatchSize  int      `json:"batch_size"`
	FlushMaxMS int      `json:"flush_max_ms"`
}

type TwitchRuntimeSettings struct {
	Channel             string `json:"channel,omitempty"`
	Nick                string `json:"nick,omitempty"`
	TLS                 bool   `json:"tls"`
	DebugDrops          bool   `json:"debug_drops"`
	BackoffMinMS        int    `json:"backoff_min_ms"`
	BackoffMaxMS        int    `json:"backoff_max_ms"`
	RefreshBackoffMinMS int    `json:"refresh_backoff_min_ms"`
	RefreshBackoffMaxMS int    `json:"refresh_backoff_max_ms"`
}

type YouTubeRuntimeSettings struct {
	URL             string `json:"url,omitempty"`
	RetrySeconds    int    `json:"retry_seconds"`
	DumpUnhandled   bool   `json:"dump_unhandled"`
	PollTimeoutSecs int    `json:"poll_timeout_secs"`
	PollIntervalMS  int    `json:"poll_interval_ms"`
	Debug           bool   `json:"debug"`
}

type KickRuntimeSettings struct {
	Enabled bool `json:"enabled"`
	Channels []string `json:"channels"`
	Nick string `json:"nick,omitempty"`
	TLS bool `json:"tls"`
	BackoffMinMS int `json:"backoff_min_ms"`
	BackoffMaxMS int `json:"backoff_max_ms"`
}

type RuntimeConfigPatch struct {
	Sinks   SinkRuntimePatch    `json:"sinks"`
	Twitch  TwitchRuntimePatch  `json:"twitch"`
	Kick    KickRuntimePatch    `json:"kick"`
	YouTube YouTubeRuntimePatch `json:"youtube"`
}

type SinkRuntimePatch struct {
	Enabled    *[]string `json:"enabled,omitempty"`
	BatchSize  *int      `json:"batch_size,omitempty"`
	FlushMaxMS *int      `json:"flush_max_ms,omitempty"`
}

type TwitchRuntimePatch struct {
	Channel             *string `json:"channel,omitempty"`
	Nick                *string `json:"nick,omitempty"`
	TLS                 *bool   `json:"tls,omitempty"`
	DebugDrops          *bool   `json:"debug_drops,omitempty"`
	BackoffMinMS        *int    `json:"backoff_min_ms,omitempty"`
	BackoffMaxMS        *int    `json:"backoff_max_ms,omitempty"`
	RefreshBackoffMinMS *int    `json:"refresh_backoff_min_ms,omitempty"`
	RefreshBackoffMaxMS *int    `json:"refresh_backoff_max_ms,omitempty"`
}

type YouTubeRuntimePatch struct {
	URL             *string `json:"url,omitempty"`
	RetrySeconds    *int    `json:"retry_seconds,omitempty"`
	DumpUnhandled   *bool   `json:"dump_unhandled,omitempty"`
	PollTimeoutSecs *int    `json:"poll_timeout_secs,omitempty"`
	PollIntervalMS  *int    `json:"poll_interval_ms,omitempty"`
	Debug           *bool   `json:"debug,omitempty"`
}

type KickRuntimePatch struct {
	Enabled *bool `json:"enabled,omitempty"`
	Channels *[]string `json:"channels,omitempty"`
	Nick *string `json:"nick,omitempty"`
	TLS *bool `json:"tls,omitempty"`
	BackoffMinMS *int `json:"backoff_min_ms,omitempty"`
	BackoffMaxMS *int `json:"backoff_max_ms,omitempty"`
}

type RuntimeApplyResult struct {
	Changed bool `json:"changed"`
	Sinks   bool `json:"sinks_changed"`
	Twitch  bool `json:"twitch_changed"`
	Kick    bool `json:"kick_changed"`
	YouTube bool `json:"youtube_changed"`
}

func NewRuntimeSettingsFromConfig(cfg config.Config) RuntimeSettings {
	channel := ""
	if len(cfg.Twitch.Channels) > 0 {
		channel = strings.TrimSpace(cfg.Twitch.Channels[0])
	}
	twBackoffMin := cfg.Twitch.BackoffMinMS
	if twBackoffMin <= 0 {
		twBackoffMin = defaultTwitchBackoffMinMS
	}
	twBackoffMax := cfg.Twitch.BackoffMaxMS
	if twBackoffMax <= 0 {
		twBackoffMax = defaultTwitchBackoffMaxMS
	}
	refreshMin := cfg.Twitch.RefreshBackoffMinMS
	if refreshMin <= 0 {
		refreshMin = defaultTwitchRefreshBackoffMinMS
	}
	refreshMax := cfg.Twitch.RefreshBackoffMaxMS
	if refreshMax <= 0 {
		refreshMax = defaultTwitchRefreshBackoffMaxMS
	}

	retry := cfg.YouTube.RetrySeconds
	if retry <= 0 {
		retry = 30
	}
	kickBackoffMin := cfg.Kick.BackoffMinMS
	if kickBackoffMin <= 0 {
		kickBackoffMin = defaultKickBackoffMinMS
	}
	kickBackoffMax := cfg.Kick.BackoffMaxMS
	if kickBackoffMax <= 0 {
		kickBackoffMax = defaultKickBackoffMaxMS
	}

	return RuntimeSettings{
		Sinks: SinkRuntimeSettings{
			Enabled:    append([]string(nil), cfg.Sinks...),
			BatchSize:  cfg.Batch(),
			FlushMaxMS: cfg.Sink.FlushMaxMS,
		},
		Twitch: TwitchRuntimeSettings{
			Channel:             channel,
			Nick:                strings.TrimSpace(cfg.Twitch.Nick),
			TLS:                 cfg.Twitch.TLS,
			DebugDrops:          cfg.Twitch.DebugDrops,
			BackoffMinMS:        twBackoffMin,
			BackoffMaxMS:        twBackoffMax,
			RefreshBackoffMinMS: refreshMin,
			RefreshBackoffMaxMS: refreshMax,
		},
		Kick: KickRuntimeSettings{
			Enabled: cfg.Kick.Enabled,
			Channels: append([]string(nil), cfg.Kick.Channels...),
			Nick: strings.TrimSpace(cfg.Kick.Nick),
			TLS: cfg.Kick.TLS,
			BackoffMinMS: kickBackoffMin,
			BackoffMaxMS: kickBackoffMax,
		},
		YouTube: YouTubeRuntimeSettings{
			URL:             strings.TrimSpace(cfg.YouTube.LiveURL),
			RetrySeconds:    retry,
			DumpUnhandled:   cfg.YouTube.DumpUnhandled,
			PollTimeoutSecs: cfg.YouTube.PollTimeoutSecs,
			PollIntervalMS:  cfg.YouTube.PollIntervalMS,
			Debug:           cfg.YouTube.Debug,
		},
	}
}

func (s RuntimeSettings) ApplyKick(patch KickRuntimePatch) (KickRuntimeSettings, bool, error) {
	next := s.Kick
	changed := false

	if patch.Enabled != nil && next.Enabled != *patch.Enabled {
		next.Enabled = *patch.Enabled
		changed = true
	}
	if patch.Channels != nil {
		channels := normalizeSinks(*patch.Channels)
		if !equalStrings(next.Channels, channels) {
			next.Channels = channels
			changed = true
		}
	}
	if patch.Nick != nil {
		nick := strings.TrimSpace(*patch.Nick)
		if next.Nick != nick {
			next.Nick = nick
			changed = true
		}
	}
	if patch.TLS != nil && next.TLS != *patch.TLS {
		next.TLS = *patch.TLS
		changed = true
	}
	if patch.BackoffMinMS != nil {
		if *patch.BackoffMinMS <= 0 {
			return s.Kick, false, fmt.Errorf("kick.backoff_min_ms: must be > 0")
		}
		if next.BackoffMinMS != *patch.BackoffMinMS {
			next.BackoffMinMS = *patch.BackoffMinMS
			changed = true
		}
	}
	if patch.BackoffMaxMS != nil {
		if *patch.BackoffMaxMS <= 0 {
			return s.Kick, false, fmt.Errorf("kick.backoff_max_ms: must be > 0")
		}
		if next.BackoffMaxMS != *patch.BackoffMaxMS {
			next.BackoffMaxMS = *patch.BackoffMaxMS
			changed = true
		}
	}
	if next.BackoffMinMS > next.BackoffMaxMS {
		return s.Kick, false, fmt.Errorf("kick.backoff: min must be <= max")
	}

	return next, changed, nil
}

func (s RuntimeSettings) ApplySinks(patch SinkRuntimePatch) (SinkRuntimeSettings, bool, error) {
	next := s.Sinks
	changed := false

	if patch.Enabled != nil {
		clean := normalizeSinks(*patch.Enabled)
		if len(clean) == 0 {
			return s.Sinks, false, fmt.Errorf("sinks.enabled: at least one sink is required")
		}
		for _, sinkName := range clean {
			if sinkName != "sqlite" {
				return s.Sinks, false, fmt.Errorf("sinks.enabled: unsupported sink %q", sinkName)
			}
		}
		if !equalStrings(next.Enabled, clean) {
			next.Enabled = clean
			changed = true
		}
	}
	if patch.BatchSize != nil {
		if *patch.BatchSize <= 0 {
			return s.Sinks, false, fmt.Errorf("sinks.batch_size: must be > 0")
		}
		if next.BatchSize != *patch.BatchSize {
			next.BatchSize = *patch.BatchSize
			changed = true
		}
	}
	if patch.FlushMaxMS != nil {
		if *patch.FlushMaxMS < 0 {
			return s.Sinks, false, fmt.Errorf("sinks.flush_max_ms: must be >= 0")
		}
		if next.FlushMaxMS != *patch.FlushMaxMS {
			next.FlushMaxMS = *patch.FlushMaxMS
			changed = true
		}
	}

	return next, changed, nil
}

func (s RuntimeSettings) ApplyTwitch(patch TwitchRuntimePatch) (TwitchRuntimeSettings, bool, error) {
	next := s.Twitch
	changed := false

	if patch.Channel != nil {
		raw := strings.TrimSpace(*patch.Channel)
		if raw != "" {
			normalized, err := twitch.NormalizeChannelLogin(raw)
			if err != nil {
				return s.Twitch, false, fmt.Errorf("twitch.channel: %w", err)
			}
			raw = normalized
		}
		if next.Channel != raw {
			next.Channel = raw
			changed = true
		}
	}
	if patch.Nick != nil {
		nick := strings.TrimSpace(*patch.Nick)
		if nick == "" {
			return s.Twitch, false, fmt.Errorf("twitch.nick: required")
		}
		if next.Nick != nick {
			next.Nick = nick
			changed = true
		}
	}
	if patch.TLS != nil && next.TLS != *patch.TLS {
		next.TLS = *patch.TLS
		changed = true
	}
	if patch.DebugDrops != nil && next.DebugDrops != *patch.DebugDrops {
		next.DebugDrops = *patch.DebugDrops
		changed = true
	}
	if patch.BackoffMinMS != nil {
		if *patch.BackoffMinMS <= 0 {
			return s.Twitch, false, fmt.Errorf("twitch.backoff_min_ms: must be > 0")
		}
		if next.BackoffMinMS != *patch.BackoffMinMS {
			next.BackoffMinMS = *patch.BackoffMinMS
			changed = true
		}
	}
	if patch.BackoffMaxMS != nil {
		if *patch.BackoffMaxMS <= 0 {
			return s.Twitch, false, fmt.Errorf("twitch.backoff_max_ms: must be > 0")
		}
		if next.BackoffMaxMS != *patch.BackoffMaxMS {
			next.BackoffMaxMS = *patch.BackoffMaxMS
			changed = true
		}
	}
	if next.BackoffMinMS > next.BackoffMaxMS {
		return s.Twitch, false, fmt.Errorf("twitch.backoff: min must be <= max")
	}
	if patch.RefreshBackoffMinMS != nil {
		if *patch.RefreshBackoffMinMS <= 0 {
			return s.Twitch, false, fmt.Errorf("twitch.refresh_backoff_min_ms: must be > 0")
		}
		if next.RefreshBackoffMinMS != *patch.RefreshBackoffMinMS {
			next.RefreshBackoffMinMS = *patch.RefreshBackoffMinMS
			changed = true
		}
	}
	if patch.RefreshBackoffMaxMS != nil {
		if *patch.RefreshBackoffMaxMS <= 0 {
			return s.Twitch, false, fmt.Errorf("twitch.refresh_backoff_max_ms: must be > 0")
		}
		if next.RefreshBackoffMaxMS != *patch.RefreshBackoffMaxMS {
			next.RefreshBackoffMaxMS = *patch.RefreshBackoffMaxMS
			changed = true
		}
	}
	if next.RefreshBackoffMinMS > next.RefreshBackoffMaxMS {
		return s.Twitch, false, fmt.Errorf("twitch.refresh_backoff: min must be <= max")
	}

	return next, changed, nil
}

func (s RuntimeSettings) ApplyYouTube(patch YouTubeRuntimePatch) (YouTubeRuntimeSettings, bool, error) {
	next := s.YouTube
	changed := false

	if patch.URL != nil {
		raw := strings.TrimSpace(*patch.URL)
		if raw != "" {
			normalized, err := ytlive.NormalizeLiveURL(raw)
			if err != nil {
				return s.YouTube, false, fmt.Errorf("youtube.url: %w", err)
			}
			raw = normalized
		}
		if next.URL != raw {
			next.URL = raw
			changed = true
		}
	}
	if patch.RetrySeconds != nil {
		if *patch.RetrySeconds <= 0 {
			return s.YouTube, false, fmt.Errorf("youtube.retry_seconds: must be > 0")
		}
		if next.RetrySeconds != *patch.RetrySeconds {
			next.RetrySeconds = *patch.RetrySeconds
			changed = true
		}
	}
	if patch.DumpUnhandled != nil && next.DumpUnhandled != *patch.DumpUnhandled {
		next.DumpUnhandled = *patch.DumpUnhandled
		changed = true
	}
	if patch.PollTimeoutSecs != nil {
		if *patch.PollTimeoutSecs < 0 {
			return s.YouTube, false, fmt.Errorf("youtube.poll_timeout_secs: must be >= 0")
		}
		if next.PollTimeoutSecs != *patch.PollTimeoutSecs {
			next.PollTimeoutSecs = *patch.PollTimeoutSecs
			changed = true
		}
	}
	if patch.PollIntervalMS != nil {
		if *patch.PollIntervalMS < 0 {
			return s.YouTube, false, fmt.Errorf("youtube.poll_interval_ms: must be >= 0")
		}
		if next.PollIntervalMS != *patch.PollIntervalMS {
			next.PollIntervalMS = *patch.PollIntervalMS
			changed = true
		}
	}
	if patch.Debug != nil && next.Debug != *patch.Debug {
		next.Debug = *patch.Debug
		changed = true
	}

	return next, changed, nil
}

func (s RuntimeSettings) ApplyConfig(patch RuntimeConfigPatch) (RuntimeSettings, RuntimeApplyResult, error) {
	next := s
	result := RuntimeApplyResult{}

	updatedSinks, sinksChanged, err := next.ApplySinks(patch.Sinks)
	if err != nil {
		return s, result, err
	}
	next.Sinks = updatedSinks
	result.Sinks = sinksChanged

	updatedTwitch, twitchChanged, err := next.ApplyTwitch(patch.Twitch)
	if err != nil {
		return s, result, err
	}
	next.Twitch = updatedTwitch
	result.Twitch = twitchChanged
	updatedKick, kickChanged, err := next.ApplyKick(patch.Kick)
	if err != nil {
		return s, result, err
	}
	next.Kick = updatedKick
	result.Kick = kickChanged

	updatedYT, ytChanged, err := next.ApplyYouTube(patch.YouTube)
	if err != nil {
		return s, result, err
	}
	next.YouTube = updatedYT
	result.YouTube = ytChanged

	result.Changed = result.Sinks || result.Twitch || result.Kick || result.YouTube
	return next, result, nil
}

func normalizeSinks(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.ToLower(strings.TrimSpace(value))
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
