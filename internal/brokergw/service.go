// Package brokergw implements the simulated broker gateway (broker-stub): a
// BrokerGatewayService server backed by a single upstream login (tradeapi
// client) against the TradeForge simulation exchange, so a channel core can
// exercise the contract later implemented by real counter SDK gateways.
package brokergw

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bg "github.com/viviwu/tradeforge/gen/brokergateway/v1"
	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

// upstream abstracts the exchange API the stub fronts; *tradeapi.Client
// satisfies it.
type upstream interface {
	SubmitOrder(ctx context.Context, req *pb.SubmitOrderRequest) (*pb.SubmitOrderResponse, error)
	CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.CancelOrderResponse, error)
	GetOrders(ctx context.Context, req *pb.GetOrdersRequest) (*pb.GetOrdersResponse, error)
	GetTrades(ctx context.Context, req *pb.GetTradesRequest) (*pb.GetTradesResponse, error)
	GetPositions(ctx context.Context, req *pb.GetPositionsRequest) (*pb.GetPositionsResponse, error)
	SubscribeOrders(ctx context.Context, req *pb.SubscribeOrdersRequest) (<-chan *pb.OrderEvent, <-chan error)
	SubscribeMarketData(ctx context.Context, req *pb.SubscribeMarketDataRequest) (<-chan *pb.MarketEvent, <-chan error)
}

// Config is the static account/simulator configuration of the stub.
type Config struct {
	ServerVersion string
	FundAccount   string
	TradeAccount  string
	Market        string
	Symbols       []string
	PriceLimitPct int
	RingSize      int
	HaltedSymbols []string
	LookupTimeout time.Duration
	SubBuffer     int
	Metrics       *Metrics // optional; nil disables metrics (unit tests)
}

// Server implements bg.BrokerGatewayServiceServer.
type Server struct {
	bg.UnimplementedBrokerGatewayServiceServer

	up    upstream
	log   *slog.Logger
	cfg   Config
	m     *Metrics
	lookd func(ctx context.Context, clientOrderID string) (*orderDetails, bool)

	rmu    sync.Mutex
	ring   *ring
	subs   map[uint64]*fan
	nextID uint64

	regMu sync.Mutex
	reg   map[string]*orderDetails // session order registry by client_order_id

	// fillMu serializes fill translation: the cumulative-fill watermark and the
	// emitted-trade set must be read/updated atomically with the trade query.
	fillMu sync.Mutex

	lastP sync.Map // symbol -> int64 latest reference price in 1e-4 units
}

// maxRegistryEntries bounds the in-memory session order registry.
const maxRegistryEntries = 8192

// orderDetails is the coarse order skeleton captured at submit time (or via
// upstream lookup) needed to compose full order reports, because upstream
// events carry only status/fill fields.
type orderDetails struct {
	PriceU          int64
	Qty             int64
	Tif             bg.TimeInForce
	CreatedUnixNano int64

	ReportedFilled int64               // cumulative filled qty already pulled from trade queries
	EmittedTrades  map[string]struct{} // trade ids already published for this order
}

// NewServer builds the stub server; call Start before serving traffic.
func NewServer(up upstream, log *slog.Logger, cfg Config) *Server {
	if cfg.RingSize <= 0 {
		cfg.RingSize = 4096
	}
	if cfg.LookupTimeout <= 0 {
		cfg.LookupTimeout = 3 * time.Second
	}
	if cfg.SubBuffer <= 0 {
		cfg.SubBuffer = 1024
	}
	if cfg.ServerVersion == "" {
		cfg.ServerVersion = "broker-stub/0.1.0"
	}
	s := &Server{up: up, log: log, cfg: cfg, m: cfg.Metrics, ring: newRing(cfg.RingSize), subs: map[uint64]*fan{}, reg: map[string]*orderDetails{}}
	// Orders placed before this process started (or by other clients against
	// the same upstream account) are absent from the submit-time registry; a
	// report for them must still resolve its static fields via an order query.
	s.lookd = s.lookupOrderDetails
	return s
}

