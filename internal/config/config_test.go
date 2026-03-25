package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("GNASTY_SINKS", "")
	t.Setenv("GNASTY_RECEIVERS", "")
	t.Setenv("GNASTY_SINK_SQLITE_PATH", "")
	t.Setenv("GNASTY_SINK_BATCH_SIZE", "")
	t.Setenv("GNASTY_SINK_FLUSH_MAX_MS", "")
	t.Setenv("GNASTY_YT_RETRY_SECS", "")
	t.Setenv("GNASTY_YT_DUMP_UNHANDLED", "")
	t.Setenv("GNASTY_YT_POLL_TIMEOUT_SECS", "")
	t.Setenv("GNASTY_YT_POLL_INTERVAL_MS", "")
	t.Setenv("GNASTY_YT_DEBUG", "")
	t.Setenv("GNASTY_TWITCH_DEBUG_DROPS", "")
	t.Setenv("GNASTY_TWITCH_BACKOFF_MIN_MS", "")
	t.Setenv("GNASTY_TWITCH_BACKOFF_MAX_MS", "")
	t.Setenv("GNASTY_TWITCH_REFRESH_BACKOFF_MIN_MS", "")
	t.Setenv("GNASTY_TWITCH_REFRESH_BACKOFF_MAX_MS", "")
	t.Setenv("GNASTY_KICK_ENABLED", "")
	t.Setenv("GNASTY_KICK_CHANNELS", "")
	t.Setenv("GNASTY_KICK_NICK", "")
	t.Setenv("GNASTY_KICK_TOKEN", "")
	t.Setenv("GNASTY_KICK_TOKEN_FILE", "")
	t.Setenv("GNASTY_KICK_TLS", "")
	t.Setenv("GNASTY_KICK_BACKOFF_MIN_MS", "")
	t.Setenv("GNASTY_KICK_BACKOFF_MAX_MS", "")
	
	cfg := Load()
	if !cfg.HasSink("sqlite") {
		t.Fatalf("expected sqlite sink by default, got %v", cfg.Sinks)
	}
	if cfg.Sink.SQLite.Path != "chat.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.Sink.SQLite.Path)
	}
	if cfg.Batch() != 1 {
		t.Fatalf("expected default batch size 1, got %d", cfg.Batch())
	}
	if cfg.FlushInterval() != 0 {
		t.Fatalf("expected zero flush interval, got %s", cfg.FlushInterval())
	}
	if cfg.YouTube.RetrySeconds != 30 {
		t.Fatalf("expected default youtube retry seconds 30, got %d", cfg.YouTube.RetrySeconds)
	}
	if cfg.YouTube.DumpUnhandled {
		t.Fatalf("expected youtube dump unhandled default false")
	}
	if cfg.YouTube.PollTimeoutSecs != 15 {
		t.Fatalf("expected youtube poll timeout default 15, got %d", cfg.YouTube.PollTimeoutSecs)
	}
	if cfg.YouTube.PollIntervalMS != 10000 {
		t.Fatalf("expected youtube poll interval default 10000, got %d", cfg.YouTube.PollIntervalMS)
	}
	if cfg.YouTube.Debug {
		t.Fatalf("expected youtube debug default false")
	}
	if cfg.Twitch.DebugDrops {
		t.Fatalf("expected twitch debug drops default false")
	}
	if cfg.Twitch.BackoffMinMS != 1000 || cfg.Twitch.BackoffMaxMS != 60000 {
		t.Fatalf("unexpected twitch backoff defaults: min=%d max=%d", cfg.Twitch.BackoffMinMS, cfg.Twitch.BackoffMaxMS)
	}
	if cfg.Kick.Enabled {
		t.Fatalf("expected kick disabled by default")
	}
	if cfg.Kick.TLS != true {
		t.Fatalf("expected kick TLS default true")
	}
	if cfg.Kick.BackoffMinMS != 1000 || cfg.Kick.BackoffMaxMS != 60000 {
		t.Fatalf("unexpected kick backoff defaults: min=%d max=%d", cfg.Kick.BackoffMinMS, cfg.Kick.BackoffMaxMS)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("GNASTY_SINKS", "sqlite")
	t.Setenv("GNASTY_SINK_SQLITE_PATH", "/data/elora.db")
	t.Setenv("GNASTY_SINK_BATCH_SIZE", "25")
	t.Setenv("GNASTY_SINK_FLUSH_MAX_MS", "250")
	t.Setenv("GNASTY_TWITCH_CHANNELS", "elora, gnasty")
	t.Setenv("GNASTY_TWITCH_NICK", "elora_bot")
	t.Setenv("GNASTY_TWITCH_TOKEN", "oauth:abc")
	t.Setenv("GNASTY_TWITCH_CLIENT_SECRET", "secret")
	t.Setenv("GNASTY_TWITCH_TLS", "false")
	t.Setenv("GNASTY_TWITCH_DEBUG_DROPS", "1")
	t.Setenv("GNASTY_TWITCH_BACKOFF_MIN_MS", "1500")
	t.Setenv("GNASTY_TWITCH_BACKOFF_MAX_MS", "45000")
	t.Setenv("GNASTY_TWITCH_REFRESH_BACKOFF_MIN_MS", "2000")
	t.Setenv("GNASTY_TWITCH_REFRESH_BACKOFF_MAX_MS", "30000")
	t.Setenv("GNASTY_YT_URL", "https://example.test/watch")
	t.Setenv("GNASTY_YT_RETRY_SECS", "45")
	t.Setenv("GNASTY_YT_DUMP_UNHANDLED", "true")
	t.Setenv("GNASTY_YT_POLL_TIMEOUT_SECS", "60")
	t.Setenv("GNASTY_YT_POLL_INTERVAL_MS", "1500")
	t.Setenv("GNASTY_YT_DEBUG", "yes")
	t.Setenv("GNASTY_KICK_CHANNELS", "alpha, beta")
	t.Setenv("GNASTY_KICK_NICK", "kick_bot")
	t.Setenv("GNASTY_KICK_TOKEN", "secretkick")
	t.Setenv("GNASTY_KICK_TOKEN_FILE", "/var/secrets/kick.token")
	t.Setenv("GNASTY_KICK_TLS", "false")
	t.Setenv("GNASTY_KICK_BACKOFF_MIN_MS", "1200")
	t.Setenv("GNASTY_KICK_BACKOFF_MAX_MS", "42000")
	
	cfg := Load()
	if cfg.Sink.SQLite.Path != "/data/elora.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.Sink.SQLite.Path)
	}
	if cfg.Batch() != 25 {
		t.Fatalf("batch size mismatch: %d", cfg.Batch())
	}
	if cfg.FlushInterval() != 250*time.Millisecond {
		t.Fatalf("flush interval mismatch: %s", cfg.FlushInterval())
	}
	if !cfg.Twitch.Enabled {
		t.Fatalf("expected twitch enabled")
	}
	if len(cfg.Twitch.Channels) != 2 {
		t.Fatalf("expected two twitch channels, got %v", cfg.Twitch.Channels)
	}
	if cfg.Twitch.Nick != "elora_bot" {
		t.Fatalf("unexpected nick: %q", cfg.Twitch.Nick)
	}
	if cfg.Twitch.Token != "oauth:abc" {
		t.Fatalf("unexpected token: %q", cfg.Twitch.Token)
	}
	if cfg.Twitch.ClientSecret != "secret" {
		t.Fatalf("unexpected client secret: %q", cfg.Twitch.ClientSecret)
	}
	if cfg.Twitch.TLS {
		t.Fatalf("expected TLS disabled from env override")
	}
	if !cfg.Twitch.DebugDrops {
		t.Fatalf("expected twitch debug drops override")
	}
	if cfg.Twitch.BackoffMinMS != 1500 || cfg.Twitch.BackoffMaxMS != 45000 {
		t.Fatalf("unexpected twitch backoff override: min=%d max=%d", cfg.Twitch.BackoffMinMS, cfg.Twitch.BackoffMaxMS)
	}
	if cfg.Twitch.RefreshBackoffMinMS != 2000 || cfg.Twitch.RefreshBackoffMaxMS != 30000 {
		t.Fatalf("unexpected twitch refresh backoff override: min=%d max=%d", cfg.Twitch.RefreshBackoffMinMS, cfg.Twitch.RefreshBackoffMaxMS)
	}
	if !cfg.YouTube.Enabled || cfg.YouTube.LiveURL == "" {
		t.Fatalf("expected youtube enabled with URL")
	}
	if cfg.YouTube.RetrySeconds != 45 {
		t.Fatalf("expected youtube retry seconds override, got %d", cfg.YouTube.RetrySeconds)
	}
	if !cfg.YouTube.DumpUnhandled {
		t.Fatalf("expected youtube dump unhandled override")
	}
	if cfg.YouTube.PollTimeoutSecs != 60 {
		t.Fatalf("expected youtube poll timeout override 60, got %d", cfg.YouTube.PollTimeoutSecs)
	}
	if cfg.YouTube.PollIntervalMS != 1500 {
		t.Fatalf("expected youtube poll interval override 1500, got %d", cfg.YouTube.PollIntervalMS)
	}
	if !cfg.YouTube.Debug {
		t.Fatalf("expected youtube debug override")
	}
	if !cfg.Kick.Enabled {
		t.Fatalf("expected kick enabled by channels")
	}
	if len(cfg.Kick.Channels) != 2 {
		t.Fatalf("expected two kick channels, got %v", cfg.Kick.Channels)
	}
	if cfg.Kick.Nick != "kick_bot" || cfg.Kick.Token != "secretkick" || cfg.Kick.TokenFile != "/var/secrets/kick.token" {
		t.Fatalf("unexpected kick auth settings: %+v", cfg.Kick)
	}
	if cfg.Kick.TLS {
		t.Fatalf("expected kick tls false override")
	}
	if cfg.Kick.BackoffMinMS != 1200 || cfg.Kick.BackoffMaxMS != 42000 {
		t.Fatalf("unexpected kick backoff override: min=%d max=%d", cfg.Kick.BackoffMinMS, cfg.Kick.BackoffMaxMS)
	}
}

