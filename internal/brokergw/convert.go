package brokergw

import (
	"encoding/json"
	"strconv"

	bg "github.com/viviwu/tradeforge/gen/brokergateway/v1"
	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

// Price units: the upstream simulation exchange prices in units of 0.01 yuan
// while the gateway contract prices in units of 0.0001 yuan.
const (
	gridToMicro   = 10 // upstream(0.01) -> contract(0.0001): multiply
	gridFromMicro = 10 // contract(0.0001) -> upstream(0.01): divide evenly
)

func sideBG(from pb.Side) bg.Side {
	switch from {
	case pb.Side_SIDE_BUY:
		return bg.Side_SIDE_BUY
	case pb.Side_SIDE_SELL:
		return bg.Side_SIDE_SELL
	default:
		return bg.Side_SIDE_UNSPECIFIED
	}
}

func typeBG(from pb.OrderType) bg.OrderType {
	switch from {
	case pb.OrderType_ORDER_TYPE_LIMIT:
		return bg.OrderType_ORDER_TYPE_LIMIT
	case pb.OrderType_ORDER_TYPE_MARKET:
		return bg.OrderType_ORDER_TYPE_MARKET
	default:
		return bg.OrderType_ORDER_TYPE_UNSPECIFIED
	}
}

// statusFromPB maps upstream statuses; the stub never emits PENDING, UNSEND,
// or WAIT_CANCEL (registered in the broker differences registry: real
// counters may pass through those states).
func statusFromPB(from pb.OrderStatus) bg.OrderStatus {
	switch from {
	case pb.OrderStatus_ORDER_STATUS_PENDING_NEW:
		return bg.OrderStatus_ORDER_STATUS_PENDING_NEW
	case pb.OrderStatus_ORDER_STATUS_ACCEPTED:
		return bg.OrderStatus_ORDER_STATUS_ACCEPTED
	case pb.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED:
		return bg.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED
	case pb.OrderStatus_ORDER_STATUS_FILLED:
		return bg.OrderStatus_ORDER_STATUS_FILLED
	case pb.OrderStatus_ORDER_STATUS_CANCELED:
		return bg.OrderStatus_ORDER_STATUS_CANCELED
	case pb.OrderStatus_ORDER_STATUS_REJECTED:
		return bg.OrderStatus_ORDER_STATUS_REJECTED
	default:
		return bg.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

// tifBG maps upstream tif to contract tif; upstream empty (session default)
// is reported as DAY.
func tifBG(from pb.TimeInForce) bg.TimeInForce {
	switch from {
	case pb.TimeInForce_TIME_IN_FORCE_GTC:
		return bg.TimeInForce_TIME_IN_FORCE_GTC
	case pb.TimeInForce_TIME_IN_FORCE_IOC:
		return bg.TimeInForce_TIME_IN_FORCE_IOC
	case pb.TimeInForce_TIME_IN_FORCE_FOK:
		return bg.TimeInForce_TIME_IN_FORCE_FOK
	default:
		return bg.TimeInForce_TIME_IN_FORCE_DAY
	}
}

// tifToUp maps contract tif to upstream tif; the simulator has no explicit
// DAY, so DAY (and unspecified) degrade to the session default.
func tifToUp(from bg.TimeInForce) pb.TimeInForce {
	switch from {
	case bg.TimeInForce_TIME_IN_FORCE_GTC:
		return pb.TimeInForce_TIME_IN_FORCE_GTC
	case bg.TimeInForce_TIME_IN_FORCE_IOC:
		return pb.TimeInForce_TIME_IN_FORCE_IOC
	case bg.TimeInForce_TIME_IN_FORCE_FOK:
		return pb.TimeInForce_TIME_IN_FORCE_FOK
	default:
		return pb.TimeInForce_TIME_IN_FORCE_UNSPECIFIED
	}
}

func brokerOrderFromPB(o *pb.Order, cfg Config) *bg.BrokerOrder {
	return &bg.BrokerOrder{
		ClientOrderId:   o.GetClientOrderId(),
		ExchangeOrderId: o.GetOrderId(),
		FundAccount:     cfg.FundAccount,
		TradeAccount:    cfg.TradeAccount,
		Market:          cfg.Market,
		Symbol:          o.GetSymbol(),
		Side:            sideBG(o.GetSide()),
		OrderType:       typeBG(o.GetType()),
		Tif:             tifBG(o.GetTimeInForce()),
		PriceU:          o.GetPrice() * gridToMicro,
		Qty:             o.GetQuantity(),
		FilledQty:       o.GetFilledQty(),
		AvgFillPriceU:   o.GetAvgPrice() * gridToMicro,
		Status:          statusFromPB(o.GetStatus()),
		RejectReason:    o.GetRejectReason(),
		CreatedUnixNano: o.GetCreatedAtUnixNano(),
		UpdatedUnixNano: o.GetUpdatedAtUnixNano(),
		ExtJson:         marshalExt(map[string]any{"source": "order query"}),
	}
}

func brokerTradeFromPB(t *pb.Trade, cfg Config, clientOrderID string) *bg.BrokerTrade {
	price := t.GetPrice() * gridToMicro
	return &bg.BrokerTrade{
		TradeId:         strconvFormatSeq(t.GetTradeSeq()),
		ClientOrderId:   clientOrderID,
		ExchangeOrderId: t.GetOrderId(),
		FundAccount:     cfg.FundAccount,
		TradeAccount:    cfg.TradeAccount,
		Market:          cfg.Market,
		Symbol:          t.GetSymbol(),
		Side:            sideBG(t.GetSide()),
		PriceU:          price,
		Qty:             t.GetQuantity(),
		AmountU:         price * t.GetQuantity(),
		TsUnixNano:      t.GetCreatedAtUnixNano(),
		ExtJson:         marshalExt(map[string]any{"source": "trade query"}),
	}
}

func strconvFormatSeq(seq uint64) string { return strconv.FormatUint(seq, 10) }

func marshalExt(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}
