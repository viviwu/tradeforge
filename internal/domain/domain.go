package domain

import "time"

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeMarket OrderType = "MARKET"
)

type TimeInForce string

const (
	TimeInForceGTC TimeInForce = "GTC"
	TimeInForceIOC TimeInForce = "IOC"
	TimeInForceFOK TimeInForce = "FOK"
)

type OrderStatus string

const (
	OrderStatusPendingNew      OrderStatus = "PENDING_NEW"
	OrderStatusAccepted        OrderStatus = "ACCEPTED"
	OrderStatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	OrderStatusFilled          OrderStatus = "FILLED"
	OrderStatusCanceled        OrderStatus = "CANCELED"
	OrderStatusRejected        OrderStatus = "REJECTED"
)

func (s OrderStatus) Terminal() bool {
	switch s {
	case OrderStatusFilled, OrderStatusCanceled, OrderStatusRejected:
		return true
	default:
		return false
	}
}

type StrategyStatus string

const (
	StrategyStatusCreated StrategyStatus = "CREATED"
	StrategyStatusRunning StrategyStatus = "RUNNING"
	StrategyStatusPaused  StrategyStatus = "PAUSED"
	StrategyStatusStopped StrategyStatus = "STOPPED"
	StrategyStatusError   StrategyStatus = "ERROR"
)

type User struct {
	ID           string
	Username     string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

func (u User) IsAdmin() bool { return u.Role == "admin" }

type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	Revoked   bool
	CreatedAt time.Time
}

type Account struct {
	AccountID     string
	AvailableCash int64
	FrozenCash    int64
	UpdatedAt     time.Time
}

type Order struct {
	ID            string
	ClientOrderID string
	AccountID     string
	Symbol        string
	Side          Side
	Type          OrderType
	TimeInForce   TimeInForce
	Price         int64
	Quantity      int64
	FilledQty     int64
	AvgPrice      int64
	Status        OrderStatus
	RejectReason  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (o Order) Remaining() int64 {
	remaining := o.Quantity - o.FilledQty
	if remaining < 0 {
		return 0
	}
	return remaining
}

type Trade struct {
	ID        string
	MatchID   string
	TradeSeq  uint64
	OrderID   string
	AccountID string
	Symbol    string
	Side      Side
	Price     int64
	Quantity  int64
	CreatedAt time.Time
}

type Position struct {
	AccountID   string
	Symbol      string
	Quantity    int64
	AvgPrice    int64
	FrozenQty   int64
	MarketPrice int64
	UpdatedAt   time.Time
}

func (p Position) Available() int64 {
	available := p.Quantity - p.FrozenQty
	if available < 0 {
		return 0
	}
	return available
}

type Strategy struct {
	ID        string
	AccountID string
	Name      string
	Status    StrategyStatus
	Params    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AuditLog struct {
	ID        int64
	Actor     string
	Action    string
	Target    string
	Params    string
	Result    string
	CreatedAt time.Time
}

// Fill is a match between a maker and a taker order.
type Fill struct {
	MatchID      string
	MakerOrderID string
	TakerOrderID string
	MakerAccount string
	TakerAccount string
	Symbol       string
	MakerSide    Side
	TakerSide    Side
	Price        int64
	Quantity     int64
}
