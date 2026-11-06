package oms

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/viviwu/tradeforge/internal/domain"
	"github.com/viviwu/tradeforge/internal/eventbus"
	"github.com/viviwu/tradeforge/internal/matching"
	"github.com/viviwu/tradeforge/internal/store"
)

var (
	ErrAccountNotFound      = errors.New("account not found")
	ErrInsufficientFunds    = errors.New("insufficient funds")
	ErrInsufficientPosition = errors.New("insufficient position")
	ErrOrderNotFound        = errors.New("order not found")
	ErrStrategyNotFound     = errors.New("strategy not found")
	ErrInvalidRequest       = errors.New("invalid request")
)

type SubmitRequest struct {
	ClientOrderID string
	Symbol        string
	Side          domain.Side
	Type          domain.OrderType
	TimeInForce   domain.TimeInForce
	Price         int64
	Quantity      int64
}

type Service struct {
	store   store.Store
	engine  *matching.Engine
	bus     *eventbus.Bus
	symbols map[string]struct{}
	now     func() time.Time
	mu      sync.Mutex
}

func New(st store.Store, engine *matching.Engine, bus *eventbus.Bus, symbols []string) *Service {
	s := &Service{
		store:   st,
		engine:  engine,
		bus:     bus,
		symbols: make(map[string]struct{}, len(symbols)),
		now:     func() time.Time { return time.Now().UTC() },
	}
	for _, sym := range symbols {
		s.symbols[sym] = struct{}{}
	}
	return s
}

func (s *Service) Submit(ctx context.Context, accountID string, req SubmitRequest) (domain.Order, error) {
	if req.TimeInForce == "" {
		if req.Type == domain.OrderTypeMarket {
			req.TimeInForce = domain.TimeInForceIOC
		} else {
			req.TimeInForce = domain.TimeInForceGTC
		}
	}
	if err := s.validate(req); err != nil {
		return domain.Order{}, err
	}

	if existing, err := s.store.GetOrderByClientID(ctx, accountID, req.ClientOrderID); err == nil {
		return existing, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return domain.Order{}, err
	}

	now := s.now()
	order := domain.Order{
		ID:            uuid.NewString(),
		ClientOrderID: req.ClientOrderID,
		AccountID:     accountID,
		Symbol:        req.Symbol,
		Side:          req.Side,
		Type:          req.Type,
		TimeInForce:   req.TimeInForce,
		Price:         req.Price,
		Quantity:      req.Quantity,
		Status:        domain.OrderStatusPendingNew,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	rejected, reason, err := s.createAndFreeze(ctx, order)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return s.store.GetOrderByClientID(ctx, accountID, req.ClientOrderID)
		}
		return domain.Order{}, err
	}
	if rejected {
		order.Status = domain.OrderStatusRejected
		order.RejectReason = reason
		s.publishOrder(order)
		return order, nil
	}

	s.mu.Lock()
	result, err := s.engine.Submit(ctx, order)
	if err != nil {
		rejectedOrder, rejectErr := s.rejectOrder(ctx, order, err.Error())
		s.mu.Unlock()
		if rejectErr != nil {
			return domain.Order{}, rejectErr
		}
		s.publishOrder(rejectedOrder)
		return rejectedOrder, nil
	}

	updatedOrders, trades, positions, err := s.applyResult(ctx, order, result)
	s.mu.Unlock()
	if err != nil {
		return domain.Order{}, err
	}
	for _, tr := range trades {
		s.publishTrade(tr)
	}
	for _, o := range updatedOrders {
		s.publishOrder(o)
	}
	for _, p := range positions {
		s.publishPosition(p)
	}
	return updatedOrders[order.ID], nil
}