// Attach establishes a gateway session and reports the account map and
// current report cursor.
func (s *Server) Attach(ctx context.Context, req *bg.AttachRequest) (*bg.AttachResponse, error) {
	ext := map[string]any{"upstream": "tradeforge-sim", "market": s.cfg.Market}
	if len(s.cfg.Symbols) > 0 {
		ext["symbols"] = s.cfg.Symbols
	}
	if s.cfg.PriceLimitPct > 0 {
		ext["price_limit_pct"] = s.cfg.PriceLimitPct
	}
	return &bg.AttachResponse{
		ServerVersion: s.cfg.ServerVersion,
		SeqCursor:     s.lastSeq(),
		Accounts: []*bg.GatewayAccount{{
			FundAccount: s.cfg.FundAccount,
			TradeAccounts: []*bg.GatewayTradeAccount{{
				TradeAccount: s.cfg.TradeAccount,
				Market:       s.cfg.Market,
			}},
		}},
		ExtJson: marshalExt(ext),
	}, nil
}

// Heartbeat echoes the request and reports the current event cursor.
func (s *Server) Heartbeat(ctx context.Context, req *bg.HeartbeatRequest) (*bg.HeartbeatResponse, error) {
	return &bg.HeartbeatResponse{
		RequestId:          req.GetRequestId(),
		SeqCursor:          s.lastSeq(),
		ServerTimeUnixNano: time.Now().UnixNano(),
	}, nil
}

// SubmitOrder validates the stub account map and simulator price grid,
// forwards upstream, and returns the synchronous acceptance result.
func (s *Server) SubmitOrder(ctx context.Context, req *bg.SubmitOrderRequest) (*bg.SubmitOrderResponse, error) {
	if err := checkAccounts(req, s.cfg.FundAccount, s.cfg.TradeAccount); err != nil {
		return rejectSubmit(req, err)
	}
	if req.GetSymbol() == "" || req.GetQty() <= 0 {
		return rejectSubmit(req, fmt.Errorf("symbol and a positive qty are required"))
	}
	if req.GetSide() == bg.Side_SIDE_UNSPECIFIED || req.GetOrderType() == bg.OrderType_ORDER_TYPE_UNSPECIFIED {
		return rejectSubmit(req, fmt.Errorf("side and order_type are required"))
	}
	px := int64(0)
	if req.GetOrderType() == bg.OrderType_ORDER_TYPE_LIMIT {
		if req.GetPriceU() <= 0 {
			return rejectSubmit(req, fmt.Errorf("limit orders require a positive price_u"))
		}
		if req.GetPriceU()%gridFromMicro != 0 {
			return rejectSubmit(req, fmt.Errorf("price_u %d is finer than the simulator price grid (0.01)", req.GetPriceU()))
		}
		px = req.GetPriceU() / gridFromMicro
	}
	out, err := s.up.SubmitOrder(ctx, &pb.SubmitOrderRequest{
		RequestId:     fmt.Sprintf("bgw-%d", req.GetRequestId()),
		ClientOrderId: req.GetClientOrderId(),
		Symbol:        req.GetSymbol(),
		Side:          pb.Side(req.GetSide()),
		Type:          pb.OrderType(req.GetOrderType()),
		TimeInForce:   pb.TimeInForce(tifToUp(req.GetTif())),
		Price:         px,
		Quantity:      req.GetQty(),
	})
	if err != nil {
		return nil, err
	}
	if out.GetStatus() != pb.OrderStatus_ORDER_STATUS_REJECTED {
		s.remember(req)
	}
	return &bg.SubmitOrderResponse{
		RequestId:     req.GetRequestId(),
		ClientOrderId: req.GetClientOrderId(),
		Accepted:      true,
		RejectReason:  out.GetRejectReason(),
		Status:        statusFromPB(out.GetStatus()),
	}, nil
}

// CancelOrder forwards a cancel request keyed by client_order_id.
func (s *Server) CancelOrder(ctx context.Context, req *bg.CancelOrderRequest) (*bg.CancelOrderResponse, error) {
	out, err := s.up.CancelOrder(ctx, &pb.CancelOrderRequest{
		RequestId:     fmt.Sprintf("bgw-%d", req.GetRequestId()),
		ClientOrderId: req.GetClientOrderId(),
	})
	if err != nil {
		return nil, err
	}
	return &bg.CancelOrderResponse{
		RequestId:     req.GetRequestId(),
		ClientOrderId: req.GetClientOrderId(),
		Accepted:      out.GetStatus() != pb.OrderStatus_ORDER_STATUS_REJECTED,
		RejectReason:  out.GetReason(),
		Status:        statusFromPB(out.GetStatus()),
	}, nil
}

