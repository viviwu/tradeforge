// bgw-cli is an interactive command-line client for the frozen
// broker-gateway.v1 contract. It is used to manually verify a C++ adapter
// process against a real broker simulation: Attach -> queries -> submit ->
// terminal reports -> cancel.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bg "github.com/viviwu/tradeforge/gen/brokergateway/v1"
)

var addr = flag.String("addr", envOr("BGW_CLI_ADDR", "127.0.0.1:50053"), "broker gateway gRPC address")

type cli struct {
	conn   *grpc.ClientConn
	client bg.BrokerGatewayServiceClient
	ctx    context.Context

	mu        sync.Mutex
	stream    bg.BrokerGatewayService_SubscribeReportsClient
	streamCxl context.CancelFunc
	fundAccount *bg.GatewayAccount
	trade     *bg.GatewayTradeAccount
	cursor    int64
	seq       int64
}

func main() {
	flag.Parse()
	c, err := newCLI(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer c.close()
	c.run()
}

func newCLI(addr string) (*cli, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &cli{
		conn:   conn,
		client: bg.NewBrokerGatewayServiceClient(conn),
		ctx:    context.Background(),
	}, nil
}

func (c *cli) close() {
	c.stopStream()
	_ = c.conn.Close()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *cli) run() {
	in := bufio.NewScanner(os.Stdin)
	fmt.Printf("broker-gateway CLI -> %s\n", *addr)
	for {
		fmt.Print("\nbgw> ")
		if !in.Scan() {
			return
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if err := c.dispatch(parts, in); err != nil {
			fmt.Printf("ERROR: %v\n", err)
		}
	}
}

func (c *cli) dispatch(parts []string, in *bufio.Scanner) error {
	ctx, cancel := context.WithTimeout(c.ctx, 45*time.Second)
	defer cancel()

	switch parts[0] {
	case "1", "attach", "login":
		return c.attach(ctx)
	case "2", "sub", "subscribe":
		return c.subscribe()
	case "3", "stopsub":
		c.stopStream()
		fmt.Println("report stream stopped")
		return nil
	case "4", "security":
		return c.security(ctx, in)
	case "5", "orders":
		return c.orders(ctx, in)
	case "6", "trades":
		return c.trades(ctx, in)
	case "7", "account":
		return c.account(ctx)
	case "8", "submit":
		return c.submit(ctx, in)
	case "9", "cancel":
		return c.cancel(ctx, in)
	case "10", "heartbeat":
		return c.heartbeat(ctx)
	case "11", "wait":
		sec := 2
		if len(parts) > 1 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				sec = n
			}
		}
		time.Sleep(time.Duration(sec) * time.Second)
		fmt.Printf("waited %ds\n", sec)
		return nil
	case "0", "quit", "exit":
		os.Exit(0)
	case "help", "h":
		c.help()
		return nil
	default:
		c.help()
		return nil
	}
	return nil
}

func (c *cli) help() {
	fmt.Println(" 1 attach/login     建立会话，获取账户清单与 seq 游标")
	fmt.Println(" 2 sub/subscribe     订阅回报流（后台打印）")
	fmt.Println(" 3 stopsub           停止订阅")
	fmt.Println(" 4 security          查询证券档案")
	fmt.Println(" 5 orders            查询当日委托")
	fmt.Println(" 6 trades            查询当日成交")
	fmt.Println(" 7 account           查询资金与持仓")
	fmt.Println(" 8 submit            下限价委托（买/卖）")
	fmt.Println(" 9 cancel            按 client_order_id 撤单")
	fmt.Println("10 heartbeat         心跳与游标")
	fmt.Println("11 wait N            等待 N 秒（脚本联调用）")
	fmt.Println(" 0 quit              退出")
}

func prompt(in *bufio.Scanner, label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	if !in.Scan() {
		return def
	}
	v := strings.TrimSpace(in.Text())
	if v == "" {
		return def
	}
	return v
}

func (c *cli) attach(ctx context.Context) error {
	resp, err := c.client.Attach(ctx, &bg.AttachRequest{ClientVersion: "bgw-cli/1.0"})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.cursor = resp.GetSeqCursor()
	for _, a := range resp.GetAccounts() {
		if len(a.GetTradeAccounts()) > 0 {
			c.fundAccount = a
			c.trade = a.GetTradeAccounts()[0]
			break
		}
	}
	c.mu.Unlock()
	fmt.Printf("Attach OK: server=%s cursor=%d\n", resp.GetServerVersion(), resp.GetSeqCursor())
	if len(resp.GetAccounts()) == 0 {
		fmt.Println("  (no accounts returned)")
	}
	for _, a := range resp.GetAccounts() {
		fmt.Printf("  fund=%s\n", a.GetFundAccount())
		for _, t := range a.GetTradeAccounts() {
			fmt.Printf("    trade=%s market=%s\n", t.GetTradeAccount(), t.GetMarket())
		}
	}
	return nil
}

