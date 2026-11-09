// broker-stub is the simulated broker gateway: a BrokerGatewayService server
// backed by a single tradeapi login against the TradeForge simulation
// exchange. It lets a channel core exercise the same contract a real counter
// SDK gateway will later implement.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"

	bg "github.com/viviwu/tradeforge/gen/brokergateway/v1"
	"github.com/viviwu/tradeforge/internal/brokergw"
	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("broker-stub stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	metrics := brokergw.NewMetrics()
	cfg := brokergw.Config{
		FundAccount:   env("BROKER_STUB_FUND_ACCOUNT", "F001"),
		TradeAccount:  env("BROKER_STUB_TRADE_ACCOUNT", "A0001"),
		Market:        env("BROKER_STUB_MARKET", "SIM"),
		Symbols:       splitCSV(env("BROKER_STUB_SYMBOLS", "AAPL,MSFT,TSLA,NVDA")),
		HaltedSymbols: splitCSV(env("BROKER_STUB_HALTED_SYMBOLS", "")),
		PriceLimitPct: envInt("BROKER_STUB_LIMIT_PCT", 0),
		RingSize:      envInt("BROKER_STUB_REPORT_RING", 4096),
		Metrics:       metrics,
	}
	if cfg.PriceLimitPct > 0 {
		if cfg.PriceLimitPct > 99 {
			return errors.New("BROKER_STUB_LIMIT_PCT must be 0..99")
		}
	}

	listen := env("BROKER_STUB_LISTEN_ADDR", ":50053")
	metricsAddr := env("BROKER_STUB_METRICS_ADDR", ":50054")
	upstreamAddr := env("BROKER_STUB_UPSTREAM_ADDR", "127.0.0.1:50051")
	user := env("BROKER_STUB_USER", "alice")
	pass := env("BROKER_STUB_PASS", "alice123")

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	up, err := connect(ctx, upstreamAddr, user, pass)
	if err != nil {
		return err
	}
	defer up.Close()
	slog.Info("connected to upstream", "addr", upstreamAddr, "user", user)

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	metricsLis, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := httpSrv.Serve(metricsLis); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			slog.Error("metrics server stopped", "error", err)
		}
	}()

	srv := brokergw.NewServer(up, slog.Default(), cfg)
	gsrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(metrics.StreamInterceptor()),
	)
	bg.RegisterBrokerGatewayServiceServer(gsrv, srv)
	srv.Start(ctx)

	slog.Info("broker-stub serving", "listen", listen, "metrics", metricsAddr, "fund_account", cfg.FundAccount, "trade_account", cfg.TradeAccount, "market", cfg.Market)
	go func() {
		<-ctx.Done()
		slog.Info("stopping broker-stub")
		_ = httpSrv.Close()
		gsrv.Stop()
	}()
	if err := gsrv.Serve(lis); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// connect dials the upstream and logs in, retrying until the upstream accepts
// (its boot can lag behind ours in compose orchestrations).
func connect(ctx context.Context, addr, user, pass string) (*tradeapi.Client, error) {
	const maxWait = 90 * time.Second
	deadline := time.Now().Add(maxWait)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		client, err := tradeapi.Dial(ctx, tradeapi.Config{Address: addr})
		if err == nil {
			if _, err = client.Login(ctx, user, pass); err == nil {
				return client, nil
			}
			lastErr = err
			_ = client.Close()
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("cannot reach/login upstream %q as %q: %w", addr, user, lastErr)
		}
		slog.Warn("upstream not ready, retrying", "addr", addr, "error", lastErr)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid integer env, using default", "key", key, "value", v, "default", def)
		return def
	}
	return n
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
