package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"

	"github.com/you/gnasty-chat/internal/core"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type Store interface {
	CountMessages(ctx context.Context, filters Filters) (int64, error)
	ListMessages(ctx context.Context, filters Filters) ([]core.ChatMessage, error)
}

type Options struct {
	Addr            string
	CORSOrigins     []string
	RateLimitRPS    int
	RateLimitBurst  int
	EnableMetrics   bool
	EnableAccessLog bool
	EnablePprof     bool
	Build           BuildInfo
}

type streamClient struct {
	ch        chan core.ChatMessage
	filters   Filters
	transport string
}

type Server struct {
	httpServer *http.Server
	store      Store
	opts       Options

	mu      sync.Mutex
	clients map[*streamClient]struct{}
	closed  bool

	rateLimiter *ipRateLimiter
	cors        *corsPolicy
	metrics     *Metrics
}

func New(store Store, opts Options) *Server {
	srv := &Server{
		store:       store,
		opts:        opts,
		clients:     make(map[*streamClient]struct{}),
		rateLimiter: newIPRateLimiter(opts.RateLimitRPS, opts.RateLimitBurst),
		cors:        newCORSPolicy(opts.CORSOrigins),
	}
	if opts.EnableMetrics {
		srv.metrics = newMetrics()
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	srv.httpServer = &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return srv
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.Handle("/healthz", s.wrap("healthz", s.handleHealthz, handlerOptions{}))
	mux.Handle("/count", s.wrap("count", s.handleCount, handlerOptions{gzip: true}))
	mux.Handle("/messages", s.wrap("messages", s.handleMessages, handlerOptions{gzip: true}))
	mux.Handle("/stream", s.wrap("stream", s.handleStream, handlerOptions{}))
	mux.Handle("/ws", s.wrap("ws", s.handleWS, handlerOptions{}))
	mux.Handle("/info", s.wrap("info", s.handleInfo, handlerOptions{}))
	if s.metrics != nil {
		mux.Handle("/metrics", s.wrap("metrics", s.handleMetrics, handlerOptions{}))
	}
	if s.opts.EnablePprof {
		mux.Handle("/debug/pprof/", s.wrap("pprof", http.HandlerFunc(pprof.Index), handlerOptions{}))
		mux.Handle("/debug/pprof/cmdline", s.wrap("pprof", http.HandlerFunc(pprof.Cmdline), handlerOptions{}))
		mux.Handle("/debug/pprof/profile", s.wrap("pprof", http.HandlerFunc(pprof.Profile), handlerOptions{}))
		mux.Handle("/debug/pprof/symbol", s.wrap("pprof", http.HandlerFunc(pprof.Symbol), handlerOptions{}))
		mux.Handle("/debug/pprof/trace", s.wrap("pprof", http.HandlerFunc(pprof.Trace), handlerOptions{}))
	}
}

type handlerOptions struct {
	gzip bool
}

func (s *Server) wrap(route string, fn http.HandlerFunc, opts handlerOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newResponseRecorder(w)
		start := time.Now()
		var gz *gzipResponseWriter
		var panicErr any

		defer func() {
			if gz != nil {
				_ = gz.Close()
			}
			if panicErr != nil {
				log.Printf("httpapi: panic recovered: %v", panicErr)
			}
			status := rec.Status()
			duration := time.Since(start)
			if s.metrics != nil {
				s.metrics.ObserveRequest(route, r.Method, status, duration, rec.Bytes())
			}
			if s.opts.EnableAccessLog {
				s.logAccess(r, status, duration, rec.Bytes())
			}
		}()

		defer func() {
			if err := recover(); err != nil {
				panicErr = err
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}
		}()

		if s.cors != nil {
			if handled, status := s.cors.handlePreflight(rec, r); handled {
				rec.status = status
				return
			}
		}

		if s.cors != nil && r.Method != http.MethodOptions {
			if !s.cors.applyHeaders(rec, r) {
				http.Error(rec, "origin not allowed", http.StatusForbidden)
				rec.status = http.StatusForbidden
				return
			}
		}

		if s.rateLimiter != nil {
			if !s.rateLimiter.Allow(remoteIP(r)) {
				if s.metrics != nil {
					s.metrics.IncRateLimited()
				}
				http.Error(rec, "rate limit exceeded", http.StatusTooManyRequests)
				rec.status = http.StatusTooManyRequests
				return
			}
		}

		if opts.gzip {
			if gzWriter, ok := maybeGzip(rec, r); ok {
				gz = gzWriter
				rec.ResponseWriter = gzWriter
			}
		}

		fn(rec, r)
	})
}

