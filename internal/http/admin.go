package httpadmin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/you/gnasty-chat/internal/harvester"
	"github.com/you/gnasty-chat/internal/twitch"
	"github.com/you/gnasty-chat/internal/ytlive"
)

type Reloader interface {
	ReloadTwitch() (login string, err error)
	SwitchTwitchChannel(channel string) (changed bool, err error)
	SwitchYouTubeURL(url string) (changed bool, err error)
	ApplyRuntimeConfig(patch harvester.RuntimeConfigPatch) (harvester.RuntimeApplyResult, harvester.RuntimeSettings, error)
	RuntimeSettings() harvester.RuntimeSettings
}

type Server struct {
	rel Reloader
}

func New(rel Reloader) *Server { return &Server{rel: rel} }

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/admin/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/admin/twitch/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		login, err := s.rel.ReloadTwitch()
		if err != nil {
			http.Error(w, "reload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status   string `json:"status"`
			Reloaded bool   `json:"reloaded"`
			Login    string `json:"login,omitempty"`
		}{
			Status:   "ok",
			Reloaded: true,
			Login:    login,
		})
	})

	mux.HandleFunc("/admin/twitch/channel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		raw, err := stringInput(r, "channel")
		if err != nil {
			writeValidationError(w, "channel")
			return
		}
		channel, err := twitch.NormalizeChannelLogin(raw)
		if err != nil {
			writeValidationError(w, "channel")
			return
		}

		changed, err := s.rel.SwitchTwitchChannel(channel)
		if err != nil {
			http.Error(w, "switch failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status  string `json:"status"`
			Changed bool   `json:"changed"`
			Channel string `json:"channel"`
		}{
			Status:  "ok",
			Changed: changed,
			Channel: channel,
		})
	})

	mux.HandleFunc("/admin/youtube/url", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		raw, err := stringInput(r, "url")
		if err != nil {
			writeValidationError(w, "url")
			return
		}
		url, err := ytlive.NormalizeLiveURL(raw)
		if err != nil {
			writeValidationError(w, "url")
			return
		}

		changed, err := s.rel.SwitchYouTubeURL(url)
		if err != nil {
			http.Error(w, "switch failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status  string `json:"status"`
			Changed bool   `json:"changed"`
			URL     string `json:"url"`
		}{
			Status:  "ok",
			Changed: changed,
			URL:     url,
		})
	})

	mux.HandleFunc("/admin/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, struct {
				Status string                    `json:"status"`
				Config harvester.RuntimeSettings `json:"config"`
			}{
				Status: "ok",
				Config: s.rel.RuntimeSettings(),
			})
			return
		case http.MethodPost:
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		patch, err := decodeConfigPatch(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}{
				Status: "error",
				Error:  err.Error(),
			})
			return
		}

		result, settings, err := s.rel.ApplyRuntimeConfig(patch)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}{
				Status: "error",
				Error:  err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, struct {
			Status  string                       `json:"status"`
			Changed bool                         `json:"changed"`
			Result  harvester.RuntimeApplyResult `json:"result"`
			Config  harvester.RuntimeSettings    `json:"config"`
		}{
			Status:  "ok",
			Changed: result.Changed,
			Result:  result,
			Config:  settings,
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeValidationError(w http.ResponseWriter, field string) {
	writeJSON(w, http.StatusBadRequest, struct {
		Status  string            `json:"status"`
		Error   string            `json:"error"`
		Details map[string]string `json:"details"`
	}{
		Status: "error",
		Error:  "validation_failed",
		Details: map[string]string{
			field: "required",
		},
	})
}

func stringInput(r *http.Request, key string) (string, error) {
	if r == nil {
		return "", errors.New("request required")
	}
	if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
		return value, nil
	}

	var payload map[string]string
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) {
			return "", errors.New("missing input")
		}
		return "", err
	}
	value := strings.TrimSpace(payload[key])
	if value == "" {
		return "", errors.New("missing input")
	}
	return value, nil
}

func decodeConfigPatch(body io.Reader) (harvester.RuntimeConfigPatch, error) {
	if body == nil {
		return harvester.RuntimeConfigPatch{}, errors.New("request body required")
	}
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	var patch harvester.RuntimeConfigPatch
	if err := dec.Decode(&patch); err != nil {
		if errors.Is(err, io.EOF) {
			return harvester.RuntimeConfigPatch{}, errors.New("request body required")
		}
		return harvester.RuntimeConfigPatch{}, err
	}
	return patch, nil
}
