package brokergw

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bg "github.com/viviwu/tradeforge/gen/brokergateway/v1"
	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

type fakeUpstream struct {
	filled   chan *pb.OrderEvent
	filledEr chan error

	submitResp *pb.SubmitOrderResponse
	submitErr  error
	submitted  *pb.SubmitOrderRequest

	orders     *pb.GetOrdersResponse
	trades     *pb.GetTradesResponse
	positns    *pb.GetPositionsResponse
	tradesFail int // number of GetTrades calls that fail before succeeding
}

func (f *fakeUpstream) SubmitOrder(ctx context.Context, req *pb.SubmitOrderRequest) (*pb.SubmitOrderResponse, error) {
	f.submitted = req
	return f.submitResp, f.submitErr
}

func (f *fakeUpstream) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.CancelOrderResponse, error) {
	return &pb.CancelOrderResponse{OrderId: "o1", Status: pb.OrderStatus_ORDER_STATUS_CANCELED}, nil
}

func (f *fakeUpstream) GetOrders(ctx context.Context, req *pb.GetOrdersRequest) (*pb.GetOrdersResponse, error) {
	return f.orders, nil
}

func (f *fakeUpstream) GetTrades(ctx context.Context, req *pb.GetTradesRequest) (*pb.GetTradesResponse, error) {
	if f.tradesFail > 0 {
		f.tradesFail--
		return nil, errors.New("upstream trades temporarily unavailable")
	}
	return f.trades, nil
}

func (f *fakeUpstream) GetPositions(ctx context.Context, req *pb.GetPositionsRequest) (*pb.GetPositionsResponse, error) {
	return f.positns, nil
}

func (f *fakeUpstream) SubscribeOrders(ctx context.Context, req *pb.SubscribeOrdersRequest) (<-chan *pb.OrderEvent, <-chan error) {
	if f.filled == nil {
		f.filled = make(chan *pb.OrderEvent)
		f.filledEr = make(chan error)
	}
	return f.filled, f.filledEr
}

func (f *fakeUpstream) SubscribeMarketData(ctx context.Context, req *pb.SubscribeMarketDataRequest) (<-chan *pb.MarketEvent, <-chan error) {
	e := make(chan *pb.MarketEvent)
	eerr := make(chan error)
	return e, eerr
}

func testConfig() Config {
	return Config{
		FundAccount:  "F001",
		TradeAccount: "A0001",
		Market:       "SIM",
		Symbols:      []string{"AAPL"},
	}
}

func newTestServer(t *testing.T, up *fakeUpstream) *Server {
	t.Helper()
	return NewServer(up, slog.New(slog.NewTextHandler(io.Discard, nil)), testConfig())
}

func TestSubmitOrderAccountAndGrid(t *testing.T) {
	up := &fakeUpstream{submitResp: &pb.SubmitOrderResponse{OrderId: "o1", ClientOrderId: "c1", Status: pb.OrderStatus_ORDER_STATUS_PENDING_NEW}}
	s := newTestServer(t, up)

	resp, err := s.SubmitOrder(context.Background(), &bg.SubmitOrderRequest{
		ClientOrderId: "c1",
		FundAccount:   "WRONG",
		TradeAccount:  "A0001",
		Symbol:        "AAPL",
		Side:          bg.Side_SIDE_BUY,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        1999000,
		Qty:           100,
	})
	if err != nil {
		t.Fatalf("SubmitOrder: %v", err)
	}
	if resp.GetAccepted() {
		t.Fatalf("unknown fund account must not be accepted")
	}

	resp, err = s.SubmitOrder(context.Background(), &bg.SubmitOrderRequest{
		ClientOrderId: "c1",
		FundAccount:   "F001",
		TradeAccount:  "A0001",
		Symbol:        "AAPL",
		Side:          bg.Side_SIDE_BUY,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        1999005, // finer than 0.01 grid
		Qty:           100,
	})
	if err != nil {
		t.Fatalf("SubmitOrder: %v", err)
	}
	if resp.GetAccepted() || resp.GetStatus() != bg.OrderStatus_ORDER_STATUS_REJECTED {
		t.Fatalf("fine price must be rejected")
	}
}

