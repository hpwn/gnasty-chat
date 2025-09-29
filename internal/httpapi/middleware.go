package httpapi

import (
	"compress/gzip"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

/***************
 * Access log recorder
 ***************/

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w}
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) Status() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *responseRecorder) Bytes() int64 { return r.bytes }

/***************
 * Gzip wrapper
 ***************/

type gzipResponseWriter struct {
	http.ResponseWriter
	writer *gzip.Writer
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.writer.Write(b)
}

func (g *gzipResponseWriter) Flush() {
	_ = g.writer.Flush()
	if flusher, ok := g.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (g *gzipResponseWriter) Close() error {
	return g.writer.Close()
}

// baseWriter peels off our recorder/wrappers and returns the underlying writer.
// Use this in handlers that require the concrete interfaces of the base
// ResponseWriter (e.g., WebSocket upgrades need http.Hijacker on HTTP/1.1).
func baseWriter(w http.ResponseWriter) http.ResponseWriter {
	if rr, ok := w.(*responseRecorder); ok && rr != nil && rr.ResponseWriter != nil {
		return rr.ResponseWriter
	}
	return w
}

// maybeGzip enables gzip when appropriate and returns the gzip writer and true.
// If gzip is not applied, it returns (nil, false).
func maybeGzip(w http.ResponseWriter, r *http.Request) (*gzipResponseWriter, bool) {
	ae := r.Header.Get("Accept-Encoding")
	if !strings.Contains(ae, "gzip") {
		return nil, false
	}
	// Never gzip upgraded (WebSocket, etc.)
	if r.Header.Get("Upgrade") != "" {
		return nil, false
	}
	// Do not gzip Server-Sent Events; it can interfere with proxies/buffering.
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return nil, false
	}

	// If we're sitting on top of a responseRecorder, unwrap to the underlying
	// real writer so we don't create recursion or double-wrapping.
	base := w
	if rr, ok := w.(*responseRecorder); ok && rr != nil && rr.ResponseWriter != nil {
		base = rr.ResponseWriter
	}

	gz := gzip.NewWriter(base)
	grw := &gzipResponseWriter{ResponseWriter: base, writer: gz}

	// Reflect compression and vary correctly on the outer writer headers.
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")

	// If a recorder is present, redirect its writes through the gzip writer,
	// keeping the recorder as the outer layer for status/byte counting.
	if rr, ok := w.(*responseRecorder); ok {
		rr.ResponseWriter = grw
	}

	return grw, true
}

/***************
 * Per-IP rate limiting
 ***************/

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type ipRateLimiter struct {
	mu       sync.Mutex
	entries  map[string]*clientLimiter
	rate     rate.Limit
	burst    int
	lifetime time.Duration
}

func newIPRateLimiter(rps int, burst int) *ipRateLimiter {
	if rps <= 0 || burst <= 0 {
		return nil
	}
	return &ipRateLimiter{
		entries:  make(map[string]*clientLimiter),
		rate:     rate.Limit(rps),
		burst:    burst,
		lifetime: 5 * time.Minute,
	}
}

func (l *ipRateLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[ip]
	if !ok {
		entry = &clientLimiter{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.entries[ip] = entry
	}
	entry.lastSeen = now
	allowed := entry.limiter.Allow()

	// Opportunistic cleanup if the map grows large.
	if len(l.entries) > 1024 {
		l.cleanup(now)
	}
	return allowed
}

func (l *ipRateLimiter) cleanup(now time.Time) {
	expireBefore := now.Add(-l.lifetime)
	for ip, entry := range l.entries {
		if entry.lastSeen.Before(expireBefore) {
			delete(l.entries, ip)
		}
	}
}

func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for _, part := range parts {
			if p := strings.TrimSpace(part); p != "" {
				return p
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

/***************
 * CORS policy
 ***************/

type corsPolicy struct {
	allowAll bool
	origins  map[string]struct{}
}

func newCORSPolicy(origins []string) *corsPolicy {
	if len(origins) == 0 {
		return nil
	}
	policy := &corsPolicy{origins: make(map[string]struct{})}
	for _, origin := range origins {
		o := strings.TrimSpace(origin)
		if o == "" {
			continue
		}
		if o == "*" {
			policy.allowAll = true
			policy.origins = nil
			break
		}
		policy.origins[o] = struct{}{}
	}
	return policy
}

func (c *corsPolicy) isAllowed(origin string) bool {
	if c == nil {
		return false
	}
	if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
		return false
	}
	if c.allowAll {
		return true
	}
	_, ok := c.origins[origin]
	return ok
}

// handlePreflight processes CORS OPTIONS requests. It returns (handled, status).
func (c *corsPolicy) handlePreflight(w http.ResponseWriter, r *http.Request) (bool, int) {
	if c == nil {
		return false, 0
	}
	if r.Method != http.MethodOptions {
		return false, 0
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false, 0
	}
	if !c.isAllowed(origin) {
		w.WriteHeader(http.StatusForbidden)
		return true, http.StatusForbidden
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET,OPTIONS")
	if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
	}
	w.Header().Set("Access-Control-Max-Age", "300")
	w.Header().Add("Vary", "Origin")
	w.WriteHeader(http.StatusNoContent)
	return true, http.StatusNoContent
}

// applyHeaders adds CORS response headers for non-preflight requests.
// Returns false if the Origin is present but not allowed.
func (c *corsPolicy) applyHeaders(w http.ResponseWriter, r *http.Request) bool {
	if c == nil {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if !c.isAllowed(origin) {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
	return true
}
