package config

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Sinks   []string
	Sink    SinkConfig
	Twitch  TwitchConfig
	YouTube YouTubeConfig
}

type SinkConfig struct {
	SQLite     SQLiteConfig
	BatchSize  int
	FlushMaxMS int
}

type SQLiteConfig struct {
	Path string
}

type TwitchConfig struct {
	Enabled           bool
	Channels          []string
	Nick              string
	Token             string
	TokenFile         string
	ClientID          string
	ClientSecret      string
	RefreshToken      string
	RefreshTokenFile  string
	TLS               bool
	LegacyChannelEnv  string
	LegacyTokenEnv    string
	LegacyClientIDEnv string
}

type YouTubeConfig struct {
	Enabled bool
	LiveURL string
}

const (
	defaultSQLitePath = "chat.db"
	defaultBatchSize  = 1
	defaultFlushMS    = 0
)

func Load() Config {
	cfg := Config{}

	sinksEnv := strings.TrimSpace(os.Getenv("GNASTY_SINKS"))
	receiversEnv := strings.TrimSpace(os.Getenv("GNASTY_RECEIVERS"))
	raw := sinksEnv
	if raw == "" {
		raw = receiversEnv
	}
	if raw == "" {
		raw = "sqlite"
	}
	cfg.Sinks = splitList(raw)

	cfg.Sink.SQLite.Path = strings.TrimSpace(os.Getenv("GNASTY_SINK_SQLITE_PATH"))
	if cfg.Sink.SQLite.Path == "" {
		cfg.Sink.SQLite.Path = defaultSQLitePath
	}

	cfg.Sink.BatchSize = readInt("GNASTY_SINK_BATCH_SIZE", defaultBatchSize)
	cfg.Sink.FlushMaxMS = readInt("GNASTY_SINK_FLUSH_MAX_MS", defaultFlushMS)

	twEnabled := readBool("GNASTY_TWITCH_ENABLED", false)
	cfg.Twitch.Enabled = twEnabled
	channels := splitList(os.Getenv("GNASTY_TWITCH_CHANNELS"))
	if len(channels) == 0 {
		legacy := strings.TrimSpace(os.Getenv("TWITCH_CHANNEL"))
		if legacy != "" {
			cfg.Twitch.LegacyChannelEnv = "TWITCH_CHANNEL"
			channels = []string{legacy}
		}
	}
	cfg.Twitch.Channels = dedupe(channels)
	cfg.Twitch.Nick = strings.TrimSpace(os.Getenv("GNASTY_TWITCH_NICK"))
	if cfg.Twitch.Nick == "" {
		cfg.Twitch.Nick = strings.TrimSpace(os.Getenv("TWITCH_NICK"))
	}

	cfg.Twitch.Token = strings.TrimSpace(os.Getenv("GNASTY_TWITCH_TOKEN"))
	if cfg.Twitch.Token == "" {
		cfg.Twitch.Token = strings.TrimSpace(os.Getenv("TWITCH_TOKEN"))
		if cfg.Twitch.Token != "" {
			cfg.Twitch.LegacyTokenEnv = "TWITCH_TOKEN"
		}
	}
	cfg.Twitch.TokenFile = strings.TrimSpace(os.Getenv("GNASTY_TWITCH_TOKEN_FILE"))
	if cfg.Twitch.TokenFile == "" {
		cfg.Twitch.TokenFile = strings.TrimSpace(os.Getenv("TWITCH_TOKEN_FILE"))
	}
	cfg.Twitch.ClientID = strings.TrimSpace(os.Getenv("GNASTY_TWITCH_CLIENT_ID"))
	if cfg.Twitch.ClientID == "" {
		cfg.Twitch.ClientID = strings.TrimSpace(os.Getenv("TWITCH_CLIENT_ID"))
		if cfg.Twitch.ClientID != "" {
			cfg.Twitch.LegacyClientIDEnv = "TWITCH_CLIENT_ID"
		}
	}
	cfg.Twitch.ClientSecret = strings.TrimSpace(os.Getenv("GNASTY_TWITCH_CLIENT_SECRET"))
	if cfg.Twitch.ClientSecret == "" {
		cfg.Twitch.ClientSecret = strings.TrimSpace(os.Getenv("TWITCH_CLIENT_SECRET"))
	}
	cfg.Twitch.RefreshToken = strings.TrimSpace(os.Getenv("GNASTY_TWITCH_REFRESH_TOKEN"))
	if cfg.Twitch.RefreshToken == "" {
		cfg.Twitch.RefreshToken = strings.TrimSpace(os.Getenv("TWITCH_REFRESH_TOKEN"))
	}
	cfg.Twitch.RefreshTokenFile = strings.TrimSpace(os.Getenv("GNASTY_TWITCH_REFRESH_TOKEN_FILE"))
	if cfg.Twitch.RefreshTokenFile == "" {
		cfg.Twitch.RefreshTokenFile = strings.TrimSpace(os.Getenv("TWITCH_REFRESH_TOKEN_FILE"))
	}
	cfg.Twitch.TLS = readBoolDefaultTrue("GNASTY_TWITCH_TLS", true)
	if !envExists("GNASTY_TWITCH_TLS") {
		cfg.Twitch.TLS = readBoolDefaultTrue("TWITCH_TLS", cfg.Twitch.TLS)
	}

	ytURL := strings.TrimSpace(os.Getenv("GNASTY_YT_URL"))
	if ytURL == "" {
		ytURL = strings.TrimSpace(os.Getenv("YOUTUBE_URL"))
	}
	cfg.YouTube.LiveURL = ytURL
	cfg.YouTube.Enabled = ytURL != ""

	if !cfg.Twitch.Enabled {
		cfg.Twitch.Enabled = len(cfg.Twitch.Channels) > 0
	}

	return cfg
}

