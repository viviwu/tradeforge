package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/viviwu/tradeforge/pkg/tradeapi"
)

func (a *App) loginCommand() *cobra.Command {
	var (
		username string
		password string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in and cache the token pair",
		RunE: func(cmd *cobra.Command, args []string) error {
			if username == "" {
				return errors.New("--user is required")
			}
			if password == "" {
				password = readPassword("password: ")
			}
			client, err := tradeapi.Dial(cmd.Context(), tradeapi.Config{Address: a.Address})
			if err != nil {
				return err
			}
			defer client.Close()
			tokens, err := client.Login(cmd.Context(), username, password)
			if err != nil {
				return err
			}
			a.file.Address = a.Address
			a.persistTokens(tokens)
			fmt.Printf("logged in as %s (%s)\n", tokens.User.GetUsername(), tokens.User.GetRole())
			fmt.Printf("access token expires at %s\n", tokens.AccessExpiresAt.Format("2006-01-02 15:04:05"))
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "user", "", "username")
	cmd.Flags().StringVar(&password, "password", "", "password (prompted if empty)")
	return cmd
}

func (a *App) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the refresh token and clear local credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if a.file.RefreshToken == "" {
				return errors.New("not logged in")
			}
			err := a.withClient(ctx, func(client *tradeapi.Client) error {
				return client.Logout(ctx)
			})
			a.file.AccessToken = ""
			a.file.RefreshToken = ""
			a.file.AccessExpiresAt = 0
			a.file.RefreshExpiresAt = 0
			a.file.Username = ""
			if saveErr := saveConfig(a.ConfigPath, a.file); saveErr != nil {
				return saveErr
			}
			if err != nil {
				return err
			}
			fmt.Println("logged out")
			return nil
		},
	}
}

func readPassword(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return ""
		}
		return string(data)
	}
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}
