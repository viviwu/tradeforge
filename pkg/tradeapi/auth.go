package tradeapi

import (
	"context"
	"time"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

func (c *Client) Login(ctx context.Context, username, password string) (Tokens, error) {
	resp, err := c.auth.Login(ctx, &pb.LoginRequest{Username: username, Password: password})
	if err != nil {
		return Tokens{}, err
	}
	tokens := Tokens{
		AccessToken:      resp.GetAccessToken(),
		RefreshToken:     resp.GetRefreshToken(),
		AccessExpiresAt:  time.Unix(resp.GetAccessTokenExpiresAtUnix(), 0).UTC(),
		RefreshExpiresAt: time.Unix(resp.GetRefreshTokenExpiresAtUnix(), 0).UTC(),
		User:             resp.GetUser(),
	}
	c.SetTokens(tokens)
	return tokens, nil
}

func (c *Client) Refresh(ctx context.Context) (Tokens, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	current := c.Tokens()
	resp, err := c.auth.Refresh(ctx, &pb.RefreshRequest{RefreshToken: current.RefreshToken})
	if err != nil {
		return Tokens{}, err
	}
	tokens := Tokens{
		AccessToken:      resp.GetAccessToken(),
		RefreshToken:     resp.GetRefreshToken(),
		AccessExpiresAt:  time.Unix(resp.GetAccessTokenExpiresAtUnix(), 0).UTC(),
		RefreshExpiresAt: time.Unix(resp.GetRefreshTokenExpiresAtUnix(), 0).UTC(),
		User:             resp.GetUser(),
	}
	c.SetTokens(tokens)
	return tokens, nil
}

func (c *Client) Logout(ctx context.Context) error {
	_, err := c.auth.Logout(c.authorizedCtx(ctx), &pb.LogoutRequest{RefreshToken: c.Tokens().RefreshToken})
	return err
}
