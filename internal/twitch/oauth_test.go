package twitch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type rewriteRoundTripper struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt *rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	clone.URL.Path = rt.target.Path
	if clone.URL.Path == "" {
		clone.URL.Path = req.URL.Path
	}
	clone.URL.RawQuery = req.URL.RawQuery
	clone.Host = rt.target.Host
	if rt.base == nil {
		rt.base = http.DefaultTransport
	}
	return rt.base.RoundTrip(clone)
}

func newTestManager(t *testing.T, handler http.HandlerFunc) (*RefreshManager, func()) {
	t.Helper()

	srv := httptest.NewServer(handler)
	originalEndpoint := tokenEndpoint
	tokenEndpoint = srv.URL
	t.Cleanup(func() { tokenEndpoint = originalEndpoint })
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	mgr := &RefreshManager{
		ClientID:     "cid",
		ClientSecret: "secret",
		RefreshToken: "refresh",
		TokenFile:    filepath.Join(t.TempDir(), "token"),
	}
	mgr.HTTP = &http.Client{Transport: &rewriteRoundTripper{target: target}}
	return mgr, srv.Close
}

func TestRefreshSuccess(t *testing.T) {
	mgr, closeSrv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh" {
			t.Fatalf("unexpected refresh token %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"abc123","expires_in":3600}`))
	})
	defer closeSrv()

	ctx := context.Background()
	token, expires, err := mgr.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh error: %v", err)
	}
	if token != "abc123" {
		t.Fatalf("unexpected token %q", token)
	}
	if expires != 3600*time.Second {
		t.Fatalf("unexpected expires %v", expires)
	}

	data, err := os.ReadFile(mgr.TokenFile)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if string(data) != "oauth:abc123\n" {
		t.Fatalf("unexpected token file contents %q", string(data))
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(mgr.TokenFile)
		if err != nil {
			t.Fatalf("stat token file: %v", err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("token file permissions too open: %v", info.Mode())
		}
	}
}

func TestRefreshInvalidGrant(t *testing.T) {
	mgr, closeSrv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":400,"message":"invalid_grant"}`))
	})
	defer closeSrv()

	_, _, err := mgr.Refresh(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestRefreshTokenFileError(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := &RefreshManager{
		ClientID:     "cid",
		ClientSecret: "secret",
		RefreshToken: "refresh",
		TokenFile:    tmpDir, // directory, not file
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rr := httptest.NewRecorder()
			rr.WriteHeader(http.StatusOK)
			_, _ = rr.Write([]byte(`{"access_token":"abc","expires_in":1}`))
			return rr.Result(), nil
		})},
	}

	_, _, err := mgr.Refresh(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "token file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
