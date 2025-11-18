package twitchbadges

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

type tokenResponder struct {
	count *atomic.Int64
}

func (t tokenResponder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.count.Add(1)
	_ = r.ParseForm()
	if r.Form.Get("client_id") == "fail" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "token-123",
		"expires_in":   60,
	})
}

func TestResolverEnrichesBadges(t *testing.T) {
	tokenCalls := &atomic.Int64{}
	mux := http.NewServeMux()
	mux.Handle("/oauth2/token", tokenResponder{count: tokenCalls})
	mux.HandleFunc("/helix/chat/badges/global", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"set_id": "partner",
					"versions": []map[string]any{
						{"id": "1", "image_url_1x": "https://cdn/partner/1x.png"},
					},
				},
			},
		})
	})
	mux.HandleFunc("/helix/chat/badges", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("broadcaster_id") != "1234" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"set_id": "subscriber",
					"versions": []map[string]any{
						{"id": "17", "image_url_1x": "https://cdn/sub/17/1x.png", "image_url_2x": "https://cdn/sub/17/2x.png"},
					},
				},
			},
		})
	})
	mux.HandleFunc("/helix/users", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("login") != "channel" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "1234"}}})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	helixBaseURL = srv.URL + "/helix"
	oauthTokenURL = srv.URL + "/oauth2/token"

	r := &Resolver{ClientID: "client", ClientSecret: "secret", TTL: time.Minute}
	r.HTTP = srv.Client()

	badges := []core.ChatBadge{{Platform: "twitch", ID: "subscriber", Version: "17"}}
	enriched := r.Enrich(context.Background(), "channel", badges)

	if len(enriched) != 1 {
		t.Fatalf("expected 1 badge, got %d", len(enriched))
	}
	if len(enriched[0].Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(enriched[0].Images))
	}
	if enriched[0].Images[0].URL != "https://cdn/sub/17/1x.png" {
		t.Fatalf("unexpected image url: %#v", enriched[0].Images)
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("expected one token request, got %d", tokenCalls.Load())
	}

	// second call should come from cache without re-hitting Helix endpoints
	enriched = r.Enrich(context.Background(), "channel", badges)
	if len(enriched[0].Images) != 2 {
		t.Fatalf("expected cached images, got %#v", enriched[0].Images)
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("expected token to be cached, count=%d", tokenCalls.Load())
	}
}

func TestResolverGracefulFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/oauth2/token", tokenResponder{count: &atomic.Int64{}})
	mux.HandleFunc("/helix/chat/badges/global", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	helixBaseURL = srv.URL + "/helix"
	oauthTokenURL = srv.URL + "/oauth2/token"

	r := &Resolver{ClientID: "client", ClientSecret: "secret", TTL: time.Millisecond}
	r.HTTP = srv.Client()

	badges := []core.ChatBadge{{Platform: "twitch", ID: "subscriber", Version: "17"}}
	enriched := r.Enrich(context.Background(), "channel", badges)

	if len(enriched) != 1 {
		t.Fatalf("expected badge passthrough, got %d", len(enriched))
	}
	if len(enriched[0].Images) != 0 {
		t.Fatalf("expected no images on failure, got %#v", enriched[0].Images)
	}
}

func TestResolverDisabledWithoutCredentials(t *testing.T) {
	// Empty credentials should short-circuit without network calls.
	tokenCalls := &atomic.Int64{}
	mux := http.NewServeMux()
	mux.Handle("/oauth2/token", tokenResponder{count: tokenCalls})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	helixBaseURL = srv.URL + "/helix"
	oauthTokenURL = srv.URL + "/oauth2/token"

	r := &Resolver{ClientID: "", ClientSecret: "", TTL: time.Minute}
	r.HTTP = srv.Client()

	badges := []core.ChatBadge{{Platform: "twitch", ID: "subscriber", Version: "17"}}
	enriched := r.Enrich(context.Background(), "channel", badges)

	if got := tokenCalls.Load(); got != 0 {
		t.Fatalf("expected no token calls when credentials empty, got %d", got)
	}
	if len(enriched) != 1 || len(enriched[0].Images) != 0 {
		t.Fatalf("expected passthrough badges without images, got %#v", enriched)
	}
}
