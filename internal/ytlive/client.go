package ytlive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/you/gnasty-chat/internal/core"
)

type Config struct {
	LiveURL         string
	DumpUnhandled   bool
	PollTimeoutSecs int
	PollIntervalMS  int
	Debug           bool
}

type Handler func(core.ChatMessage)

type Client struct {
	cfg         Config
	handler     Handler
	http        *http.Client
	pollDelay   time.Duration
	pollTimeout time.Duration
}

const (
	defaultLivePollDelay = 3 * time.Second
	defaultPollTimeout   = 20 * time.Second
)

type ytEmoteLocation struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type ytEmoteImage struct {
	URL    string `json:"url,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

type ytEmote struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Locations []ytEmoteLocation `json:"locations,omitempty"`
	Images    []ytEmoteImage    `json:"images,omitempty"`
}

func New(cfg Config, handler Handler) *Client {
	httpClient := &http.Client{}

	timeout := defaultPollTimeout
	switch {
	case cfg.PollTimeoutSecs > 0:
		timeout = time.Duration(cfg.PollTimeoutSecs) * time.Second
	case cfg.PollTimeoutSecs < 0:
		timeout = 0
	}
	if timeout > 0 {
		httpClient.Timeout = timeout
	}

	pollDelay := defaultLivePollDelay
	if cfg.PollIntervalMS > 0 {
		pollDelay = time.Duration(cfg.PollIntervalMS) * time.Millisecond
	}

	return &Client{
		cfg:         cfg,
		handler:     handler,
		http:        httpClient,
		pollDelay:   pollDelay,
		pollTimeout: timeout,
	}
}

func (c *Client) Run(ctx context.Context) error {
	liveURL := strings.TrimSpace(c.cfg.LiveURL)
	if liveURL == "" {
		return errors.New("ytlive: LiveURL is required")
	}
	if _, err := url.ParseRequestURI(liveURL); err != nil {
		return fmt.Errorf("ytlive: invalid LiveURL: %w", err)
	}

	backoff := time.Second
	const maxBackoff = 60 * time.Second

	var (
		apiKey        string
		clientVersion string
		continuation  string
		totalMessages int
		lastLog       = time.Now()
	)

	bootstrap := func() bool {
		var err error
		apiKey, clientVersion, continuation, err = c.bootstrap(ctx, liveURL)
		if err != nil {
			log.Printf("ytlive: bootstrap failed: %v", err)
			if !sleepContext(ctx, backoff) {
				return false
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			return false
		}
		log.Printf("ytlive: bootstrap succeeded (version=%s)", clientVersion)
		backoff = time.Second
		return true
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if apiKey == "" || clientVersion == "" || continuation == "" {
			if !bootstrap() {
				continue
			}
		}

		pollCtx := ctx
		var cancel context.CancelFunc
		if c.pollTimeout > 0 {
			pollCtx, cancel = context.WithTimeout(ctx, c.pollTimeout)
		}
		if c.cfg.Debug {
			log.Printf(
				"ytlive[debug]: starting poll cont_len=%d poll_delay_ms=%d poll_timeout=%s",
				len(continuation),
				c.pollDelay.Milliseconds(),
				c.pollTimeoutString(),
			)
		}

		messages, nextContinuation, timeoutMs, hasTimeout, err := c.poll(pollCtx, apiKey, clientVersion, continuation)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			log.Printf("ytlive: poll error: %v", err)
			if !sleepContext(ctx, backoff) {
				return ctx.Err()
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			apiKey, clientVersion, continuation = "", "", ""
			continue
		}

		if len(messages) > 0 && c.handler != nil {
			for _, msg := range messages {
				c.handler(msg)
			}
		}

		if c.cfg.Debug {
			log.Printf(
				"ytlive[debug]: poll finished messages=%d cont_len=%d timeout_ms=%d has_timeout=%t",
				len(messages),
				len(nextContinuation),
				timeoutMs,
				hasTimeout,
			)
		}

		totalMessages += len(messages)
		if time.Since(lastLog) >= 10*time.Second {
			log.Printf("ytlive: received %d messages (total %d)", len(messages), totalMessages)
			lastLog = time.Now()
		}

		continuation = nextContinuation
		if continuation == "" {
			log.Printf("ytlive: missing continuation, re-bootstrap")
			apiKey, clientVersion, continuation = "", "", ""
		}

		delay, fromContinuation := nextLivePollDelay(timeoutMs, hasTimeout, c.pollDelay)
		if fromContinuation {
			log.Printf("ytlive: next poll in %dms (from continuation)", delay.Milliseconds())
		} else {
			if delay > 0 && c.pollDelay != delay {
				c.pollDelay = delay
			}
			log.Printf("ytlive: next poll in %dms (fallback)", delay.Milliseconds())
		}
		if !sleepContext(ctx, delay) {
			return ctx.Err()
		}
	}
}

func (c *Client) pollTimeoutString() string {
	if c.pollTimeout <= 0 {
		return "none"
	}
	return c.pollTimeout.String()
}

func (c *Client) bootstrap(ctx context.Context, liveURL string) (apiKey, clientVersion, continuation string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liveURL, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ytlive-harvester/1.0)")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", "", "", err
	}
	text := string(body)

	apiKey = extractString(text, `"INNERTUBE_API_KEY":"`)
	clientVersion = extractString(text, `"INNERTUBE_CLIENT_VERSION":"`)

	if apiKey == "" || clientVersion == "" {
		return "", "", "", errors.New("ytlive: could not locate api key or client version")
	}

	var initJSON string
	markers := []string{
		`ytInitialData"] = `,
		`ytInitialData" = `,
		`ytInitialData":`,
		`ytInitialData = `,
		`window["ytInitialData"] = `,
	}
	for _, marker := range markers {
		initJSON = extractJSONObject(text, marker)
		if initJSON != "" {
			break
		}
	}
	if initJSON == "" {
		return "", "", "", errors.New("ytlive: could not locate initial data")
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(initJSON), &data); err != nil {
		return "", "", "", fmt.Errorf("ytlive: parse initial data: %w", err)
	}

	continuation = findInitialContinuation(data)
	if continuation == "" {
		return "", "", "", errors.New("ytlive: continuation not found in initial data")
	}

	return apiKey, clientVersion, continuation, nil
}

