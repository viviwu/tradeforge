package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

func printProto(message proto.Message) error {
	data, err := protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}.Marshal(message)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func newTable() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func money(cents int64) string {
	return fmt.Sprintf("%.2f", float64(cents)/100)
}

func printOrders(resp *pb.GetOrdersResponse) error {
	w := newTable()
	fmt.Fprintln(w, "ORDER_ID\tSYMBOL\tSIDE\tTYPE\tPRICE\tQTY\tFILLED\tAVG\tSTATUS\tCLIENT_ORDER_ID")
	for _, o := range resp.GetOrders() {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			o.GetOrderId(), o.GetSymbol(), shortEnum(o.GetSide().String()), shortEnum(o.GetType().String()), money(o.GetPrice()),
			o.GetQuantity(), o.GetFilledQty(), money(o.GetAvgPrice()), shortEnum(o.GetStatus().String()), o.GetClientOrderId())
	}
	if resp.GetNextPageToken() != "" {
		fmt.Fprintf(w, "\nnext_page_token\t%s\n", resp.GetNextPageToken())
	}
	return w.Flush()
}

func printTrades(resp *pb.GetTradesResponse) error {
	w := newTable()
	fmt.Fprintln(w, "TRADE_SEQ\tTRADE_ID\tORDER_ID\tSYMBOL\tSIDE\tPRICE\tQTY\tTIME")
	for _, t := range resp.GetTrades() {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			t.GetTradeSeq(), t.GetTradeId(), t.GetOrderId(), t.GetSymbol(), shortEnum(t.GetSide().String()),
			money(t.GetPrice()), t.GetQuantity(), formatNano(t.GetCreatedAtUnixNano()))
	}
	if resp.GetNextPageToken() != "" {
		fmt.Fprintf(w, "\nnext_page_token\t%s\n", resp.GetNextPageToken())
	}
	return w.Flush()
}

func printPositions(resp *pb.GetPositionsResponse) error {
	w := newTable()
	fmt.Fprintln(w, "SYMBOL\tQTY\tFROZEN\tAVG\tMARKET\tUNREALIZED_PNL")
	for _, p := range resp.GetPositions() {
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%s\n",
			p.GetSymbol(), p.GetQuantity(), p.GetFrozenQty(), money(p.GetAvgPrice()),
			money(p.GetMarketPrice()), money(p.GetUnrealizedPnl()))
	}
	return w.Flush()
}

func printStrategies(resp *pb.GetStrategiesResponse) error {
	w := newTable()
	fmt.Fprintln(w, "STRATEGY_ID\tNAME\tSTATUS\tUPDATED\tPARAMS")
	for _, s := range resp.GetStrategies() {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			s.GetStrategyId(), s.GetName(), shortEnum(s.GetStatus().String()), formatNano(s.GetUpdatedAtUnixNano()), s.GetParamsJson())
	}
	if resp.GetNextPageToken() != "" {
		fmt.Fprintf(w, "\nnext_page_token\t%s\n", resp.GetNextPageToken())
	}
	return w.Flush()
}

func shortEnum(value string) string {
	value = strings.TrimPrefix(value, "ORDER_STATUS_")
	value = strings.TrimPrefix(value, "ORDER_TYPE_")
	value = strings.TrimPrefix(value, "STRATEGY_STATUS_")
	value = strings.TrimPrefix(value, "SIDE_")
	value = strings.TrimPrefix(value, "TIME_IN_FORCE_")
	return value
}

func formatNano(ns int64) string {
	if ns <= 0 {
		return "-"
	}
	return time.Unix(0, ns).UTC().Format("2006-01-02 15:04:05")
}
