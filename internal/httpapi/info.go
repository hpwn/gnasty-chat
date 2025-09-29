package httpapi

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

// BuildInfo describes the compiled binary.
type BuildInfo struct {
	Version  string
	Revision string
	BuiltAt  time.Time
}

type infoResponse struct {
	Version  string `json:"version"`
	Revision string `json:"rev"`
	BuiltAt  string `json:"built_at"`
	Go       string `json:"go"`
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	resp := infoResponse{
		Version:  s.opts.Build.Version,
		Revision: s.opts.Build.Revision,
		Go:       runtime.Version(),
	}
	if !s.opts.Build.BuiltAt.IsZero() {
		resp.BuiltAt = s.opts.Build.BuiltAt.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}