func TestSubmitOrderConvertsScale(t *testing.T) {
	up := &fakeUpstream{submitResp: &pb.SubmitOrderResponse{OrderId: "o1", ClientOrderId: "c1", Status: pb.OrderStatus_ORDER_STATUS_PENDING_NEW}}
	s := newTestServer(t, up)

	resp, err := s.SubmitOrder(context.Background(), &bg.SubmitOrderRequest{
		ClientOrderId: "c1",
		FundAccount:   "F001",
		TradeAccount:  "A0001",
		Symbol:        "AAPL",
		Side:          bg.Side_SIDE_BUY,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        1999000, // 199.90 yuan
		Qty:           100,
		Tif:           bg.TimeInForce_TIME_IN_FORCE_DAY,
	})
	if err != nil {
		t.Fatalf("SubmitOrder: %v", err)
	}
	if !resp.GetAccepted() {
		t.Fatalf("expected acceptance, got %s", resp.GetRejectReason())
	}
	got := up.submitted
	if got.GetPrice() != 199900 {
		t.Errorf("upstream price = %d, want 199900", got.GetPrice())
	}
	if got.GetQuantity() != 100 {
		t.Errorf("quantity = %d, want 100", got.GetQuantity())
	}
	if got.GetTimeInForce() != pb.TimeInForce_TIME_IN_FORCE_UNSPECIFIED {
		t.Errorf("TIF DAY should map to upstream session default, got %v", got.GetTimeInForce())
	}
}

func TestCancelOrderAccepts(t *testing.T) {
	up := &fakeUpstream{}
	s := newTestServer(t, up)
	resp, err := s.CancelOrder(context.Background(), &bg.CancelOrderRequest{RequestId: 1, ClientOrderId: "c1"})
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if !resp.GetAccepted() || resp.GetStatus() != bg.OrderStatus_ORDER_STATUS_CANCELED {
		t.Fatalf("unexpected cancel response %+v", resp)
	}
}

func TestQueryOrdersConvertsPrices(t *testing.T) {
	up := &fakeUpstream{orders: &pb.GetOrdersResponse{Orders: []*pb.Order{{
		OrderId:           "o9",
		ClientOrderId:     "c9",
		Symbol:            "AAPL",
		Side:              pb.Side_SIDE_SELL,
		Type:              pb.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:       pb.TimeInForce_TIME_IN_FORCE_GTC,
		Price:             199900,
		Quantity:          200,
		FilledQty:         50,
		AvgPrice:          199800,
		Status:            pb.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED,
		CreatedAtUnixNano: 1,
		UpdatedAtUnixNano: 2,
	}}}}
	s := newTestServer(t, up)
	resp, err := s.QueryOrders(context.Background(), &bg.QueryOrdersRequest{Filter: &bg.QueryFilter{PageSize: 10}})
	if err != nil {
		t.Fatalf("QueryOrders: %v", err)
	}
	if !resp.GetIsLast() || len(resp.GetOrders()) != 1 {
		t.Fatalf("unexpected result: %+v", resp)
	}
	o := resp.GetOrders()[0]
	if o.GetPriceU() != 1999000 || o.GetAvgFillPriceU() != 1998000 {
		t.Errorf("price conversion wrong: price_u=%d avg=%d", o.GetPriceU(), o.GetAvgFillPriceU())
	}
	if o.GetExchangeOrderId() != "o9" || o.GetTif() != bg.TimeInForce_TIME_IN_FORCE_GTC {
		t.Errorf("unexpected order mapping %+v", o)
	}
}

