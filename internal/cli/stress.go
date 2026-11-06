package cli

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

func (a *App) stressCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stress",
		Short: "Built-in load test helpers",
	}
	cmd.AddCommand(a.stressLoginCommand())
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