func (s *Service) createAndFreeze(ctx context.Context, order domain.Order) (bool, string, error) {
	var (
		rejected bool
		reason   string
	)
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		account, err := tx.GetAccountForUpdate(ctx, order.AccountID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return ErrAccountNotFound
			}
			return err
		}
		switch {
		case order.Side == domain.SideBuy && order.Type == domain.OrderTypeLimit:
			cost := order.Price * order.Quantity
			if account.AvailableCash < cost {
				rejected = true
				reason = ErrInsufficientFunds.Error()
				break
			}
			account.AvailableCash -= cost
			account.FrozenCash += cost
			account.UpdatedAt = s.now()
			if err := tx.UpdateAccount(ctx, account); err != nil {
				return err
			}
		case order.Side == domain.SideSell:
			position, err := tx.GetPositionForUpdate(ctx, order.AccountID, order.Symbol)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					rejected = true
					reason = ErrInsufficientPosition.Error()
					break
				}
				return err
			}
			if position.Available() < order.Quantity {
				rejected = true
				reason = ErrInsufficientPosition.Error()
				break
			}
			position.FrozenQty += order.Quantity
			position.UpdatedAt = s.now()
			if err := tx.UpsertPosition(ctx, position); err != nil {
				return err
			}
		case order.Side == domain.SideBuy && account.AvailableCash <= 0:
			rejected = true
			reason = ErrInsufficientFunds.Error()
		}
		if rejected {
			order.Status = domain.OrderStatusRejected
			order.RejectReason = reason
		}
		return tx.CreateOrder(ctx, order)
	})
	return rejected, reason, err
}

func (s *Service) rejectOrder(ctx context.Context, order domain.Order, reason string) (domain.Order, error) {
	var state *txState
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		state = newTxState(ctx, tx, s.now())
		current, err := state.order(order.ID)
		if err != nil {
			return err
		}
		current.Status = domain.OrderStatusRejected
		current.RejectReason = reason
		current.UpdatedAt = s.now()
		state.markOrder(current)
		if err := state.releaseTerminal(current); err != nil {
			return err
		}
		return state.flush()
	})
	if err != nil {
		return domain.Order{}, err
	}
	return state.updatedOrders[order.ID], nil
}

func (s *Service) applyResult(ctx context.Context, takerOrder domain.Order, result matching.Result) (map[string]domain.Order, []domain.Trade, []domain.Position, error) {
	var state *txState
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		state = newTxState(ctx, tx, s.now())
		taker, err := state.order(takerOrder.ID)
		if err != nil {
			return err
		}
		var trades []domain.Trade
		for _, fill := range result.Fills {
			maker, err := state.order(fill.MakerOrderID)
			if err != nil {
				return err
			}
			applyFillToOrder(maker, fill.Price, fill.Quantity, s.now())
			state.markOrder(maker)

			buyer, seller := maker, taker
			if fill.MakerSide == domain.SideSell {
				buyer, seller = taker, maker
			}
			if err := state.settleFill(buyer, seller, fill); err != nil {
				return err
			}

			trades = append(trades,
				domain.Trade{
					ID:        uuid.NewString(),
					MatchID:   fill.MatchID,
					OrderID:   fill.MakerOrderID,
					AccountID: fill.MakerAccount,
					Symbol:    fill.Symbol,
					Side:      fill.MakerSide,
					Price:     fill.Price,
					Quantity:  fill.Quantity,
					CreatedAt: s.now(),
				},
				domain.Trade{
					ID:        uuid.NewString(),
					MatchID:   fill.MatchID,
					OrderID:   fill.TakerOrderID,
					AccountID: fill.TakerAccount,
					Symbol:    fill.Symbol,
					Side:      fill.TakerSide,
					Price:     fill.Price,
					Quantity:  fill.Quantity,
					CreatedAt: s.now(),
				},
			)
		}

		taker.FilledQty = result.FilledQty
		taker.AvgPrice = result.AvgPrice
		taker.Status = result.Status
		taker.RejectReason = result.RejectReason
		taker.UpdatedAt = s.now()
		state.markOrder(taker)

		if err := tx.InsertTrades(ctx, trades); err != nil {
			return err
		}
		state.trades = trades

		for _, order := range state.orders {
			if err := state.releaseTerminal(order); err != nil {
				return err
			}
		}
		return state.flush()
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return state.updatedOrders, state.trades, state.positionsList(), nil
}

