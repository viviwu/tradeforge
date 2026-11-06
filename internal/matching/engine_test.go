package matching

import (
	"context"
	"testing"

	"github.com/viviwu/tradeforge/internal/domain"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	e := New([]string{"AAPL"})
	e.Start()
	t.Cleanup(e.Stop)
	return e
}

func order(id string, side domain.Side, price, qty int64) domain.Order {
	return domain.Order{
		ID:          id,
		AccountID:   "acct-" + id,
		Symbol:      "AAPL",
		Side:        side,
		Type:        domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC,
		Price:       price,
		Quantity:    qty,
	}
}

func TestLimitCrossFillsBothSides(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()

	sell, err := e.Submit(ctx, order("sell-1", domain.SideSell, 10_000, 100))
	if err != nil {
		t.Fatal(err)
	}
	if sell.Status != domain.OrderStatusAccepted {
		t.Fatalf("sell status = %s", sell.Status)
	}

	buy, err := e.Submit(ctx, order("buy-1", domain.SideBuy, 10_000, 100))
	if err != nil {
		t.Fatal(err)
	}
	if buy.Status != domain.OrderStatusFilled {
		t.Fatalf("buy status = %s", buy.Status)
	}
	if len(buy.Fills) != 1 {
		t.Fatalf("fills = %d", len(buy.Fills))
	}
	if got := buy.Fills[0]; got.Price != 10_000 || got.Quantity != 100 || got.MakerOrderID != "sell-1" {
		t.Fatalf("unexpected fill: %+v", got)
	}

	resting, err := e.Submit(ctx, order("buy-2", domain.SideBuy, 10_000, 1))
	if err != nil {
		t.Fatal(err)
	}
	if resting.Status != domain.OrderStatusAccepted {
		t.Fatalf("empty book buy status = %s", resting.Status)
	}
}

func TestPartialFillLeavesRemainder(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()

	if _, err := e.Submit(ctx, order("sell-1", domain.SideSell, 10_000, 30)); err != nil {
		t.Fatal(err)
	}
	buy, err := e.Submit(ctx, order("buy-1", domain.SideBuy, 10_000, 100))
	if err != nil {
		t.Fatal(err)
	}
	if buy.Status != domain.OrderStatusPartiallyFilled {
		t.Fatalf("status = %s", buy.Status)
	}
	if buy.FilledQty != 30 {
		t.Fatalf("filled = %d", buy.FilledQty)
	}

	sell, err := e.Submit(ctx, order("sell-2", domain.SideSell, 9_000, 70))
	if err != nil {
		t.Fatal(err)
	}
	if sell.Status != domain.OrderStatusFilled || sell.FilledQty != 70 {
		t.Fatalf("remainder fill = %+v", sell)
	}
}

func TestMarketOrderIOC(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()

	if _, err := e.Submit(ctx, order("sell-1", domain.SideSell, 10_000, 40)); err != nil {
		t.Fatal(err)
	}
	market := order("buy-1", domain.SideBuy, 0, 100)
	market.Type = domain.OrderTypeMarket
	market.TimeInForce = domain.TimeInForceIOC
	result, err := e.Submit(ctx, market)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.OrderStatusCanceled {
		t.Fatalf("status = %s", result.Status)
	}
	if result.FilledQty != 40 || result.CanceledQty != 60 {
		t.Fatalf("filled=%d canceled=%d", result.FilledQty, result.CanceledQty)
	}
}

func TestFOKRejectedWithoutLiquidity(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()

	if _, err := e.Submit(ctx, order("sell-1", domain.SideSell, 10_000, 40)); err != nil {
		t.Fatal(err)
	}
	fok := order("buy-1", domain.SideBuy, 10_000, 100)
	fok.TimeInForce = domain.TimeInForceFOK
	result, err := e.Submit(ctx, fok)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.OrderStatusRejected {
		t.Fatalf("status = %s", result.Status)
	}
}

func TestCancelRemovesLiquidity(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()

	if _, err := e.Submit(ctx, order("sell-1", domain.SideSell, 10_000, 100)); err != nil {
		t.Fatal(err)
	}
	result, err := e.Cancel(ctx, "sell-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.OrderStatusCanceled || result.CanceledQty != 100 {
		t.Fatalf("cancel = %+v", result)
	}
	buy, err := e.Submit(ctx, order("buy-1", domain.SideBuy, 10_000, 100))
	if err != nil {
		t.Fatal(err)
	}
	if buy.Status != domain.OrderStatusAccepted {
		t.Fatalf("status = %s", buy.Status)
	}
}