func (c *Client) poll(ctx context.Context, apiKey, clientVersion, continuation string) ([]core.ChatMessage, string, int, bool, error) {
	endpoint := fmt.Sprintf("https://www.youtube.com/youtubei/v1/live_chat/get_live_chat?key=%s", url.QueryEscape(apiKey))

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB",
				"clientVersion": clientVersion,
				"hl":            "en",
			},
		},
		"continuation": continuation,
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, continuation, 0, false, err
	}

	if c.cfg.Debug {
		log.Printf("ytlive[debug]: poll request continuation_len=%d payload_bytes=%d", len(continuation), len(buf))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, continuation, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ytlive-harvester/1.0)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, continuation, 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, continuation, 0, false, fmt.Errorf("ytlive: poll status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, continuation, 0, false, err
	}

	if c.cfg.Debug {
		log.Printf(
			"ytlive[debug]: poll response status=%s bytes=%d snippet=%q",
			resp.Status,
			len(body),
			truncateString(string(body), 256),
		)
	}

	var payloadResp map[string]any
	if err := json.Unmarshal(body, &payloadResp); err != nil {
		return nil, continuation, 0, false, fmt.Errorf("ytlive: decode poll response: %w", err)
	}

	continuation, timeout, hasTimeout := extractContinuation(payloadResp)
	messages, summary, failures, nonChats := extractMessages(payloadResp)

	if c.cfg.Debug {
		log.Printf(
			"ytlive[debug]: poll parsed actions=%d chat_messages=%d timeout_ms=%d has_timeout=%t next_cont_len=%d",
			summary.actions,
			summary.chatMessages,
			timeout,
			hasTimeout,
			len(continuation),
		)
	}

	logPollResults(summary, failures, nonChats, c.cfg.DumpUnhandled)

	return messages, continuation, timeout, hasTimeout, nil
}