func (s *Service) Cancel(ctx context.Context, accountID, orderID, clientOrderID string) (domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel(ctx, accountID, orderID, clientOrderID)
}

func (s *Service) cancel(ctx context.Context, accountID, orderID, clientOrderID string) (domain.Order, error) {
	order, err := s.lookupOrder(ctx, accountID, orderID, clientOrderID)
	if err != nil {
		return domain.Order{}, err
	}
	if order.Status.Terminal() {
		return order, nil
	}
	if _, err := s.engine.Cancel(ctx, order.ID); err != nil {
		return domain.Order{}, err
	}

	var (
		state   *txState
		changed bool
	)
	err = s.store.WithTx(ctx, func(tx store.Store) error {
		state = newTxState(ctx, tx, s.now())
		current, err := state.order(order.ID)
		if err != nil {
			return err
		}
		if current.Status.Terminal() {
			return nil
		}
		current.Status = domain.OrderStatusCanceled
		current.UpdatedAt = s.now()
		state.markOrder(current)
		if err := state.releaseTerminal(current); err != nil {
			return err
		}
		changed = true
		return state.flush()
	})
	if err != nil {
		return domain.Order{}, err
	}
	if !changed {
		return s.store.GetOrder(ctx, accountID, order.ID)
	}
	canceled := state.updatedOrders[order.ID]
	s.publishOrder(canceled)
	for _, p := range state.positionsList() {
		s.publishPosition(p)
	}
	return canceled, nil
}

func (s *Service) lookupOrder(ctx context.Context, accountID, orderID, clientOrderID string) (domain.Order, error) {
	var (
		order domain.Order
		err   error
	)
	if orderID != "" {
		order, err = s.store.GetOrder(ctx, accountID, orderID)
	} else if clientOrderID != "" {
		order, err = s.store.GetOrderByClientID(ctx, accountID, clientOrderID)
	} else {
		return domain.Order{}, fmt.Errorf("%w: order_id or client_order_id required", ErrInvalidRequest)
	}
	if errors.Is(err, store.ErrNotFound) {
		return domain.Order{}, ErrOrderNotFound
	}
	return order, err
}

func (s *Service) Recover(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	active, err := s.store.ListActiveOrders(ctx)
	if err != nil {
		return 0, err
	}
	for _, order := range active {
		if order.Type != domain.OrderTypeLimit || order.TimeInForce != domain.TimeInForceGTC {
			if _, err := s.cancel(ctx, order.AccountID, order.ID, ""); err != nil {
				return 0, err
			}
			continue
		}
		if order.Status == domain.OrderStatusPendingNew {
			order.Status = domain.OrderStatusAccepted
			order.UpdatedAt = s.now()
			if err := s.store.UpdateOrder(ctx, order); err != nil {
				return 0, err
			}
		}
		if err := s.engine.Restore(ctx, order); err != nil {
			if _, cancelErr := s.cancel(ctx, order.AccountID, order.ID, ""); cancelErr != nil {
				return 0, cancelErr
			}
		}
	}
	return len(active), nil
}

func (s *Service) ListOrders(ctx context.Context, accountID string, filter store.OrderFilter) ([]domain.Order, string, error) {
	filter.AccountID = accountID
	return s.store.ListOrders(ctx, filter)
}

func (s *Service) ListTrades(ctx context.Context, accountID string, filter store.TradeFilter) ([]domain.Trade, string, error) {
	filter.AccountID = accountID
	return s.store.ListTrades(ctx, filter)
}

func (s *Service) ListPositions(ctx context.Context, accountID, symbol string) ([]domain.Position, error) {
	return s.store.ListPositions(ctx, accountID, symbol)
}

