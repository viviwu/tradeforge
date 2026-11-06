package tradeapi

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

type Config struct {
	Address string
}

type Tokens struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	User             *pb.User
}

func (t Tokens) Valid() bool { return t.AccessToken != "" }

type Client struct {
	conn *grpc.ClientConn

	auth   pb.AuthServiceClient
	trade  pb.TradeServiceClient
	stream pb.StreamServiceClient

	mu        sync.RWMutex
	tokens    Tokens
	refreshMu sync.Mutex
}

func Dial(ctx context.Context, cfg Config) (*Client, error) {
	conn, err := grpc.NewClient(cfg.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:   conn,
		auth:   pb.NewAuthServiceClient(conn),
		trade:  pb.NewTradeServiceClient(conn),
		stream: pb.NewStreamServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) SetTokens(t Tokens) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens = t
}

func (c *Client) Tokens() Tokens {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokens
}

func (c *Client) authorizedCtx(ctx context.Context) context.Context {
	t := c.Tokens()
	if t.AccessToken == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+t.AccessToken)
}

func (c *Client) withAuth(ctx context.Context, fn func(context.Context) error) error {
	err := fn(c.authorizedCtx(ctx))
	if status.Code(err) != codes.Unauthenticated {
		return err
	}
	if c.Tokens().RefreshToken == "" {
		return err
	}
	if _, refreshErr := c.Refresh(ctx); refreshErr != nil {
		return err
	}
	return fn(c.authorizedCtx(ctx))
}

func (c *Client) Auth() pb.AuthServiceClient     { return c.auth }
func (c *Client) Trade() pb.TradeServiceClient   { return c.trade }
func (c *Client) Stream() pb.StreamServiceClient { return c.stream }
