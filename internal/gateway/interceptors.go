package gateway

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/viviwu/tradeforge/internal/auth"
)

type claimsKey struct{}

var publicMethods = map[string]bool{
	"/tradeforge.v1.AuthService/Login":   true,
	"/tradeforge.v1.AuthService/Refresh": true,
	"/grpc.health.v1.Health/Check":       true,
}

func ClaimsFromContext(ctx context.Context) (auth.Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(auth.Claims)
	return claims, ok
}

func AccountIDFromContext(ctx context.Context) string {
	claims, ok := ClaimsFromContext(ctx)
	if !ok {
		return ""
	}
	return claims.UserID
}

func IsAdmin(ctx context.Context) bool {
	claims, ok := ClaimsFromContext(ctx)
	return ok && claims.IsAdmin()
}

func AuthUnaryInterceptor(svc *auth.Service) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}
		claims, err := authenticate(ctx, svc)
		if err != nil {
			return nil, err
		}
		return handler(context.WithValue(ctx, claimsKey{}, claims), req)
	}
}

func AuthStreamInterceptor(svc *auth.Service) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if publicMethods[info.FullMethod] {
			return handler(srv, stream)
		}
		claims, err := authenticate(stream.Context(), svc)
		if err != nil {
			return err
		}
		wrapped := &wrappedStream{ServerStream: stream, ctx: context.WithValue(stream.Context(), claimsKey{}, claims)}
		return handler(srv, wrapped)
	}
}

func authenticate(ctx context.Context, svc *auth.Service) (auth.Claims, error) {
	token := tokenFromMetadata(ctx)
	if token == "" {
		return auth.Claims{}, status.Error(codes.Unauthenticated, "missing bearer token")
	}
	claims, err := svc.Authenticate(ctx, token)
	if err != nil {
		return auth.Claims{}, status.Error(codes.Unauthenticated, "invalid token")
	}
	return claims, nil
}

func tokenFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	value := strings.TrimSpace(values[0])
	if len(value) > 7 && strings.EqualFold(value[:7], "bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return ""
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

func RecoveryUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered", "method", info.FullMethod, "panic", r)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func RecoveryStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered", "method", info.FullMethod, "panic", r)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(srv, stream)
	}
}

func LoggingUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		userID := AccountIDFromContext(ctx)
		logger.Info("grpc request",
			"method", info.FullMethod,
			"code", status.Code(err).String(),
			"duration_ms", time.Since(start).Milliseconds(),
			"user_id", userID,
		)
		return resp, err
	}
}

func LoggingStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, stream)
		logger.Info("grpc stream",
			"method", info.FullMethod,
			"code", status.Code(err).String(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return err
	}
}
