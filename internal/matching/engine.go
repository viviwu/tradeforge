package matching

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/viviwu/tradeforge/internal/domain"
)

var (
	ErrSymbolNotAllowed = errors.New("symbol not allowed")
	ErrInvalidOrder     = errors.New("invalid order")
)

type Result struct {
	OrderID      string
	Status       domain.OrderStatus
	FilledQty    int64
	AvgPrice     int64
	CanceledQty  int64
	RejectReason string
	Fills        []domain.Fill
}

type command struct {
	kind     string
	order    domain.Order
	reply    chan Result
	replyErr chan error
}

type Engine struct {
	cmds    chan command
	done    chan struct{}
	once    sync.Once
	books   map[string]*book
	symbols map[string]struct{}
}

func New(symbols []string) *Engine {
	e := &Engine{
		cmds:    make(chan command, 1024),
		done:    make(chan struct{}),
		books:   make(map[string]*book),
		symbols: make(map[string]struct{}, len(symbols)),
	}
	for _, s := range symbols {
		e.symbols[s] = struct{}{}
		e.books[s] = newBook()
	}
	return e
}

func (e *Engine) Start() {
	go e.run()
}

func (e *Engine) Stop() {
	e.once.Do(func() { close(e.done) })
}

func (e *Engine) run() {
	for {
		select {
		case <-e.done:
			return
		case cmd := <-e.cmds:
			switch cmd.kind {
			case "submit":
				cmd.reply <- e.processSubmit(cmd.order)
			case "cancel":
				cmd.reply <- e.processCancel(cmd.order.ID)
			case "restore":
				cmd.replyErr <- e.processRestore(cmd.order)
			}
		}
	}
}