func TestQueryAccountSnowPositions(t *testing.T) {
	up := &fakeUpstream{positns: &pb.GetPositionsResponse{Positions: []*pb.Position{{
		Symbol:    "AAPL",
		Quantity:  300,
		FrozenQty: 100,
		AvgPrice:  199900,
	}}}}
	s := newTestServer(t, up)
	resp, err := s.QueryAccount(context.Background(), &bg.QueryAccountRequest{RequestId: 1, FundAccount: "F001"})
	if err != nil {
		t.Fatalf("QueryAccount: %v", err)
	}
	pos := resp.GetSnapshot().GetPositions()
	if len(pos) != 1 || pos[0].GetQty() != 300 || pos[0].GetAvailableQty() != 200 {
		t.Fatalf("unexpected positions %+v", pos)
	}
}

func TestQuerySecurityInfoProfile(t *testing.T) {
	up := &fakeUpstream{}
	s := newTestServer(t, up)
	resp, err := s.QuerySecurityInfo(context.Background(), &bg.QuerySecurityInfoRequest{Symbol: "AAPL"})
	if err != nil {
		t.Fatalf("QuerySecurityInfo: %v", err)
	}
	if resp.GetInfo().GetPriceTickU() != 100 || resp.GetInfo().GetLotSize() != 100 {
		t.Fatalf("unexpected profile %+v", resp.GetInfo())
	}
	if resp.GetInfo().GetTradingStatus() != bg.TradingStatus_TRADING_STATUS_NORMAL {
		t.Fatalf("unexpected trading status")
	}

	cfgHalted := testConfig()
	cfgHalted.HaltedSymbols = []string{"MSFT"}
	s = NewServer(up, slog.New(slog.NewTextHandler(io.Discard, nil)), cfgHalted)
	resp, err = s.QuerySecurityInfo(context.Background(), &bg.QuerySecurityInfoRequest{Symbol: "MSFT"})
	if err != nil {
		t.Fatalf("QuerySecurityInfo: %v", err)
	}
	if resp.GetInfo().GetTradingStatus() != bg.TradingStatus_TRADING_STATUS_SUSPENDED {
		t.Fatalf("halted symbol must be SUSPENDED")
	}
}

func TestQuerySecurityInfoPriceLimits(t *testing.T) {
	up := &fakeUpstream{}
	cfg := testConfig()
	cfg.PriceLimitPct = 10
	s := NewServer(up, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	s.SetLastPrice("AAPL", 2000000) // 200.00 yuan

	resp, err := s.QuerySecurityInfo(context.Background(), &bg.QuerySecurityInfoRequest{Symbol: "AAPL"})
	if err != nil {
		t.Fatalf("QuerySecurityInfo: %v", err)
	}
	if resp.GetInfo().GetLimitUpU() != 2200000 || resp.GetInfo().GetLimitDownU() != 1800000 {
		t.Fatalf("unexpected limits: up=%d down=%d", resp.GetInfo().GetLimitUpU(), resp.GetInfo().GetLimitDownU())
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context

	mu   sync.Mutex
	sent []*bg.ReportEvent
}

func (f *fakeStream) Context() context.Context { return f.ctx }
func (f *fakeStream) Send(ev *bg.ReportEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ev)
	return nil
}

func (f *fakeStream) sentLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeStream) events() []*bg.ReportEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*bg.ReportEvent, len(f.sent))
	copy(out, f.sent)
	return out
}

