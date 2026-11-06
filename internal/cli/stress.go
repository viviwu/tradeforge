package cli

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

func (a *App) stressCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stress",
		Short: "Built-in load test helpers",
	}
	cmd.AddCommand(a.stressLoginCommand(), a.stressOrderCommand())
	return cmd
}

func (a *App) stressOrderCommand() *cobra.Command {
	var (
		total       int
		concurrency int
		symbol      string
		price       int64
		quantity    int64
		timeout     time.Duration
	)
	cmd := &cobra.Command{
		Use:   "order",
		Short: "Submit concurrent resting limit orders with the cached token",
		RunE: func(cmd *cobra.Command, args []string) error {
			if total <= 0 || concurrency <= 0 {
				return fmt.Errorf("--n and --concurrency must be positive")
			}
			if quantity <= 0 {
				return fmt.Errorf("--qty must be positive")
			}
			if a.file.AccessToken == "" {
				return fmt.Errorf("not logged in; run 'forge login' first")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			client, err := tradeapi.Dial(ctx, tradeapi.Config{Address: a.Address})
			if err != nil {
				return err
			}
			defer client.Close()
			client.SetTokens(tradeapi.Tokens{
				AccessToken:      a.file.AccessToken,
				RefreshToken:     a.file.RefreshToken,
				AccessExpiresAt:  time.Unix(a.file.AccessExpiresAt, 0),
				RefreshExpiresAt: time.Unix(a.file.RefreshExpiresAt, 0),
			})

			var (
				mu        sync.Mutex
				latencies []time.Duration
				errs      = make(map[string]int)
			)
			jobs := make(chan int)
			var wg sync.WaitGroup
			start := time.Now()
			for w := 0; w < concurrency; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := range jobs {
						t0 := time.Now()
						_, err := client.SubmitOrder(ctx, &pb.SubmitOrderRequest{
							RequestId:     requestID(),
							ClientOrderId: fmt.Sprintf("stress-%d-%d-%d", w, i, time.Now().UnixNano()),
							Symbol:        symbol,
							Side:          pb.Side_SIDE_BUY,
							Type:          pb.OrderType_ORDER_TYPE_LIMIT,
							TimeInForce:   pb.TimeInForce_TIME_IN_FORCE_GTC,
							Price:         price,
							Quantity:      quantity,
						})
						mu.Lock()
						if err != nil {
							errs[err.Error()]++
						} else {
							latencies = append(latencies, time.Since(t0))
						}
						mu.Unlock()
					}
				}(w)
			}
			for i := 0; i < total; i++ {
				jobs <- i
			}
			close(jobs)
			wg.Wait()
			elapsed := time.Since(start)
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

			fmt.Printf("total=%d success=%d errors=%d elapsed=%s rps=%.0f\n",
				total, len(latencies), total-len(latencies), elapsed.Round(time.Millisecond),
				float64(len(latencies))/elapsed.Seconds())
			if len(latencies) > 0 {
				fmt.Printf("latency min=%s avg=%s p50=%s p95=%s p99=%s max=%s\n",
					latencies[0], average(latencies), percentile(latencies, 0.50),
					percentile(latencies, 0.95), percentile(latencies, 0.99), latencies[len(latencies)-1])
			}
			if len(errs) > 0 {
				fmt.Println("errors:")
				for msg, count := range errs {
					fmt.Printf("  %d x %s\n", count, msg)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&total, "n", 1000, "total orders")
	cmd.Flags().IntVar(&concurrency, "concurrency", 16, "concurrent workers")
	cmd.Flags().StringVar(&symbol, "symbol", "AAPL", "symbol")
	cmd.Flags().Int64Var(&price, "price", 1000, "limit price (well below market to avoid fills)")
	cmd.Flags().Int64Var(&quantity, "qty", 10, "quantity per order")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "overall timeout")
	return cmd
}

func (a *App) stressLoginCommand() *cobra.Command {
	var (
		total       int
		concurrency int
		username    string
		password    string
		timeout     time.Duration
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Run concurrent login requests",
		RunE: func(cmd *cobra.Command, args []string) error {
			if total <= 0 || concurrency <= 0 {
				return fmt.Errorf("--n and --concurrency must be positive")
			}
			if username == "" || password == "" {
				return fmt.Errorf("--user and --password are required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			jobs := make(chan int)
			results := make(chan loginResult, total)
			var wg sync.WaitGroup
			for i := 0; i < concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range jobs {
						start := time.Now()
						client, err := tradeapi.Dial(ctx, tradeapi.Config{Address: a.Address})
						if err != nil {
							results <- loginResult{latency: time.Since(start), err: err}
							continue
						}
						_, err = client.Login(ctx, username, password)
						_ = client.Close()
						results <- loginResult{latency: time.Since(start), err: err}
					}
				}()
			}
			go func() {
				for i := 0; i < total; i++ {
					jobs <- i
				}
				close(jobs)
			}()
			go func() {
				wg.Wait()
				close(results)
			}()

			var (
				latencies []time.Duration
				errCount  int
				errs      = make(map[string]int)
			)
			for result := range results {
				if result.err != nil {
					errCount++
					errs[result.err.Error()]++
					continue
				}
				latencies = append(latencies, result.latency)
			}
			fmt.Printf("total=%d success=%d errors=%d\n", total, len(latencies), errCount)
			if len(latencies) > 0 {
				sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
				fmt.Printf("latency min=%s avg=%s p50=%s p95=%s p99=%s max=%s\n",
					latencies[0], average(latencies), percentile(latencies, 0.50),
					percentile(latencies, 0.95), percentile(latencies, 0.99), latencies[len(latencies)-1])
			}
			if len(errs) > 0 {
				fmt.Println("errors:")
				for msg, count := range errs {
					fmt.Printf("  %d x %s\n", count, msg)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&total, "n", 1000, "total login attempts")
	cmd.Flags().IntVar(&concurrency, "concurrency", 100, "concurrent workers")
	cmd.Flags().StringVar(&username, "user", "alice", "username")
	cmd.Flags().StringVar(&password, "password", "", "password")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout")
	return cmd
}

type loginResult struct {
	latency time.Duration
	err     error
}

func average(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var total time.Duration
	for _, v := range values {
		total += v
	}
	return total / time.Duration(len(values))
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * p)
	return sorted[index]
}
