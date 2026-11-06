package gateway

import (
	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/internal/domain"
	"github.com/viviwu/tradeforge/internal/marketdata"
)

func sideToProto(side domain.Side) pb.Side {
	switch side {
	case domain.SideBuy:
		return pb.Side_SIDE_BUY
	case domain.SideSell:
		return pb.Side_SIDE_SELL
	default:
		return pb.Side_SIDE_UNSPECIFIED
	}
}

func sideFromProto(side pb.Side) domain.Side {
	switch side {
	case pb.Side_SIDE_BUY:
		return domain.SideBuy
	case pb.Side_SIDE_SELL:
		return domain.SideSell
	default:
		return ""
	}
}

func orderTypeToProto(t domain.OrderType) pb.OrderType {
	switch t {
	case domain.OrderTypeLimit:
		return pb.OrderType_ORDER_TYPE_LIMIT
	case domain.OrderTypeMarket:
		return pb.OrderType_ORDER_TYPE_MARKET
	default:
		return pb.OrderType_ORDER_TYPE_UNSPECIFIED
	}
}

func orderTypeFromProto(t pb.OrderType) domain.OrderType {
	switch t {
	case pb.OrderType_ORDER_TYPE_LIMIT:
		return domain.OrderTypeLimit
	case pb.OrderType_ORDER_TYPE_MARKET:
		return domain.OrderTypeMarket
	default:
		return ""
	}
}

func tifToProto(t domain.TimeInForce) pb.TimeInForce {
	switch t {
	case domain.TimeInForceGTC:
		return pb.TimeInForce_TIME_IN_FORCE_GTC
	case domain.TimeInForceIOC:
		return pb.TimeInForce_TIME_IN_FORCE_IOC
	case domain.TimeInForceFOK:
		return pb.TimeInForce_TIME_IN_FORCE_FOK
	default:
		return pb.TimeInForce_TIME_IN_FORCE_UNSPECIFIED
	}
}

func tifFromProto(t pb.TimeInForce) domain.TimeInForce {
	switch t {
	case pb.TimeInForce_TIME_IN_FORCE_GTC:
		return domain.TimeInForceGTC
	case pb.TimeInForce_TIME_IN_FORCE_IOC:
		return domain.TimeInForceIOC
	case pb.TimeInForce_TIME_IN_FORCE_FOK:
		return domain.TimeInForceFOK
	default:
		return ""
	}
}

