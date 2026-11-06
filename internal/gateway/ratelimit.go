package gateway

import (
	"context"
	"strings"
	"sync"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/viviwu/tradeforge/internal/observability"
)

const bucketCapacity = 10_000

// RateLimits configures per-account rate limiting.
// A zero or negative value disables the corresponding limit.
type RateLimits struct {
	TradePerSecond float64 // SubmitOrder + CancelOrder, per account
	TradeBurst     int
	QueryPerSecond float64 // remaining TradeService methods, per account
	QueryBurst     int
	MaxStreams     int // concurrent StreamService connections, per account
}

// RateLimiter enforces fairness between authenticated accounts on top of the
// login limiter (which only protects against credential stuffing).
type RateLimiter struct {
	trade      *bucket
	query      *bucket
	maxStreams int

	streamsMu sync.Mutex
	streams   map[string]int

	metrics *observability.Metrics
}

type bucket struct {
	mu       sync.Mutex
	rps      rate.Limit
	burst    int
	limiters map[string]*rate.Limiter
}

func newBucket(rps float64, burst int) *bucket {
	return &bucket{
		rps:      rate.Limit(rps),
		burst:    burst,
		limiters: make(map[string]*rate.Limiter, 64),
	}
}

func (b *bucket) allow(key string) bool {
	if b == nil || b.rps <= 0 || b.burst <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.limiters) >= bucketCapacity {
		// Same policy as the login limiter: shed state on saturation.
		b.limiters = make(map[string]*rate.Limiter, 16)
	}
	limiter, ok := b.limiters[key]
	if !ok {
		limiter = rate.NewLimiter(b.rps, b.burst)
		b.limiters[key] = limiter
	}
	return limiter.Allow()
}

func NewRateLimiter(limits RateLimits, metrics *observability.Metrics) *RateLimiter {
	return &RateLimiter{
		trade:      newBucket(limits.TradePerSecond, limits.TradeBurst),
		query:      newBucket(limits.QueryPerSecond, limits.QueryBurst),
		maxStreams: limits.MaxStreams,
		streams:    make(map[string]int, 64),
		metrics:    metrics,
	}
}

func (r *RateLimiter) deny(kind string) error {
	if r.metrics != nil {
		r.metrics.RateLimited.WithLabelValues(kind).Inc()
	}
	return status.Error(codes.ResourceExhausted, "rate limit exceeded, please retry later")
}

func classify(fullMethod string) string {
	switch {
	case strings.HasSuffix(fullMethod, "TradeService/SubmitOrder"),
		strings.HasSuffix(fullMethod, "TradeService/CancelOrder"):
		return "trade"
	case strings.HasPrefix(fullMethod, "/tradeforge.v1.TradeService/"):
		return "query"
	default:
		return ""
	}
}

func (r *RateLimiter) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		kind := classify(info.FullMethod)
		if r == nil || kind == "" {
			return handler(ctx, req)
		}
		user := AccountIDFromContext(ctx)
		if user == "" {
			return handler(ctx, req)
		}
		var allowed bool
		if kind == "trade" {
			allowed = r.trade.allow(user)
		} else {
			allowed = r.query.allow(user)
		}
		if !allowed {
			return nil, r.deny(kind)
		}
		return handler(ctx, req)
	}
}

func (r *RateLimiter) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if r == nil || r.maxStreams <= 0 || !strings.HasPrefix(info.FullMethod, "/tradeforge.v1.StreamService/") {
			return handler(srv, stream)
		}
		user := AccountIDFromContext(stream.Context())
		if user == "" {
			return handler(srv, stream)
		}
		if !r.acquireStream(user) {
			return r.deny("stream")
		}
		defer r.releaseStream(user)
		return handler(srv, stream)
	}
}

func (r *RateLimiter) acquireStream(user string) bool {
	r.streamsMu.Lock()
	defer r.streamsMu.Unlock()
	if len(r.streams) >= bucketCapacity {
		r.streams = make(map[string]int, 16)
	}
	if r.streams[user] >= r.maxStreams {
		return false
	}
	r.streams[user]++
	return true
}

func (r *RateLimiter) releaseStream(user string) {
	r.streamsMu.Lock()
	defer r.streamsMu.Unlock()
	if n := r.streams[user]; n <= 1 {
		delete(r.streams, user)
	} else {
		r.streams[user] = n - 1
	}
}