func (s *Server) logAccess(r *http.Request, status int, dur time.Duration, bytes int64) {
	remote := remoteIP(r)
	path := r.URL.RequestURI()
	ua := r.Header.Get("User-Agent")
	log.Printf("http access remote=%s method=%s path=%s status=%d dur=%s bytes=%d ua=%q", remote, r.Method, path, status, dur, bytes, ua)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	filters, err := FiltersFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	count, err := s.store.CountMessages(r.Context(), filters)
	if err != nil {
		http.Error(w, "count error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"count": count})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	filters, err := FiltersFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := s.store.ListMessages(r.Context(), filters)
	if err != nil {
		http.Error(w, "list error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(rows)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filters, err := FiltersFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filters = filters.CloneForStream()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if r.Method == http.MethodHead {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}

	client := &streamClient{
		ch:        make(chan core.ChatMessage, 256),
		filters:   filters,
		transport: "sse",
	}

	if !s.addClient(client) {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	defer s.removeClient(client)

	if s.metrics != nil {
		s.metrics.IncSSEClients(1)
		defer s.metrics.IncSSEClients(-1)
	}

	fmt.Fprintf(w, ":ok\n\n")
	flusher.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ":ping %d\n\n", time.Now().Unix()); err != nil {
				return
			}
			flusher.Flush()
		case msg, ok := <-client.ch:
			if !ok {
				return
			}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
			if s.metrics != nil {
				s.metrics.IncMessagesSent("sse")
			}
		}
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	filters, err := FiltersFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filters = filters.CloneForStream()

	if s.isClosed() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		log.Printf("websocket accept error: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := conn.CloseRead(r.Context())

	client := &streamClient{
		ch:        make(chan core.ChatMessage, 256),
		filters:   filters,
		transport: "ws",
	}

	if !s.addClient(client) {
		_ = conn.Close(websocket.StatusPolicyViolation, "server shutting down")
		return
	}
	defer s.removeClient(client)

	if s.metrics != nil {
		s.metrics.IncWSClients(1)
		defer s.metrics.IncWSClients(-1)
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := conn.Ping(pingCtx); err != nil {
				cancel()
				return
			}
			cancel()
		case msg, ok := <-client.ch:
			if !ok {
				_ = conn.Close(websocket.StatusNormalClosure, "server shutting down")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := wsjson.Write(writeCtx, conn, msg); err != nil {
				cancel()
				return
			}
			cancel()
			if s.metrics != nil {
				s.metrics.IncMessagesSent("ws")
			}
		}
	}
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.NotFound(w, r)
		return
	}
	s.metrics.Handler().ServeHTTP(w, r)
}

func (s *Server) addClient(client *streamClient) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.clients[client] = struct{}{}
	return true
}

func (s *Server) removeClient(client *streamClient) {
	s.mu.Lock()
	if _, ok := s.clients[client]; ok {
		delete(s.clients, client)
		close(client.ch)
	}
	s.mu.Unlock()
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) Broadcast(msg core.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for client := range s.clients {
		if !client.filters.Matches(msg) {
			continue
		}
		select {
		case client.ch <- msg:
		default:
			if s.metrics != nil {
				s.metrics.IncBroadcastDrops(client.transport)
			}
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
	for client := range s.clients {
		close(client.ch)
	}
	s.clients = make(map[*streamClient]struct{})
	s.mu.Unlock()
	return s.httpServer.Shutdown(ctx)
}

// ReportDBWriteError increments the DB write error metric if enabled.
func (s *Server) ReportDBWriteError() {
	if s.metrics != nil {
		s.metrics.IncDBWriteErrors()
	}
}

// MetricsEnabled reports whether metrics are enabled for this server.
func (s *Server) MetricsEnabled() bool {
	return s.metrics != nil
}
