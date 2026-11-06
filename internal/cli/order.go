package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

func (a *App) orderCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "order",
		Short: "Submit or cancel orders",
	}
	cmd.AddCommand(a.orderSubmitCommand(), a.orderCancelCommand())
	return cmd
}

func (a *App) orderSubmitCommand() *cobra.Command {
	var (
		symbol        string
		side          string
		orderType     string
		price         int64
		quantity      int64
		tif           string
		clientOrderID string
	)
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit a limit or market order",
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedSide, err := parseSide(side)
			if err != nil {
				return err
			}
			parsedType, err := parseOrderType(orderType)
			if err != nil {
				return err
			}
			parsedTIF, err := parseTimeInForce(tif)
			if err != nil {
				return err
			}
			if clientOrderID == "" {
				clientOrderID = fmt.Sprintf("cli-%d", time.Now().UnixNano())
			}
			if quantity <= 0 {
				return errors.New("--qty must be positive")
			}
			if parsedType == pb.OrderType_ORDER_TYPE_LIMIT && price <= 0 {
				return errors.New("--price must be positive for limit orders")
			}
			ctx := cmd.Context()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				resp, err := client.SubmitOrder(ctx, &pb.SubmitOrderRequest{
					RequestId:     requestID(),
					ClientOrderId: clientOrderID,
					Symbol:        symbol,
					Side:          parsedSide,
					Type:          parsedType,
					TimeInForce:   parsedTIF,
					Price:         price,
					Quantity:      quantity,
				})
				if err != nil {
					return err
				}
				if a.Output == "json" {
					return printProto(resp)
				}
				fmt.Printf("order_id=%s client_order_id=%s status=%s\n",
					resp.GetOrderId(), resp.GetClientOrderId(), shortEnum(resp.GetStatus().String()))
				if resp.GetRejectReason() != "" {
					fmt.Printf("reject_reason=%s\n", resp.GetRejectReason())
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&symbol, "symbol", "", "symbol")
	cmd.Flags().StringVar(&side, "side", "BUY", "side: BUY|SELL")
	cmd.Flags().StringVar(&orderType, "type", "LIMIT", "order type: LIMIT|MARKET")
	cmd.Flags().Int64Var(&price, "price", 0, "limit price in minimum units")
	cmd.Flags().Int64Var(&quantity, "qty", 0, "quantity")
	cmd.Flags().StringVar(&tif, "tif", "GTC", "time in force: GTC|IOC|FOK")
	cmd.Flags().StringVar(&clientOrderID, "client-order-id", "", "idempotency key (generated if empty)")
	_ = cmd.MarkFlagRequired("symbol")
	_ = cmd.MarkFlagRequired("qty")
	return cmd
}

func (a *App) orderCancelCommand() *cobra.Command {
	var (
		orderID       string
		clientOrderID string
	)
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel an active order",
		RunE: func(cmd *cobra.Command, args []string) error {
			if orderID == "" && clientOrderID == "" {
				return errors.New("--id or --client-order-id is required")
			}
			ctx := cmd.Context()
			return a.withClient(ctx, func(client *tradeapi.Client) error {
				resp, err := client.CancelOrder(ctx, &pb.CancelOrderRequest{
					RequestId:     requestID(),
					OrderId:       orderID,
					ClientOrderId: clientOrderID,
				})
				if err != nil {
					return err
				}
				if a.Output == "json" {
					return printProto(resp)
				}
				fmt.Printf("order_id=%s status=%s canceled_qty=%d\n",
					resp.GetOrderId(), shortEnum(resp.GetStatus().String()), resp.GetCanceledQty())
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&orderID, "id", "", "order id")
	cmd.Flags().StringVar(&clientOrderID, "client-order-id", "", "client order id")
	return cmd
}
