package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/httpapi"
	"github.com/you/gnasty-chat/internal/sink"
)

type emitReq struct {
	ID         string    `json:"id,omitempty"`
	Platform   string    `json:"platform"`
	Username   string    `json:"username"`
	Text       string    `json:"text"`
	Ts         time.Time `json:"ts,omitempty"`
	EmotesJSON string    `json:"emotes_json,omitempty"`
	RawJSON    string    `json:"raw_json,omitempty"`
	BadgesJSON string    `json:"badges_json,omitempty"`
	Colour     string    `json:"colour,omitempty"`
}

func main() {
	var (
		addr   string
		sqlite string
	)

	flag.StringVar(&addr, "addr", ":8765", "HTTP listen address")
	flag.StringVar(&sqlite, "db", "devapi.db", "SQLite database path")
	flag.Parse()

	s, err := sink.OpenSQLite(sqlite)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer s.Close()
	if err := s.Ping(); err != nil {
		log.Fatalf("ping: %v", err)
	}

	log.Printf("devapi listening on %s (db=%s)", addr, sqlite)

	mux := http.NewServeMux()

	mux.HandleFunc("POST /emit", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req emitReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Platform == "" || req.Username == "" || req.Text == "" {
			http.Error(w, "platform, username, text required", http.StatusBadRequest)
			return
		}
		if req.Ts.IsZero() {
			req.Ts = time.Now().UTC()
		}
		if req.ID == "" {
			req.ID = req.Platform + "-" + req.Ts.Format("20060102T150405.000000000Z07:00")
		}

		msg := core.ChatMessage{
			ID:         req.ID,
			Ts:         req.Ts,
			Username:   req.Username,
			Platform:   req.Platform,
			Text:       req.Text,
			EmotesJSON: req.EmotesJSON,
			RawJSON:    req.RawJSON,
			BadgesJSON: req.BadgesJSON,
			Colour:     req.Colour,
		}
		if err := s.Write(msg); err != nil {
			http.Error(w, "insert failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": msg.ID})
	})

	mux.HandleFunc("GET /count", func(w http.ResponseWriter, r *http.Request) {
		filters, err := httpapi.FiltersFromRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		n, err := s.CountMessages(r.Context(), filters)
		if err != nil {
			http.Error(w, "count failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"count": n})
	})

	mux.HandleFunc("GET /messages", func(w http.ResponseWriter, r *http.Request) {
		filters, err := httpapi.FiltersFromRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		list, err := s.ListMessages(r.Context(), filters)
		if err != nil {
			http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