func (s *Service) ListStrategies(ctx context.Context, accountID string, filter store.StrategyFilter) ([]domain.Strategy, string, error) {
	filter.AccountID = accountID
	return s.store.ListStrategies(ctx, filter)
}

func (s *Service) SetStrategyStatus(ctx context.Context, strategyID string, next domain.StrategyStatus, actor string) (domain.Strategy, error) {
	switch next {
	case domain.StrategyStatusRunning, domain.StrategyStatusPaused, domain.StrategyStatusStopped:
	default:
		return domain.Strategy{}, fmt.Errorf("%w: invalid strategy status", ErrInvalidRequest)
	}
	strategy, err := s.store.GetStrategy(ctx, strategyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return domain.Strategy{}, ErrStrategyNotFound
		}
		return domain.Strategy{}, err
	}
	strategy.Status = next
	strategy.UpdatedAt = s.now()
	if err := s.store.UpsertStrategy(ctx, strategy); err != nil {
		return domain.Strategy{}, err
	}
	_ = s.store.InsertAudit(ctx, domain.AuditLog{
		Actor:     actor,
		Action:    "UPDATE_STRATEGY_STATUS",
		Target:    strategy.ID,
		Params:    fmt.Sprintf(`{"status":%q}`, next),
		Result:    "ok",
		CreatedAt: s.now(),
	})
	s.bus.Publish(eventbus.Event{
		Type:      eventbus.EventStrategyStatus,
		AccountID: strategy.AccountID,
		Payload:   strategy,
	})
	return strategy, nil
}

func (s *Service) Reconcile(ctx context.Context) ([]string, error) {
	orderMismatches, err := s.store.ReconcileOrderFills(ctx)
	if err != nil {
		return nil, err
	}
	positionMismatches, err := s.store.ReconcilePositions(ctx)
	if err != nil {
		return nil, err
	}
	var issues []string
	for _, m := range orderMismatches {
		issues = append(issues, fmt.Sprintf("order %s filled_qty recorded=%d trades=%d", m.OrderID, m.Recorded, m.Actual))
	}
	for _, m := range positionMismatches {
		issues = append(issues, fmt.Sprintf("position %s/%s recorded=%d trades=%d", m.AccountID, m.Symbol, m.Recorded, m.Actual))
	}
	return issues, nil
}

func (s *Service) validate(req SubmitRequest) error {
	if req.ClientOrderID == "" {
		return fmt.Errorf("%w: client_order_id required", ErrInvalidRequest)
	}
	if _, ok := s.symbols[req.Symbol]; !ok {
		return fmt.Errorf("%w: symbol %s not allowed", ErrInvalidRequest, req.Symbol)
	}
	if req.Quantity <= 0 {
		return fmt.Errorf("%w: quantity must be positive", ErrInvalidRequest)
	}
	if req.Side != domain.SideBuy && req.Side != domain.SideSell {
		return fmt.Errorf("%w: invalid side", ErrInvalidRequest)
	}
	if req.Type != domain.OrderTypeLimit && req.Type != domain.OrderTypeMarket {
		return fmt.Errorf("%w: invalid order type", ErrInvalidRequest)
	}
	if req.Type == domain.OrderTypeLimit && req.Price <= 0 {
		return fmt.Errorf("%w: limit price must be positive", ErrInvalidRequest)
	}
	switch req.TimeInForce {
	case domain.TimeInForceGTC, domain.TimeInForceIOC, domain.TimeInForceFOK:
	default:
		return fmt.Errorf("%w: invalid time in force", ErrInvalidRequest)
	}
	return nil
}

func (s *Service) publishOrder(order domain.Order) {
	s.bus.Publish(eventbus.Event{
		Type:      orderEventType(order.Status),
		AccountID: order.AccountID,
		Symbol:    order.Symbol,
		Payload:   order,
	})
}

