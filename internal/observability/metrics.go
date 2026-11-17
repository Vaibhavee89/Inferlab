package observability

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// UnknownModel labels requests that failed before a model was accepted, so
// client-supplied names cannot create unbounded label cardinality.
const UnknownModel = "unknown"

// Request outcomes for the requests counter.
const (
	StatusOK    = "ok"
	StatusError = "error"
)

// Metrics holds InferLab's Prometheus collectors. Latency and token
// histograms only receive successful generations so failures do not skew
// percentiles; failures are visible in the requests counter.
type Metrics struct {
	ttft     *prometheus.HistogramVec
	tpot     *prometheus.HistogramVec
	duration *prometheus.HistogramVec
	tokens   *prometheus.HistogramVec
	requests *prometheus.CounterVec
	inflight prometheus.Gauge
}

// NewMetrics creates the collectors and registers them on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	labels := []string{"model", "stream"}
	seconds := []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
	m := &Metrics{
		ttft: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "inferlab_ttft_seconds", Help: "Time to first output token.", Buckets: seconds,
		}, labels),
		tpot: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "inferlab_tpot_seconds", Help: "Mean time per output token after the first.",
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "inferlab_request_duration_seconds", Help: "End-to-end generation latency.", Buckets: seconds,
		}, labels),
		tokens: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "inferlab_output_tokens", Help: "Output tokens per generation.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 13), // 1 .. 4096
		}, labels),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "inferlab_requests_total", Help: "Chat completion requests by outcome.",
		}, []string{"model", "status"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "inferlab_inflight_requests", Help: "Chat completion requests currently being served.",
		}),
	}
	reg.MustRegister(m.ttft, m.tpot, m.duration, m.tokens, m.requests, m.inflight)
	return m
}

// Observe records a successful generation. TTFT and TPOT are skipped when
// undefined (no tokens, or a single token respectively).
func (m *Metrics) Observe(model string, stream bool, t Timing) {
	l := prometheus.Labels{"model": model, "stream": strconv.FormatBool(stream)}
	if d, ok := t.TTFT(); ok {
		m.ttft.With(l).Observe(d.Seconds())
	}
	if d, ok := t.TPOT(); ok {
		m.tpot.With(l).Observe(d.Seconds())
	}
	m.duration.With(l).Observe(t.Total().Seconds())
	m.tokens.With(l).Observe(float64(t.Tokens))
	m.requests.WithLabelValues(model, StatusOK).Inc()
}

// ObserveFailure counts a request that did not complete (upstream error,
// bad request or client cancellation).
func (m *Metrics) ObserveFailure(model string) {
	m.requests.WithLabelValues(model, StatusError).Inc()
}

// TrackInFlight increments the in-flight gauge and returns the function that
// decrements it. Call it once per request and defer the result.
func (m *Metrics) TrackInFlight() (done func()) {
	m.inflight.Inc()
	return m.inflight.Dec
}

// Handler serves reg in the Prometheus text format.
func Handler(reg prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
