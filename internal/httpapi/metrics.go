package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics bundles Prometheus collectors for the HTTP API.
type Metrics struct {
	registry        *prometheus.Registry
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	wsClients       prometheus.Gauge
	sseClients      prometheus.Gauge
	broadcastDrops  *prometheus.CounterVec
	rateLimited     prometheus.Counter
	messagesSent    *prometheus.CounterVec
	dbWriteErrors   prometheus.Counter
}

func newMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gnasty",
			Name:      "http_requests_total",
			Help:      "Total HTTP requests received",
		}, []string{"route", "method", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "gnasty",
			Name:      "http_request_duration_seconds",
			Help:      "Histogram of HTTP request durations",
			Buckets:   prometheus.DefBuckets,
		}, []string{"route", "method"}),
		wsClients: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "gnasty",
			Name:      "ws_clients",
			Help:      "Current connected WebSocket clients",
		}),
		sseClients: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "gnasty",
			Name:      "sse_clients",
			Help:      "Current connected SSE clients",
		}),
		broadcastDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gnasty",
			Name:      "broadcast_drops_total",
			Help:      "Number of messages dropped due to slow clients",
		}, []string{"transport"}),
		rateLimited: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "gnasty",
			Name:      "http_rate_limited_total",
			Help:      "Number of HTTP requests rejected due to rate limiting",
		}),
		messagesSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gnasty",
			Name:      "messages_sent_total",
			Help:      "Number of chat messages delivered to clients",
		}, []string{"transport"}),
		dbWriteErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "gnasty",
			Name:      "db_write_errors_total",
			Help:      "Number of database write errors reported",
		}),
	}

	registry.MustRegister(
		m.requestsTotal,
		m.requestDuration,
		m.wsClients,
		m.sseClients,
		m.broadcastDrops,
		m.rateLimited,
		m.messagesSent,
		m.dbWriteErrors,
	)

	return m
}

// Handler returns an HTTP handler exposing the metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveRequest records timing and status information.
func (m *Metrics) ObserveRequest(route, method string, status int, dur time.Duration, bytes int64) {
	if m == nil {
		return
	}
	m.requestsTotal.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.requestDuration.WithLabelValues(route, method).Observe(dur.Seconds())
}

// IncWSClients adjusts the WebSocket client gauge by delta.
func (m *Metrics) IncWSClients(delta float64) {
	if m == nil {
		return
	}
	m.wsClients.Add(delta)
}

// IncSSEClients adjusts the SSE client gauge by delta.
func (m *Metrics) IncSSEClients(delta float64) {
	if m == nil {
		return
	}
	m.sseClients.Add(delta)
}

// IncBroadcastDrops increments the drop counter.
func (m *Metrics) IncBroadcastDrops(transport string) {
	if m == nil {
		return
	}
	m.broadcastDrops.WithLabelValues(transport).Inc()
}

// IncRateLimited increments the rate limit counter.
func (m *Metrics) IncRateLimited() {
	if m == nil {
		return
	}
	m.rateLimited.Inc()
}

// IncMessagesSent increments the sent counter for a transport.
func (m *Metrics) IncMessagesSent(transport string) {
	if m == nil {
		return
	}
	m.messagesSent.WithLabelValues(transport).Inc()
}

// IncDBWriteErrors increments the DB write error counter.
func (m *Metrics) IncDBWriteErrors() {
	if m == nil {
		return
	}
	m.dbWriteErrors.Inc()
}
