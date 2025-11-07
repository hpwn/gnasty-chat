package ytlive

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// ResolveResult captures the outcome of a YouTube livestream lookup.
type ResolveResult struct {
	Live     bool
	WatchURL string
	ChatURL  string
}

// Resolver locates the active livestream for a configured YouTube URL or handle.
type Resolver struct {
	http *http.Client
}

// NewResolver creates a resolver backed by the provided HTTP client.
// If client is nil a default client with a sane timeout is used.
func NewResolver(client *http.Client) *Resolver {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Resolver{http: client}
}

// Resolve normalizes the provided input, fetches the associated YouTube page and
// attempts to determine whether a livestream is active. When a livestream is
// available the resolved watch and live-chat URLs are returned.
func (r *Resolver) Resolve(ctx context.Context, raw string) (ResolveResult, error) {
	if strings.TrimSpace(raw) == "" {
		return ResolveResult{}, errors.New("ytlive: empty url")
	}

	normalized, err := normalizeYouTubeURL(raw)
	if err != nil {
		return ResolveResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized.String(), nil)
	if err != nil {
		return ResolveResult{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ytlive-resolver/1.0)")

	resp, err := r.http.Do(req)
	if err != nil {
		return ResolveResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return ResolveResult{}, fmt.Errorf("ytlive: resolve status %s", resp.Status)
	}

	// Final request URL after redirects.
	finalURL := resp.Request.URL
	watchURL := canonicalWatchURL(finalURL)

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return ResolveResult{}, err
	}

	text := decodePage(string(body))
	chatURL := extractChatURL(text)

	live := false
	if chatURL != "" && watchURL != "" {
		live = true
	} else if watchURL != "" && containsLiveIndicator(text) {
		live = true
		// Fall back to the default live chat URL when we can't extract one.
		if chatURL == "" {
			chatURL = defaultChatURL(finalURL)
		}
	}

	if !live {
		return ResolveResult{Live: false, WatchURL: watchURL}, nil
	}

	return ResolveResult{Live: true, WatchURL: watchURL, ChatURL: chatURL}, nil
}

// normalizeYouTubeURL coerces YouTube URLs and handle shorthand into canonical
// https://www.youtube.com endpoints that can be fetched.
func normalizeYouTubeURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("ytlive: empty url")
	}

	if strings.HasPrefix(trimmed, "@") {
		trimmed = "https://www.youtube.com/" + trimmed
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("ytlive: parse url: %w", err)
	}
	u.Fragment = ""

	host := strings.ToLower(u.Host)
	switch host {
	case "youtu.be":
		id := strings.Trim(u.Path, "/")
		if id == "" {
			return nil, errors.New("ytlive: missing video id in youtu.be url")
		}
		return &url.URL{
			Scheme:   "https",
			Host:     "www.youtube.com",
			Path:     "/watch",
			RawQuery: url.Values{"v": []string{id}}.Encode(),
		}, nil
	case "youtube.com", "www.youtube.com":
		// Canonicalize the host.
		u.Scheme = "https"
		u.Host = "www.youtube.com"

		if isHandlePath(u.Path) {
			handle := normalizeHandlePath(u.Path)
			u.Path = handle
			u.RawQuery = ""
			return u, nil
		}

		if strings.EqualFold(u.Path, "/watch") {
			q := u.Query()
			videoID := strings.TrimSpace(q.Get("v"))
			if videoID == "" {
				return nil, errors.New("ytlive: watch url missing video id")
			}
			v := url.Values{"v": []string{videoID}}
			u.RawQuery = v.Encode()
			return u, nil
		}

		// Preserve other paths (e.g. existing live URLs) verbatim.
		u.Path = path.Clean(u.Path)
		return u, nil
	default:
		return nil, fmt.Errorf("ytlive: unsupported host %q", u.Host)
	}
}

func isHandlePath(p string) bool {
	return strings.HasPrefix(p, "/@")
}

func normalizeHandlePath(p string) string {
	trimmed := strings.TrimSuffix(p, "/")
	trimmed = strings.TrimSuffix(trimmed, "/live")
	if !strings.HasPrefix(trimmed, "/@") {
		return p
	}
	return trimmed + "/live"
}

func canonicalWatchURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	if !strings.EqualFold(u.Path, "/watch") {
		return ""
	}
	videoID := u.Query().Get("v")
	if strings.TrimSpace(videoID) == "" {
		return ""
	}
	values := url.Values{"v": []string{videoID}}
	return (&url.URL{Scheme: "https", Host: "www.youtube.com", Path: "/watch", RawQuery: values.Encode()}).String()
}

func defaultChatURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	videoID := strings.TrimSpace(u.Query().Get("v"))
	if videoID == "" {
		return ""
	}
	values := url.Values{"v": []string{videoID}}
	return (&url.URL{Scheme: "https", Host: "www.youtube.com", Path: "/live_chat", RawQuery: values.Encode()}).String()
}

func decodePage(body string) string {
	text := strings.ReplaceAll(body, "\\/", "/")
	text = strings.ReplaceAll(text, "\\u0026", "&")
	return html.UnescapeString(text)
}

func extractChatURL(body string) string {
	idx := strings.Index(body, "/live_chat?")
	if idx == -1 {
		idx = strings.Index(body, "https://www.youtube.com/live_chat?")
	}
	if idx == -1 {
		return ""
	}
	end := idx
	for end < len(body) {
		ch := body[end]
		if ch == '"' || ch == '\'' || ch == '<' || ch == '>' {
			break
		}
		end++
	}
	raw := body[idx:end]
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		u.Scheme = "https"
		u.Host = "www.youtube.com"
		return u.String()
	}
	if strings.HasPrefix(raw, "/") {
		return "https://www.youtube.com" + raw
	}
	return ""
}

func containsLiveIndicator(body string) bool {
	lowered := strings.ToLower(body)
	switch {
	case strings.Contains(lowered, "\"islivenow\":true"):
		return true
	case strings.Contains(lowered, "\"islive\":true"):
		return true
	case strings.Contains(lowered, "\"islivecontent\":true"):
		return true
	case strings.Contains(lowered, "livechatrenderer"):
		return true
	}
	return false
}
