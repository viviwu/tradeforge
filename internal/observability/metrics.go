package observability

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

type Metrics struct {
	Registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec

	OrdersSubmitted prometheus.Counter
	TradesTotal     prometheus.Counter
	SlowConsumers   prometheus.Counter
}

func New() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		Registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tradeforge_grpc_requests_total",
			Help: "Total number of gRPC requests.",
		}, []string{"method", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "tradeforge_grpc_request_duration_seconds",
			Help:    "gRPC request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method"}),
		OrdersSubmitted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tradeforge_orders_submitted_total",
			Help: "Total number of submitted orders.",
		}),
		TradesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tradeforge_trades_total",
			Help: "Total number of executions.",
		}),
		SlowConsumers: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tradeforge_slow_consumers_total",
			Help: "Total number of subscriptions terminated for slow consumption.",
		}),
	}
	registry.MustRegister(m.requests, m.duration, m.OrdersSubmitted, m.TradesTotal, m.SlowConsumers)
	registry.MustRegister(prometheus.NewGoCollector())
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

func (m *Metrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err).String()
		m.requests.WithLabelValues(info.FullMethod, code).Inc()
		m.duration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

func (m *Metrics) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, stream)
		code := status.Code(err).String()
		m.requests.WithLabelValues(info.FullMethod, code).Inc()
		m.duration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		return err
	}
}