func orderStatusToProto(s domain.OrderStatus) pb.OrderStatus {
	switch s {
	case domain.OrderStatusPendingNew:
		return pb.OrderStatus_ORDER_STATUS_PENDING_NEW
	case domain.OrderStatusAccepted:
		return pb.OrderStatus_ORDER_STATUS_ACCEPTED
	case domain.OrderStatusPartiallyFilled:
		return pb.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED
	case domain.OrderStatusFilled:
		return pb.OrderStatus_ORDER_STATUS_FILLED
	case domain.OrderStatusCanceled:
		return pb.OrderStatus_ORDER_STATUS_CANCELED
	case domain.OrderStatusRejected:
		return pb.OrderStatus_ORDER_STATUS_REJECTED
	default:
		return pb.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

func orderStatusFromProto(s pb.OrderStatus) domain.OrderStatus {
	switch s {
	case pb.OrderStatus_ORDER_STATUS_PENDING_NEW:
		return domain.OrderStatusPendingNew
	case pb.OrderStatus_ORDER_STATUS_ACCEPTED:
		return domain.OrderStatusAccepted
	case pb.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED:
		return domain.OrderStatusPartiallyFilled
	case pb.OrderStatus_ORDER_STATUS_FILLED:
		return domain.OrderStatusFilled
	case pb.OrderStatus_ORDER_STATUS_CANCELED:
		return domain.OrderStatusCanceled
	case pb.OrderStatus_ORDER_STATUS_REJECTED:
		return domain.OrderStatusRejected
	default:
		return ""
	}
}

func strategyStatusToProto(s domain.StrategyStatus) pb.StrategyStatus {
	switch s {
	case domain.StrategyStatusCreated:
		return pb.StrategyStatus_STRATEGY_STATUS_CREATED
	case domain.StrategyStatusRunning:
		return pb.StrategyStatus_STRATEGY_STATUS_RUNNING
	case domain.StrategyStatusPaused:
		return pb.StrategyStatus_STRATEGY_STATUS_PAUSED
	case domain.StrategyStatusStopped:
		return pb.StrategyStatus_STRATEGY_STATUS_STOPPED
	case domain.StrategyStatusError:
		return pb.StrategyStatus_STRATEGY_STATUS_ERROR
	default:
		return pb.StrategyStatus_STRATEGY_STATUS_UNSPECIFIED
	}
}

func strategyStatusFromProto(s pb.StrategyStatus) domain.StrategyStatus {
	switch s {
	case pb.StrategyStatus_STRATEGY_STATUS_CREATED:
		return domain.StrategyStatusCreated
	case pb.StrategyStatus_STRATEGY_STATUS_RUNNING:
		return domain.StrategyStatusRunning
	case pb.StrategyStatus_STRATEGY_STATUS_PAUSED:
		return domain.StrategyStatusPaused
	case pb.StrategyStatus_STRATEGY_STATUS_STOPPED:
		return domain.StrategyStatusStopped
	case pb.StrategyStatus_STRATEGY_STATUS_ERROR:
		return domain.StrategyStatusError
	default:
		return ""
	}
}

func orderToProto(o domain.Order) *pb.Order {
	return &pb.Order{
		OrderId:           o.ID,
		ClientOrderId:     o.ClientOrderID,
		AccountId:         o.AccountID,
		Symbol:            o.Symbol,
		Side:              sideToProto(o.Side),
		Type:              orderTypeToProto(o.Type),
		TimeInForce:       tifToProto(o.TimeInForce),
		Price:             o.Price,
		Quantity:          o.Quantity,
		FilledQty:         o.FilledQty,
		AvgPrice:          o.AvgPrice,
		Status:            orderStatusToProto(o.Status),
		RejectReason:      o.RejectReason,
		CreatedAtUnixNano: o.CreatedAt.UnixNano(),
		UpdatedAtUnixNano: o.UpdatedAt.UnixNano(),
	}
}

func tradeToProto(t domain.Trade) *pb.Trade {
	return &pb.Trade{
		TradeId:           t.ID,
		TradeSeq:          t.TradeSeq,
		OrderId:           t.OrderID,
		AccountId:         t.AccountID,
		Symbol:            t.Symbol,
		Side:              sideToProto(t.Side),
		Price:             t.Price,
		Quantity:          t.Quantity,
		CreatedAtUnixNano: t.CreatedAt.UnixNano(),
	}
}

func positionToProto(p domain.Position) *pb.Position {
	return &pb.Position{
		AccountId:         p.AccountID,
		Symbol:            p.Symbol,
		Quantity:          p.Quantity,
		AvgPrice:          p.AvgPrice,
		FrozenQty:         p.FrozenQty,
		MarketPrice:       p.MarketPrice,
		UnrealizedPnl:     unrealizedPnL(p),
		UpdatedAtUnixNano: p.UpdatedAt.UnixNano(),
	}
}

func unrealizedPnL(p domain.Position) int64 {
	if p.MarketPrice <= 0 || p.Quantity <= 0 {
		return 0
	}
	return (p.MarketPrice - p.AvgPrice) * p.Quantity
}

func strategyToProto(s domain.Strategy) *pb.Strategy {
	return &pb.Strategy{
		StrategyId:        s.ID,
		AccountId:         s.AccountID,
		Name:              s.Name,
		Status:            strategyStatusToProto(s.Status),
		ParamsJson:        s.Params,
		CreatedAtUnixNano: s.CreatedAt.UnixNano(),
		UpdatedAtUnixNano: s.UpdatedAt.UnixNano(),
	}
}

func userToProto(u domain.User) *pb.User {
	return &pb.User{UserId: u.ID, Username: u.Username, Role: u.Role}
}

func marketEventToProto(seq uint64, tick marketdata.Tick) *pb.MarketEvent {
	return &pb.MarketEvent{
		Seq:        seq,
		Symbol:     tick.Symbol,
		Type:       pb.MarketEventType_MARKET_EVENT_TYPE_TICK,
		BidPrice:   tick.BidPrice,
		BidQty:     tick.BidQty,
		AskPrice:   tick.AskPrice,
		AskQty:     tick.AskQty,
		LastPrice:  tick.LastPrice,
		TsUnixNano: tick.Time.UnixNano(),
	}
}
