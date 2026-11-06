package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

func (a *App) subCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sub",
		Short: "Subscribe to order, strategy, or market streams",
	}
	cmd.AddCommand(a.subOrdersCommand(), a.subStrategyCommand(), a.subMarketCommand())
	return cmd
}

func (a *App) subOrdersCommand() *cobra.Command {
	var (
		symbols string
		fromSeq uint64
	)
	cmd := &cobra.Command{
		Use:   "orders",
		Short: "Subscribe to order and execution events",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				events, errs := client.SubscribeOrders(ctx, &pb.SubscribeOrdersRequest{
					RequestId: requestID(),
					FromSeq:   fromSeq,
					Symbols:   splitSymbols(symbols),
				})
				for {
					select {
					case <-ctx.Done():
						return nil
					case err := <-errs:
						if err != nil {
							return err
						}
					case event, ok := <-events:
						if !ok {
							return nil
						}
						printOrderEvent(event)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&symbols, "symbols", "", "comma separated symbol filter")
	cmd.Flags().Uint64Var(&fromSeq, "from-seq", 0, "resume after this sequence")
	return cmd
}

func (a *App) subStrategyCommand() *cobra.Command {
	var (
		ids     string
		fromSeq uint64
	)
	cmd := &cobra.Command{
		Use:   "strategy",
		Short: "Subscribe to strategy status events",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				events, errs := client.SubscribeStrategies(ctx, &pb.SubscribeStrategiesRequest{
					RequestId:   requestID(),
					FromSeq:     fromSeq,
					StrategyIds: splitSymbols(ids),
				})
				for {
					select {
					case <-ctx.Done():
						return nil
					case err := <-errs:
						if err != nil {
							return err
						}
					case event, ok := <-events:
						if !ok {
							return nil
						}
						printStrategyEvent(event)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&ids, "id", "", "comma separated strategy id filter")
	cmd.Flags().Uint64Var(&fromSeq, "from-seq", 0, "resume after this sequence")
	return cmd
}

func (a *App) subMarketCommand() *cobra.Command {
	var (
		symbols string
		fromSeq uint64
	)
	cmd := &cobra.Command{
		Use:   "market",
		Short: "Subscribe to market data",
		RunE: func(cmd *cobra.Command, args []string) error {
			requested := splitSymbols(symbols)
			if len(requested) == 0 {
				return fmt.Errorf("--symbol is required")
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				events, errs := client.SubscribeMarketData(ctx, &pb.SubscribeMarketDataRequest{
					RequestId: requestID(),
					FromSeq:   fromSeq,
					Symbols:   requested,
				})
				for {
					select {
					case <-ctx.Done():
						return nil
					case err := <-errs:
						if err != nil {
							return err
						}
					case event, ok := <-events:
						if !ok {
							return nil
						}
						printMarketEvent(event)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&symbols, "symbol", "", "comma separated symbols")
	cmd.Flags().Uint64Var(&fromSeq, "from-seq", 0, "resume after this sequence")
	return cmd
}

func splitSymbols(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func printOrderEvent(event *pb.OrderEvent) {
	fmt.Printf("[seq=%d] order=%s status=%s filled=%d avg=%s reason=%s\n",
		event.GetSeq(), event.GetOrderId(), shortEnum(string(event.GetStatus())), event.GetFilledQty(),
		money(event.GetAvgPrice()), event.GetReason())
}

func printStrategyEvent(event *pb.StrategyEvent) {
	fmt.Printf("[seq=%d] strategy=%s status=%s reason=%s\n",
		event.GetSeq(), event.GetStrategyId(), shortEnum(string(event.GetStatus())), event.GetReason())
}

func printMarketEvent(event *pb.MarketEvent) {
	fmt.Printf("[seq=%d] %s bid=%s x %d ask=%s x %d last=%s\n",
		event.GetSeq(), event.GetSymbol(), money(event.GetBidPrice()), event.GetBidQty(),
		money(event.GetAskPrice()), event.GetAskQty(), money(event.GetLastPrice()))
}
