CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS accounts (
    account_id UUID PRIMARY KEY,
    available_cash BIGINT NOT NULL DEFAULT 0,
    frozen_cash BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY,
    client_order_id TEXT NOT NULL,
    account_id UUID NOT NULL REFERENCES accounts(account_id),
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    type TEXT NOT NULL,
    time_in_force TEXT NOT NULL,
    price BIGINT NOT NULL DEFAULT 0,
    quantity BIGINT NOT NULL,
    filled_qty BIGINT NOT NULL DEFAULT 0,
    avg_price BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    reject_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (account_id, client_order_id)
);

CREATE SEQUENCE IF NOT EXISTS trade_seq_seq START 1;

CREATE TABLE IF NOT EXISTS trades (
    id UUID PRIMARY KEY,
    match_id UUID NOT NULL,
    trade_seq BIGINT NOT NULL UNIQUE DEFAULT nextval('trade_seq_seq'),
    order_id UUID NOT NULL,
    account_id UUID NOT NULL,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    price BIGINT NOT NULL,
    quantity BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS positions (
    account_id UUID NOT NULL,
    symbol TEXT NOT NULL,
    quantity BIGINT NOT NULL DEFAULT 0,
    avg_price BIGINT NOT NULL DEFAULT 0,
    frozen_qty BIGINT NOT NULL DEFAULT 0,
    market_price BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, symbol)
);

CREATE TABLE IF NOT EXISTS strategies (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    params JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    params JSONB NOT NULL DEFAULT '{}'::jsonb,
    result TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_orders_account_status_created ON orders (account_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_order_id ON orders (id);
CREATE INDEX IF NOT EXISTS idx_trades_account_created ON trades (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trades_order ON trades (order_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_strategies_account ON strategies (account_id, status);
