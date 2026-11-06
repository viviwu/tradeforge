package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/viviwu/tradeforge/internal/domain"
	"github.com/viviwu/tradeforge/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("rate limited")
	ErrTokenRevoked       = errors.New("token revoked")
)

type LoginResult struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	User             domain.User
}

type Service struct {
	store      store.Store
	tokens     *TokenManager
	refreshTTL time.Duration
	bcryptCost int
	limiter    *loginLimiter
}

func NewService(st store.Store, tokens *TokenManager, refreshTTL time.Duration, bcryptCost int, loginRPS float64, loginBurst int) *Service {
	return &Service{
		store:      st,
		tokens:     tokens,
		refreshTTL: refreshTTL,
		bcryptCost: bcryptCost,
		limiter:    newLoginLimiter(loginRPS, loginBurst),
	}
}

func (s *Service) TokenManager() *TokenManager { return s.tokens }

func (s *Service) Login(ctx context.Context, username, password, clientKey string) (LoginResult, error) {
	if !s.limiter.Allow(clientKey) {
		return LoginResult{}, ErrRateLimited
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, err
	}
	if !VerifyPassword(user.PasswordHash, password) {
		return LoginResult{}, ErrInvalidCredentials
	}
	return s.issue(ctx, user)
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (LoginResult, error) {
	hash := HashRefreshToken(refreshToken)
	record, err := s.store.GetRefreshToken(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return LoginResult{}, ErrInvalidToken
		}
		return LoginResult{}, err
	}
	if record.Revoked {
		return LoginResult{}, ErrTokenRevoked
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		return LoginResult{}, ErrInvalidToken
	}
	user, err := s.store.GetUserByID(ctx, record.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return LoginResult{}, ErrInvalidToken
		}
		return LoginResult{}, err
	}
	consumed, err := s.store.ConsumeRefreshToken(ctx, hash)
	if err != nil {
		return LoginResult{}, err
	}
	if !consumed {
		return LoginResult{}, ErrTokenRevoked
	}
	return s.issue(ctx, user)
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.store.RevokeRefreshToken(ctx, HashRefreshToken(refreshToken))
}

func (s *Service) Authenticate(ctx context.Context, accessToken string) (Claims, error) {
	return s.tokens.ParseAccess(accessToken)
}

func (s *Service) issue(ctx context.Context, user domain.User) (LoginResult, error) {
	access, accessExp, err := s.tokens.IssueAccess(user)
	if err != nil {
		return LoginResult{}, err
	}
	rawRefresh, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return LoginResult{}, err
	}
	refreshExp := time.Now().UTC().Add(s.refreshTTL)
	record := domain.RefreshToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: refreshExp,
		Revoked:   false,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateRefreshToken(ctx, record); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		AccessToken:      access,
		RefreshToken:     rawRefresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
		User:             user,
	}, nil
}

type loginLimiter struct {
	mu      sync.Mutex
	rps     rate.Limit
	burst   int
	buckets map[string]*rate.Limiter
}

func newLoginLimiter(rps float64, burst int) *loginLimiter {
	if rps <= 0 {
		rps = 20
	}
	if burst <= 0 {
		burst = int(rps)
	}
	return &loginLimiter{rps: rate.Limit(rps), burst: burst, buckets: make(map[string]*rate.Limiter)}
}

func (l *loginLimiter) Allow(key string) bool {
	if key == "" {
		key = "anonymous"
	}
	l.mu.Lock()
	bucket, ok := l.buckets[key]
	if !ok || len(l.buckets) > 10_000 {
		if len(l.buckets) > 10_000 {
			l.buckets = make(map[string]*rate.Limiter)
		}
		bucket = rate.NewLimiter(l.rps, l.burst)
		l.buckets[key] = bucket
	}
	l.mu.Unlock()
	return bucket.Allow()
}
