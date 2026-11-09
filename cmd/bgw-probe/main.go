// bgw-probe is the minimal channel-core client used to exercise a
// BrokerGatewayService endpoint end to end: attach, security profile,
// rest order, self-cross fill, queries, heartbeat, and cancel.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bg "github.com/viviwu/tradeforge/gen/brokergateway/v1"
)

var (
	addr   = flag.String("addr", envOr("BGW_PROBE_ADDR", "127.0.0.1:50053"), "broker gateway gRPC address")
	symbol = flag.String("symbol", envOr("BGW_PROBE_SYMBOL", "AAPL"), "symbol")
	qty    = flag.Int64("qty", envIntOr("BGW_PROBE_QTY", 10), "order quantity")
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
}

type collector struct {
	mu          sync.Mutex
	seen        map[string]map[bg.OrderStatus]bool
	fills       map[string]int
	seqs        []int64
	expectedSeq int64
	seqErr      error
}

// hasSeen reports whether a status was ever observed for the order (states
// can arrive back-to-back, so tracking the latest one alone would be lossy).
func (c *collector) hasSeen(cid string, want bg.OrderStatus) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[cid][want], c.seen[cid][bg.OrderStatus_ORDER_STATUS_REJECTED]
}

func (c *collector) fillCount(cid string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fills[cid]
}

func (c *collector) lastSeq() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seqs) == 0 {
		return 0
	}
	return c.seqs[len(c.seqs)-1]
}

func (c *collector) seqError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seqErr
}

func (c *collector) seqCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seqs)
}

