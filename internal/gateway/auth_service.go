package gateway

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/internal/auth"
)

type AuthServer struct {
	pb.UnimplementedAuthServiceServer
	svc *auth.Service
}

func NewAuthServer(svc *auth.Service) *AuthServer {
	return &AuthServer{svc: svc}
}

func (s *AuthServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	result, err := s.svc.Login(ctx, req.GetUsername(), req.GetPassword(), loginKey(ctx, req.GetUsername()))
	if err != nil {
		return nil, mapAuthError(err)
	}
	return loginResponse(result), nil
}

func (s *AuthServer) Refresh(ctx context.Context, req *pb.RefreshRequest) (*pb.RefreshResponse, error) {
	if req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token required")
	}
	result, err := s.svc.Refresh(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &pb.RefreshResponse{
		AccessToken:               result.AccessToken,
		RefreshToken:              result.RefreshToken,
		AccessTokenExpiresAtUnix:  result.AccessExpiresAt.Unix(),
		RefreshTokenExpiresAtUnix: result.RefreshExpiresAt.Unix(),
		User:                      userToProto(result.User),
	}, nil
}

func (s *AuthServer) Logout(ctx context.Context, req *pb.LogoutRequest) (*emptypb.Empty, error) {
	if req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token required")
	}
	if err := s.svc.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &emptypb.Empty{}, nil
}

func loginResponse(result auth.LoginResult) *pb.LoginResponse {
	return &pb.LoginResponse{
		AccessToken:               result.AccessToken,
		RefreshToken:              result.RefreshToken,
		AccessTokenExpiresAtUnix:  result.AccessExpiresAt.Unix(),
		RefreshTokenExpiresAtUnix: result.RefreshExpiresAt.Unix(),
		User:                      userToProto(result.User),
	}
}

func loginKey(ctx context.Context, username string) string {
	addr := "unknown"
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		addr = p.Addr.String()
	}
	return addr + "|" + username
}

func mapAuthError(err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return status.Error(codes.Unauthenticated, "invalid username or password")
	case errors.Is(err, auth.ErrInvalidToken):
		return status.Error(codes.Unauthenticated, "invalid or expired refresh token")
	case errors.Is(err, auth.ErrTokenRevoked):
		return status.Error(codes.Unauthenticated, "refresh token revoked")
	case errors.Is(err, auth.ErrRateLimited):
		return status.Error(codes.ResourceExhausted, "too many login attempts")
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