func (s *Service) publishTrade(trade domain.Trade) {
	s.bus.Publish(eventbus.Event{
		Type:      eventbus.EventTrade,
		AccountID: trade.AccountID,
		Symbol:    trade.Symbol,
		Payload:   trade,
	})
}

func (s *Service) publishPosition(position domain.Position) {
	s.bus.Publish(eventbus.Event{
		Type:      eventbus.EventPositionUpdated,
		AccountID: position.AccountID,
		Symbol:    position.Symbol,
		Payload:   position,
	})
}

func orderEventType(status domain.OrderStatus) eventbus.EventType {
	switch status {
	case domain.OrderStatusAccepted:
		return eventbus.EventOrderAccepted
	case domain.OrderStatusPartiallyFilled:
		return eventbus.EventOrderPartiallyFill
	case domain.OrderStatusFilled:
		return eventbus.EventOrderFilled
	case domain.OrderStatusCanceled:
		return eventbus.EventOrderCanceled
	case domain.OrderStatusRejected:
		return eventbus.EventOrderRejected
	default:
		return eventbus.EventOrderAccepted
	}
}

type txState struct {
	ctx           context.Context
	st            store.Store
	now           time.Time
	accounts      map[string]*domain.Account
	positions     map[string]*domain.Position
	orders        map[string]*domain.Order
	dirtyAcct     map[string]bool
	dirtyPos      map[string]bool
	updatedOrders map[string]domain.Order
	trades        []domain.Trade
}

func newTxState(ctx context.Context, st store.Store, now time.Time) *txState {
	return &txState{
		ctx:           ctx,
		st:            st,
		now:           now,
		accounts:      make(map[string]*domain.Account),
		positions:     make(map[string]*domain.Position),
		orders:        make(map[string]*domain.Order),
		dirtyAcct:     make(map[string]bool),
		dirtyPos:      make(map[string]bool),
		updatedOrders: make(map[string]domain.Order),
	}
}

func (t *txState) account(id string) (*domain.Account, error) {
	if a, ok := t.accounts[id]; ok {
		return a, nil
	}
	a, err := t.st.GetAccountForUpdate(t.ctx, id)
	if err != nil {
		return nil, err
	}
	t.accounts[id] = &a
	return &a, nil
}

func (t *txState) position(accountID, symbol string) (*domain.Position, error) {
	key := accountID + "|" + symbol
	if p, ok := t.positions[key]; ok {
		return p, nil
	}
	p, err := t.st.GetPositionForUpdate(t.ctx, accountID, symbol)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			p = domain.Position{AccountID: accountID, Symbol: symbol, UpdatedAt: t.now}
		} else {
			return nil, err
		}
	}
	t.positions[key] = &p
	return &p, nil
}

func (t *txState) order(id string) (*domain.Order, error) {
	if o, ok := t.orders[id]; ok {
		return o, nil
	}
	o, err := t.st.GetOrderForUpdate(t.ctx, id)
	if err != nil {
		return nil, err
	}
	t.orders[id] = &o
	return &o, nil
}

func (t *txState) markOrder(o *domain.Order) {
	t.updatedOrders[o.ID] = *o
}