// QueryOrders returns a page of the day's orders.
func (s *Server) QueryOrders(ctx context.Context, req *bg.QueryOrdersRequest) (*bg.QueryOrdersResponse, error) {
	f := req.GetFilter()
	out, err := s.up.GetOrders(ctx, &pb.GetOrdersRequest{
		RequestId:     fmt.Sprintf("bgw-qo-%d", f.GetPageSize()),
		PageSize:      uint32(f.GetPageSize()),
		PageToken:     f.GetPageToken(),
		Symbol:        f.GetSymbol(),
		ClientOrderId: f.GetClientOrderId(),
	})
	if err != nil {
		return nil, err
	}
	orders := make([]*bg.BrokerOrder, 0, len(out.GetOrders()))
	for _, o := range out.GetOrders() {
		orders = append(orders, brokerOrderFromPB(o, s.cfg))
	}
	return &bg.QueryOrdersResponse{
		Orders:        orders,
		NextPageToken: out.GetNextPageToken(),
		IsLast:        out.GetNextPageToken() == "",
	}, nil
}

// QueryTrades returns a page of the day's executions; a client_order_id
// filter is resolved through an upstream order lookup first.
func (s *Server) QueryTrades(ctx context.Context, req *bg.QueryTradesRequest) (*bg.QueryTradesResponse, error) {
	f := req.GetFilter()
	orderID := ""
	if f.GetClientOrderId() != "" {
		id, err := resolveOrderID(ctx, s.up, f.GetClientOrderId())
		if err != nil {
			return nil, err
		}
		orderID = id
	}
	out, err := s.up.GetTrades(ctx, &pb.GetTradesRequest{
		RequestId: fmt.Sprintf("bgw-qt-%d", f.GetPageSize()),
		PageSize:  uint32(f.GetPageSize()),
		PageToken: f.GetPageToken(),
		OrderId:   orderID,
		Symbol:    f.GetSymbol(),
	})
	if err != nil {
		return nil, err
	}
	trades := make([]*bg.BrokerTrade, 0, len(out.GetTrades()))
	for _, t := range out.GetTrades() {
		trades = append(trades, brokerTradeFromPB(t, s.cfg, f.GetClientOrderId()))
	}
	return &bg.QueryTradesResponse{
		Trades:        trades,
		NextPageToken: out.GetNextPageToken(),
		IsLast:        out.GetNextPageToken() == "",
	}, nil
}

// QueryAccount returns the account snapshot: positions plus an explicit note
// that the simulator does not expose cash balances.
func (s *Server) QueryAccount(ctx context.Context, req *bg.QueryAccountRequest) (*bg.QueryAccountResponse, error) {
	out, err := s.up.GetPositions(ctx, &pb.GetPositionsRequest{RequestId: fmt.Sprintf("bgw-qa-%d", req.GetRequestId())})
	if err != nil {
		return nil, err
	}
	pos := make([]*bg.BrokerPosition, 0, len(out.GetPositions()))
	for _, p := range out.GetPositions() {
		pos = append(pos, &bg.BrokerPosition{
			TradeAccount: s.cfg.TradeAccount,
			Market:       s.cfg.Market,
			Symbol:       p.GetSymbol(),
			Qty:          p.GetQuantity(),
			AvailableQty: p.GetQuantity() - p.GetFrozenQty(),
			AvgCostU:     p.GetAvgPrice() * gridToMicro,
			ExtJson:      marshalExt(map[string]any{"upstream_frozen_qty": p.GetFrozenQty()}),
		})
	}
	return &bg.QueryAccountResponse{
		Snapshot: &bg.AccountSnapshot{
			FundAccount: req.GetFundAccount(),
			Positions:   pos,
			TsUnixNano:  time.Now().UnixNano(),
			ExtJson: marshalExt(map[string]any{
				"cash_unavailable": "simulator does not expose cash balances",
			}),
		},
	}, nil
}

