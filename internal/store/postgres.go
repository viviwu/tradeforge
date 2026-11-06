package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/viviwu/tradeforge/internal/domain"
)

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Postgres struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

func Open(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (s *Postgres) Close() { s.pool.Close() }

func (s *Postgres) Pool() *pgxpool.Pool { return s.pool }

func (s *Postgres) q() querier {
	if s.tx != nil {
		return s.tx
	}
	return s.pool
}

func (s *Postgres) WithTx(ctx context.Context, fn func(Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	wrapped := &Postgres{pool: s.pool, tx: tx}
	if err := fn(wrapped); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Postgres) CreateUser(ctx context.Context, user domain.User) error {
	_, err := s.q().Exec(ctx,
		`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ($1,$2,$3,$4,$5)`,
		user.ID, user.Username, user.PasswordHash, user.Role, user.CreatedAt,
	)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	return err
}

func (s *Postgres) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	return scanUser(s.q().QueryRow(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users WHERE username=$1`, username))
}

func (s *Postgres) GetUserByID(ctx context.Context, id string) (domain.User, error) {
	return scanUser(s.q().QueryRow(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users WHERE id=$1`, id))
}

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	return u, err
}

func (s *Postgres) CreateRefreshToken(ctx context.Context, token domain.RefreshToken) error {
	_, err := s.q().Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, revoked, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		token.ID, token.UserID, token.TokenHash, token.ExpiresAt, token.Revoked, token.CreatedAt,
	)
	return err
}