func splitList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\t', '\n':
			return true
		}
		return false
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return dedupe(out)
}

func dedupe(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		key := strings.ToLower(strings.TrimSpace(v))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(v))
	}
	sort.Strings(out)
	return out
}

func readInt(name string, def int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n <= 0 {
		return def
	}
	return n
}

func readBool(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

func readBoolDefaultTrue(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

func envExists(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

func (c Config) Summary() Summary {
	twitchChannels := len(c.Twitch.Channels)
	ytChannels := 0
	if c.YouTube.LiveURL != "" {
		ytChannels = 1
	}
	refreshEnabled := c.Twitch.ClientID != "" && c.Twitch.ClientSecret != "" && (c.Twitch.RefreshToken != "" || c.Twitch.RefreshTokenFile != "")

	summary := Summary{
		Sinks:      append([]string(nil), c.Sinks...),
		SQLitePath: c.Sink.SQLite.Path,
		BatchSize:  c.Sink.BatchSize,
		FlushMaxMS: c.Sink.FlushMaxMS,
		Twitch: TwitchSummary{
			Enabled:          c.Twitch.Enabled,
			Channels:         twitchChannels,
			Nick:             c.Twitch.Nick,
			Token:            redactString(c.Twitch.Token),
			TokenFile:        c.Twitch.TokenFile,
			ClientID:         redactString(c.Twitch.ClientID),
			ClientSecret:     redactString(c.Twitch.ClientSecret),
			RefreshToken:     redactString(c.Twitch.RefreshToken),
			RefreshTokenFile: c.Twitch.RefreshTokenFile,
			RefreshEnabled:   refreshEnabled,
		},
		YouTube: YouTubeSummary{
			Enabled:  c.YouTube.Enabled,
			Channels: ytChannels,
			LiveURL:  c.YouTube.LiveURL,
		},
	}
	return summary
}

type Summary struct {
	Sinks      []string       `json:"sinks"`
	SQLitePath string         `json:"sqlite_path"`
	BatchSize  int            `json:"batch"`
	FlushMaxMS int            `json:"flush_ms"`
	Twitch     TwitchSummary  `json:"twitch"`
	YouTube    YouTubeSummary `json:"yt"`
}

type TwitchSummary struct {
	Enabled          bool   `json:"enabled"`
	Channels         int    `json:"channels"`
	Nick             string `json:"nick,omitempty"`
	Token            string `json:"token,omitempty"`
	TokenFile        string `json:"token_file,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
	ClientSecret     string `json:"client_secret,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	RefreshTokenFile string `json:"refresh_token_file,omitempty"`
	RefreshEnabled   bool   `json:"refresh_enabled"`
}

type YouTubeSummary struct {
	Enabled  bool   `json:"enabled"`
	Channels int    `json:"channels"`
	LiveURL  string `json:"live_url,omitempty"`
}

func (c Config) Redacted() map[string]any {
	refreshEnabled := c.Twitch.ClientID != "" && c.Twitch.ClientSecret != "" && (c.Twitch.RefreshToken != "" || c.Twitch.RefreshTokenFile != "")

	payload := map[string]any{
		"sinks": append([]string(nil), c.Sinks...),
		"sink": map[string]any{
			"sqlite_path": c.Sink.SQLite.Path,
			"batch_size":  c.Sink.BatchSize,
			"flush_ms":    c.Sink.FlushMaxMS,
		},
		"twitch": map[string]any{
			"enabled":            c.Twitch.Enabled,
			"channels":           append([]string(nil), c.Twitch.Channels...),
			"nick":               c.Twitch.Nick,
			"token":              redactString(c.Twitch.Token),
			"token_file":         c.Twitch.TokenFile,
			"client_id":          redactString(c.Twitch.ClientID),
			"client_secret":      redactString(c.Twitch.ClientSecret),
			"refresh_token":      redactString(c.Twitch.RefreshToken),
			"refresh_token_file": c.Twitch.RefreshTokenFile,
			"tls":                c.Twitch.TLS,
			"refresh_enabled":    refreshEnabled,
		},
		"youtube": map[string]any{
			"enabled": c.YouTube.Enabled,
			"live_url": func() string {
				if c.YouTube.LiveURL == "" {
					return ""
				}
				return c.YouTube.LiveURL
			}(),
		},
	}
	return payload
}

func (c Config) RedactedJSON() []byte {
	data, _ := json.MarshalIndent(c.Redacted(), "", "  ")
	return data
}

func redactString(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "***REDACTED*** (len=" + strconv.Itoa(len(value)) + ")"
}

func (c Config) HasSink(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, s := range c.Sinks {
		if strings.ToLower(strings.TrimSpace(s)) == name {
			return true
		}
	}
	return false
}

func (c Config) FlushInterval() time.Duration {
	if c.Sink.FlushMaxMS <= 0 {
		return 0
	}
	return time.Duration(c.Sink.FlushMaxMS) * time.Millisecond
}

func (c Config) Batch() int {
	if c.Sink.BatchSize <= 0 {
		return defaultBatchSize
	}
	return c.Sink.BatchSize
}

func (c Config) SummaryJSON() []byte {
	summary := struct {
		Config Summary `json:"config_summary"`
	}{Config: c.Summary()}
	data, _ := json.Marshal(summary)
	return data
}