func extractContinuation(payload map[string]any) (string, int, bool) {
	cont := ""
	timeout := 0
	hasTimeout := false

	var walk func(any)
	walk = func(v any) {
		if cont != "" && hasTimeout {
			// still walk to capture better timeout or continuation if missing
		}
		switch val := v.(type) {
		case map[string]any:
			if cont == "" {
				if s, ok := val["continuation"].(string); ok && s != "" {
					cont = s
				}
				if cmd := digMap(val, "continuationEndpoint", "continuationCommand"); cmd != nil {
					if s, ok := cmd["token"].(string); ok && s != "" {
						cont = s
					}
				}
				if cmd := digMap(val, "liveChatContinuationEndpoint", "continuationCommand"); cmd != nil {
					if s, ok := cmd["token"].(string); ok && s != "" {
						cont = s
					}
				}
			}
			if !hasTimeout {
				switch tm := val["timeoutMs"].(type) {
				case float64:
					if tm > 0 {
						timeout = int(tm)
						hasTimeout = true
					}
				case string:
					tm = strings.TrimSpace(tm)
					if tm == "" {
						break
					}
					if n, err := strconv.Atoi(tm); err == nil && n > 0 {
						timeout = n
						hasTimeout = true
					}
				}
			}
			for _, child := range val {
				walk(child)
			}
		case []any:
			for _, child := range val {
				walk(child)
			}
		}
	}

	walk(payload)
	return cont, timeout, hasTimeout
}

type pollSummary struct {
	actions      int
	chatMessages int
	stored       int
	skipped      int
}

type chatFailure struct {
	id     string
	reason string
}

type nonChatAction struct {
	actionType string
	key        string
	raw        map[string]any
}

func extractMessages(payload map[string]any) ([]core.ChatMessage, pollSummary, []chatFailure, []nonChatAction) {
	actions := gatherActions(payload)
	summary := pollSummary{actions: len(actions)}

	var (
		messages []core.ChatMessage
		failures []chatFailure
		nonChats []nonChatAction
	)

	for _, action := range actions {
		renderers := collectTextRenderers(action)
		if len(renderers) == 0 {
			nonChats = append(nonChats, nonChatAction{
				actionType: detectActionType(action),
				key:        shortActionID(action),
				raw:        action,
			})
			continue
		}

		summary.chatMessages += len(renderers)
		for _, renderer := range renderers {
			if msg, ok, reason := buildMessage(renderer); ok {
				messages = append(messages, msg)
				continue
			} else {
				failures = append(failures, chatFailure{
					id:     shortActionID(renderer),
					reason: reason,
				})
			}
		}
	}

	summary.stored = len(messages)
	summary.skipped = summary.actions - summary.chatMessages
	if summary.skipped < 0 {
		summary.skipped = 0
	}

	return messages, summary, failures, nonChats
}

