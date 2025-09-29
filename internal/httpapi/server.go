package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

type Store interface {
	Count() (int64, error)
	ListRecent(limit int) ([]core.ChatMessage, error)
}

type Server struct {
	httpServer *http.Server
	store      Store

	mu      sync.Mutex
	clients map[chan core.ChatMessage]struct{}
	closed  bool
}

type Options struct {
	Addr string
}

func New(store Store, opts Options) *Server {
	srv := &Server{
		store:   store,
		clients: make(map[chan core.ChatMessage]struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealthz)
	mux.HandleFunc("/count", srv.handleCount)
	mux.HandleFunc("/messages", srv.handleMessages)
	mux.HandleFunc("/stream", srv.handleStream)

	srv.httpServer = &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return srv
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleCount(w http.ResponseWriter, _ *http.Request) {
	count, err := s.store.Count()
	if err != nil {
		http.Error(w, "count error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"count": count})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	rows, err := s.store.ListRecent(limit)
	if err != nil {
		http.Error(w, "list error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(rows)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}

	clientCh := make(chan core.ChatMessage, 256)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	s.clients[clientCh] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, clientCh)
		s.mu.Unlock()
	}()

	fmt.Fprintf(w, ":ok\n\n")
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ":ping\n\n")
			flusher.Flush()
		case msg, ok := <-clientCh:
			if !ok {
				return
			}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) Broadcast(msg core.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Server) Start() error {
	log.Printf("http api listening on %s", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for ch := range s.clients {
		close(ch)
	}
	s.mu.Unlock()
	return s.httpServer.Shutdown(ctx)
}
