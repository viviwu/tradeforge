package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/viviwu/tradeforge/internal/auth"
	"github.com/viviwu/tradeforge/internal/domain"
	"github.com/viviwu/tradeforge/internal/store"
)

type demoUser struct {
	username string
	password string
	role     string
}

func Seed(ctx context.Context, st store.Store, logger *slog.Logger, bcryptCost int, initialCash int64) error {
	users := []demoUser{
		{username: "admin", password: "admin123", role: "admin"},
		{username: "alice", password: "alice123", role: "viewer"},
	}
	for _, demo := range users {
		user, err := st.GetUserByUsername(ctx, demo.username)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			hash, err := auth.HashPassword(demo.password, bcryptCost)
			if err != nil {
				return err
			}
			user = domain.User{
				ID:           uuid.NewString(),
				Username:     demo.username,
				PasswordHash: hash,
				Role:         demo.role,
				CreatedAt:    time.Now().UTC(),
			}
			if err := st.CreateUser(ctx, user); err != nil {
				return err
			}
			logger.Info("seeded user", "username", demo.username, "role", demo.role)
		}
		if err := st.EnsureAccount(ctx, user.ID, initialCash); err != nil {
			return err
		}
		if demo.username == "alice" {
			if err := seedStrategies(ctx, st, user.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func seedStrategies(ctx context.Context, st store.Store, accountID string) error {
	existing, _, err := st.ListStrategies(ctx, store.StrategyFilter{AccountID: accountID, PageSize: 1})
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	now := time.Now().UTC()
	items := []domain.Strategy{
		{
			ID:        uuid.NewString(),
			AccountID: accountID,
			Name:      "momentum",
			Status:    domain.StrategyStatusRunning,
			Params:    `{"lookback":20}`,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        uuid.NewString(),
			AccountID: accountID,
			Name:      "mean-reversion",
			Status:    domain.StrategyStatusPaused,
			Params:    `{"window":50}`,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	for _, item := range items {
		if err := st.UpsertStrategy(ctx, item); err != nil {
			return err
		}
	}
	return nil
}
