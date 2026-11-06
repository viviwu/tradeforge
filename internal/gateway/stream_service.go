package gateway

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/internal/domain"
	"github.com/viviwu/tradeforge/internal/eventbus"
	"github.com/viviwu/tradeforge/internal/marketdata"
	"github.com/viviwu/tradeforge/internal/observability"
)

type StreamServer struct {
	pb.UnimplementedStreamServiceServer
	bus          *eventbus.Bus
	metrics      *observability.Metrics
	symbols      map[string]struct{}
	orderBuffer  int
	marketBuffer int
}

func NewStreamServer(bus *eventbus.Bus, metrics *observability.Metrics, symbols []string, orderBuffer, marketBuffer int) *StreamServer {
	set := make(map[string]struct{}, len(symbols))
	for _, sym := range symbols {
		set[sym] = struct{}{}
	}
	if orderBuffer <= 0 {
		orderBuffer = 1024
	}
	if marketBuffer <= 0 {
		marketBuffer = 256
	}
	return &StreamServer{
		bus:          bus,
		metrics:      metrics,
		symbols:      set,
		orderBuffer:  orderBuffer,
		marketBuffer: marketBuffer,
	}
}

func (s *StreamServer) SubscribeOrders(req *pb.SubscribeOrdersRequest, stream pb.StreamService_SubscribeOrdersServer) error {
	accountID := AccountIDFromContext(stream.Context())
	if accountID == "" {
		return status.Error(codes.Unauthenticated, "missing account")
	}
	symbolFilter := make(map[string]struct{}, len(req.GetSymbols()))
	for _, sym := range req.GetSymbols() {
		symbolFilter[sym] = struct{}{}
	}
	filter := func(e eventbus.Event) bool {
		if e.AccountID != accountID {
			return false
		}
		if e.Type != eventbus.EventOrderAccepted &&
			e.Type != eventbus.EventOrderPartiallyFill &&
			e.Type != eventbus.EventOrderFilled &&
			e.Type != eventbus.EventOrderCanceled &&
			e.Type != eventbus.EventOrderRejected {
			return false
		}
		if len(symbolFilter) > 0 {
			_, ok := symbolFilter[e.Symbol]
			return ok
		}
		return true
	}
	sub := s.bus.Subscribe(s.orderBuffer, false, filter, req.GetFromSeq())
	defer sub.Close()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case err := <-sub.Err:
			if err != nil {
				s.metrics.SlowConsumers.Inc()
				return status.Error(codes.ResourceExhausted, "slow consumer, reconnect with from_seq")
			}
		case event, ok := <-sub.C:
			if !ok {
				return nil
			}
			order, ok := event.Payload.(domain.Order)
			if !ok {
				continue
			}
			if err := stream.Send(orderEventToProto(event, order)); err != nil {
				return err
			}
		}
	}
}

func (s *StreamServer) SubscribeStrategies(req *pb.SubscribeStrategiesRequest, stream pb.StreamService_SubscribeStrategiesServer) error {
	accountID := AccountIDFromContext(stream.Context())
	if accountID == "" {
		return status.Error(codes.Unauthenticated, "missing account")
	}
	idFilter := make(map[string]struct{}, len(req.GetStrategyIds()))
	for _, id := range req.GetStrategyIds() {
		idFilter[id] = struct{}{}
	}
	filter := func(e eventbus.Event) bool {
		if e.Type != eventbus.EventStrategyStatus || e.AccountID != accountID {
			return false
		}
		if len(idFilter) > 0 {
			strategy, ok := e.Payload.(domain.Strategy)
			if !ok {
				return false
			}
			_, ok = idFilter[strategy.ID]
			return ok
		}
		return true
	}
	sub := s.bus.Subscribe(s.orderBuffer, false, filter, req.GetFromSeq())
	defer sub.Close()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case err := <-sub.Err:
			if err != nil {
				s.metrics.SlowConsumers.Inc()
				return status.Error(codes.ResourceExhausted, "slow consumer, reconnect with from_seq")
			}
		case event, ok := <-sub.C:
			if !ok {
				return nil
			}
			strategy, ok := event.Payload.(domain.Strategy)
			if !ok {
				continue
			}
			if err := stream.Send(strategyEventToProto(event, strategy)); err != nil {
				return err
			}
		}
	}
}

func (s *StreamServer) SubscribeMarketData(req *pb.SubscribeMarketDataRequest, stream pb.StreamService_SubscribeMarketDataServer) error {
	if len(req.GetSymbols()) == 0 {
		return status.Error(codes.InvalidArgument, "at least one symbol is required")
	}
	symbolFilter := make(map[string]struct{}, len(req.GetSymbols()))
	for _, sym := range req.GetSymbols() {
		if _, ok := s.symbols[sym]; !ok {
			return status.Errorf(codes.InvalidArgument, "symbol %s not allowed", sym)
		}
		symbolFilter[sym] = struct{}{}
	}
	filter := func(e eventbus.Event) bool {
		if e.Type != eventbus.EventMarketTick && e.Type != eventbus.EventMarketBook {
			return false
		}
		_, ok := symbolFilter[e.Symbol]
		return ok
	}
	sub := s.bus.Subscribe(s.marketBuffer, true, filter, req.GetFromSeq())
	defer sub.Close()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case err := <-sub.Err:
			if err != nil && !errors.Is(err, eventbus.ErrSlowConsumer) {
				return status.Error(codes.Internal, err.Error())
			}
		case event, ok := <-sub.C:
			if !ok {
				return nil
			}
			tick, ok := event.Payload.(marketdata.Tick)
			if !ok {
				continue
			}
			if err := stream.Send(marketEventToProto(event.Seq, tick)); err != nil {
				return err
			}
		}
	}
}

func orderEventToProto(event eventbus.Event, order domain.Order) *pb.OrderEvent {
	return &pb.OrderEvent{
		Seq:           event.Seq,
		OrderId:       order.ID,
		ClientOrderId: order.ClientOrderID,
		AccountId:     order.AccountID,
		Symbol:        order.Symbol,
		Side:          sideToProto(order.Side),
		Type:          orderTypeToProto(order.Type),
		Status:        orderStatusToProto(order.Status),
		FilledQty:     order.FilledQty,
		AvgPrice:      order.AvgPrice,
		Reason:        order.RejectReason,
		TsUnixNano:    order.UpdatedAt.UnixNano(),
	}
}

func strategyEventToProto(event eventbus.Event, strategy domain.Strategy) *pb.StrategyEvent {
	return &pb.StrategyEvent{
		Seq:        event.Seq,
		StrategyId: strategy.ID,
		AccountId:  strategy.AccountID,
		Status:     strategyStatusToProto(strategy.Status),
		TsUnixNano: strategy.UpdatedAt.UnixNano(),
	}
}
