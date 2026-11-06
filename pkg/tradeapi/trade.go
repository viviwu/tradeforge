package tradeapi

import (
	"context"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

func (c *Client) SubmitOrder(ctx context.Context, req *pb.SubmitOrderRequest) (*pb.SubmitOrderResponse, error) {
	var resp *pb.SubmitOrderResponse
	err := c.withAuth(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.trade.SubmitOrder(ctx, req)
		return err
	})
	return resp, err
}

func (c *Client) CancelOrder(ctx context.Context, req *pb.CancelOrderRequest) (*pb.CancelOrderResponse, error) {
	var resp *pb.CancelOrderResponse
	err := c.withAuth(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.trade.CancelOrder(ctx, req)
		return err
	})
	return resp, err
}

func (c *Client) GetOrders(ctx context.Context, req *pb.GetOrdersRequest) (*pb.GetOrdersResponse, error) {
	var resp *pb.GetOrdersResponse
	err := c.withAuth(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.trade.GetOrders(ctx, req)
		return err
	})
	return resp, err
}

func (c *Client) GetTrades(ctx context.Context, req *pb.GetTradesRequest) (*pb.GetTradesResponse, error) {
	var resp *pb.GetTradesResponse
	err := c.withAuth(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.trade.GetTrades(ctx, req)
		return err
	})
	return resp, err
}

func (c *Client) GetPositions(ctx context.Context, req *pb.GetPositionsRequest) (*pb.GetPositionsResponse, error) {
	var resp *pb.GetPositionsResponse
	err := c.withAuth(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.trade.GetPositions(ctx, req)
		return err
	})
	return resp, err
}

func (c *Client) GetStrategies(ctx context.Context, req *pb.GetStrategiesRequest) (*pb.GetStrategiesResponse, error) {
	var resp *pb.GetStrategiesResponse
	err := c.withAuth(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.trade.GetStrategies(ctx, req)
		return err
	})
	return resp, err
}

func (c *Client) UpdateStrategyStatus(ctx context.Context, req *pb.UpdateStrategyStatusRequest) (*pb.UpdateStrategyStatusResponse, error) {
	var resp *pb.UpdateStrategyStatusResponse
	err := c.withAuth(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.trade.UpdateStrategyStatus(ctx, req)
		return err
	})
	return resp, err
}
