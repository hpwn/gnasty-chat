package httpadmin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/you/gnasty-chat/internal/harvester"
)

type fakeReloader struct {
	login          string
	reloadErr      error
	switchTw       func(string) (bool, error)
	switchYouTube  func(string) (bool, error)
	applyConfig    func(harvester.RuntimeConfigPatch) (harvester.RuntimeApplyResult, harvester.RuntimeSettings, error)
	runtime        harvester.RuntimeSettings
	seenTwChannel  string
	seenYouTubeURL string
}

func (f *fakeReloader) ReloadTwitch() (string, error) {
	return f.login, f.reloadErr
}

func (f *fakeReloader) SwitchTwitchChannel(channel string) (bool, error) {
	f.seenTwChannel = channel
	if f.switchTw == nil {
		return false, nil
	}
	return f.switchTw(channel)
}

func (f *fakeReloader) SwitchYouTubeURL(url string) (bool, error) {
	f.seenYouTubeURL = url
	if f.switchYouTube == nil {
		return false, nil
	}
	return f.switchYouTube(url)
}

func (f *fakeReloader) ApplyRuntimeConfig(patch harvester.RuntimeConfigPatch) (harvester.RuntimeApplyResult, harvester.RuntimeSettings, error) {
	if f.applyConfig == nil {
		return harvester.RuntimeApplyResult{}, f.runtime, nil
	}
	return f.applyConfig(patch)
}

func (f *fakeReloader) RuntimeSettings() harvester.RuntimeSettings {
	return f.runtime
}

func TestServerReloadSuccess(t *testing.T) {
	rel := &fakeReloader{login: "streamer"}
	srv := New(rel)

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
	rel := &fakeReloader{reloadErr: errors.New("boom")}
	srv := New(rel)

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

func TestTwitchChannelEndpointNormalizesAndChanges(t *testing.T) {
	rel := &fakeReloader{
		switchTw: func(string) (bool, error) { return true, nil },
	}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/twitch/channel", bytes.NewBufferString(`{"channel":"https://www.twitch.tv/RocketLeague"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rel.seenTwChannel != "rocketleague" {
		t.Fatalf("normalized channel = %q, want rocketleague", rel.seenTwChannel)
	}
	var payload struct {
		Status  string `json:"status"`
		Changed bool   `json:"changed"`
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "ok" || !payload.Changed || payload.Channel != "rocketleague" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestTwitchChannelEndpointSameValue(t *testing.T) {
	rel := &fakeReloader{
		switchTw: func(string) (bool, error) { return false, nil },
	}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/twitch/channel?channel=@squeex", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Changed bool `json:"changed"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Changed {
		t.Fatalf("expected changed=false")
	}
}

func TestTwitchChannelEndpointEmpty(t *testing.T) {
	rel := &fakeReloader{}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/twitch/channel", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Status  string            `json:"status"`
		Error   string            `json:"error"`
		Details map[string]string `json:"details"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "error" || payload.Error != "validation_failed" || payload.Details["channel"] != "required" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestYouTubeURLEndpointNormalizesAndChanges(t *testing.T) {
	rel := &fakeReloader{
		switchYouTube: func(string) (bool, error) { return true, nil },
	}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/youtube/url", bytes.NewBufferString(`{"url":"@jynxzi"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rel.seenYouTubeURL != "https://www.youtube.com/@jynxzi/live" {
		t.Fatalf("normalized url = %q", rel.seenYouTubeURL)
	}
	var payload struct {
		Status  string `json:"status"`
		Changed bool   `json:"changed"`
		URL     string `json:"url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "ok" || !payload.Changed || payload.URL != "https://www.youtube.com/@jynxzi/live" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestYouTubeURLEndpointSameValue(t *testing.T) {
	rel := &fakeReloader{
		switchYouTube: func(string) (bool, error) { return false, nil },
	}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/youtube/url?url=https://youtu.be/abc123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Changed bool `json:"changed"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Changed {
		t.Fatalf("expected changed=false")
	}
}

func TestYouTubeURLEndpointEmpty(t *testing.T) {
	rel := &fakeReloader{}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/youtube/url", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Status  string            `json:"status"`
		Error   string            `json:"error"`
		Details map[string]string `json:"details"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "error" || payload.Error != "validation_failed" || payload.Details["url"] != "required" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAdminConfigValidationError(t *testing.T) {
	rel := &fakeReloader{
		applyConfig: func(_ harvester.RuntimeConfigPatch) (harvester.RuntimeApplyResult, harvester.RuntimeSettings, error) {
			return harvester.RuntimeApplyResult{}, harvester.RuntimeSettings{}, errors.New("youtube.retry_seconds: must be > 0")
		},
	}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/config", bytes.NewBufferString(`{"youtube":{"retry_seconds":0}}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "error" || payload.Error == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAdminConfigNoopChangedFalse(t *testing.T) {
	rel := &fakeReloader{
		runtime: harvester.RuntimeSettings{
			Sinks: harvester.SinkRuntimeSettings{
				Enabled:    []string{"sqlite"},
				BatchSize:  1,
				FlushMaxMS: 0,
			},
		},
		applyConfig: func(_ harvester.RuntimeConfigPatch) (harvester.RuntimeApplyResult, harvester.RuntimeSettings, error) {
			return harvester.RuntimeApplyResult{}, harvester.RuntimeSettings{
				Sinks: harvester.SinkRuntimeSettings{
					Enabled:    []string{"sqlite"},
					BatchSize:  1,
					FlushMaxMS: 0,
				},
			}, nil
		},
	}
	srv := New(rel)
	mux := http.NewServeMux()
	srv.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/config", bytes.NewBufferString(`{"sinks":{"batch_size":1}}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Status  string `json:"status"`
		Changed bool   `json:"changed"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "ok" || payload.Changed {
		t.Fatalf("payload = %+v", payload)
	}
}