func TestRedactedSnapshot(t *testing.T) {
	cfg := Config{
		Sinks: []string{"sqlite"},
		Sink: SinkConfig{
			SQLite:     SQLiteConfig{Path: "/data/elora.db"},
			BatchSize:  10,
			FlushMaxMS: 500,
		},
		Twitch: TwitchConfig{
			Enabled:          true,
			Channels:         []string{"elora"},
			Nick:             "elora_bot",
			Token:            "oauth:secret",
			ClientID:         "abcd",
			ClientSecret:     "shh",
			RefreshToken:     "refresh",
			RefreshTokenFile: "/secrets/refresh",
		},
		Kick: KickConfig{
			Enabled: true,
			Channels: []string{"kickch"},
			Nick: "kick_bot",
			Token: "kicksecret",
			TokenFile: "/secrets/kick.token",
			TLS: true,
			BackoffMinMS: 900,
			BackoffMaxMS: 5000,
		},
		YouTube: YouTubeConfig{Enabled: true, LiveURL: "https://youtube.test/watch", RetrySeconds: 45, DumpUnhandled: true, PollTimeoutSecs: 30, PollIntervalMS: 7500, Debug: true},
	}

	summary := cfg.Summary()
	if summary.Twitch.Token != "***REDACTED*** (len=12)" {
		t.Fatalf("expected redacted token, got %q", summary.Twitch.Token)
	}
	if summary.Kick.Token != "***REDACTED*** (len=10)" {
		t.Fatalf("expected redacted kick token, got %q", summary.Kick.Token)
	}
	if !summary.Twitch.RefreshEnabled {
		t.Fatalf("expected refresh enabled to be true")
	}
	redacted := cfg.Redacted()
	twitchRaw := redacted["twitch"].(map[string]any)
	if twitchRaw["client_secret"].(string) != "***REDACTED*** (len=3)" {
		t.Fatalf("unexpected redacted client secret: %v", twitchRaw["client_secret"])
	}
	if twitchRaw["token"].(string) != "***REDACTED*** (len=12)" {
		t.Fatalf("unexpected redacted token: %v", twitchRaw["token"])
	}
	if twitchRaw["refresh_token"].(string) != "***REDACTED*** (len=7)" {
		t.Fatalf("unexpected redacted refresh token: %v", twitchRaw["refresh_token"])
	}
	if twitchRaw["refresh_enabled"].(bool) != true {
		t.Fatalf("expected refresh_enabled to be true, got %v", twitchRaw["refresh_enabled"])
	}
	if redacted["sink"].(map[string]any)["sqlite_path"].(string) != "/data/elora.db" {
		t.Fatalf("expected sqlite path preserved in redacted snapshot")
	}
	kickRaw := redacted["kick"].(map[string]any)
	if kickRaw["token"].(string) != "***REDACTED*** (len=10)" {
		t.Fatalf("expected redacted kick token: %v", kickRaw["token"])
	}
	if kickRaw["backoff_min_ms"].(int) != 900 || kickRaw["backoff_max_ms"].(int) != 5000 {
		t.Fatalf("unexpected kick backoff values in redacted snapshot, got min=%v max=%v", kickRaw["backoff_min_ms"], kickRaw["backoff_max_ms"])
	}
	youtubeRaw := redacted["youtube"].(map[string]any)
	if youtubeRaw["retry_seconds"].(int) != 45 {
		t.Fatalf("expected youtube retry seconds in redacted snapshot, got %v", youtubeRaw["retry_seconds"])
	}
	if youtubeRaw["dump_unhandled"].(bool) != true {
		t.Fatalf("expected youtube dump_unhandled true in redacted snapshot")
	}
	if youtubeRaw["poll_timeout_secs"].(int) != 30 {
		t.Fatalf("expected youtube poll_timeout_secs 30 in redacted snapshot, got %v", youtubeRaw["poll_timeout_secs"])
	}
	if youtubeRaw["poll_interval_ms"].(int) != 7500 {
		t.Fatalf("expected youtube poll_interval_ms 7500 in redacted snapshot, got %v", youtubeRaw["poll_interval_ms"])
	}
	if youtubeRaw["debug"].(bool) != true {
		t.Fatalf("expected youtube debug true in redacted snapshot")
	}
}