func TestTranslateEmitsTradeThenOrder(t *testing.T) {
	up := &fakeUpstream{}
	s := newTestServer(t, up)
	s.remember(&bg.SubmitOrderRequest{
		ClientOrderId: "c1",
		FundAccount:   "F001",
		TradeAccount:  "A0001",
		Symbol:        "AAPL",
		Side:          bg.Side_SIDE_BUY,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        1999000,
		Qty:           100,
	})
	s.translate(context.Background(), &pb.OrderEvent{
		Seq:           7,
		OrderId:       "o1",
		ClientOrderId: "c1",
		Symbol:        "AAPL",
		Side:          pb.Side_SIDE_BUY,
		Type:          pb.OrderType_ORDER_TYPE_LIMIT,
		Status:        pb.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED,
		FilledQty:     40,
		AvgPrice:      199850,
		LastFillQty:   40,
		LastFillPrice: 199850,
		TradeSeq:      3,
		TsUnixNano:    123,
	})
	evs := drain(s)
	if len(evs) != 1 {
		t.Fatalf("expected 1 order event (trades come from incremental queries), got %d", len(evs))
	}
	order := evs[0].GetOrder()
	if order == nil || order.GetPriceU() != 1999000 || order.GetQty() != 100 || order.GetFilledQty() != 40 {
		t.Fatalf("unexpected order event %+v", evs[0])
	}
	if order.GetStatus() != bg.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED {
		t.Fatal("wrong status")
	}
	if evs[0].GetSeq() != 1 {
		t.Fatalf("seq must start at 1: %d", evs[0].GetSeq())
	}
}

func TestEmitTradesForFillIncremental(t *testing.T) {
	up := &fakeUpstream{}
	s := newTestServer(t, up)
	s.remember(&bg.SubmitOrderRequest{
		ClientOrderId: "c1",
		FundAccount:   "F001",
		TradeAccount:  "A0001",
		Symbol:        "AAPL",
		PriceU:        1999000,
		Qty:           100,
	})
	up.trades = &pb.GetTradesResponse{Trades: []*pb.Trade{{
		TradeSeq: 7, OrderId: "o1", Symbol: "AAPL", Side: pb.Side_SIDE_BUY,
		Price: 199900, Quantity: 100, CreatedAtUnixNano: 5,
	}}}

	s.translate(context.Background(), &pb.OrderEvent{
		OrderId: "o1", ClientOrderId: "c1", Symbol: "AAPL", Side: pb.Side_SIDE_BUY,
		Type: pb.OrderType_ORDER_TYPE_LIMIT, Status: pb.OrderStatus_ORDER_STATUS_FILLED,
		FilledQty: 100, AvgPrice: 199900, TsUnixNano: 9,
	})
	evs := drain(s)
	if len(evs) != 2 {
		t.Fatalf("expected trade+order events, got %d", len(evs))
	}
	trade := evs[0].GetTrade()
	if trade == nil || trade.GetPriceU() != 1999000 || trade.GetQty() != 100 || trade.GetTradeId() != "7" || trade.GetAmountU() != 1999000*100 {
		t.Fatalf("unexpected trade event %+v", evs[0])
	}
	if trade.GetClientOrderId() != "c1" {
		t.Fatalf("trade report must carry client_order_id")
	}

	// A repeated event with the same cumulative fill still maps to one order
	// report, but must not emit a second trade report.
	s.translate(context.Background(), &pb.OrderEvent{
		OrderId: "o1", ClientOrderId: "c1", Symbol: "AAPL", Side: pb.Side_SIDE_BUY,
		Type: pb.OrderType_ORDER_TYPE_LIMIT, Status: pb.OrderStatus_ORDER_STATUS_FILLED,
		FilledQty: 100, AvgPrice: 199900, TsUnixNano: 10,
	})
	evs = drain(s)
	trades := 0
	for _, ev := range evs {
		if ev.GetTrade() != nil {
			trades++
		}
	}
	if len(evs) != 3 || trades != 1 {
		t.Fatalf("duplicate fill must emit only an order report, got %d events / %d trades", len(evs), trades)
	}
}

