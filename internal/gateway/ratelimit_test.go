package gateway

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/viviwu/tradeforge/internal/auth"
	"github.com/viviwu/tradeforge/internal/observability"
)

func TestBucketAllowsBurstThenLimits(t *testing.T) {
	b := newBucket(1, 2)
	if !b.allow("u") || !b.allow("u") {
		t.Fatal("burst should be allowed")
	}
	if b.allow("u") {
		t.Fatal("expected request over burst to be rejected")
	}
	if !b.allow("other") {
		t.Fatal("accounts must be limited independently")
	}
}

func TestBucketDisabledWhenUnlimited(t *testing.T) {
	b := newBucket(0, 0)
	for i := 0; i < 1000; i++ {
		if !b.allow("u") {
			t.Fatalf("request %d should pass when bucket disabled", i)
		}
	}
}

func authCtx(userID string) context.Context {
	return context.WithValue(context.Background(), claimsKey{}, auth.Claims{UserID: userID})
}

func TestUnaryInterceptorLimitsTradeBucket(t *testing.T) {
	r := NewRateLimiter(RateLimits{TradePerSecond: 1, TradeBurst: 1}, observability.New())
	info := &grpc.UnaryServerInfo{FullMethod: "/tradeforge.v1.TradeService/SubmitOrder"}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	if _, err := r.UnaryInterceptor()(authCtx("u1"), nil, info, handler); err != nil {
		t.Fatalf("first submit should pass: %v", err)
	}
	_, err := r.UnaryInterceptor()(authCtx("u1"), nil, info, handler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second submit should be limited, got %v", err)
	}
	// Query bucket stays unlimited.
	qInfo := &grpc.UnaryServerInfo{FullMethod: "/tradeforge.v1.TradeService/GetOrders"}
	for i := 0; i < 10; i++ {
		if _, err := r.UnaryInterceptor()(authCtx("u1"), nil, qInfo, handler); err != nil {
			t.Fatalf("query should be unlimited: %v", err)
		}
	}
	if _, err := r.UnaryInterceptor()(authCtx("u2"), nil, info, handler); err != nil {
		t.Fatalf("other account should not be limited: %v", err)
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s fakeStream) Context() context.Context { return s.ctx }

func TestStreamInterceptorLimitsConcurrency(t *testing.T) {
	r := NewRateLimiter(RateLimits{MaxStreams: 1}, observability.New())
	info := &grpc.StreamServerInfo{
		FullMethod:     "/tradeforge.v1.StreamService/SubscribeOrders",
		IsClientStream: false,
		IsServerStream: true,
	}
	handler := func(srv any, stream grpc.ServerStream) error { return nil }
	interceptor := r.StreamInterceptor()

	if err := interceptor(nil, fakeStream{ctx: authCtx("u1")}, info, handler); err != nil {
		t.Fatalf("first stream should pass: %v", err)
	}
	// Same account again within MaxStreams is fine (handler returned, slot released).
	if err := interceptor(nil, fakeStream{ctx: authCtx("u1")}, info, handler); err != nil {
		t.Fatalf("stream after release should pass: %v", err)
	}

	block := make(chan struct{})
	started := make(chan struct{})
	blocking := func(srv any, stream grpc.ServerStream) error {
		close(started)
		<-block
		return nil
	}
	errCh := make(chan error, 2)
	go func() {
		errCh <- interceptor(nil, fakeStream{ctx: authCtx("u1")}, info, blocking)
	}()
	<-started
	err := interceptor(nil, fakeStream{ctx: authCtx("u1")}, info, handler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second concurrent stream should be limited, got %v", err)
	}
	if err := interceptor(nil, fakeStream{ctx: authCtx("u2")}, info, handler); err != nil {
		t.Fatalf("other account should not be limited: %v", err)
	}
	close(block)
	if err := <-errCh; err != nil {
		t.Fatalf("blocking stream failed: %v", err)
	}
}

func TestStreamReleaseThenAcquire(t *testing.T) {
	r := NewRateLimiter(RateLimits{MaxStreams: 2}, observability.New())
	if r.acquireStream("u1") && r.acquireStream("u1") {
		if r.acquireStream("u1") {
			t.Fatal("third stream should be rejected")
		}
		r.releaseStream("u1")
		if !r.acquireStream("u1") {
			t.Fatal("should reacquire after release")
		}
		return
	}
	t.Fatal("first two streams should be acquired")
}