// QuerySecurityInfo returns the static simulator security profile; optional
// price-limit simulation uses the latest upstream tick price as reference.
func (s *Server) QuerySecurityInfo(ctx context.Context, req *bg.QuerySecurityInfoRequest) (*bg.QuerySecurityInfoResponse, error) {
	sym := req.GetSymbol()
	if sym == "" {
		return nil, status.Error(codes.InvalidArgument, "symbol is required")
	}
	trading := bg.TradingStatus_TRADING_STATUS_NORMAL
	for _, h := range s.cfg.HaltedSymbols {
		if h == sym {
			trading = bg.TradingStatus_TRADING_STATUS_SUSPENDED
			break
		}
	}
	info := &bg.SecurityInfo{
		Market:        s.cfg.Market,
		Symbol:        sym,
		TradingStatus: trading,
		PriceTickU:    gridToMicro * gridFromMicro, // 0.01 yuan -> 100 units
		LotSize:       100,
	}
	if s.cfg.PriceLimitPct > 0 {
		if ref, ok := s.lastPrice(sym); ok {
			info.LimitUpU = ref * int64(100+s.cfg.PriceLimitPct) / 100
			info.LimitDownU = ref * int64(100-s.cfg.PriceLimitPct) / 100
		}
	}
	return &bg.QuerySecurityInfoResponse{Info: info}, nil
}

// SubscribeReports replays the ring window from from_seq and then streams
// live events; from_seq 0 starts live at the current cursor.
func (s *Server) SubscribeReports(req *bg.SubscribeReportsRequest, stream bg.BrokerGatewayService_SubscribeReportsServer) error {
	filter := req.GetFundAccount()
	if filter != "" && filter != s.cfg.FundAccount {
		return status.Errorf(codes.InvalidArgument, "unknown fund account %q (stub maps a single upstream user)", filter)
	}
	f, replay, err := s.attach(filter, req.GetFromSeq())
	if err != nil {
		return err
	}
	defer s.detach(f)
	for _, ev := range replay {
		if err := stream.Send(ev); err != nil {
			if status.Code(err) == codes.Canceled {
				return nil
			}
			return err
		}
	}
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-f.dead:
			return status.Error(codes.ResourceExhausted, "slow consumer, resubscribe with a newer from_seq")
		case ev, ok := <-f.ch:
			if !ok {
				return status.Error(codes.ResourceExhausted, "slow consumer, resubscribe with a newer from_seq")
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

// Start launches the pump goroutine that translates upstream order/execution
// events into ReportEvents (and the tick watcher when price limits are on).
func (s *Server) Start(ctx context.Context) {
	if s.cfg.PriceLimitPct > 0 && len(s.cfg.Symbols) > 0 {
		go s.watchTicks(ctx, s.cfg.Symbols)
	}
	go s.pump(ctx)
}

func (s *Server) pump(ctx context.Context) {
	events, errs := s.up.SubscribeOrders(ctx, &pb.SubscribeOrdersRequest{RequestId: "bgw-pump"})
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errs:
			if err != nil && ctx.Err() == nil {
				s.log.Error("upstream order stream ended", "error", err)
				s.m.upstreamError()
			}
			return
		case ev, ok := <-events:
			if !ok {
				s.log.Error("upstream order stream ended", "error", "event channel closed")
				s.m.upstreamError()
				return
			}
			s.translate(ctx, ev)
		}
	}
}

// watchTicks subscribes to upstream market data and maintains the reference
// price cache used by optional price-limit simulation.
func (s *Server) watchTicks(ctx context.Context, symbols []string) {
	events, errs := s.up.SubscribeMarketData(ctx, &pb.SubscribeMarketDataRequest{RequestId: "bgw-md", Symbols: symbols})
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errs:
			if err != nil && ctx.Err() == nil {
				s.log.Error("upstream market data stream ended", "error", err)
				s.m.upstreamError()
			}
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.GetLastPrice() > 0 {
				s.SetLastPrice(ev.GetSymbol(), ev.GetLastPrice()*gridToMicro)
			}
		}
	}
}

