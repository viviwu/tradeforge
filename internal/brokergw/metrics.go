package brokergw

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	bg "github.com/viviwu/tradeforge/gen/brokergateway/v1"
)

// Metrics collects gateway-side Prometheus metrics. A nil *Metrics is a
// no-op, so unit tests can build a Server without a registry.
type Metrics struct {
	registry      *prometheus.Registry
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	reports       *prometheus.CounterVec
	replayed      prometheus.Counter
	consumers     prometheus.Gauge
	slowConsumers prometheus.Counter
	upstreamErrs  prometheus.Counter
}

// NewMetrics builds an isolated registry for the gateway process.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "brokergw", Name: "grpc_requests_total",
			Help: "Total number of BrokerGateway gRPC calls.",
		}, []string{"method", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "brokergw", Name: "grpc_request_duration_seconds",
			Help:    "BrokerGateway gRPC call latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method"}),
		reports: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "brokergw", Name: "reports_published_total",
			Help: "Total number of report events published by payload type.",
		}, []string{"payload"}),
		replayed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "brokergw", Name: "reports_replayed_total",
			Help: "Total number of report events replayed on resubscribe.",
		}),
		consumers: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "brokergw", Name: "report_consumers",
			Help: "Current number of active report subscriptions.",
		}),
		slowConsumers: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "brokergw", Name: "slow_consumers_total",
			Help: "Total number of report subscriptions dropped for slow consumption.",
		}),
		upstreamErrs: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "brokergw", Name: "upstream_errors_total",
			Help: "Total number of upstream stream/query failures.",
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.reports, m.replayed, m.consumers, m.slowConsumers, m.upstreamErrs)
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return m
}

// Handler serves /metrics; nil metrics yields a 404 handler.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// UnaryInterceptor records request count and latency for unary RPCs.
func (m *Metrics) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		if m != nil {
			m.requests.WithLabelValues(info.FullMethod, status.Code(err).String()).Inc()
			m.duration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		}
		return resp, err
	}
}

// StreamInterceptor records request count and latency for streaming RPCs.
func (m *Metrics) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, stream)
		if m != nil {
			m.requests.WithLabelValues(info.FullMethod, status.Code(err).String()).Inc()
			m.duration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		}
		return err
	}
}

func (m *Metrics) reportPublished(ev *bg.ReportEvent) {
	if m == nil {
		return
	}
	payload := "other"
	switch {
	case ev.GetOrder() != nil:
		payload = "order"
	case ev.GetTrade() != nil:
		payload = "trade"
	case ev.GetAccount() != nil:
		payload = "account"
	}
	m.reports.WithLabelValues(payload).Inc()
}

func (m *Metrics) replayedN(n int) {
	if m != nil {
		m.replayed.Add(float64(n))
	}
}

func (m *Metrics) consumerAdded() {
	if m != nil {
		m.consumers.Inc()
	}
}

func (m *Metrics) consumerRemoved() {
	if m != nil {
		m.consumers.Dec()
	}
}

func (m *Metrics) slowConsumersN(n int) {
	if m != nil {
		m.slowConsumers.Add(float64(n))
	}
}

func (m *Metrics) upstreamError() {
	if m != nil {
		m.upstreamErrs.Inc()
	}
}
