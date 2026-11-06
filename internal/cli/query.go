package cli

import (
	"github.com/spf13/cobra"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

func (a *App) queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query orders, trades, positions, and strategies",
	}
	cmd.AddCommand(
		a.queryOrdersCommand(),
		a.queryTradesCommand(),
		a.queryPositionsCommand(),
		a.queryStrategiesCommand(),
	)
	return cmd
}

func (a *App) queryOrdersCommand() *cobra.Command {
	var (
		status    string
		symbol    string
		pageSize  uint32
		pageToken string
	)
	cmd := &cobra.Command{
		Use:   "orders",
		Short: "List orders",
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedStatus, err := parseOrderStatus(status)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				resp, err := client.GetOrders(ctx, &pb.GetOrdersRequest{
					RequestId: requestID(),
					Status:    parsedStatus,
					Symbol:    symbol,
					PageSize:  pageSize,
					PageToken: pageToken,
				})
				if err != nil {
					return err
				}
				if a.Output == "json" {
					return printProto(resp)
				}
				return printOrders(resp)
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&symbol, "symbol", "", "filter by symbol")
	cmd.Flags().Uint32Var(&pageSize, "page-size", 50, "page size")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "page token")
	return cmd
}

func (a *App) queryTradesCommand() *cobra.Command {
	var (
		orderID   string
		symbol    string
		pageSize  uint32
		pageToken string
	)
	cmd := &cobra.Command{
		Use:   "trades",
		Short: "List executions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				resp, err := client.GetTrades(ctx, &pb.GetTradesRequest{
					RequestId: requestID(),
					OrderId:   orderID,
					Symbol:    symbol,
					PageSize:  pageSize,
					PageToken: pageToken,
				})
				if err != nil {
					return err
				}
				if a.Output == "json" {
					return printProto(resp)
				}
				return printTrades(resp)
			})
		},
	}
	cmd.Flags().StringVar(&orderID, "order-id", "", "filter by order id")
	cmd.Flags().StringVar(&symbol, "symbol", "", "filter by symbol")
	cmd.Flags().Uint32Var(&pageSize, "page-size", 50, "page size")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "page token")
	return cmd
}

func (a *App) queryPositionsCommand() *cobra.Command {
	var symbol string
	cmd := &cobra.Command{
		Use:   "positions",
		Short: "List positions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				resp, err := client.GetPositions(ctx, &pb.GetPositionsRequest{
					RequestId: requestID(),
					Symbol:    symbol,
				})
				if err != nil {
					return err
				}
				if a.Output == "json" {
					return printProto(resp)
				}
				return printPositions(resp)
			})
		},
	}
	cmd.Flags().StringVar(&symbol, "symbol", "", "filter by symbol")
	return cmd
}

func (a *App) queryStrategiesCommand() *cobra.Command {
	var (
		status    string
		pageSize  uint32
		pageToken string
	)
	cmd := &cobra.Command{
		Use:   "strategies",
		Short: "List strategies",
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedStatus, err := parseStrategyStatus(status)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				resp, err := client.GetStrategies(ctx, &pb.GetStrategiesRequest{
					RequestId: requestID(),
					Status:    parsedStatus,
					PageSize:  pageSize,
					PageToken: pageToken,
				})
				if err != nil {
					return err
				}
				if a.Output == "json" {
					return printProto(resp)
				}
				return printStrategies(resp)
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().Uint32Var(&pageSize, "page-size", 50, "page size")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "page token")
	return cmd
}
