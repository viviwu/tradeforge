package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GRPCAddr    string
	HTTPAddr    string
	DBDSN       string
	JWTSecret   string
	AccessTTL   time.Duration
	RefreshTTL  time.Duration
	BcryptCost  int
	Seed        bool
	InitialCash int64
	Symbols     []string
	MDInterval  time.Duration
	LogLevel    string

	LoginRatePerSecond float64
	LoginRateBurst     int
	MarketBuffer       int
	OrderBuffer        int
	ReplayBuffer       int
}

func Load() Config {
	cfg := Config{
		GRPCAddr:           env("TRADEFORGE_GRPC_ADDR", ":9090"),
		HTTPAddr:           env("TRADEFORGE_HTTP_ADDR", ":8080"),
		DBDSN:              env("TRADEFORGE_DB_DSN", "postgres://tradeforge:tradeforge@localhost:5432/tradeforge?sslmode=disable"),
		JWTSecret:          env("TRADEFORGE_JWT_SECRET", "dev-secret-change-me"),
		AccessTTL:          envDuration("TRADEFORGE_ACCESS_TTL", 15*time.Minute),
		RefreshTTL:         envDuration("TRADEFORGE_REFRESH_TTL", 7*24*time.Hour),
		BcryptCost:         envInt("TRADEFORGE_BCRYPT_COST", 10),
		Seed:               envBool("TRADEFORGE_SEED", true),
		InitialCash:        envInt64("TRADEFORGE_INITIAL_CASH", 100_000_000),
		Symbols:            splitCSV(env("TRADEFORGE_SYMBOLS", "AAPL,MSFT,TSLA,NVDA")),
		MDInterval:         envDuration("TRADEFORGE_MD_INTERVAL", 200*time.Millisecond),
		LogLevel:           env("TRADEFORGE_LOG_LEVEL", "info"),
		LoginRatePerSecond: envFloat("TRADEFORGE_LOGIN_RPS", 20),
		LoginRateBurst:     envInt("TRADEFORGE_LOGIN_BURST", 40),
		MarketBuffer:       envInt("TRADEFORGE_MARKET_BUFFER", 256),
		OrderBuffer:        envInt("TRADEFORGE_ORDER_BUFFER", 1024),
		ReplayBuffer:       envInt("TRADEFORGE_REPLAY_BUFFER", 4096),
	}
	return cfg
}

func (c Config) Validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("TRADEFORGE_JWT_SECRET must not be empty")
	}
	if len(c.Symbols) == 0 {
		return fmt.Errorf("TRADEFORGE_SYMBOLS must not be empty")
	}
	if c.BcryptCost < 4 || c.BcryptCost > 31 {
		return fmt.Errorf("TRADEFORGE_BCRYPT_COST must be between 4 and 31")
	}
	return nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envInt64(key string, fallback int64) int64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func splitCSV(s string) []string {
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
