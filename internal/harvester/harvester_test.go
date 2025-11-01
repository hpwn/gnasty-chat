package harvester

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/you/gnasty-chat/internal/twitch"
	"github.com/you/gnasty-chat/internal/twitchauth"
)

type stubTwitchConn struct {
	mu    sync.Mutex
	token string
}

func (s *stubTwitchConn) Reconnect(access string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = access
	return nil
}

func (s *stubTwitchConn) JoinedNick() string {
	return "tester"
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	return f(req)
}

func TestReloadTwitchUpdatesRefreshManager(t *testing.T) {
	dir := t.TempDir()

	accessPath := filepath.Join(dir, "access.txt")
	if err := os.WriteFile(accessPath, []byte("oauth:initial\n"), 0o600); err != nil {
		t.Fatalf("write access file: %v", err)
	}

	refreshPath := filepath.Join(dir, "refresh.txt")
	if err := os.WriteFile(refreshPath, []byte("refresh-old\n"), 0o600); err != nil {
		t.Fatalf("write refresh file: %v", err)
	}

	tokens := twitchauth.TokenFiles{
		AccessPath:  accessPath,
		RefreshPath: refreshPath,
	}

	stub := &stubTwitchConn{}
	refreshMgr := &twitch.RefreshManager{
		ClientID:     "client",
		ClientSecret: "secret",
		RefreshToken: "refresh-old",
		TokenFile:    accessPath,
	}

	har := New(tokens, stub, refreshMgr.SetRefreshToken)

	if err := os.WriteFile(accessPath, []byte("oauth:new-access\n"), 0o600); err != nil {
		t.Fatalf("update access file: %v", err)
	}
	if err := os.WriteFile(refreshPath, []byte("refresh-new\n"), 0o600); err != nil {
		t.Fatalf("update refresh file: %v", err)
	}

	login, err := har.ReloadTwitch()
	if err != nil {
		t.Fatalf("ReloadTwitch: %v", err)
	}
	if login != "tester" {
		t.Fatalf("ReloadTwitch login = %q, want tester", login)
	}

	stub.mu.Lock()
	if got := stub.token; got != "oauth:new-access" {
		stub.mu.Unlock()
		t.Fatalf("Reconnect token = %q, want oauth:new-access", got)
	}
	stub.mu.Unlock()

	var seenRefresh string
	refreshMgr.HTTP = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := req.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			seenRefresh = req.Form.Get("refresh_token")
			body := io.NopCloser(strings.NewReader(`{"access_token":"next","expires_in":1}`))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       body,
				Header:     make(http.Header),
			}, nil
		}),
	}

	if _, _, err := refreshMgr.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh after reload: %v", err)
	}
	if seenRefresh != "refresh-new" {
		t.Fatalf("refresh token sent = %q, want refresh-new", seenRefresh)
	}
}