func (e *Engine) Submit(ctx context.Context, order domain.Order) (Result, error) {
	if err := e.validate(order); err != nil {
		return Result{}, err
	}
	reply := make(chan Result, 1)
	select {
	case e.cmds <- command{kind: "submit", order: order, reply: reply}:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-e.done:
		return Result{}, context.Canceled
	}
	select {
	case result := <-reply:
		return result, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (e *Engine) Restore(ctx context.Context, order domain.Order) error {
	if order.Type != domain.OrderTypeLimit || order.TimeInForce != domain.TimeInForceGTC {
		return fmt.Errorf("%w: only GTC limit orders can be restored", ErrInvalidOrder)
	}
	if err := e.validate(order); err != nil {
		return err
	}
	reply := make(chan error, 1)
	select {
	case e.cmds <- command{kind: "restore", order: order, replyErr: reply}:
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return context.Canceled
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) Cancel(ctx context.Context, orderID string) (Result, error) {
	reply := make(chan Result, 1)
	select {
	case e.cmds <- command{kind: "cancel", order: domain.Order{ID: orderID}, reply: reply}:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-e.done:
		return Result{}, context.Canceled
	}
	select {
	case result := <-reply:
		return result, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (e *Engine) validate(order domain.Order) error {
	if _, ok := e.symbols[order.Symbol]; !ok {
		return fmt.Errorf("%w: %s", ErrSymbolNotAllowed, order.Symbol)
	}
	if order.Quantity <= 0 {
		return fmt.Errorf("%w: quantity must be positive", ErrInvalidOrder)
	}
	if order.Side != domain.SideBuy && order.Side != domain.SideSell {
		return fmt.Errorf("%w: unknown side", ErrInvalidOrder)
	}
	if order.Type != domain.OrderTypeLimit && order.Type != domain.OrderTypeMarket {
		return fmt.Errorf("%w: unknown order type", ErrInvalidOrder)
	}
	if order.Type == domain.OrderTypeLimit && order.Price <= 0 {
		return fmt.Errorf("%w: limit price must be positive", ErrInvalidOrder)
	}
	return nil
}

func (e *Engine) processSubmit(order domain.Order) Result {
	b := e.books[order.Symbol]
	if b == nil {
		return Result{OrderID: order.ID, Status: domain.OrderStatusRejected, RejectReason: "symbol not allowed"}
	}
	if order.TimeInForce == domain.TimeInForceFOK && !canFill(b, order) {
		return Result{OrderID: order.ID, Status: domain.OrderStatusRejected, RejectReason: "insufficient liquidity for FOK"}
	}

	remaining := order.Remaining()
	var (
		fills    []domain.Fill
		filled   int64
		notional int64
	)
	for remaining > 0 {
		maker := bestOpposite(b, order.Side)
		if maker == nil {
			break
		}
		if order.Type == domain.OrderTypeLimit {
			if order.Side == domain.SideBuy && maker.order.Price > order.Price {
				break
			}
			if order.Side == domain.SideSell && maker.order.Price < order.Price {
				break
			}
		}
		qty := min64(remaining, maker.remaining)
		fills = append(fills, domain.Fill{
			MatchID:      uuid.NewString(),
			MakerOrderID: maker.order.ID,
			TakerOrderID: order.ID,
			MakerAccount: maker.order.AccountID,
			TakerAccount: order.AccountID,
			Symbol:       order.Symbol,
			MakerSide:    maker.order.Side,
			TakerSide:    order.Side,
			Price:        maker.order.Price,
			Quantity:     qty,
		})
		filled += qty
		notional += qty * maker.order.Price
		remaining -= qty
		maker.remaining -= qty
		maker.order.FilledQty += qty
		maker.order.AvgPrice = weightedAvg(maker.order.AvgPrice, maker.order.FilledQty-qty, maker.order.Price, qty)
		if maker.remaining == 0 {
			b.remove(maker)
		}
	}

	result := Result{OrderID: order.ID, Fills: fills, FilledQty: filled}
	if filled > 0 {
		result.AvgPrice = notional / filled
	}
	if remaining == 0 {
		result.Status = domain.OrderStatusFilled
		return result
	}

	switch {
	case order.Type == domain.OrderTypeLimit && order.TimeInForce == domain.TimeInForceGTC:
		order.FilledQty = filled
		order.AvgPrice = result.AvgPrice
		b.add(order)
		if filled > 0 {
			result.Status = domain.OrderStatusPartiallyFilled
		} else {
			result.Status = domain.OrderStatusAccepted
		}
	case order.Type == domain.OrderTypeMarket:
		result.CanceledQty = remaining
		if filled == 0 {
			result.Status = domain.OrderStatusRejected
			result.RejectReason = "no liquidity"
		} else {
			result.Status = domain.OrderStatusCanceled
			result.RejectReason = "insufficient liquidity"
		}
	case order.TimeInForce == domain.TimeInForceFOK:
		result.CanceledQty = remaining
		result.Status = domain.OrderStatusRejected
		result.RejectReason = "insufficient liquidity for FOK"
	default:
		result.CanceledQty = remaining
		if filled == 0 {
			result.Status = domain.OrderStatusCanceled
			result.RejectReason = "no liquidity"
		} else {
			result.Status = domain.OrderStatusCanceled
			result.RejectReason = "insufficient liquidity"
		}
	}
	return result
}

func (e *Engine) processRestore(order domain.Order) error {
	b := e.books[order.Symbol]
	if b == nil {
		return fmt.Errorf("%w: %s", ErrSymbolNotAllowed, order.Symbol)
	}
	if order.Remaining() <= 0 {
		return nil
	}
	b.add(order)
	return nil
}

func (e *Engine) processCancel(orderID string) Result {
	for _, b := range e.books {
		bo := b.cancel(orderID)
		if bo != nil {
			return Result{
				OrderID:     orderID,
				Status:      domain.OrderStatusCanceled,
				FilledQty:   bo.order.FilledQty,
				CanceledQty: bo.remaining,
			}
		}
	}
	return Result{}
}

func canFill(b *book, order domain.Order) bool {
	need := order.Quantity
	if order.Side == domain.SideBuy {
		for _, maker := range b.asks {
			if order.Type == domain.OrderTypeLimit && maker.order.Price > order.Price {
				break
			}
			need -= maker.remaining
			if need <= 0 {
				return true
			}
		}
		return false
	}
	for _, maker := range b.bids {
		if order.Type == domain.OrderTypeLimit && maker.order.Price < order.Price {
			break
		}
		need -= maker.remaining
		if need <= 0 {
			return true
		}
	}
	return false
}

func bestOpposite(b *book, side domain.Side) *bookOrder {
	if side == domain.SideBuy {
		return b.bestAsk()
	}
	return b.bestBid()
}

func weightedAvg(oldAvg, oldQty, price, qty int64) int64 {
	if oldQty+qty <= 0 {
		return 0
	}
	return (oldAvg*oldQty + price*qty) / (oldQty + qty)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
