package httpadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeReloader struct {
	login string
	err   error
}

func (f fakeReloader) ReloadTwitch() (string, error) {
	return f.login, f.err
}

func TestServerReloadSuccess(t *testing.T) {
	srv := New(fakeReloader{login: "streamer"})

	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/twitch/reload", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("expected content-type application/json; charset=utf-8, got %q", ct)
	}

	var payload struct {
		Status   string `json:"status"`
		Reloaded bool   `json:"reloaded"`
		Login    string `json:"login"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload.Status != "ok" || !payload.Reloaded || payload.Login != "streamer" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestServerReloadError(t *testing.T) {
	srv := New(fakeReloader{err: errors.New("boom")})

	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/twitch/reload", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	if body := rec.Body.String(); body != "reload failed: boom\n" {
		t.Fatalf("unexpected body: %q", body)
	}
}
