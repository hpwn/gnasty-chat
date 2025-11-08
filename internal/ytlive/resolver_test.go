package ytlive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNormalizeYouTubeURL_HandleVariants(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare handle", "@creator", "https://www.youtube.com/@creator/live"},
		{"short host", "youtube.com/@creator/live", "https://www.youtube.com/@creator/live"},
		{"www host", "https://www.youtube.com/@creator", "https://www.youtube.com/@creator/live"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeYouTubeURL(tc.in)
			if err != nil {
				t.Fatalf("normalizeYouTubeURL() error = %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("normalizeYouTubeURL() = %q, want %q", got.String(), tc.want)
			}
		})
	}
}

func TestResolver_HandleLive(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/@creator/live", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/watch?v=abc123", http.StatusFound)
	})
	handler.HandleFunc("/watch", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "abc123" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html><html><head><script nonce="test">var ytInitialPlayerResponse = {"streamingData":{"hlsManifestUrl":"https://example.com/hls.m3u8"},"videoDetails":{"videoId":"abc123","isLiveContent":true}};</script></head><body></body></html>`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	resolver := NewResolver(&http.Client{Transport: rewriteTransport(server.URL), Timeout: 2 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	res, err := resolver.Resolve(ctx, "https://youtube.com/@creator/live")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !res.Live {
		t.Fatalf("Resolve() Live = false, want true")
	}
	if res.WatchURL != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("Resolve() WatchURL = %q", res.WatchURL)
	}
	if res.ChatURL != "https://www.youtube.com/live_chat?v=abc123" {
		t.Fatalf("Resolve() ChatURL = %q", res.ChatURL)
	}
}

func TestResolver_HandleOffline(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/@creator/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html><html><head><script>var ytInitialPlayerResponse = {"videoDetails":{"videoId":"offline123","isLiveContent":false}};</script></head><body></body></html>`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	resolver := NewResolver(&http.Client{Transport: rewriteTransport(server.URL), Timeout: 2 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	res, err := resolver.Resolve(ctx, "www.youtube.com/@creator/live")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if res.Live {
		t.Fatalf("Resolve() Live = true, want false")
	}
	if res.WatchURL != "https://www.youtube.com/watch?v=offline123" {
		t.Fatalf("Resolve() WatchURL = %q", res.WatchURL)
	}
	if res.ChatURL != "" {
		t.Fatalf("Resolve() ChatURL = %q, want empty", res.ChatURL)
	}
}

func TestResolver_DirectWatch(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/watch", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "def456" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html><html><head><script>var ytInitialData = {"playerResponse":{"streamingData":{"dashManifestUrl":"https://example.com/manifest.mpd"},"videoDetails":{"videoId":"def456","isLiveContent":true}}};</script></head><body></body></html>`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	resolver := NewResolver(&http.Client{Transport: rewriteTransport(server.URL), Timeout: 2 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	res, err := resolver.Resolve(ctx, "https://www.youtube.com/watch?v=def456")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !res.Live {
		t.Fatalf("Resolve() Live = false, want true")
	}
	if res.ChatURL != "https://www.youtube.com/live_chat?v=def456" {
		t.Fatalf("Resolve() ChatURL = %q", res.ChatURL)
	}
}

func rewriteTransport(target string) http.RoundTripper {
	urlTarget, err := url.Parse(target)
	if err != nil {
		panic(err)
	}

	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Host, "youtube.com") || req.URL.Host == "youtu.be" {
			clone := req.Clone(req.Context())
			clone.URL = cloneURL(clone.URL)
			clone.URL.Scheme = urlTarget.Scheme
			clone.URL.Host = urlTarget.Host
			clone.Host = urlTarget.Host
			return http.DefaultTransport.RoundTrip(clone)
		}
		return http.DefaultTransport.RoundTrip(req)
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	copy := *u
	return &copy
}