func (c *cli) requireAccount() (*bg.GatewayAccount, *bg.GatewayTradeAccount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fundAccount == nil || c.trade == nil {
		return nil, nil, fmt.Errorf("please run attach/login first")
	}
	return c.fundAccount, c.trade, nil
}

func (c *cli) subscribe() error {
	c.stopStream()
	acc, _, err := c.requireAccount()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(c.ctx)
	stream, err := c.client.SubscribeReports(ctx, &bg.SubscribeReportsRequest{
		FromSeq: c.cursor, FundAccount: acc.GetFundAccount(),
	})
	if err != nil {
		cancel()
		return err
	}
	c.mu.Lock()
	c.stream, c.streamCxl = stream, cancel
	c.mu.Unlock()
	go func() {
		for {
			ev, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					fmt.Printf("[reports] ended: %v\n", err)
				}
				return
			}
			c.mu.Lock()
			c.seq = ev.GetSeq()
			c.mu.Unlock()
			switch p := ev.GetPayload().(type) {
			case *bg.ReportEvent_Order:
				o := p.Order
				fmt.Printf("[report #%d] ORDER %s %s %s filled=%d/%d\n",
					ev.GetSeq(), o.GetClientOrderId(), o.GetSymbol(), o.GetStatus(),
					o.GetFilledQty(), o.GetQty())
			case *bg.ReportEvent_Trade:
				t := p.Trade
				fmt.Printf("[report #%d] TRADE %s %s price=%d qty=%d\n",
					ev.GetSeq(), t.GetClientOrderId(), t.GetTradeId(), t.GetPriceU(), t.GetQty())
			case *bg.ReportEvent_Account:
				fmt.Printf("[report #%d] ACCOUNT %s\n", ev.GetSeq(), p.Account.GetFundAccount())
			}
		}
	}()
	fmt.Printf("Subscribed reports from seq=%d\n", c.cursor)
	return nil
}

func (c *cli) stopStream() {
	c.mu.Lock()
	cancel := c.streamCxl
	c.stream = nil
	c.streamCxl = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *cli) security(ctx context.Context, in *bufio.Scanner) error {
	_, trade, err := c.requireAccount()
	if err != nil {
		return err
	}
	symbol := prompt(in, "symbol", "600000")
	market := prompt(in, "market(SH/SZ)", trade.GetMarket())
	req := &bg.QuerySecurityInfoRequest{TradeAccount: trade.GetTradeAccount(), Market: market, Symbol: symbol}
	resp, err := c.client.QuerySecurityInfo(ctx, req)
	if err != nil {
		return err
	}
	i := resp.GetInfo()
	fmt.Printf("Security %s@%s: status=%s tick=%d lot=%d up=%d down=%d ext=%s\n",
		i.GetSymbol(), i.GetMarket(), i.GetTradingStatus(), i.GetPriceTickU(),
		i.GetLotSize(), i.GetLimitUpU(), i.GetLimitDownU(), i.GetExtJson())
	return nil
}

func (c *cli) orders(ctx context.Context, in *bufio.Scanner) error {
	acc, _, err := c.requireAccount()
	if err != nil {
		return err
	}
	cid := prompt(in, "client_order_id(blank=all)", "")
	resp, err := c.client.QueryOrders(ctx, &bg.QueryOrdersRequest{
		Filter: &bg.QueryFilter{FundAccount: acc.GetFundAccount(), ClientOrderId: cid, PageSize: 50},
	})
	if err != nil {
		return err
	}
	for _, o := range resp.GetOrders() {
		fmt.Printf("ORDER %s ex=%s %s@%s %s %s price=%d qty=%d filled=%d status=%s reject=%s ext=%s\n",
			o.GetClientOrderId(), o.GetExchangeOrderId(), o.GetSymbol(), o.GetMarket(),
			o.GetSide(), o.GetOrderType(), o.GetPriceU(), o.GetQty(), o.GetFilledQty(),
			o.GetStatus(), o.GetRejectReason(), o.GetExtJson())
	}
	fmt.Printf("  total=%d is_last=%v\n", len(resp.GetOrders()), resp.GetIsLast())
	return nil
}

