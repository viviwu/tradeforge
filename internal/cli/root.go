package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

const defaultAddress = "127.0.0.1:9090"

type App struct {
	Address    string
	ConfigPath string
	Output     string

	file fileConfig
}

func Execute() error {
	app := &App{}
	return app.Root().Execute()
}

func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "forge",
		Short:         "TradeForge command line client",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(a.ConfigPath)
			if err != nil {
				return err
			}
			a.file = cfg
			if a.Address == "" {
				a.Address = cfg.Address
			}
			if a.Address == "" {
				a.Address = defaultAddress
			}
			return nil
		},
	}
	root.PersistentFlags().StringVar(&a.Address, "address", "", "gRPC server address")
	root.PersistentFlags().StringVar(&a.ConfigPath, "config", "", "config file path")
	root.PersistentFlags().StringVar(&a.Output, "output", "table", "output format: table|json")

	root.AddCommand(
		a.loginCommand(),
		a.logoutCommand(),
		a.orderCommand(),
		a.queryCommand(),
		a.subCommand(),
		a.stressCommand(),
	)
	return root
}

func (a *App) withClient(ctx context.Context, fn func(*tradeapi.Client) error) error {
	client, err := tradeapi.Dial(ctx, tradeapi.Config{Address: a.Address})
	if err != nil {
		return err
	}
	defer client.Close()
	if a.file.AccessToken != "" {
		client.SetTokens(tradeapi.Tokens{
			AccessToken:      a.file.AccessToken,
			RefreshToken:     a.file.RefreshToken,
			AccessExpiresAt:  time.Unix(a.file.AccessExpiresAt, 0),
			RefreshExpiresAt: time.Unix(a.file.RefreshExpiresAt, 0),
		})
	}
	runErr := fn(client)
	a.persistTokens(client.Tokens())
	return runErr
}

func (a *App) persistTokens(tokens tradeapi.Tokens) {
	if tokens.AccessToken == "" {
		return
	}
	a.file.AccessToken = tokens.AccessToken
	a.file.RefreshToken = tokens.RefreshToken
	a.file.AccessExpiresAt = tokens.AccessExpiresAt.Unix()
	a.file.RefreshExpiresAt = tokens.RefreshExpiresAt.Unix()
	if tokens.User != nil {
		a.file.Username = tokens.User.GetUsername()
	}
	if err := saveConfig(a.ConfigPath, a.file); err != nil {
		fmt.Println("warning: failed to save tokens:", err)
	}
}

func requestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}