func (s *Postgres) GetRefreshToken(ctx context.Context, tokenHash string) (domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := s.q().QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, revoked, created_at FROM refresh_tokens WHERE token_hash=$1`,
		tokenHash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.Revoked, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshToken{}, ErrNotFound
	}
	return t, err
}

func (s *Postgres) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := s.q().Exec(ctx, `UPDATE refresh_tokens SET revoked=true WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *Postgres) ConsumeRefreshToken(ctx context.Context, tokenHash string) (bool, error) {
	tag, err := s.q().Exec(ctx,
		`UPDATE refresh_tokens SET revoked=true WHERE token_hash=$1 AND revoked=false AND expires_at > now()`,
		tokenHash,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Postgres) RevokeUserRefreshTokens(ctx context.Context, userID string) error {
	_, err := s.q().Exec(ctx, `UPDATE refresh_tokens SET revoked=true WHERE user_id=$1`, userID)
	return err
}

func (s *Postgres) EnsureAccount(ctx context.Context, accountID string, initialCash int64) error {
	_, err := s.q().Exec(ctx,
		`INSERT INTO accounts (account_id, available_cash, frozen_cash, updated_at) VALUES ($1,$2,0,now())
		 ON CONFLICT (account_id) DO NOTHING`,
		accountID, initialCash,
	)
	return err
}

func (s *Postgres) EnsurePosition(ctx context.Context, accountID, symbol string, quantity int64) error {
	if quantity <= 0 {
		return nil
	}
	_, err := s.q().Exec(ctx,
		`INSERT INTO positions (account_id, symbol, quantity, avg_price, frozen_qty, market_price, seed_qty, updated_at)
		 VALUES ($1,$2,$3,0,0,0,$3,now())
		 ON CONFLICT (account_id, symbol) DO NOTHING`,
		accountID, symbol, quantity,
	)
	return err
}

func (s *Postgres) GetAccount(ctx context.Context, accountID string) (domain.Account, error) {
	return scanAccount(s.q().QueryRow(ctx,
		`SELECT account_id, available_cash, frozen_cash, updated_at FROM accounts WHERE account_id=$1`, accountID))
}

func (s *Postgres) GetAccountForUpdate(ctx context.Context, accountID string) (domain.Account, error) {
	return scanAccount(s.q().QueryRow(ctx,
		`SELECT account_id, available_cash, frozen_cash, updated_at FROM accounts WHERE account_id=$1 FOR UPDATE`, accountID))
}

func scanAccount(row pgx.Row) (domain.Account, error) {
	var a domain.Account
	err := row.Scan(&a.AccountID, &a.AvailableCash, &a.FrozenCash, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrNotFound
	}
	return a, err
}

func (s *Postgres) UpdateAccount(ctx context.Context, account domain.Account) error {
	tag, err := s.q().Exec(ctx,
		`UPDATE accounts SET available_cash=$2, frozen_cash=$3, updated_at=$4 WHERE account_id=$1`,
		account.AccountID, account.AvailableCash, account.FrozenCash, account.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Postgres) GetPosition(ctx context.Context, accountID, symbol string) (domain.Position, error) {
	return scanPosition(s.q().QueryRow(ctx,
		`SELECT account_id, symbol, quantity, avg_price, frozen_qty, market_price, updated_at
		 FROM positions WHERE account_id=$1 AND symbol=$2`, accountID, symbol))
}

func (s *Postgres) GetPositionForUpdate(ctx context.Context, accountID, symbol string) (domain.Position, error) {
	return scanPosition(s.q().QueryRow(ctx,
		`SELECT account_id, symbol, quantity, avg_price, frozen_qty, market_price, updated_at
		 FROM positions WHERE account_id=$1 AND symbol=$2 FOR UPDATE`, accountID, symbol))
}

func scanPosition(row pgx.Row) (domain.Position, error) {
	var p domain.Position
	err := row.Scan(&p.AccountID, &p.Symbol, &p.Quantity, &p.AvgPrice, &p.FrozenQty, &p.MarketPrice, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Position{}, ErrNotFound
	}
	return p, err
}

func (s *Postgres) UpsertPosition(ctx context.Context, p domain.Position) error {
	_, err := s.q().Exec(ctx,
		`INSERT INTO positions (account_id, symbol, quantity, avg_price, frozen_qty, market_price, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (account_id, symbol) DO UPDATE SET
		   quantity=EXCLUDED.quantity, avg_price=EXCLUDED.avg_price,
		   frozen_qty=EXCLUDED.frozen_qty, market_price=EXCLUDED.market_price,
		   updated_at=EXCLUDED.updated_at`,
		p.AccountID, p.Symbol, p.Quantity, p.AvgPrice, p.FrozenQty, p.MarketPrice, p.UpdatedAt,
	)
	return err
}

func (s *Postgres) ListPositions(ctx context.Context, accountID, symbol string) ([]domain.Position, error) {
	query := `SELECT account_id, symbol, quantity, avg_price, frozen_qty, market_price, updated_at FROM positions WHERE account_id=$1`
	args := []any{accountID}
	if symbol != "" {
		query += ` AND symbol=$2`
		args = append(args, symbol)
	}
	query += ` ORDER BY symbol`
	rows, err := s.q().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Position
	for rows.Next() {
		var p domain.Position
		if err := rows.Scan(&p.AccountID, &p.Symbol, &p.Quantity, &p.AvgPrice, &p.FrozenQty, &p.MarketPrice, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Postgres) CreateOrder(ctx context.Context, o domain.Order) error {
	_, err := s.q().Exec(ctx,
		`INSERT INTO orders (id, client_order_id, account_id, symbol, side, type, time_in_force,
		   price, quantity, filled_qty, avg_price, status, reject_reason, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		o.ID, o.ClientOrderID, o.AccountID, o.Symbol, string(o.Side), string(o.Type), string(o.TimeInForce),
		o.Price, o.Quantity, o.FilledQty, o.AvgPrice, string(o.Status), o.RejectReason, o.CreatedAt, o.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	return err
}

const orderColumns = `id, client_order_id, account_id, symbol, side, type, time_in_force,
	price, quantity, filled_qty, avg_price, status, reject_reason, created_at, updated_at`

func scanOrder(row pgx.Row) (domain.Order, error) {
	var (
		o         domain.Order
		side      string
		orderType string
		tif       string
		status    string
	)
	err := row.Scan(&o.ID, &o.ClientOrderID, &o.AccountID, &o.Symbol, &side, &orderType, &tif,
		&o.Price, &o.Quantity, &o.FilledQty, &o.AvgPrice, &status, &o.RejectReason, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, ErrNotFound
	}
	if err != nil {
		return domain.Order{}, err
	}
	o.Side = domain.Side(side)
	o.Type = domain.OrderType(orderType)
	o.TimeInForce = domain.TimeInForce(tif)
	o.Status = domain.OrderStatus(status)
	return o, nil
}

func (s *Postgres) GetOrderByID(ctx context.Context, orderID string) (domain.Order, error) {
	return scanOrder(s.q().QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id=$1`, orderID))
}

func (s *Postgres) GetOrder(ctx context.Context, accountID, orderID string) (domain.Order, error) {
	return scanOrder(s.q().QueryRow(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE id=$1 AND account_id=$2`, orderID, accountID))
}

func (s *Postgres) GetOrderByClientID(ctx context.Context, accountID, clientOrderID string) (domain.Order, error) {
	return scanOrder(s.q().QueryRow(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE account_id=$1 AND client_order_id=$2`, accountID, clientOrderID))
}

func (s *Postgres) GetOrderForUpdate(ctx context.Context, orderID string) (domain.Order, error) {
	return scanOrder(s.q().QueryRow(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE id=$1 FOR UPDATE`, orderID))
}

func (s *Postgres) UpdateOrder(ctx context.Context, o domain.Order) error {
	tag, err := s.q().Exec(ctx,
		`UPDATE orders SET filled_qty=$2, avg_price=$3, status=$4, reject_reason=$5, updated_at=$6 WHERE id=$1`,
		o.ID, o.FilledQty, o.AvgPrice, string(o.Status), o.RejectReason, o.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func normalizePageSize(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 500 {
		return 500
	}
	return n
}

func parsePageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid page token")
	}
	return n, nil
}

func (s *Postgres) ListOrders(ctx context.Context, f OrderFilter) ([]domain.Order, string, error) {
	offset, err := parsePageToken(f.PageToken)
	if err != nil {
		return nil, "", err
	}
	pageSize := normalizePageSize(f.PageSize)
	var (
		where []string
		args  []any
	)
	where = append(where, "account_id=$1")
	args = append(args, f.AccountID)
	if f.Status != "" {
		args = append(args, string(f.Status))
		where = append(where, fmt.Sprintf("status=$%d", len(args)))
	}
	if f.Symbol != "" {
		args = append(args, f.Symbol)
		where = append(where, fmt.Sprintf("symbol=$%d", len(args)))
	}
	if f.OrderID != "" {
		args = append(args, f.OrderID)
		where = append(where, fmt.Sprintf("id=$%d", len(args)))
	}
	if f.ClientOrderID != "" {
		args = append(args, f.ClientOrderID)
		where = append(where, fmt.Sprintf("client_order_id=$%d", len(args)))
	}
	if !f.From.IsZero() {
		args = append(args, f.From)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if !f.To.IsZero() {
		args = append(args, f.To)
		where = append(where, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	args = append(args, pageSize, offset)
	query := fmt.Sprintf(`SELECT %s FROM orders WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`,
		orderColumns, strings.Join(where, " AND "), len(args)-1, len(args))
	rows, err := s.q().Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []domain.Order
	for rows.Next() {
		var (
			o         domain.Order
			side      string
			orderType string
			tif       string
			status    string
		)
		if err := rows.Scan(&o.ID, &o.ClientOrderID, &o.AccountID, &o.Symbol, &side, &orderType, &tif,
			&o.Price, &o.Quantity, &o.FilledQty, &o.AvgPrice, &status, &o.RejectReason, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, "", err
		}
		o.Side = domain.Side(side)
		o.Type = domain.OrderType(orderType)
		o.TimeInForce = domain.TimeInForce(tif)
		o.Status = domain.OrderStatus(status)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == pageSize {
		next = strconv.Itoa(offset + len(out))
	}
	return out, next, nil
}

func (s *Postgres) ListActiveOrders(ctx context.Context) ([]domain.Order, error) {
	rows, err := s.q().Query(ctx, `SELECT `+orderColumns+` FROM orders
		WHERE status IN ('PENDING_NEW','ACCEPTED','PARTIALLY_FILLED')
		ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Order
	for rows.Next() {
		var (
			o         domain.Order
			side      string
			orderType string
			tif       string
			status    string
		)
		if err := rows.Scan(&o.ID, &o.ClientOrderID, &o.AccountID, &o.Symbol, &side, &orderType, &tif,
			&o.Price, &o.Quantity, &o.FilledQty, &o.AvgPrice, &status, &o.RejectReason, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.Side = domain.Side(side)
		o.Type = domain.OrderType(orderType)
		o.TimeInForce = domain.TimeInForce(tif)
		o.Status = domain.OrderStatus(status)
		out = append(out, o)
	}
	return out, rows.Err()
}

const tradeColumns = `id, match_id, trade_seq, order_id, account_id, symbol, side, price, quantity, created_at`

func (s *Postgres) InsertTrades(ctx context.Context, trades []domain.Trade) error {
	for i := range trades {
		var seq int64
		err := s.q().QueryRow(ctx,
			`INSERT INTO trades (id, match_id, order_id, account_id, symbol, side, price, quantity, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING trade_seq`,
			trades[i].ID, trades[i].MatchID, trades[i].OrderID, trades[i].AccountID,
			trades[i].Symbol, string(trades[i].Side), trades[i].Price, trades[i].Quantity, trades[i].CreatedAt,
		).Scan(&seq)
		if err != nil {
			return err
		}
		trades[i].TradeSeq = uint64(seq)
	}
	return nil
}

func (s *Postgres) FilledNotional(ctx context.Context, orderID string) (int64, error) {
	var total int64
	err := s.q().QueryRow(ctx,
		`SELECT COALESCE(SUM(price*quantity),0) FROM trades WHERE order_id=$1`, orderID).Scan(&total)
	return total, err
}

func (s *Postgres) ListTrades(ctx context.Context, f TradeFilter) ([]domain.Trade, string, error) {
	offset, err := parsePageToken(f.PageToken)
	if err != nil {
		return nil, "", err
	}
	pageSize := normalizePageSize(f.PageSize)
	where := []string{"account_id=$1"}
	args := []any{f.AccountID}
	if f.OrderID != "" {
		args = append(args, f.OrderID)
		where = append(where, fmt.Sprintf("order_id=$%d", len(args)))
	}
	if f.Symbol != "" {
		args = append(args, f.Symbol)
		where = append(where, fmt.Sprintf("symbol=$%d", len(args)))
	}
	if !f.From.IsZero() {
		args = append(args, f.From)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if !f.To.IsZero() {
		args = append(args, f.To)
		where = append(where, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	args = append(args, pageSize, offset)
	query := fmt.Sprintf(`SELECT %s FROM trades WHERE %s ORDER BY trade_seq DESC LIMIT $%d OFFSET $%d`,
		tradeColumns, strings.Join(where, " AND "), len(args)-1, len(args))
	rows, err := s.q().Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []domain.Trade
	for rows.Next() {
		var (
			t    domain.Trade
			side string
			seq  int64
		)
		if err := rows.Scan(&t.ID, &t.MatchID, &seq, &t.OrderID, &t.AccountID, &t.Symbol, &side,
			&t.Price, &t.Quantity, &t.CreatedAt); err != nil {
			return nil, "", err
		}
		t.TradeSeq = uint64(seq)
		t.Side = domain.Side(side)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == pageSize {
		next = strconv.Itoa(offset + len(out))
	}
	return out, next, nil
}

func (s *Postgres) ListStrategies(ctx context.Context, f StrategyFilter) ([]domain.Strategy, string, error) {
	offset, err := parsePageToken(f.PageToken)
	if err != nil {
		return nil, "", err
	}
	pageSize := normalizePageSize(f.PageSize)
	where := []string{"account_id=$1"}
	args := []any{f.AccountID}
	if f.Status != "" {
		args = append(args, string(f.Status))
		where = append(where, fmt.Sprintf("status=$%d", len(args)))
	}
	args = append(args, pageSize, offset)
	query := fmt.Sprintf(`SELECT id, account_id, name, status, params::text, created_at, updated_at
		FROM strategies WHERE %s ORDER BY updated_at DESC LIMIT $%d OFFSET $%d`,
		strings.Join(where, " AND "), len(args)-1, len(args))
	rows, err := s.q().Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []domain.Strategy
	for rows.Next() {
		var st domain.Strategy
		if err := rows.Scan(&st.ID, &st.AccountID, &st.Name, &st.Status, &st.Params, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == pageSize {
		next = strconv.Itoa(offset + len(out))
	}
	return out, next, nil
}

func (s *Postgres) GetStrategy(ctx context.Context, strategyID string) (domain.Strategy, error) {
	var st domain.Strategy
	err := s.q().QueryRow(ctx,
		`SELECT id, account_id, name, status, params::text, created_at, updated_at FROM strategies WHERE id=$1`,
		strategyID,
	).Scan(&st.ID, &st.AccountID, &st.Name, &st.Status, &st.Params, &st.CreatedAt, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Strategy{}, ErrNotFound
	}
	return st, err
}

func (s *Postgres) UpsertStrategy(ctx context.Context, st domain.Strategy) error {
	if st.Params == "" {
		st.Params = "{}"
	}
	_, err := s.q().Exec(ctx,
		`INSERT INTO strategies (id, account_id, name, status, params, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)
		 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, status=EXCLUDED.status,
		   params=EXCLUDED.params, updated_at=EXCLUDED.updated_at`,
		st.ID, st.AccountID, st.Name, string(st.Status), st.Params, st.CreatedAt, st.UpdatedAt,
	)
	return err
}

func (s *Postgres) InsertAudit(ctx context.Context, log domain.AuditLog) error {
	if log.Params == "" {
		log.Params = "{}"
	}
	_, err := s.q().Exec(ctx,
		`INSERT INTO audit_logs (actor, action, target, params, result, created_at) VALUES ($1,$2,$3,$4::jsonb,$5,$6)`,
		log.Actor, log.Action, log.Target, log.Params, log.Result, log.CreatedAt,
	)
	return err
}

func (s *Postgres) OrderFillTotals(ctx context.Context) ([]OrderFillTotal, error) {
	rows, err := s.q().Query(ctx,
		`SELECT order_id, COALESCE(SUM(quantity),0) FROM trades GROUP BY order_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrderFillTotal
	for rows.Next() {
		var t OrderFillTotal
		if err := rows.Scan(&t.OrderID, &t.FilledQty); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Postgres) PositionTotals(ctx context.Context) ([]PositionTotal, error) {
	rows, err := s.q().Query(ctx,
		`SELECT account_id, symbol,
		        COALESCE(SUM(CASE WHEN side='BUY' THEN quantity ELSE -quantity END),0)
		 FROM trades GROUP BY account_id, symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PositionTotal
	for rows.Next() {
		var t PositionTotal
		if err := rows.Scan(&t.AccountID, &t.Symbol, &t.Quantity); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Postgres) ReconcileOrderFills(ctx context.Context) ([]OrderMismatch, error) {
	rows, err := s.q().Query(ctx,
		`SELECT o.id, o.filled_qty, COALESCE(SUM(t.quantity),0)
		 FROM orders o LEFT JOIN trades t ON t.order_id=o.id
		 GROUP BY o.id, o.filled_qty
		 HAVING o.filled_qty <> COALESCE(SUM(t.quantity),0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrderMismatch
	for rows.Next() {
		var m OrderMismatch
		if err := rows.Scan(&m.OrderID, &m.Recorded, &m.Actual); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Postgres) ReconcilePositions(ctx context.Context) ([]PositionMismatch, error) {
	rows, err := s.q().Query(ctx,
		`SELECT COALESCE(p.account_id, t.account_id), COALESCE(p.symbol, t.symbol),
		        COALESCE(p.quantity,0), COALESCE(p.seed_qty,0) + COALESCE(t.quantity,0)
		 FROM positions p
		 FULL JOIN (
		   SELECT account_id, symbol,
		          SUM(CASE WHEN side='BUY' THEN quantity ELSE -quantity END) AS quantity
		   FROM trades GROUP BY account_id, symbol
		 ) t USING (account_id, symbol)
		 WHERE COALESCE(p.quantity,0) <> COALESCE(p.seed_qty,0) + COALESCE(t.quantity,0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PositionMismatch
	for rows.Next() {
		var m PositionMismatch
		if err := rows.Scan(&m.AccountID, &m.Symbol, &m.Recorded, &m.Actual); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func Now() time.Time { return time.Now().UTC() }