func (c *collector) record(ev *bg.ReportEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := ev.GetSeq()
	if c.seqErr == nil && seq != c.expectedSeq {
		c.seqErr = fmt.Errorf("report seq gap: expected %d, got %d", c.expectedSeq, seq)
	}
	c.expectedSeq = seq + 1
	c.seqs = append(c.seqs, seq)
	if o := ev.GetOrder(); o != nil {
		cid := o.GetClientOrderId()
		if c.seen[cid] == nil {
			c.seen[cid] = map[bg.OrderStatus]bool{}
		}
		c.seen[cid][o.GetStatus()] = true
	}
	if t := ev.GetTrade(); t != nil {
		c.fills[t.GetClientOrderId()]++
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	setup := time.Now().UTC().Unix()
	sellCID := fmt.Sprintf("bgwprobe-sell-%d-%d", setup, os.Getpid())
	buyCID := fmt.Sprintf("bgwprobe-buy-%d-%d", setup, os.Getpid())
	restCID := fmt.Sprintf("bgwprobe-rest-%d-%d", setup, os.Getpid())

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", *addr, err)
	}
	defer conn.Close()
	client := bg.NewBrokerGatewayServiceClient(conn)

	fmt.Printf("== bgw-probe against %s (symbol %s qty %d) ==\n", *addr, *symbol, *qty)

	// 1) Attach.
	att, err := client.Attach(ctx, &bg.AttachRequest{ClientVersion: "bgw-probe/1.0"})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	if len(att.GetAccounts()) == 0 || len(att.GetAccounts()[0].GetTradeAccounts()) == 0 {
		return fmt.Errorf("attach returned no trade account: %s", describeAccounts(att.GetAccounts()))
	}
	acct := att.GetAccounts()[0]
	tradeAcct := acct.GetTradeAccounts()[0]
	fmt.Printf("1 attach ok: server=%s cursor=%d accounts=%s\n", att.GetServerVersion(), att.GetSeqCursor(), describeAccounts(att.GetAccounts()))

	// 2) Subscribe reports from the attach cursor. Every event after that
	// cursor must arrive exactly once, with contiguous gateway seq numbers.
	coll := &collector{
		seen:        map[string]map[bg.OrderStatus]bool{},
		fills:       map[string]int{},
		expectedSeq: att.GetSeqCursor() + 1,
	}
	sctx, scancel := context.WithCancel(context.Background())
	defer scancel()
	reportStream, err := client.SubscribeReports(sctx, &bg.SubscribeReportsRequest{FromSeq: att.GetSeqCursor()})
	if err != nil {
		return fmt.Errorf("subscribe reports: %w", err)
	}
	go func() {
		for {
			ev, err := reportStream.Recv()
			if err != nil {
				return
			}
			coll.record(ev)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	// 3) Security profile.
	si, err := client.QuerySecurityInfo(ctx, &bg.QuerySecurityInfoRequest{
		TradeAccount: tradeAcct.GetTradeAccount(), Market: tradeAcct.GetMarket(), Symbol: *symbol,
	})
	if err != nil {
		return fmt.Errorf("query security info: %w", err)
	}
	if si.GetInfo().GetTradingStatus() == bg.TradingStatus_TRADING_STATUS_SUSPENDED {
		return fmt.Errorf("symbol %s is suspended", *symbol)
	}
	tick := si.GetInfo().GetPriceTickU()
	if tick <= 0 {
		return fmt.Errorf("security info for %s has no price tick", *symbol)
	}
	fmt.Printf("2 security info: trading=%s tick=%d lot=%d up=%d down=%d\n", si.GetInfo().GetTradingStatus(), tick, si.GetInfo().GetLotSize(), si.GetInfo().GetLimitUpU(), si.GetInfo().GetLimitDownU())

	// Probe price aligned to the security tick, kept in a low band so it never
	// competes with the demo prices used by scripts/e2e.sh (1 unit = 0.0001 yuan).
	price := time.Now().UnixNano()%1000*int64(tick) + 10*int64(tick)
	if q := price % int64(tick); q != 0 {
		return fmt.Errorf("probe price %d not aligned to tick %d", price, tick)
	}

	// 4) Rest sell order.
	resp, err := client.SubmitOrder(ctx, &bg.SubmitOrderRequest{
		ClientOrderId: sellCID,
		FundAccount:   acct.GetFundAccount(),
		TradeAccount:  tradeAcct.GetTradeAccount(),
		Market:        tradeAcct.GetMarket(),
		Symbol:        *symbol,
		Side:          bg.Side_SIDE_SELL,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        price,
		Qty:           *qty,
		Tif:           bg.TimeInForce_TIME_IN_FORCE_DAY,
	})
	if err != nil {
		return fmt.Errorf("submit sell: %w", err)
	}
	if !resp.GetAccepted() {
		return fmt.Errorf("sell rejected: %s", resp.GetRejectReason())
	}
	fmt.Printf("3 sell accepted: %s status=%s price_u=%d\n", resp.GetClientOrderId(), resp.GetStatus(), price)

	// 5) Watch the report stream for SELL ACCEPTED.
	if err := waitForStatus(coll, sellCID, bg.OrderStatus_ORDER_STATUS_ACCEPTED, 5*time.Second); err != nil {
		return err
	}
	fmt.Println("4 sell order report ACCEPTED seen on reports stream")

	// 6) Cross it with a buy.
	resp, err = client.SubmitOrder(ctx, &bg.SubmitOrderRequest{
		ClientOrderId: buyCID,
		FundAccount:   acct.GetFundAccount(),
		TradeAccount:  tradeAcct.GetTradeAccount(),
		Market:        tradeAcct.GetMarket(),
		Symbol:        *symbol,
		Side:          bg.Side_SIDE_BUY,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        price,
		Qty:           *qty,
	})
	if err != nil {
		return fmt.Errorf("submit buy: %w", err)
	}
	if !resp.GetAccepted() {
		return fmt.Errorf("buy rejected: %s", resp.GetRejectReason())
	}
	// The synchronous response is only an acceptance ack; terminal states and
	// executions must arrive on the reports stream (contract semantics).
	fmt.Printf("5 buy accepted: %s status=%s\n", resp.GetClientOrderId(), resp.GetStatus())

	if err := waitForStatus(coll, buyCID, bg.OrderStatus_ORDER_STATUS_FILLED, 8*time.Second); err != nil {
		return err
	}
	// Trade reports must cover both sides of the self-cross.
	if err := waitForTrade(coll, sellCID, 8*time.Second); err != nil {
		return err
	}
	if err := waitForTrade(coll, buyCID, 8*time.Second); err != nil {
		return err
	}
	fmt.Printf("6 self-cross FILLED: trades(sell)=%d trades(buy)=%d\n", coll.fillCount(sellCID), coll.fillCount(buyCID))

	// 7) Queries must agree (client_order_id filter on both sides).
	for _, cid := range []string{sellCID, buyCID} {
		orders, err := client.QueryOrders(ctx, &bg.QueryOrdersRequest{Filter: &bg.QueryFilter{PageSize: 10, ClientOrderId: cid}})
		if err != nil {
			return fmt.Errorf("query orders(%s): %w", cid, err)
		}
		if len(orders.GetOrders()) != 1 {
			return fmt.Errorf("order %s: expected 1 match, got %d", cid, len(orders.GetOrders()))
		}
		if got := orders.GetOrders()[0].GetStatus(); got != bg.OrderStatus_ORDER_STATUS_FILLED {
			return fmt.Errorf("order %s status %s, want FILLED", cid, got)
		}
	}
	trades, err := client.QueryTrades(ctx, &bg.QueryTradesRequest{Filter: &bg.QueryFilter{PageSize: 50, ClientOrderId: sellCID}})
	if err != nil {
		return fmt.Errorf("query trades: %w", err)
	}
	if len(trades.GetTrades()) == 0 {
		return fmt.Errorf("no trades found for %s", sellCID)
	}
	snap, err := client.QueryAccount(ctx, &bg.QueryAccountRequest{FundAccount: acct.GetFundAccount()})
	if err != nil {
		return fmt.Errorf("query account: %w", err)
	}
	found := false
	for _, p := range snap.GetSnapshot().GetPositions() {
		if p.GetSymbol() == *symbol {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("position %s missing from account snapshot", *symbol)
	}
	fmt.Println("7 queries consistent (orders FILLED, trades, positions)")

	// 8) Heartbeat.
	hb, err := client.Heartbeat(ctx, &bg.HeartbeatRequest{})
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	if hb.GetSeqCursor() < att.GetSeqCursor() {
		return fmt.Errorf("heartbeat cursor %d went backwards from attach cursor %d", hb.GetSeqCursor(), att.GetSeqCursor())
	}
	fmt.Printf("8 heartbeat ok: cursor=%d\n", hb.GetSeqCursor())

	// 9) Cancel a resting buy above the ask.
	resp, err = client.SubmitOrder(ctx, &bg.SubmitOrderRequest{
		ClientOrderId: restCID,
		FundAccount:   acct.GetFundAccount(),
		TradeAccount:  tradeAcct.GetTradeAccount(),
		Market:        tradeAcct.GetMarket(),
		Symbol:        *symbol,
		Side:          bg.Side_SIDE_BUY,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        price + 2*int64(tick),
		Qty:           1,
	})
	if err != nil {
		return fmt.Errorf("submit rest buy: %w", err)
	}
	if !resp.GetAccepted() {
		return fmt.Errorf("rest buy rejected: %s", resp.GetRejectReason())
	}
	cresp, err := client.CancelOrder(ctx, &bg.CancelOrderRequest{ClientOrderId: restCID})
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	if !cresp.GetAccepted() {
		return fmt.Errorf("cancel rejected: %s", cresp.GetRejectReason())
	}
	if err := waitForStatus(coll, restCID, bg.OrderStatus_ORDER_STATUS_CANCELED, 8*time.Second); err != nil {
		return err
	}
	fmt.Println("9 cancel ok: rest order CANCELED")

	// 10) The gateway seq stream must be gap-free from the attach cursor.
	if err := coll.seqError(); err != nil {
		return fmt.Errorf("report stream not contiguous: %w", err)
	}
	if coll.seqCount() == 0 {
		return fmt.Errorf("no report events received")
	}
	fmt.Printf("10 reports ok: %d events, seq contiguous from %d\n", coll.seqCount(), att.GetSeqCursor()+1)

	// 11) Reconnect replay: subscribing from an older cursor must replay the
	// buffered window with contiguous seq numbers before going live again.
	from := coll.lastSeq() - 2
	if from < att.GetSeqCursor() {
		from = att.GetSeqCursor()
	}
	want := coll.lastSeq()
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	rs, err := client.SubscribeReports(rctx, &bg.SubscribeReportsRequest{FromSeq: from})
	if err != nil {
		return fmt.Errorf("resubscribe from %d: %w", from, err)
	}
	replayed := make([]int64, 0, int(want-from))
	for int64(len(replayed)) < want-from {
		ev, err := rs.Recv()
		if err != nil {
			return fmt.Errorf("replay from %d stopped early: %w", from, err)
		}
		replayed = append(replayed, ev.GetSeq())
	}
	for i, seq := range replayed {
		if expect := from + 1 + int64(i); seq != expect {
			return fmt.Errorf("replay seq gap: expected %d, got %d", expect, seq)
		}
	}
	fmt.Printf("11 replay ok: %d events replayed from seq %d\n", len(replayed), from+1)

	fmt.Println()
	fmt.Println("== ALL PASS: broker gateway end-to-end flow ==")
	return nil
}

func waitForStatus(coll *collector, cid string, want bg.OrderStatus, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		seen, rejected := coll.hasSeen(cid, want)
		if seen {
			return nil
		}
		if rejected {
			return fmt.Errorf("order %s was REJECTED, want %v", cid, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("order %s never reached %v (last seq=%d)", cid, want, coll.lastSeq())
}

func waitForTrade(coll *collector, cid string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if coll.fillCount(cid) > 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("no trade report for %s (last seq=%d)", cid, coll.lastSeq())
}

func describeAccounts(accs []*bg.GatewayAccount) string {
	parts := make([]string, 0, len(accs))
	for _, a := range accs {
		accounts := make([]string, 0, len(a.GetTradeAccounts()))
		for _, ta := range a.GetTradeAccounts() {
			accounts = append(accounts, fmt.Sprintf("%s@%s", ta.GetTradeAccount(), ta.GetMarket()))
		}
		parts = append(parts, fmt.Sprintf("%s:[%s]", a.GetFundAccount(), strings.Join(accounts, ",")))
	}
	return strings.Join(parts, ",")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}
