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
	"strconv"
	"strings"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

type Config struct {
	LiveURL string
}

type Handler func(core.ChatMessage)

type Client struct {
	cfg     Config
	handler Handler
	http    *http.Client
}

func New(cfg Config, handler Handler) *Client {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	return &Client{
		cfg:     cfg,
		handler: handler,
		http:    httpClient,
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

		messages, nextContinuation, timeout, err := c.poll(ctx, apiKey, clientVersion, continuation)
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

		if timeout <= 0 {
			timeout = 1500
		}
		if !sleepContext(ctx, time.Duration(timeout)*time.Millisecond) {
			return ctx.Err()
		}
	}
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

func (c *Client) poll(ctx context.Context, apiKey, clientVersion, continuation string) ([]core.ChatMessage, string, int, error) {
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
		return nil, continuation, 1500, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, continuation, 1500, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ytlive-harvester/1.0)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, continuation, 1500, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, continuation, 1500, fmt.Errorf("ytlive: poll status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, continuation, 1500, err
	}

	var payloadResp map[string]any
	if err := json.Unmarshal(body, &payloadResp); err != nil {
		return nil, continuation, 1500, fmt.Errorf("ytlive: decode poll response: %w", err)
	}

	continuation, timeout := extractContinuation(payloadResp)
	messages := extractMessages(payloadResp)
	return messages, continuation, timeout, nil
}

func extractContinuation(payload map[string]any) (string, int) {
	cont := ""
	timeout := 0

	var walk func(any)
	walk = func(v any) {
		if cont != "" && timeout > 0 {
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
			if timeout == 0 {
				if tm, ok := val["timeoutMs"].(float64); ok && tm > 0 {
					timeout = int(tm)
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
	return cont, timeout
}

func extractMessages(payload map[string]any) []core.ChatMessage {
	var messages []core.ChatMessage
	actions := gatherActions(payload)
	for _, action := range actions {
		if renderer := digMap(action, "addChatItemAction", "item", "liveChatTextMessageRenderer"); renderer != nil {
			if msg, ok := buildMessage(renderer); ok {
				messages = append(messages, msg)
			}
		}
		if appendAction := digMap(action, "appendContinuationItemsAction"); appendAction != nil {
			if items, ok := appendAction["continuationItems"].([]any); ok {
				for _, item := range items {
					itemMap, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if renderer, ok := itemMap["liveChatTextMessageRenderer"].(map[string]any); ok {
						if msg, ok := buildMessage(renderer); ok {
							messages = append(messages, msg)
						}
					}
					if renderer := digMap(itemMap, "addChatItemAction", "item", "liveChatTextMessageRenderer"); renderer != nil {
						if msg, ok := buildMessage(renderer); ok {
							messages = append(messages, msg)
						}
					}
				}
			}
		}
	}
	return messages
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

func buildMessage(renderer map[string]any) (core.ChatMessage, bool) {
	msg := core.ChatMessage{
		ID:            stringField(renderer, "id"),
		PlatformMsgID: stringField(renderer, "id"),
		Username:      textField(renderer, "authorName"),
		Platform:      "YouTube",
		Text:          textField(renderer, "message"),
	}
	if msg.Text == "" {
		return core.ChatMessage{}, false
	}
	if msg.ID == "" {
		msg.ID = fmt.Sprintf("yt-%d", time.Now().UnixNano())
	}
	if msg.PlatformMsgID == "" {
		msg.PlatformMsgID = msg.ID
	}
	msg.Ts = timestampField(renderer, "timestampUsec")
	return msg, true
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
			if text, ok := part["text"].(string); ok {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
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