func (t *txState) settleFill(buyer, seller *domain.Order, fill domain.Fill) error {
	notional := fill.Price * fill.Quantity
	buyerAccount, err := t.account(buyer.AccountID)
	if err != nil {
		return err
	}
	if buyer.Type == domain.OrderTypeLimit {
		take := notional
		if buyerAccount.FrozenCash < take {
			take = buyerAccount.FrozenCash
		}
		buyerAccount.FrozenCash -= take
		buyerAccount.AvailableCash -= notional - take
	} else {
		buyerAccount.AvailableCash -= notional
	}
	buyerAccount.UpdatedAt = t.now
	t.dirtyAcct[buyer.AccountID] = true

	buyerPosition, err := t.position(buyer.AccountID, fill.Symbol)
	if err != nil {
		return err
	}
	oldQty := buyerPosition.Quantity
	buyerPosition.Quantity += fill.Quantity
	buyerPosition.AvgPrice = weightedAvg(buyerPosition.AvgPrice, oldQty, fill.Price, fill.Quantity)
	buyerPosition.UpdatedAt = t.now
	t.dirtyPos[buyer.AccountID+"|"+fill.Symbol] = true

	sellerAccount, err := t.account(seller.AccountID)
	if err != nil {
		return err
	}
	sellerAccount.AvailableCash += notional
	sellerAccount.UpdatedAt = t.now
	t.dirtyAcct[seller.AccountID] = true

	sellerPosition, err := t.position(seller.AccountID, fill.Symbol)
	if err != nil {
		return err
	}
	sellerPosition.Quantity -= fill.Quantity
	release := fill.Quantity
	if sellerPosition.FrozenQty < release {
		release = sellerPosition.FrozenQty
	}
	sellerPosition.FrozenQty -= release
	sellerPosition.UpdatedAt = t.now
	t.dirtyPos[seller.AccountID+"|"+fill.Symbol] = true
	return nil
}

func (t *txState) releaseTerminal(order *domain.Order) error {
	if !order.Status.Terminal() {
		return nil
	}
	if order.Side == domain.SideBuy && order.Type == domain.OrderTypeLimit {
		notional, err := t.st.FilledNotional(t.ctx, order.ID)
		if err != nil {
			return err
		}
		release := order.Price*order.Quantity - notional
		if release <= 0 {
			return nil
		}
		account, err := t.account(order.AccountID)
		if err != nil {
			return err
		}
		take := release
		if account.FrozenCash < take {
			take = account.FrozenCash
		}
		account.FrozenCash -= take
		account.AvailableCash += take
		account.UpdatedAt = t.now
		t.dirtyAcct[order.AccountID] = true
	}
	if order.Side == domain.SideSell {
		remaining := order.Quantity - order.FilledQty
		if remaining <= 0 {
			return nil
		}
		position, err := t.position(order.AccountID, order.Symbol)
		if err != nil {
			return err
		}
		release := remaining
		if position.FrozenQty < release {
			release = position.FrozenQty
		}
		position.FrozenQty -= release
		position.UpdatedAt = t.now
		t.dirtyPos[order.AccountID+"|"+order.Symbol] = true
	}
	return nil
}

func (t *txState) flush() error {
	for id := range t.dirtyAcct {
		if err := t.st.UpdateAccount(t.ctx, *t.accounts[id]); err != nil {
			return err
		}
	}
	for key := range t.dirtyPos {
		if err := t.st.UpsertPosition(t.ctx, *t.positions[key]); err != nil {
			return err
		}
	}
	for _, order := range t.updatedOrders {
		if err := t.st.UpdateOrder(t.ctx, order); err != nil {
			return err
		}
	}
	return nil
}

func (t *txState) positionsList() []domain.Position {
	out := make([]domain.Position, 0, len(t.dirtyPos))
	for key := range t.dirtyPos {
		if p, ok := t.positions[key]; ok {
			out = append(out, *p)
		}
	}
	return out
}

func applyFillToOrder(order *domain.Order, price, qty int64, now time.Time) {
	oldFilled := order.FilledQty
	order.FilledQty += qty
	order.AvgPrice = weightedAvg(order.AvgPrice, oldFilled, price, qty)
	if order.FilledQty >= order.Quantity {
		order.Status = domain.OrderStatusFilled
	} else {
		order.Status = domain.OrderStatusPartiallyFilled
	}
	order.UpdatedAt = now
}

func weightedAvg(oldAvg, oldQty, price, qty int64) int64 {
	total := oldQty + qty
	if total <= 0 {
		return 0
	}
	return (oldAvg*oldQty + price*qty) / total
}
