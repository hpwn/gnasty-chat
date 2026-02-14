package twitchirc

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	dropSummaryInterval = 5 * time.Second
	dropSampleMaxLen    = 96
	dropChannelMaxLen   = 32
)

var (
	oauthTokenRe = regexp.MustCompile(`(?i)oauth:[^\s;]+`)
	longTokenRe  = regexp.MustCompile(`[A-Za-z0-9+/_=\-]{24,}`)
)

type ircSummary struct {
	command string
	channel string
	sample  string
}

type dropReasonSummary struct {
	total        int
	byCommand    map[string]int
	sampleByCmd  map[string]string
	channelByCmd map[string]string
}

type dropLogger struct {
	verbose  bool
	interval time.Duration
	nextEmit time.Time
	reasons  map[string]*dropReasonSummary
}

func newDropLogger(now time.Time, verbose bool, interval time.Duration) *dropLogger {
	if interval <= 0 {
		interval = dropSummaryInterval
	}
	return &dropLogger{
		verbose:  verbose,
		interval: interval,
		nextEmit: now.Add(interval),
		reasons:  make(map[string]*dropReasonSummary),
	}
}

func (d *dropLogger) note(now time.Time, reason, rawLine string) {
	if d == nil {
		return
	}
	summary := summarizeIRC(rawLine)
	if d.verbose {
		slog.Debug("twitchirc: dropped message",
			"reason", reason,
			"command", summary.command,
			"channel", summary.channel,
			"sample", summary.sample,
		)
	}

	entry := d.reasons[reason]
	if entry == nil {
		entry = &dropReasonSummary{
			byCommand:    make(map[string]int),
			sampleByCmd:  make(map[string]string),
			channelByCmd: make(map[string]string),
		}
		d.reasons[reason] = entry
	}

	entry.total++
	entry.byCommand[summary.command]++
	if _, ok := entry.sampleByCmd[summary.command]; !ok {
		entry.sampleByCmd[summary.command] = summary.sample
	}
	if _, ok := entry.channelByCmd[summary.command]; !ok {
		entry.channelByCmd[summary.command] = summary.channel
	}

	if now.After(d.nextEmit) || now.Equal(d.nextEmit) {
		d.flush(now)
	}
}

func (d *dropLogger) flush(now time.Time) {
	if d == nil {
		return
	}
	if len(d.reasons) == 0 {
		d.nextEmit = now.Add(d.interval)
		return
	}

	reasons := sortedKeys(d.reasons)
	for _, reason := range reasons {
		rs := d.reasons[reason]
		if rs == nil || rs.total == 0 {
			continue
		}
		slog.Info("twitchirc: dropped_"+logReasonName(reason),
			"total", rs.total,
			"commands", formatCommandCounts(rs.byCommand),
			"samples", formatCommandSamples(rs.sampleByCmd, rs.channelByCmd),
		)
	}

	clear(d.reasons)
	d.nextEmit = now.Add(d.interval)
}

func summarizeIRC(rawLine string) ircSummary {
	line := strings.TrimSpace(rawLine)
	if line == "" {
		return ircSummary{command: "UNKNOWN", sample: ""}
	}

	tagPart := ""
	if strings.HasPrefix(line, "@") {
		idx := strings.IndexByte(line, ' ')
		if idx == -1 {
			return ircSummary{
				command: "UNKNOWN",
				sample:  sanitizeAndTruncate(line, dropSampleMaxLen),
			}
		}
		tagPart = line[1:idx]
		line = strings.TrimSpace(line[idx+1:])
	}

	if strings.HasPrefix(line, ":") {
		idx := strings.IndexByte(line, ' ')
		if idx == -1 {
			return ircSummary{
				command: "UNKNOWN",
				sample:  sanitizeAndTruncate(line, dropSampleMaxLen),
			}
		}
		line = strings.TrimSpace(line[idx+1:])
	}

	if line == "" {
		return ircSummary{command: "UNKNOWN", sample: ""}
	}

	cmd := line
	rest := ""
	if idx := strings.IndexByte(line, ' '); idx != -1 {
		cmd = line[:idx]
		rest = strings.TrimSpace(line[idx+1:])
	}
	cmd = strings.ToUpper(strings.TrimSpace(cmd))
	if cmd == "" {
		cmd = "UNKNOWN"
	}

	channel := ""
	for _, part := range strings.Fields(rest) {
		if strings.HasPrefix(part, "#") {
			channel = part
			break
		}
	}

	sample := ""
	if cmd == "USERNOTICE" {
		if msgID := tagValue(tagPart, "msg-id"); msgID != "" {
			sample = "msg-id=" + msgID
		}
	}
	if sample == "" {
		if idx := strings.Index(rest, " :"); idx != -1 {
			sample = strings.TrimSpace(rest[idx+2:])
		}
	}
	if sample == "" && channel != "" {
		sample = channel
	}
	if sample == "" {
		sample = rest
	}
	sample = strings.TrimPrefix(sample, ":")

	return ircSummary{
		command: cmd,
		channel: sanitizeAndTruncate(channel, dropChannelMaxLen),
		sample:  sanitizeAndTruncate(sample, dropSampleMaxLen),
	}
}

func sanitizeAndTruncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")

	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "PASS ") || upper == "PASS" {
		s = "PASS [REDACTED]"
	}

	s = oauthTokenRe.ReplaceAllString(s, "oauth:[REDACTED]")
	s = longTokenRe.ReplaceAllStringFunc(s, func(v string) string {
		if strings.HasPrefix(v, "#") {
			return v
		}
		return "[REDACTED]"
	})

	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func tagValue(rawTags, key string) string {
	if rawTags == "" || key == "" {
		return ""
	}
	for _, kv := range strings.Split(rawTags, ";") {
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == key {
			return unescapeIRC(v)
		}
	}
	return ""
}

func readTwitchDropDebugEnv() bool {
	raw := strings.TrimSpace(os.Getenv("GNASTY_TWITCH_DEBUG_DROPS"))
	if raw == "" {
		return false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func formatCommandCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(counts))
	for _, cmd := range sortedKeys(counts) {
		parts = append(parts, fmt.Sprintf("%s:%d", cmd, counts[cmd]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func formatCommandSamples(samples map[string]string, channels map[string]string) string {
	if len(samples) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(samples))
	for _, cmd := range sortedKeys(samples) {
		sample := samples[cmd]
		channel := channels[cmd]
		if channel != "" {
			parts = append(parts, cmd+":'"+channel+" "+sample+"'")
			continue
		}
		parts = append(parts, cmd+":'"+sample+"'")
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func logReasonName(reason string) string {
	switch reason {
	case "not_privmsg":
		return "non_privmsg"
	default:
		return reason
	}
}
