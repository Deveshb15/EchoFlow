package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry *prometheus.Registry

	httpRequestsTotal     *prometheus.CounterVec
	httpRequestDuration   *prometheus.HistogramVec
	upstreamRequestsTotal *prometheus.CounterVec
	upstreamDuration      *prometheus.HistogramVec
	pipelineFallbacks     prometheus.Counter
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		registry: registry,
		httpRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "echoflow_http_requests_total",
				Help: "Total number of HTTP requests handled.",
			},
			[]string{"route", "method", "status"},
		),
		httpRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "echoflow_http_request_duration_seconds",
				Help:    "HTTP request duration in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"route", "method", "status"},
		),
		upstreamRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "echoflow_upstream_requests_total",
				Help: "Total upstream OpenAI-compatible API requests.",
			},
			[]string{"endpoint", "status"},
		),
		upstreamDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "echoflow_upstream_request_duration_seconds",
				Help:    "Upstream request duration in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint", "status"},
		),
		pipelineFallbacks: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "echoflow_pipeline_postprocess_fallback_total",
				Help: "Number of pipeline requests that fell back to raw transcript due to post-process failure.",
			},
		),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequestsTotal,
		m.httpRequestDuration,
		m.upstreamRequestsTotal,
		m.upstreamDuration,
		m.pipelineFallbacks,
	)

	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveHTTP(route, method string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if route == "" {
		route = "unknown"
	}
	if method == "" {
		method = "UNKNOWN"
	}
	statusLabel := strconv.Itoa(status)
	m.httpRequestsTotal.WithLabelValues(route, method, statusLabel).Inc()
	m.httpRequestDuration.WithLabelValues(route, method, statusLabel).Observe(duration.Seconds())
}

func (m *Metrics) ObserveUpstream(endpoint string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if endpoint == "" {
		endpoint = "unknown"
	}
	statusLabel := strconv.Itoa(status)
	m.upstreamRequestsTotal.WithLabelValues(endpoint, statusLabel).Inc()
	m.upstreamDuration.WithLabelValues(endpoint, statusLabel).Observe(duration.Seconds())
}

func (m *Metrics) IncPipelineFallback() {
	if m == nil {
		return
	}
	m.pipelineFallbacks.Inc()
}