func (c *cli) trades(ctx context.Context, in *bufio.Scanner) error {
	acc, _, err := c.requireAccount()
	if err != nil {
		return err
	}
	cid := prompt(in, "client_order_id(blank=all)", "")
	resp, err := c.client.QueryTrades(ctx, &bg.QueryTradesRequest{
		Filter: &bg.QueryFilter{FundAccount: acc.GetFundAccount(), ClientOrderId: cid, PageSize: 50},
	})
	if err != nil {
		return err
	}
	for _, t := range resp.GetTrades() {
		fmt.Printf("TRADE %s cid=%s ex=%s %s@%s price=%d qty=%d amt=%d ext=%s\n",
			t.GetTradeId(), t.GetClientOrderId(), t.GetExchangeOrderId(), t.GetSymbol(),
			t.GetMarket(), t.GetPriceU(), t.GetQty(), t.GetAmountU(), t.GetExtJson())
	}
	fmt.Printf("  total=%d is_last=%v\n", len(resp.GetTrades()), resp.GetIsLast())
	return nil
}

func (c *cli) account(ctx context.Context) error {
	acc, _, err := c.requireAccount()
	if err != nil {
		return err
	}
	resp, err := c.client.QueryAccount(ctx, &bg.QueryAccountRequest{FundAccount: acc.GetFundAccount()})
	if err != nil {
		return err
	}
	s := resp.GetSnapshot()
	fmt.Printf("Account %s: available=%d frozen=%d\n", s.GetFundAccount(), s.GetAvailableCashU(), s.GetFrozenCashU())
	for _, p := range s.GetPositions() {
		fmt.Printf("  POS %s@%s qty=%d avail=%d cost=%d\n",
			p.GetSymbol(), p.GetMarket(), p.GetQty(), p.GetAvailableQty(), p.GetAvgCostU())
	}
	return nil
}

func (c *cli) submit(ctx context.Context, in *bufio.Scanner) error {
	acc, trade, err := c.requireAccount()
	if err != nil {
		return err
	}
	symbol := prompt(in, "symbol", "600000")
	market := prompt(in, "market(SH/SZ)", trade.GetMarket())
	side := prompt(in, "side(BUY/SELL)", "BUY")
	price := prompt(in, "price_u(1e-4 yuan)", "10000")
	qty := prompt(in, "qty", "100")
	cid := prompt(in, "client_order_id", fmt.Sprintf("cli-%d", time.Now().UnixNano()))

	p, err := strconv.ParseInt(price, 10, 64)
	if err != nil {
		return err
	}
	q, err := strconv.ParseInt(qty, 10, 64)
	if err != nil {
		return err
	}
	s := bg.Side_SIDE_BUY
	if strings.EqualFold(side, "SELL") || side == "2" {
		s = bg.Side_SIDE_SELL
	}
	resp, err := c.client.SubmitOrder(ctx, &bg.SubmitOrderRequest{
		RequestId:     time.Now().UnixNano(),
		ClientOrderId: cid,
		FundAccount:   acc.GetFundAccount(),
		TradeAccount:  trade.GetTradeAccount(),
		Market:        market,
		Symbol:        symbol,
		Side:          s,
		OrderType:     bg.OrderType_ORDER_TYPE_LIMIT,
		PriceU:        p,
		Qty:           q,
		Tif:           bg.TimeInForce_TIME_IN_FORCE_DAY,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Submit ack: accepted=%v status=%s cid=%s reject=%s\n",
		resp.GetAccepted(), resp.GetStatus(), resp.GetClientOrderId(), resp.GetRejectReason())
	return nil
}

func (c *cli) cancel(ctx context.Context, in *bufio.Scanner) error {
	cid := prompt(in, "client_order_id", "")
	if cid == "" {
		return fmt.Errorf("client_order_id required")
	}
	resp, err := c.client.CancelOrder(ctx, &bg.CancelOrderRequest{
		RequestId: time.Now().UnixNano(), ClientOrderId: cid,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Cancel ack: accepted=%v status=%s reject=%s\n",
		resp.GetAccepted(), resp.GetStatus(), resp.GetRejectReason())
	return nil
}

func (c *cli) heartbeat(ctx context.Context) error {
	resp, err := c.client.Heartbeat(ctx, &bg.HeartbeatRequest{RequestId: time.Now().UnixNano()})
	if err != nil {
		return err
	}
	fmt.Printf("Heartbeat: cursor=%d server_time=%d\n", resp.GetSeqCursor(), resp.GetServerTimeUnixNano())
	return nil
}