func TestUnknownOrderFillResolvesDetails(t *testing.T) {
	// An order submitted before the gateway started is absent from the
	// submit-time registry: static fields must be resolved by an order query
	// and the result cached (no panic, no repeated lookup).
	up := &fakeUpstream{
		orders: &pb.GetOrdersResponse{Orders: []*pb.Order{{
			OrderId: "o7", ClientOrderId: "c7", Symbol: "AAPL",
			Price: 199900, Quantity: 100, TimeInForce: pb.TimeInForce_TIME_IN_FORCE_GTC,
		}}},
		trades: &pb.GetTradesResponse{Trades: []*pb.Trade{{
			TradeSeq: 9, OrderId: "o7", Symbol: "AAPL", Side: pb.Side_SIDE_BUY,
			Price: 199900, Quantity: 100,
		}}},
	}
	s := newTestServer(t, up)
	ev := &pb.OrderEvent{
		OrderId: "o7", ClientOrderId: "c7", Symbol: "AAPL", Side: pb.Side_SIDE_BUY,
		Type: pb.OrderType_ORDER_TYPE_LIMIT, Status: pb.OrderStatus_ORDER_STATUS_FILLED,
		FilledQty: 100, AvgPrice: 199900,
	}
	s.translate(context.Background(), ev)
	evs := drain(s)
	if len(evs) != 2 {
		t.Fatalf("expected trade + order reports, got %d", len(evs))
	}
	order := evs[1].GetOrder()
	if order == nil || order.GetPriceU() != 1999000 || order.GetQty() != 100 || order.GetTif() != bg.TimeInForce_TIME_IN_FORCE_GTC {
		t.Fatalf("looked-up order fields missing from report: %+v", order)
	}
	s.translate(context.Background(), ev)
	if got := countTrades(drain(s)); got != 1 {
		t.Fatalf("looked-up order trades must be deduplicated, got %d", got)
	}
}

func TestTradeQueryFailureRetriesOnNextEvent(t *testing.T) {
	up := &fakeUpstream{
		tradesFail: 1,
		trades: &pb.GetTradesResponse{Trades: []*pb.Trade{{
			TradeSeq: 11, OrderId: "o1", Symbol: "AAPL", Side: pb.Side_SIDE_BUY,
			Price: 199900, Quantity: 100,
		}}},
	}
	s := newTestServer(t, up)
	s.remember(&bg.SubmitOrderRequest{ClientOrderId: "c1", PriceU: 1999000, Qty: 100})
	ev := &pb.OrderEvent{
		OrderId: "o1", ClientOrderId: "c1", Symbol: "AAPL", Side: pb.Side_SIDE_BUY,
		Type: pb.OrderType_ORDER_TYPE_LIMIT, Status: pb.OrderStatus_ORDER_STATUS_FILLED, FilledQty: 100,
	}
	s.translate(context.Background(), ev)
	if got := countTrades(drain(s)); got != 0 {
		t.Fatalf("failed trade query must not publish trade reports, got %d", got)
	}
	// The watermark did not advance, so the next upstream event retries.
	s.translate(context.Background(), ev)
	if got := countTrades(drain(s)); got != 1 {
		t.Fatalf("retry after a failed query must publish the trade report, got %d", got)
	}
}

func countTrades(evs []*bg.ReportEvent) int {
	n := 0
	for _, ev := range evs {
		if ev.GetTrade() != nil {
			n++
		}
	}
	return n
}

func TestRingReplayAndWindow(t *testing.T) {
	r := newRing(3)
	var seqs []int64
	for i := 0; i < 4; i++ {
		seqs = append(seqs, r.push(&bg.ReportEvent{ClientOrderId: string(rune('a' + i))}))
	}
	if last := seqs[len(seqs)-1]; last != 4 {
		t.Fatalf("last seq = %d, want 4", last)
	}
	got, err := r.snapshot(2)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(got) != 3 || got[0].GetSeq() != 2 || got[len(got)-1].GetSeq() != 4 {
		t.Fatalf("unexpected replay seqs")
	}
	if _, err := r.snapshot(1); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expect ResourceExhausted for expired window, got %v", err)
	}
	if got, err := r.snapshot(5); err != nil || len(got) != 0 {
		t.Fatalf("from beyond cursor should yield nothing, got %v %v", got, err)
	}
}

func waitFans(t *testing.T, s *Server, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.rmu.Lock()
		got := len(s.subs)
		s.rmu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fans never reached %d", n)
}

