//go:build integration

package oms_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/viviwu/tradeforge/internal/auth"
	"github.com/viviwu/tradeforge/internal/domain"
	"github.com/viviwu/tradeforge/internal/eventbus"
	"github.com/viviwu/tradeforge/internal/matching"
	"github.com/viviwu/tradeforge/internal/oms"
	"github.com/viviwu/tradeforge/internal/store"
)

type fixture struct {
	ctx    context.Context
	st     *store.Postgres
	engine *matching.Engine
	bus    *eventbus.Bus
	oms    *oms.Service
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TRADEFORGE_TEST_DSN")
	if dsn == "" {
		t.Skip("TRADEFORGE_TEST_DSN not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx,
		`TRUNCATE audit_logs, trades, positions, orders, strategies, refresh_tokens, accounts, users CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(ctx, `ALTER SEQUENCE trade_seq_seq RESTART WITH 1`); err != nil {
		t.Fatal(err)
	}
	engine := matching.New([]string{"AAPL", "MSFT"})
	engine.Start()
	t.Cleanup(engine.Stop)
	bus := eventbus.New(1024)
	return &fixture{ctx: ctx, st: st, engine: engine, bus: bus, oms: oms.New(st, engine, bus, []string{"AAPL", "MSFT"})}
}

func (f *fixture) account(t *testing.T, cash int64) string {
	t.Helper()
	id := uuid.NewString()
	if err := f.st.EnsureAccount(f.ctx, id, cash); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) position(t *testing.T, accountID, symbol string, qty, avg int64) {
	t.Helper()
	if err := f.st.UpsertPosition(f.ctx, domain.Position{
		AccountID: accountID,
		Symbol:    symbol,
		Quantity:  qty,
		AvgPrice:  avg,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitCrossSettleAndIdempotency(t *testing.T) {
	f := setup(t)
	buyer := f.account(t, 10_000_000)
	seller := f.account(t, 10_000_000)
	f.position(t, seller, "AAPL", 100, 9_000)

	sell, err := f.oms.Submit(f.ctx, seller, oms.SubmitRequest{
		ClientOrderID: "sell-1",
		Symbol:        "AAPL",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         10_000,
		Quantity:      100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sell.Status != domain.OrderStatusAccepted {
		t.Fatalf("sell status = %s", sell.Status)
	}

	buy, err := f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "buy-1",
		Symbol:        "AAPL",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         10_000,
		Quantity:      100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if buy.Status != domain.OrderStatusFilled || buy.AvgPrice != 10_000 || buy.FilledQty != 100 {
		t.Fatalf("buy = %+v", buy)
	}

	buyerAccount, err := f.st.GetAccount(f.ctx, buyer)
	if err != nil {
		t.Fatal(err)
	}
	if buyerAccount.AvailableCash != 9_000_000 || buyerAccount.FrozenCash != 0 {
		t.Fatalf("buyer account = %+v", buyerAccount)
	}
	sellerAccount, err := f.st.GetAccount(f.ctx, seller)
	if err != nil {
		t.Fatal(err)
	}
	if sellerAccount.AvailableCash != 11_000_000 || sellerAccount.FrozenCash != 0 {
		t.Fatalf("seller account = %+v", sellerAccount)
	}
	buyerPosition, err := f.st.GetPosition(f.ctx, buyer, "AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if buyerPosition.Quantity != 100 || buyerPosition.AvgPrice != 10_000 || buyerPosition.FrozenQty != 0 {
		t.Fatalf("buyer position = %+v", buyerPosition)
	}
	sellerPosition, err := f.st.GetPosition(f.ctx, seller, "AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if sellerPosition.Quantity != 0 || sellerPosition.FrozenQty != 0 {
		t.Fatalf("seller position = %+v", sellerPosition)
	}

	trades, _, err := f.st.ListTrades(f.ctx, store.TradeFilter{AccountID: buyer})
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].Quantity != 100 || trades[0].Price != 10_000 {
		t.Fatalf("buyer trades = %+v", trades)
	}

	sellAgain, err := f.oms.Submit(f.ctx, seller, oms.SubmitRequest{
		ClientOrderID: "sell-1",
		Symbol:        "AAPL",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		Price:         10_000,
		Quantity:      100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sellAgain.ID != sell.ID {
		t.Fatalf("idempotency returned %s want %s", sellAgain.ID, sell.ID)
	}
	if trades, _, _ := f.st.ListTrades(f.ctx, store.TradeFilter{AccountID: seller}); len(trades) != 1 {
		t.Fatalf("duplicate submit created trades: %+v", trades)
	}

	if mismatches, err := f.st.ReconcileOrderFills(f.ctx); err != nil {
		t.Fatal(err)
	} else if len(mismatches) != 0 {
		t.Fatalf("order fill mismatches: %+v", mismatches)
	}
}

func TestMarketBuyBudget(t *testing.T) {
	f := setup(t)
	buyer := f.account(t, 1_500_000)
	seller := f.account(t, 10_000_000)
	f.position(t, seller, "AAPL", 200, 9_000)

	if _, err := f.oms.Submit(f.ctx, seller, oms.SubmitRequest{
		ClientOrderID: "marker-ask",
		Symbol:        "AAPL",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		Price:         10_000,
		Quantity:      100,
	}); err != nil {
		t.Fatal(err)
	}

	oversized, err := f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "market-too-big",
		Symbol:        "AAPL",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeMarket,
		Quantity:      300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if oversized.Status != domain.OrderStatusRejected {
		t.Fatalf("oversized market buy = %+v", oversized)
	}
	if account, _ := f.st.GetAccount(f.ctx, buyer); account.FrozenCash != 0 || account.AvailableCash != 1_500_000 {
		t.Fatalf("rejected market buy mutated funds: %+v", account)
	}
	if oversized.Price != 3_000_000 {
		t.Fatalf("budget not recorded: %d", oversized.Price)
	}

	filled, err := f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "market-fill",
		Symbol:        "AAPL",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeMarket,
		Quantity:      100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filled.Status != domain.OrderStatusFilled || filled.AvgPrice != 10_000 {
		t.Fatalf("market fill = %+v", filled)
	}
	account, _ := f.st.GetAccount(f.ctx, buyer)
	if account.FrozenCash != 0 || account.AvailableCash != 500_000 {
		t.Fatalf("buyer funds after market fill = %+v", account)
	}
	position, err := f.st.GetPosition(f.ctx, buyer, "AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if position.Quantity != 100 || position.AvgPrice != 10_000 {
		t.Fatalf("buyer position = %+v", position)
	}

	noLiquidity, err := f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "market-empty-book",
		Symbol:        "AAPL",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeMarket,
		Quantity:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if noLiquidity.Status != domain.OrderStatusRejected {
		t.Fatalf("empty book market buy = %+v", noLiquidity)
	}
	if account, _ := f.st.GetAccount(f.ctx, buyer); account.FrozenCash != 0 || account.AvailableCash != 500_000 {
		t.Fatalf("empty book market buy mutated funds: %+v", account)
	}

	if mismatches, err := f.st.ReconcileOrderFills(f.ctx); err != nil {
		t.Fatal(err)
	} else if len(mismatches) != 0 {
		t.Fatalf("mismatches: %+v", mismatches)
	}
}

func TestRecoverRestoresRestingOrders(t *testing.T) {
	f := setup(t)
	buyer := f.account(t, 10_000_000)
	seller := f.account(t, 10_000_000)
	f.position(t, seller, "AAPL", 10, 9_000)

	if _, err := f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "resting-buy",
		Symbol:        "AAPL",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		Price:         9_000,
		Quantity:      10,
	}); err != nil {
		t.Fatal(err)
	}

	f.engine.Stop()
	restarted := matching.New([]string{"AAPL", "MSFT"})
	restarted.Start()
	t.Cleanup(restarted.Stop)
	f.engine = restarted
	f.oms = oms.New(f.st, restarted, f.bus, []string{"AAPL", "MSFT"})
	recovered, err := f.oms.Recover(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d", recovered)
	}

	sell, err := f.oms.Submit(f.ctx, seller, oms.SubmitRequest{
		ClientOrderID: "sell-after-restart",
		Symbol:        "AAPL",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		Price:         9_000,
		Quantity:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sell.Status != domain.OrderStatusFilled {
		t.Fatalf("sell = %+v", sell)
	}
	position, err := f.st.GetPosition(f.ctx, buyer, "AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if position.Quantity != 10 {
		t.Fatalf("buyer position = %+v", position)
	}
	if account, _ := f.st.GetAccount(f.ctx, buyer); account.FrozenCash != 0 {
		t.Fatalf("buyer frozen cash = %d", account.FrozenCash)
	}
}

func TestCancelReleasesFunds(t *testing.T) {
	f := setup(t)
	buyer := f.account(t, 10_000_000)

	order, err := f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "buy-rest",
		Symbol:        "AAPL",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		Price:         9_000,
		Quantity:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != domain.OrderStatusAccepted {
		t.Fatalf("status = %s", order.Status)
	}
	account, _ := f.st.GetAccount(f.ctx, buyer)
	if account.FrozenCash != 90_000 || account.AvailableCash != 9_910_000 {
		t.Fatalf("frozen funds = %+v", account)
	}

	canceled, err := f.oms.Cancel(f.ctx, buyer, order.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.OrderStatusCanceled {
		t.Fatalf("canceled = %+v", canceled)
	}
	account, _ = f.st.GetAccount(f.ctx, buyer)
	if account.FrozenCash != 0 || account.AvailableCash != 10_000_000 {
		t.Fatalf("released funds = %+v", account)
	}

	again, err := f.oms.Cancel(f.ctx, buyer, order.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != domain.OrderStatusCanceled {
		t.Fatalf("repeat cancel = %+v", again)
	}
}

func TestRejections(t *testing.T) {
	f := setup(t)
	buyer := f.account(t, 1_000)
	seller := f.account(t, 1_000)

	insufficient, err := f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "too-big",
		Symbol:        "AAPL",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		Price:         10_000,
		Quantity:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if insufficient.Status != domain.OrderStatusRejected {
		t.Fatalf("status = %s", insufficient.Status)
	}
	if account, _ := f.st.GetAccount(f.ctx, buyer); account.FrozenCash != 0 || account.AvailableCash != 1_000 {
		t.Fatalf("rejected order mutated funds: %+v", account)
	}

	noPosition, err := f.oms.Submit(f.ctx, seller, oms.SubmitRequest{
		ClientOrderID: "no-pos",
		Symbol:        "AAPL",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		Price:         10_000,
		Quantity:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if noPosition.Status != domain.OrderStatusRejected {
		t.Fatalf("status = %s", noPosition.Status)
	}

	_, err = f.oms.Submit(f.ctx, buyer, oms.SubmitRequest{
		ClientOrderID: "bad-symbol",
		Symbol:        "NOPE",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		Price:         100,
		Quantity:      1,
	})
	if !errors.Is(err, oms.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthRefreshRotation(t *testing.T) {
	f := setup(t)
	hash, err := auth.HashPassword("secret", 4)
	if err != nil {
		t.Fatal(err)
	}
	user := domain.User{
		ID:           uuid.NewString(),
		Username:     "alice",
		PasswordHash: hash,
		Role:         "viewer",
		CreatedAt:    time.Now().UTC(),
	}
	if err := f.st.CreateUser(f.ctx, user); err != nil {
		t.Fatal(err)
	}
	manager := auth.NewTokenManager("test-secret", time.Minute)
	service := auth.NewService(f.st, manager, time.Hour, 4, 100, 100)

	login, err := service.Login(f.ctx, "alice", "secret", "test")
	if err != nil {
		t.Fatal(err)
	}
	if login.AccessToken == "" || login.RefreshToken == "" {
		t.Fatal("empty tokens")
	}
	claims, err := service.Authenticate(f.ctx, login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != user.ID {
		t.Fatalf("claims = %+v", claims)
	}

	refreshed, err := service.Refresh(f.ctx, login.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.RefreshToken == login.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := service.Refresh(f.ctx, login.RefreshToken); !errors.Is(err, auth.ErrTokenRevoked) {
		t.Fatalf("replayed refresh token err = %v", err)
	}
}
