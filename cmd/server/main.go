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
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/internal/auth"
	"github.com/viviwu/tradeforge/internal/bootstrap"
	"github.com/viviwu/tradeforge/internal/config"
	"github.com/viviwu/tradeforge/internal/eventbus"
	"github.com/viviwu/tradeforge/internal/gateway"
	"github.com/viviwu/tradeforge/internal/marketdata"
	"github.com/viviwu/tradeforge/internal/matching"
	"github.com/viviwu/tradeforge/internal/observability"
	"github.com/viviwu/tradeforge/internal/oms"
	"github.com/viviwu/tradeforge/internal/store"
	"github.com/viviwu/tradeforge/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBDSN)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool()); err != nil {
		return err
	}
	if cfg.Seed {
		if err := bootstrap.Seed(ctx, st, logger, cfg.BcryptCost, cfg.InitialCash); err != nil {
			return err
		}
	}

	bus := eventbus.New(cfg.ReplayBuffer)
	engine := matching.New(cfg.Symbols)
	engine.Start()
	defer engine.Stop()

	provider := marketdata.New(bus, cfg.Symbols, cfg.MDInterval)
	go provider.Run(ctx)

	tokenManager := auth.NewTokenManager(cfg.JWTSecret, cfg.AccessTTL)
	authService := auth.NewService(st, tokenManager, cfg.RefreshTTL, cfg.BcryptCost, cfg.LoginRatePerSecond, cfg.LoginRateBurst)
	omsService := oms.New(st, engine, bus, cfg.Symbols)
	if recovered, err := omsService.Recover(ctx); err != nil {
		return fmt.Errorf("recover active orders: %w", err)
	} else if recovered > 0 {
		logger.Info("recovered active orders", "count", recovered)
	}
	metrics := observability.New()

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			gateway.RecoveryUnaryInterceptor(logger),
			metrics.UnaryServerInterceptor(),
			gateway.AuthUnaryInterceptor(authService),
			gateway.LoggingUnaryInterceptor(logger),
		),
		grpc.ChainStreamInterceptor(
			gateway.RecoveryStreamInterceptor(logger),
			metrics.StreamServerInterceptor(),
			gateway.AuthStreamInterceptor(authService),
			gateway.LoggingStreamInterceptor(logger),
		),
	)
	pb.RegisterAuthServiceServer(grpcServer, gateway.NewAuthServer(authService))
	pb.RegisterTradeServiceServer(grpcServer, gateway.NewTradeServer(omsService, metrics))
	pb.RegisterStreamServiceServer(grpcServer, gateway.NewStreamServer(bus, metrics, cfg.Symbols, cfg.OrderBuffer, cfg.MarketBuffer))
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	defer listener.Close()

	errCh := make(chan error, 2)
	go func() {
		logger.Info("grpc listening", "addr", cfg.GRPCAddr)
		if err := grpcServer.Serve(listener); err != nil {
			errCh <- err
		}
	}()

	handler, err := web.NewHandler(ctx, dialEndpoint(cfg.GRPCAddr), metrics.Handler())
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("http listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go reconcileLoop(ctx, omsService, logger)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		stop()
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	grpcServer.GracefulStop()
	logger.Info("shutdown complete")
	return nil
}

func reconcileLoop(ctx context.Context, svc *oms.Service, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			issues, err := svc.Reconcile(ctx)
			if err != nil {
				logger.Error("reconcile failed", "error", err)
				continue
			}
			if len(issues) > 0 {
				logger.Warn("reconcile mismatches", "count", len(issues), "issues", issues)
			}
		}
	}
}

func dialEndpoint(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
