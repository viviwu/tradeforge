package gateway

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/internal/observability"
	"github.com/viviwu/tradeforge/internal/oms"
	"github.com/viviwu/tradeforge/internal/store"
)

type TradeServer struct {
	pb.UnimplementedTradeServiceServer
	oms     *oms.Service
	metrics *observability.Metrics
}

func NewTradeServer(svc *oms.Service, metrics *observability.Metrics) *TradeServer {
	return &TradeServer{oms: svc, metrics: metrics}
}

func (s *TradeServer) SubmitOrder(ctx context.Context, req *pb.SubmitOrderRequest) (*pb.SubmitOrderResponse, error) {
	accountID := AccountIDFromContext(ctx)
	if accountID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing account")
	}
	order, err := s.oms.Submit(ctx, accountID, oms.SubmitRequest{
		ClientOrderID: req.GetClientOrderId(),
		Symbol:        req.GetSymbol(),
		Side:          sideFromProto(req.GetSide()),
		Type:          orderTypeFromProto(req.GetType()),
		TimeInForce:   tifFromProto(req.GetTimeInForce()),
		Price:         req.GetPrice(),
		Quantity:      req.GetQuantity(),
	})
	if err != nil {
		return nil, mapOMSStatusError(err)
	}
	s.metrics.OrdersSubmitted.Inc()
	return &pb.SubmitOrderResponse{
		OrderId:       order.ID,
		ClientOrderId: order.ClientOrderID,
		Status:        orderStatusToProto(order.Status),
		RejectReason:  order.RejectReason,
	}, nil
}

func (s *TradeServer) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.CancelOrderResponse, error) {
	accountID := AccountIDFromContext(ctx)
	order, err := s.oms.Cancel(ctx, accountID, req.GetOrderId(), req.GetClientOrderId())
	if err != nil {
		return nil, mapOMSStatusError(err)
	}
	return &pb.CancelOrderResponse{
		OrderId:     order.ID,
		Status:      orderStatusToProto(order.Status),
		CanceledQty: order.Remaining(),
		Reason:      order.RejectReason,
	}, nil
}

func (s *TradeServer) GetOrders(ctx context.Context, req *pb.GetOrdersRequest) (*pb.GetOrdersResponse, error) {
	accountID := AccountIDFromContext(ctx)
	orders, next, err := s.oms.ListOrders(ctx, accountID, store.OrderFilter{
		Status:        orderStatusFromProto(req.GetStatus()),
		Symbol:        req.GetSymbol(),
		OrderID:       req.GetOrderId(),
		ClientOrderID: req.GetClientOrderId(),
		From:          unixNano(req.GetFromUnixNano()),
		To:            unixNano(req.GetToUnixNano()),
		PageSize:      int(req.GetPageSize()),
		PageToken:     req.GetPageToken(),
	})
	if err != nil {
		return nil, mapOMSStatusError(err)
	}
	out := make([]*pb.Order, 0, len(orders))
	for _, order := range orders {
		out = append(out, orderToProto(order))
	}
	return &pb.GetOrdersResponse{Orders: out, NextPageToken: next}, nil
}

func (s *TradeServer) GetTrades(ctx context.Context, req *pb.GetTradesRequest) (*pb.GetTradesResponse, error) {
	accountID := AccountIDFromContext(ctx)
	trades, next, err := s.oms.ListTrades(ctx, accountID, store.TradeFilter{
		OrderID:   req.GetOrderId(),
		Symbol:    req.GetSymbol(),
		From:      unixNano(req.GetFromUnixNano()),
		To:        unixNano(req.GetToUnixNano()),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, mapOMSStatusError(err)
	}
	out := make([]*pb.Trade, 0, len(trades))
	for _, trade := range trades {
		out = append(out, tradeToProto(trade))
	}
	return &pb.GetTradesResponse{Trades: out, NextPageToken: next}, nil
}

func (s *TradeServer) GetPositions(ctx context.Context, req *pb.GetPositionsRequest) (*pb.GetPositionsResponse, error) {
	accountID, err := s.resolveAccount(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	positions, err := s.oms.ListPositions(ctx, accountID, req.GetSymbol())
	if err != nil {
		return nil, mapOMSStatusError(err)
	}
	out := make([]*pb.Position, 0, len(positions))
	for _, position := range positions {
		out = append(out, positionToProto(position))
	}
	return &pb.GetPositionsResponse{Positions: out}, nil
}

func (s *TradeServer) GetStrategies(ctx context.Context, req *pb.GetStrategiesRequest) (*pb.GetStrategiesResponse, error) {
	accountID, err := s.resolveAccount(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	strategies, next, err := s.oms.ListStrategies(ctx, accountID, store.StrategyFilter{
		Status:    strategyStatusFromProto(req.GetStatus()),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, mapOMSStatusError(err)
	}
	out := make([]*pb.Strategy, 0, len(strategies))
	for _, strategy := range strategies {
		out = append(out, strategyToProto(strategy))
	}
	return &pb.GetStrategiesResponse{Strategies: out, NextPageToken: next}, nil
}

func (s *TradeServer) UpdateStrategyStatus(ctx context.Context, req *pb.UpdateStrategyStatusRequest) (*pb.UpdateStrategyStatusResponse, error) {
	claims, ok := ClaimsFromContext(ctx)
	if !ok || !claims.IsAdmin() {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	strategy, err := s.oms.SetStrategyStatus(ctx, req.GetStrategyId(), strategyStatusFromProto(req.GetStatus()), claims.Username)
	if err != nil {
		return nil, mapOMSStatusError(err)
	}
	return &pb.UpdateStrategyStatusResponse{Strategy: strategyToProto(strategy)}, nil
}

func (s *TradeServer) resolveAccount(ctx context.Context, requested string) (string, error) {
	caller := AccountIDFromContext(ctx)
	if requested == "" || requested == caller {
		return caller, nil
	}
	if !IsAdmin(ctx) {
		return "", status.Error(codes.PermissionDenied, "admin role required")
	}
	return requested, nil
}

func unixNano(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(0, v).UTC()
}

func mapOMSStatusError(err error) error {
	switch {
	case errors.Is(err, oms.ErrAccountNotFound):
		return status.Error(codes.NotFound, "account not found")
	case errors.Is(err, oms.ErrOrderNotFound):
		return status.Error(codes.NotFound, "order not found")
	case errors.Is(err, oms.ErrStrategyNotFound):
		return status.Error(codes.NotFound, "strategy not found")
	case errors.Is(err, oms.ErrInvalidRequest), errors.Is(err, oms.ErrInsufficientFunds), errors.Is(err, oms.ErrInsufficientPosition):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, "not found")
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
