package store

import (
	"context"
	"errors"
	"time"

	"github.com/viviwu/tradeforge/internal/domain"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate")
)

type OrderFilter struct {
	AccountID     string
	Status        domain.OrderStatus
	Symbol        string
	OrderID       string
	ClientOrderID string
	From          time.Time
	To            time.Time
	PageSize      int
	PageToken     string
}

type TradeFilter struct {
	AccountID string
	OrderID   string
	Symbol    string
	From      time.Time
	To        time.Time
	PageSize  int
	PageToken string
}

type StrategyFilter struct {
	AccountID string
	Status    domain.StrategyStatus
	PageSize  int
	PageToken string
}

type OrderFillTotal struct {
	OrderID   string
	FilledQty int64
}

type PositionTotal struct {
	AccountID string
	Symbol    string
	Quantity  int64
}

type OrderMismatch struct {
	OrderID  string
	Recorded int64
	Actual   int64
}

type PositionMismatch struct {
	AccountID string
	Symbol    string
	Recorded  int64
	Actual    int64
}

type Store interface {
	CreateUser(ctx context.Context, user domain.User) error
	GetUserByUsername(ctx context.Context, username string) (domain.User, error)
	GetUserByID(ctx context.Context, id string) (domain.User, error)

	CreateRefreshToken(ctx context.Context, token domain.RefreshToken) error
	GetRefreshToken(ctx context.Context, tokenHash string) (domain.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, tokenHash string) error
	ConsumeRefreshToken(ctx context.Context, tokenHash string) (bool, error)
	RevokeUserRefreshTokens(ctx context.Context, userID string) error

	EnsureAccount(ctx context.Context, accountID string, initialCash int64) error
	EnsurePosition(ctx context.Context, accountID, symbol string, quantity int64) error
	GetAccount(ctx context.Context, accountID string) (domain.Account, error)
	GetAccountForUpdate(ctx context.Context, accountID string) (domain.Account, error)
	UpdateAccount(ctx context.Context, account domain.Account) error

	GetPosition(ctx context.Context, accountID, symbol string) (domain.Position, error)
	GetPositionForUpdate(ctx context.Context, accountID, symbol string) (domain.Position, error)
	UpsertPosition(ctx context.Context, position domain.Position) error
	ListPositions(ctx context.Context, accountID, symbol string) ([]domain.Position, error)

	CreateOrder(ctx context.Context, order domain.Order) error
	GetOrderByID(ctx context.Context, orderID string) (domain.Order, error)
	GetOrder(ctx context.Context, accountID, orderID string) (domain.Order, error)
	GetOrderByClientID(ctx context.Context, accountID, clientOrderID string) (domain.Order, error)
	GetOrderForUpdate(ctx context.Context, orderID string) (domain.Order, error)
	UpdateOrder(ctx context.Context, order domain.Order) error
	ListOrders(ctx context.Context, filter OrderFilter) ([]domain.Order, string, error)
	ListActiveOrders(ctx context.Context) ([]domain.Order, error)

	InsertTrades(ctx context.Context, trades []domain.Trade) error
	FilledNotional(ctx context.Context, orderID string) (int64, error)
	ListTrades(ctx context.Context, filter TradeFilter) ([]domain.Trade, string, error)

	ListStrategies(ctx context.Context, filter StrategyFilter) ([]domain.Strategy, string, error)
	GetStrategy(ctx context.Context, strategyID string) (domain.Strategy, error)
	UpsertStrategy(ctx context.Context, strategy domain.Strategy) error

	InsertAudit(ctx context.Context, log domain.AuditLog) error

	OrderFillTotals(ctx context.Context) ([]OrderFillTotal, error)
	PositionTotals(ctx context.Context) ([]PositionTotal, error)
	ReconcileOrderFills(ctx context.Context) ([]OrderMismatch, error)
	ReconcilePositions(ctx context.Context) ([]PositionMismatch, error)

	WithTx(ctx context.Context, fn func(Store) error) error
}
