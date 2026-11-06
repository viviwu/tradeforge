package cli

import (
	"fmt"
	"strings"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

func parseSide(value string) (pb.Side, error) {
	switch strings.ToUpper(strings.TrimPrefix(value, "SIDE_")) {
	case "BUY":
		return pb.Side_SIDE_BUY, nil
	case "SELL":
		return pb.Side_SIDE_SELL, nil
	default:
		return pb.Side_SIDE_UNSPECIFIED, fmt.Errorf("invalid side %q", value)
	}
}

func parseOrderType(value string) (pb.OrderType, error) {
	switch strings.ToUpper(strings.TrimPrefix(value, "ORDER_TYPE_")) {
	case "LIMIT":
		return pb.OrderType_ORDER_TYPE_LIMIT, nil
	case "MARKET":
		return pb.OrderType_ORDER_TYPE_MARKET, nil
	default:
		return pb.OrderType_ORDER_TYPE_UNSPECIFIED, fmt.Errorf("invalid order type %q", value)
	}
}

func parseTimeInForce(value string) (pb.TimeInForce, error) {
	switch strings.ToUpper(strings.TrimPrefix(value, "TIME_IN_FORCE_")) {
	case "", "GTC":
		return pb.TimeInForce_TIME_IN_FORCE_GTC, nil
	case "IOC":
		return pb.TimeInForce_TIME_IN_FORCE_IOC, nil
	case "FOK":
		return pb.TimeInForce_TIME_IN_FORCE_FOK, nil
	default:
		return pb.TimeInForce_TIME_IN_FORCE_UNSPECIFIED, fmt.Errorf("invalid time in force %q", value)
	}
}

func parseOrderStatus(value string) (pb.OrderStatus, error) {
	switch strings.ToUpper(strings.TrimPrefix(value, "ORDER_STATUS_")) {
	case "":
		return pb.OrderStatus_ORDER_STATUS_UNSPECIFIED, nil
	case "PENDING_NEW":
		return pb.OrderStatus_ORDER_STATUS_PENDING_NEW, nil
	case "ACCEPTED":
		return pb.OrderStatus_ORDER_STATUS_ACCEPTED, nil
	case "PARTIALLY_FILLED":
		return pb.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED, nil
	case "FILLED":
		return pb.OrderStatus_ORDER_STATUS_FILLED, nil
	case "CANCELED", "CANCELLED":
		return pb.OrderStatus_ORDER_STATUS_CANCELED, nil
	case "REJECTED":
		return pb.OrderStatus_ORDER_STATUS_REJECTED, nil
	default:
		return pb.OrderStatus_ORDER_STATUS_UNSPECIFIED, fmt.Errorf("invalid order status %q", value)
	}
}

func parseStrategyStatus(value string) (pb.StrategyStatus, error) {
	switch strings.ToUpper(strings.TrimPrefix(value, "STRATEGY_STATUS_")) {
	case "":
		return pb.StrategyStatus_STRATEGY_STATUS_UNSPECIFIED, nil
	case "CREATED":
		return pb.StrategyStatus_STRATEGY_STATUS_CREATED, nil
	case "RUNNING":
		return pb.StrategyStatus_STRATEGY_STATUS_RUNNING, nil
	case "PAUSED":
		return pb.StrategyStatus_STRATEGY_STATUS_PAUSED, nil
	case "STOPPED":
		return pb.StrategyStatus_STRATEGY_STATUS_STOPPED, nil
	case "ERROR":
		return pb.StrategyStatus_STRATEGY_STATUS_ERROR, nil
	default:
		return pb.StrategyStatus_STRATEGY_STATUS_UNSPECIFIED, fmt.Errorf("invalid strategy status %q", value)
	}
}