// translate converts one upstream order event into a full order report,
// pulling incremental trade reports first when the cumulative fill grew.
func (s *Server) translate(ctx context.Context, ev *pb.OrderEvent) {
	// The upstream stream carries only cumulative order state, so trade
	// reports are pulled incrementally whenever the reported fill grows.
	if ev.GetFilledQty() > 0 {
		s.emitTradesForFill(ctx, ev)
	}
	o := &bg.BrokerOrder{
		ClientOrderId:   ev.GetClientOrderId(),
		ExchangeOrderId: ev.GetOrderId(),
		FundAccount:     s.cfg.FundAccount,
		TradeAccount:    s.cfg.TradeAccount,
		Market:          s.cfg.Market,
		Symbol:          ev.GetSymbol(),
		Side:            sideBG(ev.GetSide()),
		OrderType:       typeBG(ev.GetType()),
		FilledQty:       ev.GetFilledQty(),
		AvgFillPriceU:   ev.GetAvgPrice() * gridToMicro,
		Status:          statusFromPB(ev.GetStatus()),
		RejectReason:    ev.GetReason(),
		UpdatedUnixNano: ev.GetTsUnixNano(),
	}
	if d, ok := s.detailsFor(ev.GetClientOrderId()); ok {
		o.PriceU = d.PriceU
		o.Qty = d.Qty
		o.Tif = d.Tif
		o.CreatedUnixNano = d.CreatedUnixNano
	}
	s.publish(&bg.ReportEvent{
		ClientOrderId:   ev.GetClientOrderId(),
		ExchangeOrderId: ev.GetOrderId(),
		Payload:         &bg.ReportEvent_Order{Order: o},
	})
}

// detailsFor resolves missing order fields from the session registry.
func (s *Server) detailsFor(clientOrderID string) (*orderDetails, bool) {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	d, ok := s.reg[clientOrderID]
	return d, ok
}

// emitTradesForFill pulls the day's executions for the order and publishes
// trade reports for the not-yet-emitted ones (deduplicated by trade id).
// The cumulative-fill watermark advances only after a successful query, so a
// transient upstream failure is retried on the next order event instead of
// silently dropping execution reports.
func (s *Server) emitTradesForFill(ctx context.Context, ev *pb.OrderEvent) {
	if ev.GetFilledQty() <= 0 {
		return
	}
	s.fillMu.Lock()
	defer s.fillMu.Unlock()

	cid := ev.GetClientOrderId()
	d := s.resolveDetails(ctx, cid)
	if d == nil || ev.GetFilledQty() <= d.ReportedFilled {
		return
	}

	var known []*bg.BrokerTrade
	var ids []string
	token := ""
	for {
		page, err := s.up.GetTrades(ctx, &pb.GetTradesRequest{
			RequestId: "bgw-fills",
			PageSize:  100,
			PageToken: token,
			OrderId:   ev.GetOrderId(),
		})
		if err != nil {
			s.log.Warn("fill trade query failed", "client_order_id", cid, "error", err)
			s.m.upstreamError()
			return
		}
		for _, t := range page.GetTrades() {
			bt := brokerTradeFromPB(t, s.cfg, cid)
			if _, seen := d.EmittedTrades[bt.GetTradeId()]; seen {
				continue
			}
			known = append(known, bt)
			ids = append(ids, bt.GetTradeId())
		}
		if page.GetNextPageToken() == "" {
			break
		}
		token = page.GetNextPageToken()
	}
	for _, id := range ids {
		d.EmittedTrades[id] = struct{}{}
	}
	d.ReportedFilled = ev.GetFilledQty()
	for _, bt := range known {
		s.publish(&bg.ReportEvent{
			ClientOrderId:   bt.GetClientOrderId(),
			ExchangeOrderId: bt.GetExchangeOrderId(),
			Payload:         &bg.ReportEvent_Trade{Trade: bt},
		})
	}
}