func gatherActions(payload map[string]any) []map[string]any {
	var out []map[string]any
	collect := func(arr []any) {
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	if arr, ok := payload["actions"].([]any); ok {
		collect(arr)
	}
	if arr, ok := payload["onResponseReceivedActions"].([]any); ok {
		collect(arr)
	}
	if lc := digMap(payload, "continuationContents", "liveChatContinuation"); lc != nil {
		if arr, ok := lc["actions"].([]any); ok {
			collect(arr)
		}
	}
	return out
}

func collectTextRenderers(action map[string]any) []map[string]any {
	var renderers []map[string]any
	keys := []string{"liveChatTextMessageRenderer", "liveChatLegacyTextMessageRenderer"}

	var walk func(any)
	walk = func(v any) {
		switch val := v.(type) {
		case map[string]any:
			for _, key := range keys {
				if renderer, ok := val[key].(map[string]any); ok {
					renderers = append(renderers, renderer)
				}
			}
			for _, child := range val {
				walk(child)
			}
		case []any:
			for _, child := range val {
				walk(child)
			}
		}
	}

	walk(action)
	return renderers
}

func detectActionType(action map[string]any) string {
	known := []string{
		"addChatItemAction",
		"addLiveChatTickerItemAction",
		"addLiveChatTickerHeaderAction",
		"addLiveChatItemAction",
		"markChatItemAsDeletedAction",
		"markChatItemsByAuthorAsDeletedAction",
		"liveChatItemListRenderer",
		"addLiveChatWarningMessageAction",
		"showLiveChatActionPanelAction",
		"showLiveChatTooltipAction",
		"updateLiveChatPollAction",
		"addLiveChatPollAction",
	}
	for _, key := range known {
		if _, ok := action[key]; ok {
			return key
		}
	}
	for key := range action {
		return key
	}
	return "unknown"
}

func shortActionID(v any) string {
	id := findStringRecursive(v, []string{"id", "key", "clientMessageId", "actionId"})
	if id == "" {
		return "unknown"
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func findStringRecursive(v any, keys []string) string {
	switch val := v.(type) {
	case map[string]any:
		for _, key := range keys {
			if s, ok := val[key].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		for _, child := range val {
			if s := findStringRecursive(child, keys); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range val {
			if s := findStringRecursive(child, keys); s != "" {
				return s
			}
		}
	}
	return ""
}

func logPollResults(summary pollSummary, failures []chatFailure, nonChats []nonChatAction, dumpRaw bool) {
	log.Printf("ytlive: poll summary actions=%d chat_messages=%d stored=%d skipped=%d", summary.actions, summary.chatMessages, summary.stored, summary.skipped)
	if summary.chatMessages != summary.stored {
		for _, failure := range failures {
			log.Printf("ytlive: warning dropped chat message id=%s reason=%s", failure.id, failure.reason)
		}
	}
	for _, action := range nonChats {
		logUnhandled(action.actionType, action.key, action.raw, dumpRaw)
	}
}

func logUnhandled(actionType, key string, raw map[string]any, dumpRaw bool) {
	log.Printf("ytlive: skipped non-chat action type=%s key=%s", actionType, key)
	if !dumpRaw {
		return
	}
	if rawDump := marshalTruncated(raw, 512); rawDump != "" {
		log.Printf("ytlive: unhandled action dump %s", rawDump)
	}
}

func marshalTruncated(v map[string]any, limit int) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	if len(data) > limit {
		data = data[:limit]
	}
	return string(data)
}

func truncateString(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

func buildMessage(renderer map[string]any) (core.ChatMessage, bool, string) {
	badges, badgesRaw := parseYouTubeBadges(renderer)
	text, emotes := messageTextAndEmotes(renderer)

	msg := core.ChatMessage{
		ID:            stringField(renderer, "id"),
		PlatformMsgID: stringField(renderer, "id"),
		Username:      textField(renderer, "authorName"),
		Platform:      "YouTube",
		Text:          text,
		Badges:        badges,
		BadgesRaw:     badgesRaw,
	}
	if len(emotes) > 0 {
		if data, err := json.Marshal(emotes); err == nil {
			msg.EmotesJSON = string(data)
		}
	}
	if raw, err := json.Marshal(renderer); err == nil {
		msg.RawJSON = string(raw)
	}
	if msg.Text == "" {
		return core.ChatMessage{}, false, "empty text"
	}
	if msg.ID == "" {
		msg.ID = fmt.Sprintf("yt-%d", time.Now().UnixNano())
	}
	if msg.PlatformMsgID == "" {
		msg.PlatformMsgID = msg.ID
	}
	msg.Ts = timestampField(renderer, "timestampUsec")
	return msg, true, ""
}

func messageTextAndEmotes(renderer map[string]any) (string, []ytEmote) {
	message, ok := renderer["message"].(map[string]any)
	if !ok {
		return "", nil
	}
	if runs, ok := message["runs"].([]any); ok {
		return runsTextAndEmotes(runs)
	}
	if s, ok := message["simpleText"].(string); ok {
		return s, nil
	}
	return "", nil
}

func runsTextAndEmotes(runs []any) (string, []ytEmote) {
	var (
		builder strings.Builder
		emotes  []ytEmote
		offset  int
	)
	for _, run := range runs {
		part, ok := run.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := part["text"].(string); ok {
			builder.WriteString(text)
			offset += utf16Len(text)
			continue
		}
		emoji, ok := part["emoji"].(map[string]any)
		if !ok {
			continue
		}

		shortcode := emojiShortcode(emoji)
		if shortcode == "" {
			if label := emojiAccessibilityLabel(emoji); label != "" {
				builder.WriteString(label)
				offset += utf16Len(label)
			}
			continue
		}

		start := offset
		builder.WriteString(shortcode)
		offset += utf16Len(shortcode)

		emoteID := stringField(emoji, "emojiId")
		if emoteID == "" {
			emoteID = shortcode
		}

		emotes = append(emotes, ytEmote{
			ID:   emoteID,
			Name: shortcode,
			Locations: []ytEmoteLocation{
				{Start: start, End: offset},
			},
			Images: emojiImages(emoji),
		})
	}
	return builder.String(), emotes
}

func parseYouTubeBadges(renderer map[string]any) ([]core.ChatBadge, core.BadgesRaw) {
	var badges []core.ChatBadge
	rawPayload := map[string]any{}

	addRaw := func(key string) {
		if v, ok := renderer[key]; ok {
			rawPayload[key] = v
		}
	}

	addRaw("authorBadges")
	addRaw("authorBadgesWithMetadata")
	addRaw("authorExternalChannelId")

	seen := make(map[string]bool)
	addBadge := func(id, version string) {
		if id == "" {
			return
		}
		key := id + "|" + version
		if seen[key] {
			return
		}
		seen[key] = true
		badges = append(badges, core.ChatBadge{Platform: "youtube", ID: id, Version: version})
	}

	extract := func(key string) {
		arr, ok := renderer[key].([]any)
		if !ok {
			return
		}
		for _, entry := range arr {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			id, version := interpretBadge(entryMap)
			addBadge(id, version)
		}
	}

	extract("authorBadges")
	extract("authorBadgesWithMetadata")

	var raw core.BadgesRaw
	if len(rawPayload) > 0 {
		raw = core.BadgesRaw{"youtube": rawPayload}
	}

	return badges, raw
}

func interpretBadge(entry map[string]any) (string, string) {
	badge := entry
	if inner, ok := entry["liveChatAuthorBadgeRenderer"].(map[string]any); ok {
		badge = inner
	}
	if inner, ok := entry["metadataBadgeRenderer"].(map[string]any); ok {
		badge = inner
	}

	style := strings.ToLower(stringField(badge, "style"))
	tooltip := stringField(badge, "tooltip")
	label := stringField(badge, "label")

	iconType := ""
	if icon, ok := badge["icon"].(map[string]any); ok {
		iconType = strings.ToLower(stringField(icon, "iconType"))
	}
	accLabel := ""
	if acc := digMap(badge, "accessibility", "accessibilityData"); acc != nil {
		accLabel = stringField(acc, "label")
	}

	combined := strings.ToLower(strings.Join([]string{style, tooltip, label, iconType, accLabel}, " "))

	switch {
	case strings.Contains(combined, "owner"):
		return "owner", ""
	case strings.Contains(combined, "moderator"):
		return "moderator", ""
	case strings.Contains(combined, "verified") || strings.Contains(iconType, "check"):
		return "verified", ""
	case strings.Contains(combined, "member"):
		return "member", extractBadgeVersion(tooltip, label, accLabel)
	}

	return "", ""
}

func extractBadgeVersion(texts ...string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\(([^)]+)\)`),
		regexp.MustCompile(`(?i)(\d+\s*(?:month|months|year|years))`),
		regexp.MustCompile(`(?i)(level\s*\d+)`),
		regexp.MustCompile(`(?i)(tier\s*\d+)`),
	}

	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		for _, pattern := range patterns {
			if match := pattern.FindStringSubmatch(text); len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
		}
	}
	return ""
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case fmt.Stringer:
			return t.String()
		}
	}
	return ""
}

func textField(m map[string]any, key string) string {
	if nested, ok := m[key].(map[string]any); ok {
		if s, ok := nested["simpleText"].(string); ok {
			return s
		}
	}
	return runsField(m, key)
}

func runsField(m map[string]any, key string) string {
	nested, ok := m[key].(map[string]any)
	if !ok {
		return ""
	}
	runs, ok := nested["runs"].([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, run := range runs {
		if part, ok := run.(map[string]any); ok {
			if text := runText(part); text != "" {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
}

func runText(part map[string]any) string {
	if text, ok := part["text"].(string); ok {
		return text
	}

	emoji, ok := part["emoji"].(map[string]any)
	if !ok {
		return ""
	}

	if shortcuts, ok := emoji["shortcuts"].([]any); ok && len(shortcuts) > 0 {
		if first, ok := shortcuts[0].(string); ok {
			return first
		}
	}

	if image, ok := emoji["image"].(map[string]any); ok {
		if acc := digMap(image, "accessibility", "accessibilityData"); acc != nil {
			if label := stringField(acc, "label"); label != "" {
				return label
			}
		}
	}

	if label := stringField(emoji, "emojiId"); label != "" {
		return label
	}

	return ""
}

func emojiShortcode(emoji map[string]any) string {
	if shortcuts, ok := emoji["shortcuts"].([]any); ok && len(shortcuts) > 0 {
		if first, ok := shortcuts[0].(string); ok {
			if trimmed := strings.TrimSpace(first); trimmed != "" {
				return trimmed
			}
		}
	}
	if label := stringField(emoji, "emojiId"); label != "" {
		return ":" + label + ":"
	}
	return ""
}

func emojiAccessibilityLabel(emoji map[string]any) string {
	if image, ok := emoji["image"].(map[string]any); ok {
		if acc := digMap(image, "accessibility", "accessibilityData"); acc != nil {
			if label := stringField(acc, "label"); label != "" {
				return label
			}
		}
	}
	return ""
}

func emojiImages(emoji map[string]any) []ytEmoteImage {
	image, ok := emoji["image"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := image["thumbnails"].([]any)
	if !ok {
		return nil
	}
	images := make([]ytEmoteImage, 0, len(raw))
	for _, entry := range raw {
		thumb, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		url := stringField(thumb, "url")
		if url == "" {
			continue
		}
		images = append(images, ytEmoteImage{
			URL:    normalizeImageURL(url),
			Width:  intField(thumb, "width"),
			Height: intField(thumb, "height"),
		})
	}
	if len(images) > 1 {
		sort.SliceStable(images, func(i, j int) bool {
			return images[i].Width*images[i].Height > images[j].Width*images[j].Height
		})
	}
	return images
}

func normalizeImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	if strings.HasPrefix(raw, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}

func intField(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case int64:
			return int(t)
		case json.Number:
			if n, err := t.Int64(); err == nil {
				return int(n)
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
		}
	}
	return 0
}

func utf16Len(s string) int {
	if s == "" {
		return 0
	}
	return len(utf16.Encode([]rune(s)))
}

func timestampField(m map[string]any, key string) time.Time {
	var ts time.Time
	raw, ok := m[key]
	if !ok {
		return time.Now().UTC()
	}
	switch v := raw.(type) {
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			ts = time.Unix(0, n*1000).UTC()
		}
	case float64:
		ts = time.Unix(0, int64(v)*1000).UTC()
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return ts
}

func extractJSONObject(text, marker string) string {
	idx := strings.Index(text, marker)
	if idx == -1 {
		return ""
	}
	start := idx + len(marker)
	for start < len(text) && (text[start] == ' ' || text[start] == '\n' || text[start] == '\r' || text[start] == '\t') {
		start++
	}
	if start >= len(text) || text[start] != '{' {
		return ""
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

func extractString(text, marker string) string {
	idx := strings.Index(text, marker)
	if idx == -1 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(text[start:], "\"")
	if end == -1 {
		return ""
	}
	return text[start : start+end]
}

func digMap(m map[string]any, keys ...string) map[string]any {
	current := m
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func findInitialContinuation(data map[string]any) string {
	type queueItem struct {
		value      any
		inLiveChat bool
	}

	queue := []queueItem{{value: data}}

	for len(queue) > 0 {
		var item queueItem
		item, queue = queue[0], queue[1:]
		switch v := item.value.(type) {
		case map[string]any:
			currentLiveChat := item.inLiveChat || mapHasLiveChatKey(v)
			if currentLiveChat {
				if cont := continuationFromNode(v); cont != "" {
					log.Printf("ytlive: using live chat continuation %q", cont)
					return cont
				}
			}
			for key, child := range v {
				nextLiveChat := currentLiveChat || isLiveChatKey(key)
				queue = append(queue, queueItem{value: child, inLiveChat: nextLiveChat})
			}
		case []any:
			for _, child := range v {
				queue = append(queue, queueItem{value: child, inLiveChat: item.inLiveChat})
			}
		}
	}
	return ""
}

func isLiveChatKey(key string) bool {
	return strings.Contains(strings.ToLower(key), "livechat")
}

func mapHasLiveChatKey(m map[string]any) bool {
	for key := range m {
		if isLiveChatKey(key) {
			return true
		}
	}
	return false
}

func continuationFromNode(node map[string]any) string {
	if arr, ok := node["continuations"].([]any); ok {
		for _, elem := range arr {
			if m, ok := elem.(map[string]any); ok {
				for _, key := range []string{"invalidationContinuationData", "timedContinuationData", "reloadContinuationData"} {
					if next := digMap(m, key); next != nil {
						if s, ok := next["continuation"].(string); ok && s != "" {
							return s
						}
					}
				}
			}
		}
	}
	if endpoint := digMap(node, "continuationEndpoint", "continuationCommand"); endpoint != nil {
		if s, ok := endpoint["token"].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func nextLivePollDelay(timeoutMs int, hasTimeout bool, fallback time.Duration) (time.Duration, bool) {
	if hasTimeout && timeoutMs > 0 {
		return time.Duration(timeoutMs) * time.Millisecond, true
	}
	if fallback <= 0 {
		return defaultLivePollDelay, false
	}
	return fallback, false
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = time.Millisecond
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
