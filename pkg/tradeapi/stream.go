package tradeapi

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

type recvStream[T any] interface {
	Recv() (T, error)
}

func (c *Client) SubscribeOrders(ctx context.Context, req *pb.SubscribeOrdersRequest) (<-chan *pb.OrderEvent, <-chan error) {
	return subscribeStream[*pb.OrderEvent](ctx, c, req.GetFromSeq(),
		func(e *pb.OrderEvent) uint64 { return e.GetSeq() },
		func(ctx context.Context, from uint64) (recvStream[*pb.OrderEvent], error) {
			return c.stream.SubscribeOrders(ctx, &pb.SubscribeOrdersRequest{
				RequestId: req.GetRequestId(),
				FromSeq:   from,
				Symbols:   req.GetSymbols(),
			})
		})
}

func (c *Client) SubscribeStrategies(ctx context.Context, req *pb.SubscribeStrategiesRequest) (<-chan *pb.StrategyEvent, <-chan error) {
	return subscribeStream[*pb.StrategyEvent](ctx, c, req.GetFromSeq(),
		func(e *pb.StrategyEvent) uint64 { return e.GetSeq() },
		func(ctx context.Context, from uint64) (recvStream[*pb.StrategyEvent], error) {
			return c.stream.SubscribeStrategies(ctx, &pb.SubscribeStrategiesRequest{
				RequestId:   req.GetRequestId(),
				FromSeq:     from,
				StrategyIds: req.GetStrategyIds(),
			})
		})
}

func (c *Client) SubscribeMarketData(ctx context.Context, req *pb.SubscribeMarketDataRequest) (<-chan *pb.MarketEvent, <-chan error) {
	return subscribeStream[*pb.MarketEvent](ctx, c, req.GetFromSeq(),
		func(e *pb.MarketEvent) uint64 { return e.GetSeq() },
		func(ctx context.Context, from uint64) (recvStream[*pb.MarketEvent], error) {
			return c.stream.SubscribeMarketData(ctx, &pb.SubscribeMarketDataRequest{
				RequestId: req.GetRequestId(),
				FromSeq:   from,
				Symbols:   req.GetSymbols(),
			})
		})
}

func subscribeStream[T any](ctx context.Context, c *Client, from uint64, seqOf func(T) uint64, open func(context.Context, uint64) (recvStream[T], error)) (<-chan T, <-chan error) {
	out := make(chan T, 256)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		backoff := 200 * time.Millisecond
		for {
			if ctx.Err() != nil {
				return
			}
			stream, err := open(c.authorizedCtx(ctx), from)
			if err != nil {
				if status.Code(err) == codes.Unauthenticated && c.Tokens().RefreshToken != "" {
					if _, refreshErr := c.Refresh(ctx); refreshErr != nil {
						errs <- refreshErr
						return
					}
					continue
				}
				if !sleep(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
			backoff = 200 * time.Millisecond
			for {
				event, err := stream.Recv()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					if status.Code(err) == codes.Unauthenticated && c.Tokens().RefreshToken != "" {
						_, _ = c.Refresh(ctx)
					}
					break
				}
				if seq := seqOf(event); seq > from {
					from = seq
				}
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
			}
			if !sleep(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
		}
	}()
	return out, errs
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}