// resolveDetails returns the mutable per-order fill bookkeeping. Orders that
// predate this process (or were submitted by another client on the same
// upstream account) are resolved through an upstream order query; the result
// is cached so the lookup happens once. Callers must hold fillMu.
func (s *Server) resolveDetails(ctx context.Context, cid string) *orderDetails {
	s.regMu.Lock()
	d, ok := s.reg[cid]
	s.regMu.Unlock()
	if !ok && s.lookd != nil {
		if looked, found := s.lookd(ctx, cid); found && looked != nil {
			d = looked
			s.regMu.Lock()
			if len(s.reg) < maxRegistryEntries {
				if cur, exists := s.reg[cid]; exists {
					d = cur
				} else {
					s.reg[cid] = d
				}
			}
			s.regMu.Unlock()
		}
	}
	if d == nil {
		// Static fields are unavailable, but trade dedup still needs a record.
		s.regMu.Lock()
		if len(s.reg) < maxRegistryEntries {
			d = &orderDetails{}
			s.reg[cid] = d
		}
		s.regMu.Unlock()
	}
	if d != nil && d.EmittedTrades == nil {
		d.EmittedTrades = map[string]struct{}{}
	}
	return d
}

// lookupOrderDetails resolves missing order fields via an upstream order
// query (test-configurable via Server.lookd).
func (s *Server) lookupOrderDetails(ctx context.Context, clientOrderID string) (*orderDetails, bool) {
	cctx, cancel := context.WithTimeout(ctx, s.cfg.LookupTimeout)
	defer cancel()
	orders, err := s.up.GetOrders(cctx, &pb.GetOrdersRequest{
		RequestId:     "bgw-lookup",
		ClientOrderId: clientOrderID,
		PageSize:      1,
	})
	if err != nil {
		s.m.upstreamError()
		return nil, false
	}
	if len(orders.GetOrders()) == 0 {
		return nil, false
	}
	o := orders.GetOrders()[0]
	return &orderDetails{
		PriceU:          o.GetPrice() * gridToMicro,
		Qty:             o.GetQuantity(),
		Tif:             tifBG(o.GetTimeInForce()),
		CreatedUnixNano: o.GetCreatedAtUnixNano(),
	}, true
}

// remember caches coarse order details at submit time to spare the pump a
// per-event lookup.
func (s *Server) remember(req *bg.SubmitOrderRequest) {
	d := &orderDetails{
		PriceU:          req.GetPriceU(),
		Qty:             req.GetQty(),
		Tif:             req.GetTif(),
		CreatedUnixNano: time.Now().UnixNano(),
		EmittedTrades:   map[string]struct{}{},
	}
	s.regMu.Lock()
	if len(s.reg) < maxRegistryEntries {
		s.reg[req.GetClientOrderId()] = d
	}
	s.regMu.Unlock()
}

// SetLastPrice caches the upstream reference price (in 1e-4 units).
func (s *Server) SetLastPrice(symbol string, priceU int64) { s.lastP.Store(symbol, priceU) }

func (s *Server) lastPrice(symbol string) (int64, bool) {
	v, ok := s.lastP.Load(symbol)
	if !ok {
		return 0, false
	}
	p, ok := v.(int64)
	return p, ok
}

func checkAccounts(req *bg.SubmitOrderRequest, fund, trade string) error {
	if req.GetFundAccount() != fund {
		return fmt.Errorf("unknown fund account %q (stub maps fund account %q)", req.GetFundAccount(), fund)
	}
	if req.GetTradeAccount() != trade {
		return fmt.Errorf("unknown trade account %q (stub maps trade account %q)", req.GetTradeAccount(), trade)
	}
	return nil
}

func rejectSubmit(req *bg.SubmitOrderRequest, err error) (*bg.SubmitOrderResponse, error) {
	return &bg.SubmitOrderResponse{
		RequestId:     req.GetRequestId(),
		ClientOrderId: req.GetClientOrderId(),
		Accepted:      false,
		RejectReason:  err.Error(),
		Status:        bg.OrderStatus_ORDER_STATUS_REJECTED,
	}, nil
}