func TestSubscribeReportsLiveFlow(t *testing.T) {
	up := &fakeUpstream{}
	s := newTestServer(t, up)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs := &fakeStream{ctx: ctx}
	doneCh := make(chan error, 1)
	go func() { doneCh <- s.SubscribeReports(&bg.SubscribeReportsRequest{FromSeq: 0}, fs) }()
	waitFans(t, s, 1)

	s.publish(&bg.ReportEvent{Payload: &bg.ReportEvent_Order{Order: &bg.BrokerOrder{ClientOrderId: "a"}}})
	s.publish(&bg.ReportEvent{Payload: &bg.ReportEvent_Trade{Trade: &bg.BrokerTrade{TradeId: "b"}}})

	deadline := time.Now().Add(2 * time.Second)
	for fs.sentLen() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	sent := fs.events()
	if len(sent) != 2 || sent[0].GetSeq() != 1 || sent[1].GetSeq() != 2 {
		t.Fatalf("unexpected sent events: %+v", sent)
	}
	cancel()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after cancel")
	}
}

func TestSubscribeReportsReplay(t *testing.T) {
	up := &fakeUpstream{}
	s := newTestServer(t, up)
	// Publish three events (seqs 1..3) before attaching; a from_seq=1
	// subscription replays 2..3, and a later publish flows live as seq 4.
	for i := 0; i < 3; i++ {
		s.publish(&bg.ReportEvent{Payload: &bg.ReportEvent_Order{Order: &bg.BrokerOrder{ClientOrderId: "x"}}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs := &fakeStream{ctx: ctx}
	doneCh := make(chan error, 1)
	go func() { doneCh <- s.SubscribeReports(&bg.SubscribeReportsRequest{FromSeq: 1}, fs) }()
	waitFans(t, s, 1)
	s.publish(&bg.ReportEvent{Payload: &bg.ReportEvent_Order{Order: &bg.BrokerOrder{ClientOrderId: "y"}}})

	deadline := time.Now().Add(2 * time.Second)
	for fs.sentLen() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	sent := fs.events()
	if len(sent) != 3 {
		t.Fatalf("expected replay+live of 3 events, got %d", len(sent))
	}
	for i, ev := range sent {
		if ev.GetSeq() != int64(i+2) {
			t.Fatalf("replay seqs must be contiguous from 2: got %d at %d", ev.GetSeq(), i)
		}
	}
	cancel()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after cancel")
	}
}

func TestSubscribeReportsSlowConsumerDropped(t *testing.T) {
	up := &fakeUpstream{}
	cfg := testConfig()
	cfg.SubBuffer = 1
	s := NewServer(up, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	fs := &blockingStream{ctx: ctx, release: release}
	doneCh := make(chan error, 1)
	go func() { doneCh <- s.SubscribeReports(&bg.SubscribeReportsRequest{FromSeq: 0}, fs) }()
	waitFans(t, s, 1)
	// Handler takes buffered event 1 and blocks inside Send (buffer drains),
	// event 2 fills the buffer, event 3 overflows -> fan dropped.
	s.publish(&bg.ReportEvent{})
	s.publish(&bg.ReportEvent{})
	s.publish(&bg.ReportEvent{})
	close(release)
	select {
	case err := <-doneCh:
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("expected slow-consumer ResourceExhausted, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no drop detected")
	}
}

// blockingStream stops inside Send until release is closed, so the test can
// control when the buffer drains.
type blockingStream struct {
	grpc.ServerStream
	ctx     context.Context
	release chan struct{}
	sent    []*bg.ReportEvent
}

func (f *blockingStream) Context() context.Context { return f.ctx }
func (f *blockingStream) Send(ev *bg.ReportEvent) error {
	<-f.release
	f.sent = append(f.sent, ev)
	return nil
}

// drain collects published events straight from the ring for translate()
// tests.
func drain(s *Server) []*bg.ReportEvent {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	out := make([]*bg.ReportEvent, len(s.ring.buf))
	copy(out, s.ring.buf)
	return out
}
