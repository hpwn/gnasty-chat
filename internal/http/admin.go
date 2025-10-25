package httpadmin

import (
	"encoding/json"
	"net/http"
)

type Reloader interface {
	ReloadTwitch() (login string, err error)
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
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true", "login": login})
	})
}