func TestYouTubePollBoolOverrides(t *testing.T) {
	t.Setenv("GNASTY_YT_URL", "https://example.test/watch")
	t.Setenv("GNASTY_YT_POLL_TIMEOUT_SECS", "false")
	t.Setenv("GNASTY_YT_POLL_INTERVAL_MS", "0")

	cfg := Load()
	if cfg.YouTube.PollTimeoutSecs != 0 {
		t.Fatalf("expected poll timeout disabled via false, got %d", cfg.YouTube.PollTimeoutSecs)
	}
	if cfg.YouTube.PollIntervalMS != 0 {
		t.Fatalf("expected poll interval disabled via 0, got %d", cfg.YouTube.PollIntervalMS)
	}
}

func TestTwitchRefreshEnabledDerivation(t *testing.T) {
	cases := []struct {
		name string
		cfg  TwitchConfig
		want bool
	}{
		{
			name: "missing client credentials",
			cfg: TwitchConfig{
				RefreshToken: "refresh",
			},
			want: false,
		},
		{
			name: "client creds without refresh",
			cfg: TwitchConfig{
				ClientID:     "id",
				ClientSecret: "secret",
			},
			want: false,
		},
		{
			name: "refresh token configured",
			cfg: TwitchConfig{
				ClientID:     "id",
				ClientSecret: "secret",
				RefreshToken: "refresh",
			},
			want: true,
		},
		{
			name: "refresh file configured",
			cfg: TwitchConfig{
				ClientID:         "id",
				ClientSecret:     "secret",
				RefreshTokenFile: "/tmp/refresh",
			},
			want: true,
		},
		{
			name: "missing secret",
			cfg: TwitchConfig{
				ClientID:         "id",
				RefreshTokenFile: "/tmp/refresh",
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Twitch: tc.cfg}
			summary := cfg.Summary()
			if summary.Twitch.RefreshEnabled != tc.want {
				t.Fatalf("summary refresh enabled mismatch: want %v got %v", tc.want, summary.Twitch.RefreshEnabled)
			}
			twitch := cfg.Redacted()["twitch"].(map[string]any)
			if twitch["refresh_enabled"].(bool) != tc.want {
				t.Fatalf("redacted refresh_enabled mismatch: want %v got %v", tc.want, twitch["refresh_enabled"])
			}
		})
	}
}