// resolveOrderID maps a client_order_id to the upstream order id; NotFound if
// the day has no such order.
func resolveOrderID(ctx context.Context, up upstream, clientOrderID string) (string, error) {
	out, err := up.GetOrders(ctx, &pb.GetOrdersRequest{
		RequestId:     "bgw-resolve",
		ClientOrderId: clientOrderID,
		PageSize:      1,
	})
	if err != nil {
		return "", err
	}
	if len(out.GetOrders()) == 0 {
		return "", status.Errorf(codes.NotFound, "no order for client_order_id %q", clientOrderID)
	}
	return out.GetOrders()[0].GetOrderId(), nil
}

// fan is one subscription's fan-out mailbox.
type fan struct {
	id   uint64
	ch   chan *bg.ReportEvent
	dead chan struct{}
	kill func()
}

// attach validates from_seq against the ring window and atomically prepares
// the replay slice plus a registered mailbox so replays and live events can
// neither overlap nor gap.
func (s *Server) attach(filter string, fromSeq int64) (*fan, []*bg.ReportEvent, error) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	last := s.ring.cursor()
	from := fromSeq
	if from == 0 {
		from = last
	}
	if from > last {
		return nil, nil, status.Errorf(codes.InvalidArgument, "from_seq %d is ahead of the gateway cursor %d", from, last)
	}
	replay, err := s.ring.snapshot(from + 1)
	if err != nil {
		return nil, nil, err
	}
	f := &fan{id: s.nextID, ch: make(chan *bg.ReportEvent, s.cfg.SubBuffer), dead: make(chan struct{})}
	f.kill = sync.OnceFunc(func() { close(f.dead) })
	s.nextID++
	s.subs[f.id] = f
	s.m.consumerAdded()
	s.m.replayedN(len(replay))
	return f, replay, nil
}

func (s *Server) detach(f *fan) {
	s.rmu.Lock()
	if cur, ok := s.subs[f.id]; ok && cur == f {
		delete(s.subs, f.id)
	}
	s.rmu.Unlock()
	s.m.consumerRemoved()
	f.kill()
}

func (s *Server) lastSeq() int64 {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	return s.ring.cursor()
}

// publish assigns the next seq, appends to the ring, and fans out; slow fans
// are marked dead and removed.
func (s *Server) publish(ev *bg.ReportEvent) {
	s.rmu.Lock()
	ev.Seq = s.ring.push(ev)
	s.m.reportPublished(ev)
	var doomed []*fan
	for _, f := range s.subs {
		select {
		case f.ch <- ev:
		default:
			doomed = append(doomed, f)
		}
	}
	for _, f := range doomed {
		delete(s.subs, f.id)
	}
	s.rmu.Unlock()
	s.m.slowConsumersN(len(doomed))
	for _, f := range doomed {
		f.kill()
		s.log.Warn("report consumer slow, dropped", "fan_id", f.id)
	}
}

// ring keeps the last cap events; seq indexes are 1-based and strictly
// increasing, snapshot(from) returns buffered events with seq >= from.
type ring struct {
	buf []*bg.ReportEvent
	cap int
	seq int64
}

func newRing(capacity int) *ring { return &ring{cap: capacity} }

func (r *ring) push(ev *bg.ReportEvent) int64 {
	r.seq++
	ev.Seq = r.seq
	r.buf = append(r.buf, ev)
	if len(r.buf) > r.cap {
		_ = copy(r.buf, r.buf[1:])
		r.buf = r.buf[:len(r.buf)-1]
	}
	return r.seq
}

func (r *ring) cursor() int64 { return r.seq }

// first returns the lowest buffered seq (0 if empty).
func (r *ring) first() int64 {
	if len(r.buf) == 0 {
		return 0
	}
	return r.buf[0].GetSeq()
}

// snapshot returns buffered events with seq >= from; from beyond the window
// triggers ResourceExhausted so clients re-attach from fresh cursor.
func (r *ring) snapshot(from int64) ([]*bg.ReportEvent, error) {
	if from > r.seq {
		return nil, nil
	}
	if len(r.buf) == 0 || from < r.first() {
		return nil, status.Errorf(codes.ResourceExhausted, "seq window expired: earliest buffered event seq is %d", r.first())
	}
	idx := int(from - r.first())
	out := make([]*bg.ReportEvent, 0, len(r.buf)-idx)
	out = append(out, r.buf[idx:]...)
	return out, nil
}
